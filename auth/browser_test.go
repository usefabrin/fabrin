package auth

import (
	"errors"
	"testing"
	"time"
)

func TestPreAuthManager_BootstrapAuthenticateConsumeAndExpire(t *testing.T) {
	store, err := NewMemoryStore(32)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewPreAuthManager(store)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return base }
	state, err := manager.Bootstrap(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Credential) != 87 || len(state.CSRFToken) != 43 || state.ExpiresAt != base.Add(10*time.Minute) {
		t.Fatalf("state: %+v", state)
	}
	if err := manager.Authenticate(t.Context(), state.Credential, "wrong"); !errors.Is(err, ErrPreAuth) {
		t.Fatalf("wrong csrf: %v", err)
	}
	if err := manager.Authenticate(t.Context(), state.Credential, state.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err := manager.Consume(t.Context(), state.Credential, state.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err := manager.Authenticate(t.Context(), state.Credential, state.CSRFToken); !errors.Is(err, ErrPreAuth) {
		t.Fatalf("consumed state: %v", err)
	}

	state, err = manager.Bootstrap(t.Context(), "another-source")
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return base.Add(10 * time.Minute) }
	if err := manager.Authenticate(t.Context(), state.Credential, state.CSRFToken); !errors.Is(err, ErrPreAuth) {
		t.Fatalf("exclusive expiry: %v", err)
	}
}

func TestPreAuthManager_BoundsStateAndSourceBudget(t *testing.T) {
	store, err := NewMemoryStore(21)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewPreAuthManager(store)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return base }
	for range 20 {
		if _, err := manager.Bootstrap(t.Context(), "shared-source"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.Bootstrap(t.Context(), "shared-source"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("source limit: %v", err)
	}

	small, _ := NewMemoryStore(1)
	smallManager, _ := NewPreAuthManager(small)
	smallManager.now = func() time.Time { return base }
	if _, err := smallManager.Bootstrap(t.Context(), "source-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := smallManager.Bootstrap(t.Context(), "source-b"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("capacity failure: %v", err)
	}
}
