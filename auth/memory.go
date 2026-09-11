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

type storedSession struct {
	record   SessionRecord
	identity Identity
	active   bool
}

type storedBrowserState struct{ record PreAuthRecord }

// MemoryStore is a bounded, concurrency-safe Store for tests and explicitly
// local development. Its limits are process-local and disappear on restart.
type MemoryStore struct {
	mu                                   sync.Mutex
	capacity                             int
	challenges                           map[string]*storedChallenge
	active                               map[string]string
	identities                           map[string]Identity
	sessions                             map[string]*storedSession
	browserStates                        map[string]*storedBrowserState
	addressSends, sourceSends            map[string][]time.Time
	browserBootstraps                    map[string][]time.Time
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
		sessions:            make(map[string]*storedSession),
		browserStates:       make(map[string]*storedBrowserState),
		addressSends:        make(map[string][]time.Time),
		sourceSends:         make(map[string][]time.Time),
		browserBootstraps:   make(map[string][]time.Time),
		addressFailures:     make(map[string][]time.Time),
		sourceVerifications: make(map[string][]time.Time),
	}, nil
}

// CreatePreAuth implements PreAuthStore.
func (s *MemoryStore) CreatePreAuth(ctx context.Context, record PreAuthRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record.ID == "" || record.Source == "" || len(record.Source) > 256 || record.IssuedAt.IsZero() || record.ExpiresAt.Sub(record.IssuedAt) != preAuthTTL {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(record.IssuedAt)
	budget := s.browserBootstraps[record.Source]
	if len(budget) >= 20 {
		return ErrRateLimited
	}
	if _, exists := s.browserBootstraps[record.Source]; !exists && len(s.browserBootstraps) >= s.capacity {
		return ErrUnavailable
	}
	if _, exists := s.browserStates[record.ID]; exists || (len(s.browserStates) >= s.capacity && !s.reclaimBrowserState(record.IssuedAt)) {
		return ErrUnavailable
	}
	s.browserBootstraps[record.Source] = append(budget, record.IssuedAt)
	s.browserStates[record.ID] = &storedBrowserState{record: record}
	return nil
}

// AuthenticatePreAuth implements PreAuthStore.
func (s *MemoryStore) AuthenticatePreAuth(ctx context.Context, proof PreAuthProof) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authenticateBrowserState(proof)
}

// ConsumePreAuth implements PreAuthStore.
func (s *MemoryStore) ConsumePreAuth(ctx context.Context, proof PreAuthProof) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authenticateBrowserState(proof); err != nil {
		return err
	}
	delete(s.browserStates, proof.ID)
	return nil
}

func (s *MemoryStore) authenticateBrowserState(proof PreAuthProof) error {
	state := s.browserStates[proof.ID]
	if state == nil || proof.Now.Before(state.record.IssuedAt) || !proof.Now.Before(state.record.ExpiresAt) || !hmac.Equal(state.record.Digest[:], proof.Digest[:]) || !hmac.Equal(state.record.CSRFDigest[:], proof.CSRFDigest[:]) {
		return ErrPreAuth
	}
	return nil
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
	valid := stored.reservation.Email == attempt.Email && stored.reservation.Purpose == attempt.Purpose && stored.reservation.KeyID == attempt.KeyID && hmac.Equal(stored.reservation.Verifier[:], attempt.Verifier[:]) && hmac.Equal(stored.reservation.Binding[:], attempt.Binding[:])
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

	if attempt.Session.ID == "" || attempt.Session.CreatedAt != attempt.Now || attempt.Session.LastSeenAt != attempt.Now || !attempt.Session.ExpiresAt.After(attempt.Now) {
		return Identity{}, ErrUnavailable
	}
	if _, exists := s.sessions[attempt.Session.ID]; exists || (len(s.sessions) >= s.capacity && !s.reclaimSession(attempt.Now)) {
		return Identity{}, ErrUnavailable
	}
	identity := s.identities[stored.reservation.Email]
	if identity.ID == "" {
		if len(s.identities) >= s.capacity {
			return Identity{}, ErrUnavailable
		}
		identity = Identity{ID: attempt.IdentityID, Email: stored.reservation.Email, CreatedAt: attempt.Now}
		s.identities[identity.Email] = identity
	}
	attempt.Session.IdentityID = identity.ID
	s.sessions[attempt.Session.ID] = &storedSession{record: attempt.Session, identity: identity, active: true}
	stored.active = false
	delete(s.active, activeKey)
	return identity, nil
}

// AuthenticateSession implements SessionStore.
func (s *MemoryStore) AuthenticateSession(ctx context.Context, proof SessionProof) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[proof.ID]
	if session == nil || !session.active || proof.Now.Before(session.record.CreatedAt) || !proof.Now.Before(session.record.ExpiresAt) || !proof.Now.Before(session.record.LastSeenAt.Add(sessionIdleTTL)) || !hmac.Equal(session.record.Digest[:], proof.Digest[:]) || (proof.CSRFDigest != [32]byte{} && !hmac.Equal(session.record.CSRFDigest[:], proof.CSRFDigest[:])) {
		return Identity{}, ErrSession
	}
	session.record.LastSeenAt = proof.Now
	return session.identity, nil
}

// RevokeSession implements SessionStore.
func (s *MemoryStore) RevokeSession(ctx context.Context, proof SessionProof) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[proof.ID]
	if session == nil || !session.active || !hmac.Equal(session.record.Digest[:], proof.Digest[:]) {
		return ErrSession
	}
	session.active = false
	return nil
}

// RevokeAllSessions implements SessionStore.
func (s *MemoryStore) RevokeAllSessions(ctx context.Context, proof SessionProof) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[proof.ID]
	if session == nil || !session.active || proof.Now.Before(session.record.CreatedAt) || !proof.Now.Before(session.record.ExpiresAt) || !proof.Now.Before(session.record.LastSeenAt.Add(sessionIdleTTL)) || !hmac.Equal(session.record.Digest[:], proof.Digest[:]) {
		return ErrSession
	}
	for _, candidate := range s.sessions {
		if candidate.identity.ID == session.identity.ID {
			candidate.active = false
		}
	}
	return nil
}

// RevokeIdentitySessions implements SessionStore.
func (s *MemoryStore) RevokeIdentitySessions(ctx context.Context, identityID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, candidate := range s.sessions {
		if candidate.identity.ID == identityID {
			candidate.active = false
		}
	}
	return nil
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
	for key, values := range s.browserBootstraps {
		s.browserBootstraps[key] = recent(values, now)
		if len(s.browserBootstraps[key]) == 0 {
			delete(s.browserBootstraps, key)
		}
	}
}

func (s *MemoryStore) reclaimBrowserState(now time.Time) bool {
	for id, state := range s.browserStates {
		if !now.Before(state.record.ExpiresAt) {
			delete(s.browserStates, id)
			return true
		}
	}
	return false
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

func (s *MemoryStore) reclaimSession(now time.Time) bool {
	for id, session := range s.sessions {
		if !session.active || !now.Before(session.record.ExpiresAt) || !now.Before(session.record.LastSeenAt.Add(sessionIdleTTL)) {
			delete(s.sessions, id)
			return true
		}
	}
	return false
}

var _ Store = (*MemoryStore)(nil)
var _ SessionStore = (*MemoryStore)(nil)
var _ PreAuthStore = (*MemoryStore)(nil)
