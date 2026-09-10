// Package mail provides email values and a bounded capture backend for tests.
// Capture never sends mail, writes logs, or exposes an HTTP inbox.
package mail

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Message is a single-recipient plain-text email. Delivery adapters own the
// sender address and transport configuration. Text may contain secret codes;
// do not log messages or expose captured messages to HTTP clients.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Errors distinguish invalid input from a full capture inbox.
var (
	ErrInvalidMessage = errors.New("mail: invalid message")
	ErrFull           = errors.New("mail: capture inbox is full")
)

// Capture stores messages in memory for tests. It is safe for concurrent use;
// do not copy it after first use. Its zero value rejects sends as full.
// Captured secrets remain in memory until drained or the capture is released;
// draining is not a guarantee of cryptographic memory erasure.
type Capture struct {
	mu       sync.Mutex
	capacity int
	messages []Message
}

// NewCapture creates an inbox with a positive maximum message count. Each
// message is limited to a 254-byte recipient, 998-byte subject and 1 MiB body.
// Allocation grows on demand rather than reserving capacity at construction.
func NewCapture(capacity int) (*Capture, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("mail: capacity must be positive")
	}
	return &Capture{capacity: capacity}, nil
}

// Send validates and records a message without network I/O. A full inbox returns
// ErrFull without silently evicting older messages. Cancellation observed before
// insertion leaves the inbox unchanged; cancellation after insertion may race
// with successful completion, as with other context-aware operations.
func (c *Capture) Send(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validate(m); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(c.messages) >= c.capacity {
		return ErrFull
	}
	// Clone prevents a small substring retaining a caller's much larger backing
	// allocation, keeping the advertised memory bounds meaningful.
	c.messages = append(c.messages, Message{To: strings.Clone(m.To), Subject: strings.Clone(m.Subject), Text: strings.Clone(m.Text)})
	return nil
}

// Messages returns an independent snapshot in insertion order.
func (c *Capture) Messages() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Message(nil), c.messages...)
}

// Drain atomically returns all messages and empties the inbox. Returned messages
// belong to the caller and retain any secrets they contain.
func (c *Capture) Drain() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.messages
	c.messages = nil
	return out
}

func validate(m Message) error {
	if m.To == "" || len(m.To) > 254 || strings.IndexFunc(m.To, unicode.IsControl) >= 0 || !utf8.ValidString(m.To) {
		return fmt.Errorf("%w: invalid recipient", ErrInvalidMessage)
	}
	address, err := mail.ParseAddress(m.To)
	if err != nil || address.Name != "" || address.Address != m.To {
		return fmt.Errorf("%w: recipient must be a bare email address", ErrInvalidMessage)
	}
	if strings.TrimSpace(m.Subject) == "" || len(m.Subject) > 998 || strings.IndexFunc(m.Subject, unicode.IsControl) >= 0 || !utf8.ValidString(m.Subject) {
		return fmt.Errorf("%w: invalid subject", ErrInvalidMessage)
	}
	if m.Text == "" || len(m.Text) > 1<<20 || !utf8.ValidString(m.Text) || strings.ContainsRune(m.Text, 0) {
		return fmt.Errorf("%w: invalid text body", ErrInvalidMessage)
	}
	return nil
}
