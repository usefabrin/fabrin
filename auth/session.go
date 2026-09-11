package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

const (
	sessionAbsoluteTTL = 7 * 24 * time.Hour
	sessionIdleTTL     = 24 * time.Hour
)

// ErrSession is returned for every unknown, malformed, expired, idle or
// revoked session credential.
var ErrSession = errors.New("auth: invalid session")

// Session is an opaque credential returned once after successful verification.
// Store only its digest; never log or place Credential in a URL.
type Session struct {
	Credential string
	CSRFToken  string
	ExpiresAt  time.Time
}

// Authentication is the atomic result of successful OTP verification.
type Authentication struct {
	Identity Identity
	Session  Session
}

// SessionRecord is protected state persisted in the same transaction as OTP
// consumption and identity resolution. Digest is SHA-256 of the secret bytes.
type SessionRecord struct {
	ID         string
	IdentityID string
	Digest     [32]byte
	CSRFDigest [32]byte
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// SessionProof is the bounded lookup material for authentication and logout.
type SessionProof struct {
	ID         string
	Digest     [32]byte
	CSRFDigest [32]byte
	Now        time.Time
}

// ValidateCSRF authenticates a session credential and its independent CSRF
// token while refreshing idle activity.
func (m *SessionManager) ValidateCSRF(ctx context.Context, credential, csrf string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	proof, err := sessionCSRFProof(credential, csrf, m.now().UTC())
	if err != nil {
		return ErrSession
	}
	if _, err := m.store.AuthenticateSession(ctx, proof); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return ErrSession
	}
	return nil
}

// SessionStore owns server-side session authentication, idle refresh and
// revocation. AuthenticateSession must update LastSeenAt atomically after a
// valid lookup and enforce the exclusive idle and absolute expiry boundaries.
type SessionStore interface {
	AuthenticateSession(context.Context, SessionProof) (Identity, error)
	RevokeSession(context.Context, SessionProof) error
	RevokeAllSessions(context.Context, SessionProof) error
}

// SessionManager authenticates and revokes opaque server-side sessions.
type SessionManager struct {
	store SessionStore
	now   func() time.Time
}

// NewSessionManager constructs a session manager over the supplied store.
func NewSessionManager(store SessionStore) (*SessionManager, error) {
	if store == nil {
		return nil, errors.New("auth: session store is required")
	}
	return &SessionManager{store: store, now: time.Now}, nil
}

// Current authenticates a session and refreshes its idle activity atomically.
func (m *SessionManager) Current(ctx context.Context, credential string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	proof, err := sessionProof(credential, m.now().UTC())
	if err != nil {
		return Identity{}, ErrSession
	}
	identity, err := m.store.AuthenticateSession(ctx, proof)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Identity{}, err
		}
		return Identity{}, ErrSession
	}
	return identity, nil
}

// Logout revokes the stored session before reporting success.
func (m *SessionManager) Logout(ctx context.Context, credential string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	proof, err := sessionProof(credential, m.now().UTC())
	if err != nil {
		return ErrSession
	}
	if err := m.store.RevokeSession(ctx, proof); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return ErrSession
	}
	return nil
}

// LogoutAll authenticates one session and atomically revokes every session for
// the same identity before reporting success.
func (m *SessionManager) LogoutAll(ctx context.Context, credential string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	proof, err := sessionProof(credential, m.now().UTC())
	if err != nil {
		return ErrSession
	}
	if err := m.store.RevokeAllSessions(ctx, proof); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return ErrSession
	}
	return nil
}

func newSession(now time.Time) (Session, SessionRecord, error) {
	id := make([]byte, 32)
	secret := make([]byte, 32)
	csrf := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return Session{}, SessionRecord{}, err
	}
	if _, err := rand.Read(secret); err != nil {
		return Session{}, SessionRecord{}, err
	}
	if _, err := rand.Read(csrf); err != nil {
		return Session{}, SessionRecord{}, err
	}
	encodedID := base64.RawURLEncoding.EncodeToString(id)
	encodedSecret := base64.RawURLEncoding.EncodeToString(secret)
	encodedCSRF := base64.RawURLEncoding.EncodeToString(csrf)
	expires := now.Add(sessionAbsoluteTTL)
	return Session{Credential: encodedID + "." + encodedSecret, CSRFToken: encodedCSRF, ExpiresAt: expires}, SessionRecord{ID: encodedID, Digest: sha256.Sum256(secret), CSRFDigest: sha256.Sum256(csrf), CreatedAt: now, LastSeenAt: now, ExpiresAt: expires}, nil
}

func sessionProof(credential string, now time.Time) (SessionProof, error) {
	if len(credential) != 87 || strings.Count(credential, ".") != 1 {
		return SessionProof{}, ErrSession
	}
	id, encodedSecret, _ := strings.Cut(credential, ".")
	decodedID, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(decodedID) != 32 {
		return SessionProof{}, ErrSession
	}
	secret, err := base64.RawURLEncoding.DecodeString(encodedSecret)
	if err != nil || len(secret) != 32 {
		return SessionProof{}, ErrSession
	}
	return SessionProof{ID: id, Digest: sha256.Sum256(secret), Now: now}, nil
}

func sessionCSRFProof(credential, csrf string, now time.Time) (SessionProof, error) {
	proof, err := sessionProof(credential, now)
	if err != nil {
		return SessionProof{}, err
	}
	value, err := base64.RawURLEncoding.DecodeString(csrf)
	if err != nil || len(csrf) != 43 || len(value) != 32 {
		return SessionProof{}, ErrSession
	}
	proof.CSRFDigest = sha256.Sum256(value)
	return proof, nil
}
