package auth

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChallenge_BindsCredentialsAndConsumesOnce(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	key := bytes.Repeat([]byte{7}, 32)
	for _, changed := range []string{"key", "purpose", "id", "email", "code"} {
		t.Run(changed, func(t *testing.T) {
			c, code, err := newChallenge(key, "Alice@EXAMPLE.com", "native", now)
			if err != nil {
				t.Fatal(err)
			}
			if c.email != "Alice@example.com" || len(c.id) != 64 || len(code) != 8 {
				t.Fatal("invalid challenge representation")
			}
			k, p, id, email, guess := key, "native", c.id, c.email, code
			switch changed {
			case "key":
				k = bytes.Repeat([]byte{8}, 32)
			case "purpose":
				p = "browser"
			case "id":
				id += "x"
			case "email":
				email = "alice@example.com"
			case "code":
				guess = wrongCode(code)
			}
			if c.consume(k, p, id, email, guess, now) {
				t.Fatal("accepted mismatched credentials")
			}
			if !c.consume(key, "native", c.id, c.email, code, now) {
				t.Fatal("valid code rejected")
			}
			if c.consume(key, "native", c.id, c.email, code, now) {
				t.Fatal("accepted replay")
			}
		})
	}
}

func TestChallenge_ExpiryAndAttemptBoundary(t *testing.T) {
	now := time.Now()
	key := bytes.Repeat([]byte{1}, 32)
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		allowed bool
	}{{"before", 5*time.Minute - time.Nanosecond, true}, {"at", 5 * time.Minute, false}, {"after", 6 * time.Minute, false}, {"before issuance", -time.Nanosecond, false}} {
		t.Run(tc.name, func(t *testing.T) {
			c, code, err := newChallenge(key, "a@example.com", "native", now)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.consume(key, "native", c.id, c.email, code, now.Add(tc.elapsed)); got != tc.allowed {
				t.Fatalf("accepted=%t", got)
			}
		})
	}
	for _, failures := range []int{4, 5, 6} {
		c, code, err := newChallenge(key, "a@example.com", "native", now)
		if err != nil {
			t.Fatal(err)
		}
		for range failures {
			if c.consume(key, "native", c.id, c.email, wrongCode(code), now) {
				t.Fatal("accepted wrong code")
			}
		}
		if got := c.consume(key, "native", c.id, c.email, code, now); got != (failures < 5) {
			t.Fatalf("attempt boundary with %d failures: %t", failures, got)
		}
	}
}

func TestChallenge_ConcurrentVerificationHasOneWinner(t *testing.T) {
	now := time.Now()
	key := bytes.Repeat([]byte{3}, 32)
	c, code, err := newChallenge(key, "a@example.com", "native", now)
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Go(func() {
			<-start
			if c.consume(key, "native", c.id, c.email, code, now) {
				wins.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("winners: %d", wins.Load())
	}
}

func TestChallenge_RejectsInvalidInputsAndCanonicalizesEmail(t *testing.T) {
	for _, email := range []string{"", "Name <a@example.com>", "a@example.com\n", "á@example.com", "a@éxample.com", " a@example.com", strings.Repeat("a", 255) + "@example.com"} {
		if _, err := canonicalEmail(email); err == nil {
			t.Fatalf("accepted %q", email)
		}
	}
	for _, email := range []string{"First.Last+tag@EXAMPLE.COM", "a@example.com"} {
		got, err := canonicalEmail(email)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(email, "@")
		if got != parts[0]+"@"+strings.ToLower(parts[1]) {
			t.Fatal("changed local part")
		}
	}
	for _, tc := range []struct {
		key     []byte
		purpose string
	}{{nil, "native"}, {make([]byte, 31), "native"}, {make([]byte, 32), ""}, {make([]byte, 32), "recovery"}} {
		if _, _, err := newChallenge(tc.key, "a@example.com", tc.purpose, time.Now()); err == nil {
			t.Fatal("accepted invalid parameters")
		}
	}
}

func TestChallenge_VerifierUsesUnambiguousFieldEncoding(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	if verifier(key, "ab", "c", "d", "e") == verifier(key, "a", "bc", "d", "e") {
		t.Fatal("ambiguous encoding")
	}
	c, code, err := newChallenge(key, "a@example.com", "native", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if c.digest != verifier(key, "native", c.id, c.email, code) {
		t.Fatal("wrong stored verifier")
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatal("code is not decimal")
		}
	}
	other, _, err := newChallenge(key, "a@example.com", "native", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if c.id == other.id {
		t.Fatal("reused challenge ID")
	}
}

func wrongCode(code string) string {
	// Preserve the format while guaranteeing a different value, including zero.
	return string('0'+(code[0]-'0'+1)%10) + code[1:]
}

func TestChallenge_ConcurrentFailuresExhaustAttempts(t *testing.T) {
	now := time.Now()
	key := bytes.Repeat([]byte{9}, 32)
	c, code, err := newChallenge(key, "a@example.com", "native", now)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			if c.consume(key, "native", c.id, c.email, wrongCode(code), now) {
				t.Error("accepted incorrect code")
			}
		})
	}
	close(start)
	wg.Wait()
	if c.consume(key, "native", c.id, c.email, code, now) {
		t.Fatal("concurrent failures did not exhaust attempts")
	}
}

func TestChallenge_MalformedGuessesConsumeAttempts(t *testing.T) {
	now := time.Now()
	key := bytes.Repeat([]byte{5}, 32)
	c, code, err := newChallenge(key, "a@example.com", "native", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, guess := range []string{"", "1234567", "123456789", "abcdefgh", "１２３４"} {
		if c.consume(key, "native", c.id, c.email, guess, now) {
			t.Fatal("accepted malformed code")
		}
	}
	if c.consume(key, "native", c.id, c.email, code, now) {
		t.Fatal("malformed attempts did not count")
	}
}
