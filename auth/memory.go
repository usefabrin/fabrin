package auth

import (
	"context"
	"crypto/hmac"
	"errors"
	"sync"
	"time"
)

const (
	addressSendLimit   = 5
	sourceSendLimit    = 20
	challengeAttempts  = 5
	addressVerifyLimit = 20
	sourceVerifyLimit  = 100
	budgetWindow       = time.Hour
	minimumSendGap     = time.Minute
)

type storedChallenge struct {
	reservation Reservation
	attempts    int
	active      bool
}

// MemoryStore is a bounded, concurrency-safe Store for tests and explicitly
// local development. Its limits are process-local and disappear on restart.
type MemoryStore struct {
	mu                                   sync.Mutex
	capacity                             int
	challenges                           map[string]*storedChallenge
	active                               map[string]string
	identities                           map[string]Identity
	addressSends, sourceSends            map[string][]time.Time
	addressFailures, sourceVerifications map[string][]time.Time
}

// NewMemoryStore creates a development store with a positive maximum entry
// count for each challenge, identity and abuse-budget collection.
func NewMemoryStore(capacity int) (*MemoryStore, error) {
	if capacity <= 0 {
		return nil, errors.New("auth: memory-store capacity must be positive")
	}
	return &MemoryStore{
		capacity:            capacity,
		challenges:          make(map[string]*storedChallenge),
		active:              make(map[string]string),
		identities:          make(map[string]Identity),
		addressSends:        make(map[string][]time.Time),
		sourceSends:         make(map[string][]time.Time),
		addressFailures:     make(map[string][]time.Time),
		sourceVerifications: make(map[string][]time.Time),
	}, nil
}

// Reserve implements Store.
func (s *MemoryStore) Reserve(ctx context.Context, reservation Reservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := reservation.IssuedAt
	s.prune(now)
	activeKey := string(reservation.Purpose) + "\x00" + reservation.Email
	address := s.addressSends[reservation.Email]
	source := s.sourceSends[reservation.Source]
	if len(address) >= addressSendLimit || len(source) >= sourceSendLimit || (len(address) > 0 && now.Sub(address[len(address)-1]) < minimumSendGap) {
		return ErrRateLimited
	}
	if _, exists := s.challenges[reservation.ID]; exists || (len(s.challenges) >= s.capacity && !s.reclaimChallenge(now)) {
		return ErrUnavailable
	}
	if _, exists := s.addressSends[reservation.Email]; !exists && len(s.addressSends) >= s.capacity {
		return ErrUnavailable
	}
	if _, exists := s.sourceSends[reservation.Source]; !exists && len(s.sourceSends) >= s.capacity {
		return ErrUnavailable
	}

	// Consume both budgets and replace the active challenge in one critical
	// section before delivery begins.
	s.addressSends[reservation.Email] = append(address, now)
	s.sourceSends[reservation.Source] = append(source, now)
	if previousID := s.active[activeKey]; previousID != "" {
		if previous := s.challenges[previousID]; previous != nil {
			previous.active = false
		}
	}
	s.challenges[reservation.ID] = &storedChallenge{reservation: reservation, active: true}
	s.active[activeKey] = reservation.ID
	return nil
}

// Verify implements Store.
func (s *MemoryStore) Verify(ctx context.Context, attempt Verification) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.prune(attempt.Now)
	if _, exists := s.sourceVerifications[attempt.Source]; !exists && len(s.sourceVerifications) >= s.capacity {
		return Identity{}, ErrRateLimited
	}
	source := s.sourceVerifications[attempt.Source]
	if len(source) >= sourceVerifyLimit {
		return Identity{}, ErrRateLimited
	}
	s.sourceVerifications[attempt.Source] = append(source, attempt.Now)

	stored := s.challenges[attempt.ID]
	if stored == nil {
		return Identity{}, ErrAuthentication
	}
	activeKey := string(stored.reservation.Purpose) + "\x00" + stored.reservation.Email
	if !stored.active || stored.attempts >= challengeAttempts || attempt.Now.Before(stored.reservation.IssuedAt) || !attempt.Now.Before(stored.reservation.ExpiresAt) {
		return Identity{}, ErrAuthentication
	}
	valid := stored.reservation.Email == attempt.Email && stored.reservation.Purpose == attempt.Purpose && stored.reservation.KeyID == attempt.KeyID && hmac.Equal(stored.reservation.Verifier[:], attempt.Verifier[:])
	if !valid {
		if _, exists := s.addressFailures[stored.reservation.Email]; !exists && len(s.addressFailures) >= s.capacity {
			return Identity{}, ErrRateLimited
		}
		failures := s.addressFailures[stored.reservation.Email]
		if len(failures) >= addressVerifyLimit {
			return Identity{}, ErrRateLimited
		}
		s.addressFailures[stored.reservation.Email] = append(failures, attempt.Now)
		stored.attempts++
		return Identity{}, ErrAuthentication
	}

	if identity := s.identities[stored.reservation.Email]; identity.ID != "" {
		stored.active = false
		delete(s.active, activeKey)
		return identity, nil
	}
	if len(s.identities) >= s.capacity {
		return Identity{}, ErrUnavailable
	}
	identity := Identity{ID: attempt.IdentityID, Email: stored.reservation.Email, CreatedAt: attempt.Now}
	s.identities[identity.Email] = identity
	stored.active = false
	delete(s.active, activeKey)
	return identity, nil
}

// Invalidate implements Store and cannot invalidate a newer replacement.
func (s *MemoryStore) Invalidate(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.challenges[id]
	if stored == nil {
		return nil
	}
	stored.active = false
	key := string(stored.reservation.Purpose) + "\x00" + stored.reservation.Email
	if s.active[key] == id {
		delete(s.active, key)
	}
	return nil
}

func (s *MemoryStore) prune(now time.Time) {
	for key, values := range s.addressSends {
		s.addressSends[key] = recent(values, now)
		if len(s.addressSends[key]) == 0 {
			delete(s.addressSends, key)
		}
	}
	for key, values := range s.sourceSends {
		s.sourceSends[key] = recent(values, now)
		if len(s.sourceSends[key]) == 0 {
			delete(s.sourceSends, key)
		}
	}
	for key, values := range s.addressFailures {
		s.addressFailures[key] = recent(values, now)
		if len(s.addressFailures[key]) == 0 {
			delete(s.addressFailures, key)
		}
	}
	for key, values := range s.sourceVerifications {
		s.sourceVerifications[key] = recent(values, now)
		if len(s.sourceVerifications[key]) == 0 {
			delete(s.sourceVerifications, key)
		}
	}
}

func recent(values []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-budgetWindow)
	first := 0
	for first < len(values) && !values[first].After(cutoff) {
		first++
	}
	return values[first:]
}

func (s *MemoryStore) reclaimChallenge(now time.Time) bool {
	for id, stored := range s.challenges {
		if !stored.active || !now.Before(stored.reservation.ExpiresAt) {
			delete(s.challenges, id)
			key := string(stored.reservation.Purpose) + "\x00" + stored.reservation.Email
			if s.active[key] == id {
				delete(s.active, key)
			}
			return true
		}
	}
	return false
}

var _ Store = (*MemoryStore)(nil)
