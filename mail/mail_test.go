package mail_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/usefabrin/fabrin/mail"
)

func TestCapture_BoundedSnapshotAndDrain(t *testing.T) {
	c, err := mail.NewCapture(2)
	if err != nil {
		t.Fatal(err)
	}
	m := mail.Message{To: "person@example.com", Subject: "Sign in", Text: "Your code is 12345678"}
	if err := c.Send(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	snapshot := c.Messages()
	snapshot[0].Text = "changed"
	if c.Messages()[0].Text != m.Text {
		t.Fatal("snapshot changed captured data")
	}
	if err := c.Send(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(t.Context(), m); !errors.Is(err, mail.ErrFull) {
		t.Fatalf("full: %v", err)
	}
	if got := c.Drain(); len(got) != 2 {
		t.Fatalf("drain: %v", got)
	}
	if len(c.Messages()) != 0 {
		t.Fatal("drain did not clear inbox")
	}
	if err := c.Send(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func TestCapture_RejectsInvalidAndCancelledMessages(t *testing.T) {
	for _, n := range []int{0, -1} {
		if _, err := mail.NewCapture(n); err == nil {
			t.Fatal("accepted invalid capacity")
		}
	}
	c, err := mail.NewCapture(5)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []mail.Message{
		{To: "", Subject: "hello", Text: "body"},
		{To: "name@example.com\r\nBcc: victim@example.com", Subject: "hello", Text: "body"},
		{To: "Person <name@example.com>", Subject: "hello", Text: "body"},
		{To: "name@example.com", Subject: "hello\nBcc: victim@example.com", Text: "body"},
		{To: "name@example.com", Subject: "hello", Text: ""},
		{To: "name@example.com", Subject: strings.Repeat("x", 999), Text: "body"},
		{To: "name@example.com", Subject: "hello", Text: strings.Repeat("x", (1<<20)+1)},
		{To: "name@example.com", Subject: "hello", Text: "bad\x00body"},
		{To: "name@example.com", Subject: "bad\x00subject", Text: "body"},
		{To: "name@example.com", Subject: "hello", Text: string([]byte{0xff})},
	} {
		if err := c.Send(t.Context(), m); !errors.Is(err, mail.ErrInvalidMessage) {
			t.Fatalf("invalid: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Send(ctx, mail.Message{To: "name@example.com", Subject: "hello", Text: "body"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if len(c.Messages()) != 0 {
		t.Fatal("invalid send captured a message")
	}
	var zero mail.Capture
	if err := zero.Send(t.Context(), mail.Message{To: "name@example.com", Subject: "hello", Text: "body"}); !errors.Is(err, mail.ErrFull) {
		t.Fatalf("zero capture: %v", err)
	}
}

func TestCapture_ConcurrentSendDoesNotExceedCapacity(t *testing.T) {
	c, err := mail.NewCapture(8)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			err := c.Send(t.Context(), mail.Message{To: "name@example.com", Subject: "hello", Text: "body"})
			if err != nil && !errors.Is(err, mail.ErrFull) {
				t.Errorf("send: %v", err)
			}
			_ = c.Messages()
		})
	}
	wg.Wait()
	if len(c.Drain()) != 8 {
		t.Fatal("capacity not enforced atomically")
	}
}

func TestCapture_ConcurrentDrainReturnsEveryMessageOnce(t *testing.T) {
	const count = 32
	c, err := mail.NewCapture(count)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	batches := make(chan []mail.Message, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			<-start
			if err := c.Send(t.Context(), mail.Message{To: "name@example.com", Subject: "hello", Text: strconv.Itoa(i)}); err != nil {
				t.Errorf("send: %v", err)
			}
			batches <- c.Drain()
		})
	}
	close(start)
	wg.Wait()
	close(batches)
	seen := map[string]int{}
	for batch := range batches {
		for _, m := range batch {
			seen[m.Text]++
		}
	}
	for _, m := range c.Drain() {
		seen[m.Text]++
	}
	if len(seen) != count {
		t.Fatalf("got %d distinct messages, want %d", len(seen), count)
	}
	for i := range count {
		if seen[strconv.Itoa(i)] != 1 {
			t.Fatalf("message %d returned %d times", i, seen[strconv.Itoa(i)])
		}
	}
}
