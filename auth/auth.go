// Package auth provides Fabrin's email-code authentication core.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/usefabrin/fabrin/mail"
)

// Purpose separates browser and native challenges so one cannot be presented
// to the other flow.
type Purpose string

const (
	PurposeBrowser Purpose = "browser"
	PurposeNative  Purpose = "native"
)

// Public errors deliberately do not reveal why authentication failed.
var (
	ErrAuthentication   = errors.New("auth: authentication failed")
	ErrRateLimited      = errors.New("auth: rate limited")
	ErrUnavailable      = errors.New("auth: store unavailable")
	ErrDelivery         = errors.New("auth: delivery failed")
	ErrUnsafeProduction = errors.New("auth: development backend is unsafe in production")
)

// Identity is Fabrin's stable authentication subject. Applications attach
// profile and authorization data to its opaque ID.
type Identity struct {
	ID        string
	Email     string
	CreatedAt time.Time
}

// Challenge contains the non-secret values a caller needs for verification.
type Challenge struct {
	ID        string
	ExpiresAt time.Time
}

// Reservation is the protected challenge state passed to Store.Reserve.
// Verifier is an HMAC digest, never the plaintext code.
type Reservation struct {
	ID, Email, KeyID string
	Purpose          Purpose
	Verifier         [32]byte
	IssuedAt         time.Time
	ExpiresAt        time.Time
	Source           string
}

// Verification is the atomic store input for one code attempt.
type Verification struct {
	ID, Email, KeyID string
	Purpose          Purpose
	Verifier         [32]byte
	Now              time.Time
	Source           string
	IdentityID       string
	Session          SessionRecord
}

// Store owns challenge replacement, abuse budgets, attempt accounting,
// one-time consumption, unique identity resolution and initial session creation
// as atomic operations.
// Reserve must keep address/source budgets across replacement. Verify must make
// one concurrent attempt the sole winner and return ErrAuthentication for every
// credential failure. Invalidate must affect only the named challenge.
type Store interface {
	SessionStore
	Reserve(context.Context, Reservation) error
	Verify(context.Context, Verification) (Identity, error)
	Invalidate(context.Context, string) error
}

// Sender is the consumer-owned mail delivery port. mail.Capture satisfies it.
type Sender interface {
	Send(context.Context, mail.Message) error
}

// Option configures Service construction.
type Option func(*settings)

// WithProduction rejects Fabrin's in-process store and capture mail backend.
func WithProduction() Option { return func(s *settings) { s.production = true } }

type settings struct{ production bool }

// Service reserves and atomically verifies email challenges.
type Service struct {
	store  Store
	sender Sender
	keyID  string
	key    []byte
	now    func() time.Time
}

// New constructs an email-code service. key must contain at least 32 bytes;
// Service copies it before returning.
func New(store Store, sender Sender, keyID string, key []byte, options ...Option) (*Service, error) {
	if store == nil || sender == nil || !validKeyID(keyID) || len(key) < 32 {
		return nil, errors.New("auth: store, sender, key ID and a 32-byte key are required")
	}
	settings := settings{}
	for i, option := range options {
		if option == nil {
			return nil, fmt.Errorf("auth: option %d is nil", i)
		}
		option(&settings)
	}
	if settings.production {
		if _, ok := store.(*MemoryStore); ok {
			return nil, ErrUnsafeProduction
		}
		if _, ok := sender.(*mail.Capture); ok {
			return nil, ErrUnsafeProduction
		}
	}
	return &Service{store: store, sender: sender, keyID: keyID, key: append([]byte(nil), key...), now: time.Now}, nil
}

func validKeyID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// Request reserves a challenge before sending its neutral sign-in message.
// A known delivery rejection invalidates that exact reservation; cancellation
// and deadlines are ambiguous and leave it verifiable until expiry.
func (s *Service) Request(ctx context.Context, email string, purpose Purpose, source string) (Challenge, error) {
	if err := ctx.Err(); err != nil {
		return Challenge{}, err
	}
	if source == "" || len(source) > 256 {
		return Challenge{}, ErrRateLimited
	}
	now := s.now().UTC()
	c, code, err := newChallenge(s.key, email, string(purpose), now)
	if err != nil {
		return Challenge{}, ErrAuthentication
	}
	reservation := Reservation{ID: c.id, Email: c.email, KeyID: s.keyID, Purpose: purpose, Verifier: c.digest, IssuedAt: c.issued, ExpiresAt: c.expires, Source: source}
	if err := s.store.Reserve(ctx, reservation); err != nil {
		return Challenge{}, storeError(err)
	}
	message := mail.Message{To: c.email, Subject: "Your Fabrin sign-in code", Text: "Your Fabrin sign-in code is: " + code}
	deliveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.sender.Send(deliveryCtx, message); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cleanupCancel()
			if invalidateErr := s.store.Invalidate(cleanupCtx, c.id); invalidateErr != nil {
				return Challenge{}, ErrDelivery
			}
		}
		return Challenge{}, ErrDelivery
	}
	return Challenge{ID: c.id, ExpiresAt: c.expires}, nil
}

// Verify atomically consumes one attempt, resolves a stable identity and
// persists its first opaque session before returning the secret once.
func (s *Service) Verify(ctx context.Context, id, email, code string, purpose Purpose, source string) (Authentication, error) {
	if err := ctx.Err(); err != nil {
		return Authentication{}, err
	}
	if source == "" || len(source) > 256 {
		return Authentication{}, ErrRateLimited
	}
	canonical, err := canonicalEmail(email)
	if err != nil {
		// Keep the request inside the store transition so malformed credentials
		// still consume source and, when the ID exists, challenge budgets.
		canonical = ""
	}
	identityID, err := randomHex(32)
	if err != nil {
		return Authentication{}, fmt.Errorf("auth: generate identity ID: %w", err)
	}
	now := s.now().UTC()
	session, record, err := newSession(now)
	if err != nil {
		return Authentication{}, fmt.Errorf("auth: generate session: %w", err)
	}
	request := Verification{ID: id, Email: canonical, KeyID: s.keyID, Purpose: purpose, Verifier: verifier(s.key, string(purpose), id, canonical, code), Now: now, Source: source, IdentityID: identityID, Session: record}
	identity, err := s.store.Verify(ctx, request)
	if err != nil {
		return Authentication{}, storeError(err)
	}
	return Authentication{Identity: identity, Session: session}, nil
}

func storeError(err error) error {
	if errors.Is(err, ErrAuthentication) || errors.Is(err, ErrRateLimited) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ErrUnavailable
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
