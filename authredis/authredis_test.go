package authredis_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/usefabrin/fabrin/auth"
	"github.com/usefabrin/fabrin/authredis"
	"github.com/usefabrin/fabrin/mail"
)

func TestNew_ValidatesConfigurationWithoutConnecting(t *testing.T) {
	identities := newIdentityStore()
	store, err := authredis.New("redis://127.0.0.1:1/0", identities, authredis.WithPrefix("test:auth"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		url  string
		ids  auth.IdentityStore
		opts []authredis.Option
	}{
		{name: "bad URL", url: "://", ids: identities},
		{name: "missing identities", url: "redis://127.0.0.1:6379", ids: nil},
		{name: "unsafe prefix", url: "redis://127.0.0.1:6379", ids: identities, opts: []authredis.Option{authredis.WithPrefix("has space")}},
		{name: "nil option", url: "redis://127.0.0.1:6379", ids: identities, opts: []authredis.Option{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if store, err := authredis.New(tc.url, tc.ids, tc.opts...); err == nil {
				_ = store.Close()
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestStore_RedisSharesBudgetsAndAllowsOneVerificationWinner(t *testing.T) {
	redisURL := os.Getenv("FABRINTEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("FABRINTEST_REDIS_URL not set; skipping live Redis auth test")
	}
	prefix := "fabrin:test:" + randomToken(t) + ":"
	identities := newIdentityStore()
	store, err := authredis.New(redisURL, identities, authredis.WithPrefix(prefix))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	secondStore, err := authredis.New(redisURL, identities, authredis.WithPrefix(prefix))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.Close() })
	inbox, _ := mail.NewCapture(64)
	service, err := auth.New(store, inbox, "test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	secondService, err := auth.New(secondStore, inbox, "test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	challenge, err := service.Request(t.Context(), "Alice@EXAMPLE.COM", auth.PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	code := messageCode(t, inbox.Drain())
	if _, err := secondService.Request(t.Context(), "Alice@EXAMPLE.COM", auth.PurposeNative, "source-b"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("shared address budget: %v", err)
	}

	var wins atomic.Int32
	var authentication auth.Authentication
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			<-start
			result, verifyErr := service.Verify(t.Context(), challenge.ID, "Alice@EXAMPLE.COM", code, auth.PurposeNative, "verify-source")
			if verifyErr == nil {
				wins.Add(1)
				mu.Lock()
				authentication = result
				mu.Unlock()
				return
			}
			if !errors.Is(verifyErr, auth.ErrAuthentication) {
				t.Errorf("verify: %v", verifyErr)
			}
		})
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("successful verifications: %d", wins.Load())
	}
	if identities.calls.Load() != 1 {
		t.Fatalf("identity resolutions: %d", identities.calls.Load())
	}

	manager, err := auth.NewSessionManager(secondStore)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.Current(t.Context(), authentication.Session.Credential)
	if err != nil || identity.ID != authentication.Identity.ID {
		t.Fatalf("current: identity=%+v err=%v", identity, err)
	}
	if err := manager.Logout(t.Context(), authentication.Session.Credential); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Current(t.Context(), authentication.Session.Credential); !errors.Is(err, auth.ErrSession) {
		t.Fatalf("revoked current: %v", err)
	}

	recoveryChallenge, err := service.Request(t.Context(), "recovery@example.com", auth.PurposeNative, "recovery-send")
	if err != nil {
		t.Fatal(err)
	}
	recoveryCode := messageCode(t, inbox.Drain())
	identities.failNext.Store(true)
	if _, err := service.Verify(t.Context(), recoveryChallenge.ID, "recovery@example.com", recoveryCode, auth.PurposeNative, "recovery-verify"); !errors.Is(err, auth.ErrUnavailable) {
		t.Fatalf("transient identity failure: %v", err)
	}
	if _, err := secondService.Verify(t.Context(), recoveryChallenge.ID, "recovery@example.com", recoveryCode, auth.PurposeNative, "recovery-verify"); err != nil {
		t.Fatalf("verification after lease release: %v", err)
	}

	for i := range 20 {
		if _, err := service.Request(t.Context(), fmt.Sprintf("budget-%02d@example.com", i), auth.PurposeNative, "shared-source"); err != nil {
			t.Fatalf("seed source budget %d: %v", i, err)
		}
	}
	if _, err := secondService.Request(t.Context(), "budget-over@example.com", auth.PurposeNative, "shared-source"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("shared source budget: %v", err)
	}
}

func messageCode(t *testing.T, messages []mail.Message) string {
	t.Helper()
	if len(messages) != 1 {
		t.Fatalf("captured messages: %d", len(messages))
	}
	const prefix = "Your Fabrin sign-in code is: "
	if len(messages[0].Text) != len(prefix)+8 {
		t.Fatalf("message: %q", messages[0].Text)
	}
	return messages[0].Text[len(prefix):]
}

func randomToken(t *testing.T) string {
	t.Helper()
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", value)
}

type identityStore struct {
	mu       sync.Mutex
	byEmail  map[string]auth.Identity
	calls    atomic.Int32
	failNext atomic.Bool
}

func newIdentityStore() *identityStore {
	return &identityStore{byEmail: make(map[string]auth.Identity)}
}

func (s *identityStore) ResolveVerified(_ context.Context, resolution auth.IdentityResolution) (auth.Identity, error) {
	s.calls.Add(1)
	if s.failNext.Swap(false) {
		return auth.Identity{}, errors.New("temporary identity outage")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if identity, ok := s.byEmail[resolution.Email]; ok {
		return identity, nil
	}
	identity := auth.Identity{ID: resolution.ProposedID, Email: resolution.Email, CreatedAt: resolution.VerifiedAt}
	s.byEmail[resolution.Email] = identity
	return identity, nil
}
