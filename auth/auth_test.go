package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type storeFunc struct {
	issue   func(context.Context, string, string, []byte, time.Time, time.Time, int, int) (time.Time, error)
	consume func(context.Context, string, []byte, time.Time, string) (Identity, error)
}

func (s storeFunc) Issue(ctx context.Context, id, email string, verifier []byte, issuedAt, expiresAt time.Time, attemptLimit, sendLimit int) (time.Time, error) {
	return s.issue(ctx, id, email, verifier, issuedAt, expiresAt, attemptLimit, sendLimit)
}

func (s storeFunc) Consume(ctx context.Context, id string, verifier []byte, now time.Time, identityID string) (Identity, error) {
	return s.consume(ctx, id, verifier, now, identityID)
}

type deliveryFunc func(context.Context, string, string, string) error

func (f deliveryFunc) Deliver(ctx context.Context, email, challengeID, code string) error {
	return f(ctx, email, challengeID, code)
}

func TestService_VerifiesOnceAndCreatesIdentityOnlyOnSuccess(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	delivery := NewCaptureDelivery()
	service := newService(t, store, delivery)

	challenge, err := service.Request(t.Context(), " User@Example.COM ")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if store.identityCount() != 0 {
		t.Fatal("Request created an identity before email verification")
	}
	messages := delivery.Messages()
	if len(messages) != 1 || messages[0].ChallengeID != challenge.ID || messages[0].Code == "" {
		t.Fatalf("captured messages = %#v, want the challenge and a code", messages)
	}

	identity, err := service.Verify(t.Context(), challenge.ID, messages[0].Code)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.Email != "user@example.com" || identity.ID == "" {
		t.Errorf("identity = %#v", identity)
	}
	if store.identityCount() != 1 {
		t.Fatalf("identities = %d, want 1", store.identityCount())
	}
	if _, err := service.Verify(t.Context(), challenge.ID, messages[0].Code); !errors.Is(err, ErrInvalidChallenge) {
		t.Errorf("replay error = %v, want ErrInvalidChallenge", err)
	}
}

func TestService_ResolvesOneIdentityAcrossChallengeWindows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	delivery := NewCaptureDelivery()
	service := newService(t, store, delivery, WithChallengeTTL(time.Minute))
	service.now = func() time.Time { return now }

	first, err := service.Request(t.Context(), "same@example.com")
	if err != nil {
		t.Fatalf("first Request: %v", err)
	}
	firstIdentity, err := service.Verify(t.Context(), first.ID, delivery.Messages()[0].Code)
	if err != nil {
		t.Fatalf("first Verify: %v", err)
	}

	now = now.Add(2 * time.Minute)
	second, err := service.Request(t.Context(), "same@example.com")
	if err != nil {
		t.Fatalf("second Request: %v", err)
	}
	secondIdentity, err := service.Verify(t.Context(), second.ID, delivery.Messages()[1].Code)
	if err != nil {
		t.Fatalf("second Verify: %v", err)
	}
	if secondIdentity != firstIdentity {
		t.Errorf("second identity = %#v, want stable %#v", secondIdentity, firstIdentity)
	}
	if store.identityCount() != 1 {
		t.Errorf("identities = %d, want 1", store.identityCount())
	}
}

func TestService_ConcurrentVerificationHasOneWinner(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	delivery := NewCaptureDelivery()
	service := newService(t, store, delivery)
	challenge, err := service.Request(t.Context(), "race@example.com")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	code := delivery.Messages()[0].Code

	start := make(chan struct{})
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := service.Verify(context.Background(), challenge.ID, code); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrInvalidChallenge) {
				t.Errorf("Verify: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Errorf("successful verifications = %d, want 1", got)
	}
	if store.identityCount() != 1 {
		t.Errorf("identities = %d, want 1", store.identityCount())
	}
}

func TestService_ExpiryAttemptsAndResendShareOneBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	delivery := NewCaptureDelivery()
	service := newService(t, store, delivery, WithAttemptLimit(2), WithSendLimit(2), WithChallengeTTL(time.Minute))
	service.now = func() time.Time { return now }

	first, err := service.Request(t.Context(), "budget@example.com")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	firstCode := delivery.Messages()[0].Code
	now = now.Add(10 * time.Second)
	second, err := service.Request(t.Context(), "budget@example.com")
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	secondCode := delivery.Messages()[1].Code
	if first.ID == second.ID || firstCode == secondCode {
		t.Fatal("resend did not rotate both id and code")
	}
	if !second.ExpiresAt.Equal(first.ExpiresAt) {
		t.Errorf("resend expiry = %s, want original %s", second.ExpiresAt, first.ExpiresAt)
	}
	if _, err := service.Verify(t.Context(), first.ID, firstCode); !errors.Is(err, ErrInvalidChallenge) {
		t.Errorf("old-code error = %v, want ErrInvalidChallenge", err)
	}
	if _, err := service.Request(t.Context(), "budget@example.com"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("third send error = %v, want ErrRateLimited", err)
	}
	if _, err := service.Verify(t.Context(), second.ID, "wrong-one"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("first bad code error = %v, want ErrInvalidCode", err)
	} else if errors.Is(err, ErrStore) {
		t.Errorf("invalid code was misclassified as a store failure: %v", err)
	}
	if _, err := service.Verify(t.Context(), second.ID, "wrong-two"); !errors.Is(err, ErrLocked) {
		t.Errorf("second bad code error = %v, want ErrLocked", err)
	}
	if _, err := service.Verify(t.Context(), second.ID, secondCode); !errors.Is(err, ErrLocked) {
		t.Errorf("correct code after lock error = %v, want ErrLocked", err)
	}

	now = now.Add(2 * time.Minute)
	third, err := service.Request(t.Context(), "budget@example.com")
	if err != nil {
		t.Fatalf("request after window: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := service.Verify(t.Context(), third.ID, delivery.Messages()[2].Code); !errors.Is(err, ErrExpired) {
		t.Errorf("expired error = %v, want ErrExpired", err)
	}
}

