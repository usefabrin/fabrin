package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
)

const preAuthTTL = 10 * time.Minute

// ErrPreAuth is returned for every malformed, unknown, expired, consumed,
// or CSRF-mismatched browser pre-authentication credential.
var ErrPreAuth = errors.New("auth: invalid browser pre-authentication state")

// PreAuth contains the secrets returned once by browser bootstrap.
type PreAuth struct {
	Credential string
	CSRFToken  string
	ExpiresAt  time.Time
}

// PreAuthRecord is protected pre-authentication state supplied to a store.
// Digest and CSRFDigest are SHA-256 hashes; plaintext secrets are never stored.
type PreAuthRecord struct {
	ID, Source string
	Digest     [32]byte
	CSRFDigest [32]byte
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

// PreAuthProof is bounded lookup material for CSRF validation and
// consumption.
type PreAuthProof struct {
	ID         string
	Digest     [32]byte
	CSRFDigest [32]byte
	Now        time.Time
}

// PreAuthStore owns bounded bootstrap limits, exclusive expiry, CSRF
// validation, and one-time consumption.
type PreAuthStore interface {
	CreatePreAuth(context.Context, PreAuthRecord) error
	AuthenticatePreAuth(context.Context, PreAuthProof) error
	ConsumePreAuth(context.Context, PreAuthProof) error
}

// PreAuthManager creates and validates browser pre-authentication state.
type PreAuthManager struct {
	store PreAuthStore
	now   func() time.Time
}

// NewPreAuthManager constructs a manager over the supplied ephemeral store.
func NewPreAuthManager(store PreAuthStore) (*PreAuthManager, error) {
	if store == nil {
		return nil, errors.New("auth: browser state store is required")
	}
	return &PreAuthManager{store: store, now: time.Now}, nil
}

// Bootstrap creates an opaque ten-minute pre-authentication credential and an
// independent CSRF token after consuming the source bootstrap budget.
func (m *PreAuthManager) Bootstrap(ctx context.Context, source string) (PreAuth, error) {
	if err := ctx.Err(); err != nil {
		return PreAuth{}, err
	}
	if source == "" || len(source) > 256 {
		return PreAuth{}, ErrRateLimited
	}
	id, err := randomEncoded(32)
	if err != nil {
		return PreAuth{}, ErrUnavailable
	}
	secret, err := randomEncoded(32)
	if err != nil {
		return PreAuth{}, ErrUnavailable
	}
	csrf, err := randomEncoded(32)
	if err != nil {
		return PreAuth{}, ErrUnavailable
	}
	now := m.now().UTC()
	secretBytes, _ := base64.RawURLEncoding.DecodeString(secret)
	csrfBytes, _ := base64.RawURLEncoding.DecodeString(csrf)
	state := PreAuth{Credential: id + "." + secret, CSRFToken: csrf, ExpiresAt: now.Add(preAuthTTL)}
	record := PreAuthRecord{ID: id, Source: source, Digest: sha256.Sum256(secretBytes), CSRFDigest: sha256.Sum256(csrfBytes), IssuedAt: now, ExpiresAt: state.ExpiresAt}
	if err := m.store.CreatePreAuth(ctx, record); err != nil {
		return PreAuth{}, preAuthError(err)
	}
	return state, nil
}

// Authenticate validates a pre-authentication cookie and CSRF token without
// consuming them.
func (m *PreAuthManager) Authenticate(ctx context.Context, credential, csrf string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	proof, err := preAuthProof(credential, csrf, m.now().UTC())
	if err != nil {
		return ErrPreAuth
	}
	return preAuthError(m.store.AuthenticatePreAuth(ctx, proof))
}

// Consume validates and atomically consumes pre-authentication state.
func (m *PreAuthManager) Consume(ctx context.Context, credential, csrf string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	proof, err := preAuthProof(credential, csrf, m.now().UTC())
	if err != nil {
		return ErrPreAuth
	}
	return preAuthError(m.store.ConsumePreAuth(ctx, proof))
}

func preAuthProof(credential, csrf string, now time.Time) (PreAuthProof, error) {
	proof, err := sessionProof(credential, now)
	if err != nil {
		return PreAuthProof{}, err
	}
	csrfBytes, err := base64.RawURLEncoding.DecodeString(csrf)
	if err != nil || len(csrfBytes) != 32 || len(csrf) != 43 {
		return PreAuthProof{}, ErrPreAuth
	}
	return PreAuthProof{ID: proof.ID, Digest: proof.Digest, CSRFDigest: sha256.Sum256(csrfBytes), Now: now}, nil
}

func randomEncoded(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func preAuthError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrPreAuth), errors.Is(err, ErrRateLimited), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return ErrUnavailable
	}
}
