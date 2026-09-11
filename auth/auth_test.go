package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/usefabrin/fabrin/mail"
)

func TestService_RequestAndVerifyWithMailCapture(t *testing.T) {
	inbox, err := mail.NewCapture(2)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore(32)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, inbox, "current", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}

	challenge, err := service.Request(t.Context(), "Alice+tag@EXAMPLE.COM", PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if challenge.ID == "" || !challenge.ExpiresAt.After(time.Now()) {
		t.Fatalf("invalid challenge: %+v", challenge)
	}
	messages := inbox.Messages()
	if len(messages) != 1 || messages[0].To != "Alice+tag@example.com" {
		t.Fatalf("messages: %+v", messages)
	}
	code := strings.TrimPrefix(messages[0].Text, "Your Fabrin sign-in code is: ")
	if len(code) != 8 {
		t.Fatalf("code %q is not eight digits", code)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("code %q is not decimal", code)
		}
	}

	authentication, err := service.Verify(t.Context(), challenge.ID, "Alice+tag@EXAMPLE.COM", code, PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if authentication.Identity.ID == "" || authentication.Identity.Email != "Alice+tag@example.com" {
		t.Fatalf("authentication: %+v", authentication)
	}
	if _, err := service.Verify(t.Context(), challenge.ID, "Alice+tag@EXAMPLE.COM", code, PurposeNative, "source-a"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("replay: %v", err)
	}
}

func TestService_ResendReplacesChallengeWithoutResettingBudgets(t *testing.T) {
	service, inbox := newService(t, 64)
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	first, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("immediate resend: %v", err)
	}

	service.now = func() time.Time { return base.Add(time.Minute) }
	second, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || len(inbox.Messages()) != 2 {
		t.Fatal("resend did not replace the challenge")
	}
	firstCode := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	if _, err := service.Verify(t.Context(), first.ID, "a@example.com", firstCode, PurposeNative, "source-a"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("replaced challenge: %v", err)
	}

	for i := range 3 {
		service.now = func() time.Time { return base.Add(time.Duration(i+2) * time.Minute) }
		if _, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a"); err != nil {
			t.Fatal(err)
		}
	}
	service.now = func() time.Time { return base.Add(5 * time.Minute) }
	if _, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("sixth send: %v", err)
	}
}

func TestService_FiveFailuresAndPurposeBinding(t *testing.T) {
	service, inbox := newService(t, 8)
	challenge, err := service.Request(t.Context(), "a@example.com", PurposeBrowser, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	if _, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeNative, "source-a"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong purpose: %v", err)
	}
	for _, guess := range []string{"", "1", "abcdefgh", "00000000"} {
		if _, err := service.Verify(t.Context(), challenge.ID, "a@example.com", guess, PurposeBrowser, "source-a"); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("wrong code: %v", err)
		}
	}
	if _, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeBrowser, "source-a"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("attempt limit: %v", err)
	}
}

func TestService_ConcurrentConsumeAndIdentityResolution(t *testing.T) {
	service, inbox := newService(t, 64)
	challenge, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	var wins atomic.Int32
	identities := make(chan Identity, 1)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Go(func() {
			<-start
			if authentication, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeNative, "source-a"); err == nil {
				wins.Add(1)
				identities <- authentication.Identity
			}
		})
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("successful consumes: %d", wins.Load())
	}
	firstIdentity := <-identities

	service.now = func() time.Time { return time.Now().Add(time.Minute) }
	next, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	nextCode := strings.TrimPrefix(inbox.Messages()[1].Text, "Your Fabrin sign-in code is: ")
	authentication, err := service.Verify(t.Context(), next.ID, "a@example.com", nextCode, PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if authentication.Identity.Email != "a@example.com" {
		t.Fatalf("authentication: %+v", authentication)
	}
	if authentication.Identity.ID != firstIdentity.ID {
		t.Fatalf("identity changed: %q then %q", firstIdentity.ID, authentication.Identity.ID)
	}
}

