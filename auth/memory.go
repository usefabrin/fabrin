package auth

import (
	"context"
	"crypto/subtle"
	"sync"
	"time"
)

type memoryChallenge struct {
	id, email         string
	verifier          []byte
	issuedAt, expires time.Time
	attempts, sends   int
	attemptLimit      int
	sendLimit         int
	active            bool
}

// MemoryStore is a concurrency-safe preview and test Store. Production service
// construction rejects it because its budgets and revocation state are local to
// one process and disappear on restart.
type MemoryStore struct {
	mu       sync.Mutex
	byID     map[string]*memoryChallenge
	active   map[string]*memoryChallenge
	identity map[string]Identity
}

// NewMemoryStore returns an empty preview store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID: make(map[string]*memoryChallenge), active: make(map[string]*memoryChallenge),
		identity: make(map[string]Identity),
	}
}

func (*MemoryStore) previewOnly() {}

// Issue implements Store.
func (s *MemoryStore) Issue(ctx context.Context, id, email string, verifier []byte, issuedAt, expiresAt time.Time, attemptLimit, sendLimit int) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	challenge := &memoryChallenge{
		id: id, email: email, verifier: append([]byte(nil), verifier...),
		issuedAt: issuedAt, expires: expiresAt, attemptLimit: attemptLimit,
		sendLimit: sendLimit, sends: 1, active: true,
	}
	if previous := s.active[email]; previous != nil && issuedAt.Before(previous.expires) {
		if previous.sends >= previous.sendLimit {
			return time.Time{}, ErrRateLimited
		}
		previous.active = false
		challenge.issuedAt = previous.issuedAt
		challenge.expires = previous.expires
		challenge.attempts = previous.attempts
		challenge.sends = previous.sends + 1
		challenge.attemptLimit = previous.attemptLimit
		challenge.sendLimit = previous.sendLimit
	}
	s.byID[id] = challenge
	s.active[email] = challenge
	return challenge.expires, nil
}

// Consume implements Store.
func (s *MemoryStore) Consume(ctx context.Context, id string, verifier []byte, now time.Time, identityID string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	challenge := s.byID[id]
	if challenge == nil || !challenge.active {
		return Identity{}, ErrInvalidChallenge
	}
	if !now.Before(challenge.expires) {
		challenge.active = false
		return Identity{}, ErrExpired
	}
	if challenge.attempts >= challenge.attemptLimit {
		return Identity{}, ErrLocked
	}
	if subtle.ConstantTimeCompare(challenge.verifier, verifier) != 1 {
		challenge.attempts++
		if challenge.attempts >= challenge.attemptLimit {
			return Identity{}, ErrLocked
		}
		return Identity{}, ErrInvalidCode
	}
	challenge.active = false
	if identity, exists := s.identity[challenge.email]; exists {
		return identity, nil
	}
	identity := Identity{ID: identityID, Email: challenge.email, CreatedAt: now}
	s.identity[challenge.email] = identity
	return identity, nil
}

func (s *MemoryStore) identityCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.identity)
}