func TestService_ProtectsVerifierAndGeneratesHighEntropyCode(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	delivery := NewCaptureDelivery()
	service := newService(t, store, delivery)
	challenge, err := service.Request(t.Context(), "secret@example.com")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	message := delivery.Messages()[0]
	if len(message.Code) != 16 {
		t.Errorf("code length = %d, want 16 base32 characters (80 random bits)", len(message.Code))
	}
	store.mu.Lock()
	record := store.byID[challenge.ID]
	stored := string(record.verifier)
	store.mu.Unlock()
	if stored == message.Code || strings.Contains(stored, message.Code) {
		t.Fatal("store retained the delivered code instead of a protected verifier")
	}
}

func TestService_PreservesStoreDeliveryAndCancellationFailures(t *testing.T) {
	t.Parallel()

	storeFailure := errors.New("database unavailable")
	store := storeFunc{
		issue: func(context.Context, string, string, []byte, time.Time, time.Time, int, int) (time.Time, error) {
			return time.Time{}, storeFailure
		},
		consume: func(context.Context, string, []byte, time.Time, string) (Identity, error) {
			return Identity{}, storeFailure
		},
	}
	service := newService(t, store, deliveryFunc(func(context.Context, string, string, string) error { return nil }))
	if _, err := service.Request(t.Context(), "store@example.com"); !errors.Is(err, ErrStore) || !errors.Is(err, storeFailure) {
		t.Errorf("store error = %v, want ErrStore and cause", err)
	}

	deliveryFailure := errors.New("mail unavailable")
	memory := NewMemoryStore()
	service = newService(t, memory, deliveryFunc(func(context.Context, string, string, string) error { return deliveryFailure }))
	if _, err := service.Request(t.Context(), "delivery@example.com"); !errors.Is(err, ErrDelivery) || !errors.Is(err, deliveryFailure) {
		t.Errorf("delivery error = %v, want ErrDelivery and cause", err)
	}
	if memory.identityCount() != 0 {
		t.Fatal("delivery failure created an identity")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Request(ctx, "cancel@example.com"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled request error = %v, want context.Canceled", err)
	}
}

func TestService_RejectsInvalidEmailBeforeCallingBackends(t *testing.T) {
	t.Parallel()

	called := false
	store := storeFunc{
		issue: func(context.Context, string, string, []byte, time.Time, time.Time, int, int) (time.Time, error) {
			called = true
			return time.Time{}, nil
		},
		consume: func(context.Context, string, []byte, time.Time, string) (Identity, error) {
			called = true
			return Identity{}, nil
		},
	}
	delivery := deliveryFunc(func(context.Context, string, string, string) error {
		called = true
		return nil
	})
	service := newService(t, store, delivery)
	for _, email := range []string{"", "missing-at.example.com", "a@", "a@b@example.com", "a@example.com\r\nBcc: victim@example.com"} {
		if _, err := service.Request(t.Context(), email); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("Request(%q) error = %v, want ErrInvalidEmail", email, err)
		}
	}
	if called {
		t.Fatal("invalid email reached a backend")
	}
}

func TestNew_ValidatesDependenciesKeyAndOptions(t *testing.T) {
	t.Parallel()

	key := []byte("0123456789abcdef0123456789abcdef")
	store := NewMemoryStore()
	delivery := NewCaptureDelivery()
	tests := []struct {
		name     string
		store    Store
		delivery Delivery
		key      []byte
		options  []Option
	}{
		{name: "nil store", delivery: delivery, key: key},
		{name: "nil delivery", store: store, key: key},
		{name: "short key", store: store, delivery: delivery, key: []byte("short")},
		{name: "nil option", store: store, delivery: delivery, key: key, options: []Option{nil}},
		{name: "zero TTL", store: store, delivery: delivery, key: key, options: []Option{WithChallengeTTL(0)}},
		{name: "zero attempts", store: store, delivery: delivery, key: key, options: []Option{WithAttemptLimit(0)}},
		{name: "zero sends", store: store, delivery: delivery, key: key, options: []Option{WithSendLimit(0)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(test.store, test.delivery, test.key, test.options...); err == nil {
				t.Fatal("New succeeded, want validation error")
			}
		})
	}
}

func TestNew_RejectsPreviewBackendsInProduction(t *testing.T) {
	t.Parallel()

	safeStore := storeFunc{}
	safeDelivery := deliveryFunc(func(context.Context, string, string, string) error { return nil })
	tests := []struct {
		name     string
		store    Store
		delivery Delivery
	}{
		{name: "memory store", store: NewMemoryStore(), delivery: safeDelivery},
		{name: "capture delivery", store: safeStore, delivery: NewCaptureDelivery()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(test.store, test.delivery, []byte("0123456789abcdef0123456789abcdef"), WithProduction())
			if !errors.Is(err, ErrUnsafeProduction) {
				t.Errorf("New production preview error = %v, want ErrUnsafeProduction", err)
			}
		})
	}
}

func newService(t *testing.T, store Store, delivery Delivery, options ...Option) *Service {
	t.Helper()
	service, err := New(store, delivery, []byte("0123456789abcdef0123456789abcdef"), options...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}
