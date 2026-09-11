// Package auth provides Fabrin's authentication core.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Errors returned by the authentication core.
var (
	ErrInvalidEmail     = errors.New("auth: invalid email")
	ErrInvalidChallenge = errors.New("auth: invalid challenge")
	ErrInvalidCode      = errors.New("auth: invalid code")
	ErrExpired          = errors.New("auth: challenge expired")
	ErrLocked           = errors.New("auth: challenge locked")
	ErrRateLimited      = errors.New("auth: rate limited")
	ErrStore            = errors.New("auth: store failure")
	ErrDelivery         = errors.New("auth: delivery failure")
	ErrUnsafeProduction = errors.New("auth: preview backend is unsafe in production")
)

// Store owns the atomic challenge and identity transitions required by Service.
//
// Issue must atomically enforce the send budget for email, retain the original
// issuedAt/expiresAt and attempt count during resend, and invalidate the prior
// challenge, then return that effective expiry. Consume must atomically compare
// verifier, increment failed attempts, consume one successful challenge, and
// create or resolve exactly one identity. Implementations compare verifiers in
// constant time.
type Store interface {
	Issue(ctx context.Context, id, email string, verifier []byte, issuedAt, expiresAt time.Time, attemptLimit, sendLimit int) (time.Time, error)
	Consume(ctx context.Context, id string, verifier []byte, now time.Time, identityID string) (Identity, error)
}

// Delivery sends one authentication code. Implementations must treat code as a
// secret and must not put it in logs or errors.
type Delivery interface {
	Deliver(ctx context.Context, email, challengeID, code string) error
}

// Challenge is the public information returned after requesting a code.
type Challenge struct {
	ID        string
	ExpiresAt time.Time
}

// Identity is Fabrin's stable authentication subject.
type Identity struct {
	ID        string
	Email     string
	CreatedAt time.Time
}

// Option configures a Service.
type Option func(*settings) error

// WithChallengeTTL sets the lifetime shared by a request and all of its
// resends. The default is ten minutes.
func WithChallengeTTL(ttl time.Duration) Option {
	return func(s *settings) error {
		if ttl <= 0 {
			return fmt.Errorf("challenge TTL must be positive")
		}
		s.ttl = ttl
		return nil
	}
}

// WithAttemptLimit sets the maximum failed verification count. The default is
// five. Reaching the limit locks the challenge.
func WithAttemptLimit(limit int) Option {
	return func(s *settings) error {
		if limit < 1 {
			return fmt.Errorf("attempt limit must be positive")
		}
		s.attemptLimit = limit
		return nil
	}
}

// WithSendLimit sets the maximum sends in one challenge window. The default is
// three; resend does not reset it.
func WithSendLimit(limit int) Option {
	return func(s *settings) error {
		if limit < 1 {
			return fmt.Errorf("send limit must be positive")
		}
		s.sendLimit = limit
		return nil
	}
}

// WithProduction rejects preview-only stores or delivery backends during
// construction.
func WithProduction() Option {
	return func(s *settings) error {
		s.production = true
		return nil
	}
}

type settings struct {
	ttl          time.Duration
	attemptLimit int
	sendLimit    int
	production   bool
}

// Service issues and verifies email challenges.
type Service struct {
	store        Store
	delivery     Delivery
	key          []byte
	ttl          time.Duration
	attemptLimit int
	sendLimit    int
	now          func() time.Time
	random       io.Reader
}

// New constructs an email authentication service. key must contain at least 32
// bytes and is copied before New returns.
func New(store Store, delivery Delivery, key []byte, options ...Option) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("auth: store is required")
	}
	if delivery == nil {
		return nil, fmt.Errorf("auth: delivery is required")
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("auth: verifier key must contain at least 32 bytes")
	}
	s := settings{ttl: 10 * time.Minute, attemptLimit: 5, sendLimit: 3}
	for i, option := range options {
		if option == nil {
			return nil, fmt.Errorf("auth: option %d is nil", i)
		}
		if err := option(&s); err != nil {
			return nil, fmt.Errorf("auth: option %d: %w", i, err)
		}
	}
	if s.production {
		if _, preview := store.(interface{ previewOnly() }); preview {
			return nil, ErrUnsafeProduction
		}
		if _, preview := delivery.(interface{ previewOnly() }); preview {
			return nil, ErrUnsafeProduction
		}
	}
	return &Service{
		store: store, delivery: delivery, key: append([]byte(nil), key...),
		ttl: s.ttl, attemptLimit: s.attemptLimit, sendLimit: s.sendLimit,
		now: time.Now, random: rand.Reader,
	}, nil
}

// Request issues or resends a code for email.
func (s *Service) Request(ctx context.Context, email string) (Challenge, error) {
	if err := ctx.Err(); err != nil {
		return Challenge{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return Challenge{}, err
	}
	id, err := s.randomString(32, base64.RawURLEncoding)
	if err != nil {
		return Challenge{}, fmt.Errorf("auth: generate challenge id: %w", err)
	}
	code, err := s.randomString(10, base32.StdEncoding.WithPadding(base32.NoPadding))
	if err != nil {
		return Challenge{}, fmt.Errorf("auth: generate code: %w", err)
	}
	now := s.now().UTC()
	expires := now.Add(s.ttl)
	verifier := s.verifier(id, code)
	effectiveExpiry, err := s.store.Issue(ctx, id, normalized, verifier, now, expires, s.attemptLimit, s.sendLimit)
	if err != nil {
		return Challenge{}, storeError("issue challenge", err)
	}
	if err := s.delivery.Deliver(ctx, normalized, id, code); err != nil {
		return Challenge{}, fmt.Errorf("%w: %w", ErrDelivery, err)
	}
	return Challenge{ID: id, ExpiresAt: effectiveExpiry}, nil
}

// Verify atomically consumes a challenge and creates or resolves its identity.
func (s *Service) Verify(ctx context.Context, challengeID, code string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	identityID, err := s.randomString(16, base64.RawURLEncoding)
	if err != nil {
		return Identity{}, fmt.Errorf("auth: generate identity id: %w", err)
	}
	identity, err := s.store.Consume(ctx, challengeID, s.verifier(challengeID, strings.ToUpper(strings.TrimSpace(code))), s.now().UTC(), identityID)
	if err != nil {
		return Identity{}, storeError("consume challenge", err)
	}
	return identity, nil
}

func storeError(operation string, err error) error {
	for _, condition := range []error{
		ErrInvalidChallenge, ErrInvalidCode, ErrExpired, ErrLocked, ErrRateLimited,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, condition) {
			return fmt.Errorf("auth: %s: %w", operation, err)
		}
	}
	return fmt.Errorf("%w: %s: %w", ErrStore, operation, err)
}

func (s *Service) verifier(id, code string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("fabrin/auth/email-otp/v1\x00"))
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte{'\x00'})
	_, _ = mac.Write([]byte(code))
	return mac.Sum(nil)
}

func (s *Service) randomString(n int, encoding interface{ EncodeToString([]byte) string }) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.random, b); err != nil {
		return "", err
	}
	return encoding.EncodeToString(b), nil
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndexByte(email, '@')
	if at < 1 || at == len(email)-1 || strings.Count(email, "@") != 1 || strings.ContainsAny(email, "\r\n\t <>\x00") {
		return "", ErrInvalidEmail
	}
	return email, nil
}
