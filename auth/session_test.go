package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usefabrin/fabrin/mail"
)

func TestService_VerificationCreatesAnAtomicOpaqueSession(t *testing.T) {
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
	challenge, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	authentication, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeNative, "source")
	if err != nil {
		t.Fatal(err)
	}
	if authentication.Identity.ID == "" || authentication.Session.Credential == "" || len(authentication.Session.CSRFToken) != 43 {
		t.Fatalf("authentication: %+v", authentication)
	}
	if authentication.Session.ExpiresAt != base.Add(7*24*time.Hour) {
		t.Fatalf("absolute expiry: %v", authentication.Session.ExpiresAt)
	}
	parts := strings.Split(authentication.Session.Credential, ".")
	if len(parts) != 2 {
		t.Fatalf("credential format: %q", authentication.Session.Credential)
	}
	stored := store.sessions[parts[0]]
	proof, err := sessionProof(authentication.Session.Credential, base)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.record.Digest != proof.Digest {
		t.Fatal("session digest was not stored atomically")
	}
	csrfProof, err := sessionCSRFProof(authentication.Session.Credential, authentication.Session.CSRFToken, base)
	if err != nil {
		t.Fatal(err)
	}
	if stored.record.CSRFDigest != csrfProof.CSRFDigest {
		t.Fatal("session csrf digest was not stored atomically")
	}
	if strings.Contains(string(stored.record.Digest[:]), parts[1]) {
		t.Fatal("stored session contains plaintext credential")
	}
}

func TestSessionManager_ValidatesSessionCSRF(t *testing.T) {
	service, inbox := newService(t, 8)
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	challenge, err := service.Request(t.Context(), "a@example.com", PurposeBrowser, "source", WithBinding("browser"))
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	authentication, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeBrowser, "source", WithBinding("browser"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewSessionManager(service.store)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return base }
	if err := manager.ValidateCSRF(t.Context(), authentication.Session.Credential, "wrong"); !errors.Is(err, ErrSession) {
		t.Fatalf("wrong csrf: %v", err)
	}
	if err := manager.ValidateCSRF(t.Context(), authentication.Session.Credential, authentication.Session.CSRFToken); err != nil {
		t.Fatal(err)
	}
}

func TestSessionManager_AuthenticatesIdleExpiresAndRevokes(t *testing.T) {
	service, inbox := newService(t, 8)
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	challenge, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	authentication, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeNative, "source")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewSessionManager(service.store)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return base.Add(24*time.Hour - time.Nanosecond) }
	identity, err := manager.Current(t.Context(), authentication.Session.Credential)
	if err != nil || identity.ID != authentication.Identity.ID {
		t.Fatalf("current: identity=%+v err=%v", identity, err)
	}

	// The successful access refreshed idle activity.
	manager.now = func() time.Time { return base.Add(48*time.Hour - 2*time.Nanosecond) }
	if _, err := manager.Current(t.Context(), authentication.Session.Credential); err != nil {
		t.Fatalf("refreshed current: %v", err)
	}
	manager.now = func() time.Time { return base.Add(7 * 24 * time.Hour) }
	if _, err := manager.Current(t.Context(), authentication.Session.Credential); !errors.Is(err, ErrSession) {
		t.Fatalf("absolute expiry: %v", err)
	}

	service.now = func() time.Time { return base.Add(time.Minute) }
	challenge, _ = service.Request(t.Context(), "b@example.com", PurposeNative, "source")
	code = strings.TrimPrefix(inbox.Messages()[1].Text, "Your Fabrin sign-in code is: ")
	authentication, _ = service.Verify(t.Context(), challenge.ID, "b@example.com", code, PurposeNative, "source")
	manager.now = func() time.Time { return base.Add(time.Minute) }
	if err := manager.Logout(t.Context(), authentication.Session.Credential); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Current(t.Context(), authentication.Session.Credential); !errors.Is(err, ErrSession) {
		t.Fatalf("revoked current: %v", err)
	}
}

