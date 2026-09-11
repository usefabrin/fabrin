// Package auth contains the private email-code verification proof. It does not
// yet expose authentication APIs, identities, sessions or HTTP handlers.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"sync"
	"time"
)

// challenge deliberately holds neither the code nor its HMAC key. This private
// state machine proves the cryptographic and single-use behavior before a store
// contract combines consumption, abuse budgets, identity and session creation.
// Never use it as a standalone production authentication store.
type challenge struct {
	mu              sync.Mutex
	id, email       string
	digest          [32]byte
	issued, expires time.Time
	attempts        int
	spent           bool
}

func newChallenge(key []byte, email, purpose string, now time.Time) (*challenge, string, error) {
	if len(key) < 32 || (purpose != "native" && purpose != "browser") {
		return nil, "", errors.New("auth: invalid challenge configuration")
	}
	canonical, err := canonicalEmail(email)
	if err != nil {
		return nil, "", err
	}
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, "", fmt.Errorf("auth: generate challenge ID: %w", err)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		return nil, "", fmt.Errorf("auth: generate code: %w", err)
	}
	code := fmt.Sprintf("%08d", n.Int64())
	c := &challenge{id: hex.EncodeToString(id[:]), email: canonical, issued: now, expires: now.Add(5 * time.Minute), attempts: 5}
	c.digest = verifier(key, purpose, c.id, c.email, code)
	return c, code, nil
}

// consume serializes attempts and the successful transition. Caller-provided
// time makes boundary tests deterministic; production stores will supply their
// trusted clock. Failed guesses consume budget even when their format is wrong.
func (c *challenge) consume(key []byte, purpose, id, email, code string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spent || c.attempts <= 0 || now.Before(c.issued) || !now.Before(c.expires) {
		return false
	}
	c.attempts--
	if len(key) < 32 || len(code) != 8 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	candidate := verifier(key, purpose, id, email, code)
	if !hmac.Equal(candidate[:], c.digest[:]) {
		return false
	}
	c.spent = true
	return true
}

func verifier(key []byte, fields ...string) [32]byte {
	h := hmac.New(sha256.New, key)
	var length [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(field)) // hash.Hash.Write never errors.
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func canonicalEmail(value string) (string, error) {
	invalid := errors.New("auth: invalid email address")
	if len(value) == 0 || len(value) > 254 {
		return "", invalid
	}
	for _, r := range value {
		if r <= 32 || r >= 127 {
			return "", invalid
		}
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Name != "" || address.Address != value {
		return "", invalid
	}
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 {
		return "", invalid
	}
	return value[:at+1] + strings.ToLower(value[at+1:]), nil
}