func TestService_BindsBrowserChallengeToOneContext(t *testing.T) {
	service, inbox := newService(t, 8)
	challenge, err := service.Request(t.Context(), "a@example.com", PurposeBrowser, "source", WithBinding("browser-a"))
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	if _, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeBrowser, "source", WithBinding("browser-b")); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("other browser binding: %v", err)
	}
	if _, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeBrowser, "source"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("missing browser binding: %v", err)
	}
	if _, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeBrowser, "source", WithBinding("browser-a")); err != nil {
		t.Fatalf("matching browser binding: %v", err)
	}
}

func TestService_ExpiryBoundaryAndNoIdentityBeforeVerification(t *testing.T) {
	store, err := NewMemoryStore(8)
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := mail.NewCapture(8)
	service, err := New(store, inbox, "current", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	challenge, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(store.identities) != 0 {
		t.Fatal("request created an identity")
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	service.now = func() time.Time { return base.Add(5 * time.Minute) }
	if _, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeNative, "source-a"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expiry boundary: %v", err)
	}
	if len(store.identities) != 0 {
		t.Fatal("failed verification created an identity")
	}
}

func TestService_UnknownChallengesConsumeSourceBudget(t *testing.T) {
	service, _ := newService(t, 8)
	for range sourceVerifyLimit {
		if _, err := service.Verify(t.Context(), "unknown", "bad email", "00000000", PurposeNative, "source-a"); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("unknown challenge: %v", err)
		}
	}
	if _, err := service.Verify(t.Context(), "unknown", "a@example.com", "00000000", PurposeNative, "source-a"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("source limit: %v", err)
	}
}