func TestSessionManager_RejectsMalformedCredentials(t *testing.T) {
	store, _ := NewMemoryStore(2)
	manager, err := NewSessionManager(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"", "missing-dot", ".", "a.b.c", strings.Repeat("x", 200)} {
		if _, err := manager.Current(t.Context(), credential); !errors.Is(err, ErrSession) {
			t.Fatalf("credential %q: %v", credential, err)
		}
	}
	if _, err := NewSessionManager(nil); err == nil {
		t.Fatal("accepted nil session store")
	}
}

func TestSessionManager_SanitizesStoreFailures(t *testing.T) {
	_, session, err := newSession(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	credential := session.ID + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	manager, err := NewSessionManager(errorStore{})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"current": func() error { _, err := manager.Current(t.Context(), credential); return err },
		"csrf": func() error {
			return manager.ValidateCSRF(t.Context(), credential, base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
		},
		"logout":     func() error { return manager.Logout(t.Context(), credential) },
		"logout all": func() error { return manager.LogoutAll(t.Context(), credential) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("store failure: %v", err)
			}
		})
	}
}

func TestSessionManager_LogoutAllRevokesEveryIdentitySession(t *testing.T) {
	service, inbox := newService(t, 16)
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	login := func(at time.Time) Authentication {
		t.Helper()
		service.now = func() time.Time { return at }
		challenge, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source")
		if err != nil {
			t.Fatal(err)
		}
		messages := inbox.Drain()
		code := strings.TrimPrefix(messages[0].Text, "Your Fabrin sign-in code is: ")
		result, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeNative, "source")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := login(base)
	second := login(base.Add(time.Minute))
	if first.Identity.ID != second.Identity.ID {
		t.Fatal("logins did not resolve the same identity")
	}
	manager, err := NewSessionManager(service.store)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return base.Add(2 * time.Minute) }
	if err := manager.LogoutAll(t.Context(), first.Session.Credential); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{first.Session.Credential, second.Session.Credential} {
		if _, err := manager.Current(t.Context(), credential); !errors.Is(err, ErrSession) {
			t.Fatalf("session remained active after logout-all: %v", err)
		}
	}
}

func TestSessionManager_RevokeIdentityRejectsEverySession(t *testing.T) {
	service, inbox := newService(t, 16)
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	login := func(at time.Time) Authentication {
		service.now = func() time.Time { return at }
		challenge, _ := service.Request(t.Context(), "a@example.com", PurposeNative, "source")
		code := strings.TrimPrefix(inbox.Drain()[0].Text, "Your Fabrin sign-in code is: ")
		result, err := service.Verify(t.Context(), challenge.ID, "a@example.com", code, PurposeNative, "source")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := login(base)
	second := login(base.Add(time.Minute))
	manager, _ := NewSessionManager(service.store)
	manager.now = func() time.Time { return base.Add(2 * time.Minute) }
	if err := manager.RevokeIdentity(t.Context(), first.Identity.ID); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{first.Session.Credential, second.Session.Credential} {
		if _, err := manager.Current(t.Context(), credential); !errors.Is(err, ErrSession) {
			t.Fatalf("session remained active: %v", err)
		}
	}
}

func TestService_SessionStoreFailureDoesNotConsumeChallenge(t *testing.T) {
	store, err := NewMemoryStore(1)
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := mail.NewCapture(2)
	service, err := New(store, inbox, "current", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	first, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source")
	if err != nil {
		t.Fatal(err)
	}
	firstCode := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")
	firstAuth, err := service.Verify(t.Context(), first.ID, "a@example.com", firstCode, PurposeNative, "source")
	if err != nil {
		t.Fatal(err)
	}

	service.now = func() time.Time { return base.Add(time.Minute) }
	second, err := service.Request(t.Context(), "a@example.com", PurposeNative, "source")
	if err != nil {
		t.Fatal(err)
	}
	secondCode := strings.TrimPrefix(inbox.Messages()[1].Text, "Your Fabrin sign-in code is: ")
	if _, err := service.Verify(t.Context(), second.ID, "a@example.com", secondCode, PurposeNative, "source"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("full session store: %v", err)
	}
	manager, _ := NewSessionManager(store)
	manager.now = service.now
	if err := manager.Logout(t.Context(), firstAuth.Session.Credential); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Verify(t.Context(), second.ID, "a@example.com", secondCode, PurposeNative, "source"); err != nil {
		t.Fatalf("retry after atomic failure: %v", err)
	}
}