func TestMemoryStore_InvalidateDoesNotRevokeReplacement(t *testing.T) {
	store, err := NewMemoryStore(8)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	first := Reservation{ID: "first", Email: "a@example.com", KeyID: "key", Purpose: PurposeNative, IssuedAt: base, ExpiresAt: base.Add(5 * time.Minute), Source: "source"}
	second := first
	second.ID = "second"
	second.IssuedAt = base.Add(time.Minute)
	second.ExpiresAt = second.IssuedAt.Add(5 * time.Minute)
	second.Verifier[0] = 1
	if err := store.Reserve(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.Reserve(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if err := store.Invalidate(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}
	_, record, sessionErr := newSession(second.IssuedAt)
	if sessionErr != nil {
		t.Fatal(sessionErr)
	}
	identity, err := store.Verify(t.Context(), Verification{ID: second.ID, Email: second.Email, KeyID: second.KeyID, Purpose: second.Purpose, Verifier: second.Verifier, Now: second.IssuedAt, Source: "verify-source", IdentityID: "identity", Session: record})
	if err != nil || identity.ID != "identity" {
		t.Fatalf("newer challenge: identity=%+v err=%v", identity, err)
	}
}

func TestMemoryStore_FailsClosedAtCapacity(t *testing.T) {
	store, err := NewMemoryStore(1)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	first := Reservation{ID: "first", Email: "a@example.com", KeyID: "key", Purpose: PurposeNative, IssuedAt: base, ExpiresAt: base.Add(5 * time.Minute), Source: "source-a"}
	second := Reservation{ID: "second", Email: "b@example.com", KeyID: "key", Purpose: PurposeNative, IssuedAt: base, ExpiresAt: base.Add(5 * time.Minute), Source: "source-b"}
	if err := store.Reserve(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.Reserve(t.Context(), second); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("capacity: %v", err)
	}
}

func TestService_SanitizesStoreAndDeliveryErrors(t *testing.T) {
	inbox, _ := mail.NewCapture(1)
	service, err := New(errorStore{}, inbox, "current", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Request(t.Context(), "a@example.com", PurposeNative, "source-a")
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "database secret") {
		t.Fatalf("store error leaked: %v", err)
	}

	store, _ := NewMemoryStore(4)
	sender := &failingSender{err: errors.New("provider secret")}
	service, _ = New(store, sender, "current", bytes.Repeat([]byte{1}, 32))
	_, err = service.Request(t.Context(), "a@example.com", PurposeNative, "source-a")
	if !errors.Is(err, ErrDelivery) || strings.Contains(err.Error(), "provider secret") {
		t.Fatalf("delivery error leaked: %v", err)
	}
}

func TestService_DeliveryFailureInvalidatesOnlyKnownFailures(t *testing.T) {
	store, err := NewMemoryStore(16)
	if err != nil {
		t.Fatal(err)
	}
	sender := &failingSender{err: errors.New("provider rejected")}
	service, err := New(store, sender, "current", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source-a"); !errors.Is(err, ErrDelivery) {
		t.Fatalf("delivery: %v", err)
	}
	if sender.message.Text == "" {
		t.Fatal("sender did not receive message")
	}
	knownID := lastChallengeID(store)
	code := strings.TrimPrefix(sender.message.Text, "Your Fabrin sign-in code is: ")
	if _, err := service.Verify(t.Context(), knownID, "a@example.com", code, PurposeNative, "source-a"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("known failure left challenge usable: %v", err)
	}

	sender.err = context.DeadlineExceeded
	if _, err := service.Request(t.Context(), "b@example.com", PurposeNative, "source-a"); !errors.Is(err, ErrDelivery) {
		t.Fatalf("ambiguous delivery: %v", err)
	}
	ambiguousID := lastChallengeID(store)
	code = strings.TrimPrefix(sender.message.Text, "Your Fabrin sign-in code is: ")
	if _, err := service.Verify(t.Context(), ambiguousID, "b@example.com", code, PurposeNative, "source-a"); err != nil {
		t.Fatalf("ambiguous timeout invalidated challenge: %v", err)
	}
}

func TestService_RejectsUnsafeAndInvalidConfiguration(t *testing.T) {
	inbox, _ := mail.NewCapture(1)
	store, _ := NewMemoryStore(1)
	for _, tc := range []struct {
		name string
		key  []byte
		opts []Option
	}{
		{"short key", make([]byte, 31), nil},
		{"invalid key ID", make([]byte, 32), nil},
		{"capture production", make([]byte, 32), []Option{WithProduction()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyID := "current"
			if tc.name == "invalid key ID" {
				keyID = "bad\nkey"
			}
			if _, err := New(store, inbox, keyID, tc.key, tc.opts...); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	if _, err := NewMemoryStore(0); err == nil {
		t.Fatal("accepted unbounded memory store")
	}
}

func newService(t *testing.T, capacity int) (*Service, *mail.Capture) {
	t.Helper()
	store, err := NewMemoryStore(capacity)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := mail.NewCapture(capacity)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, inbox, "current", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return service, inbox
}

type failingSender struct {
	err     error
	message mail.Message
}

func (s *failingSender) Send(_ context.Context, message mail.Message) error {
	s.message = message
	return s.err
}

type errorStore struct{}

func (errorStore) Reserve(context.Context, Reservation) error { return errors.New("database secret") }
func (errorStore) Verify(context.Context, Verification) (Identity, error) {
	return Identity{}, errors.New("database secret")
}
func (errorStore) Invalidate(context.Context, string) error { return errors.New("database secret") }
func (errorStore) AuthenticateSession(context.Context, SessionProof) (Identity, error) {
	return Identity{}, errors.New("database secret")
}
func (errorStore) RevokeSession(context.Context, SessionProof) error {
	return errors.New("database secret")
}
func (errorStore) RevokeAllSessions(context.Context, SessionProof) error {
	return errors.New("database secret")
}

func lastChallengeID(store *MemoryStore) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	var latest *storedChallenge
	for _, challenge := range store.challenges {
		if latest == nil || challenge.reservation.IssuedAt.After(latest.reservation.IssuedAt) || challenge.reservation.Email > latest.reservation.Email {
			latest = challenge
		}
	}
	if latest == nil {
		return ""
	}
	return latest.reservation.ID
}
