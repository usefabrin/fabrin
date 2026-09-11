package auth

import (
	"context"
	"sync"
)

// CapturedMessage is one secret-bearing delivery retained by CaptureDelivery.
// It is for tests and local preview only and must never be logged.
type CapturedMessage struct {
	Email       string
	ChallengeID string
	Code        string
}

// CaptureDelivery retains codes in memory for tests and local preview.
// Production service construction rejects it.
type CaptureDelivery struct {
	mu       sync.Mutex
	messages []CapturedMessage
}

// NewCaptureDelivery returns an empty capture backend.
func NewCaptureDelivery() *CaptureDelivery { return &CaptureDelivery{} }

func (*CaptureDelivery) previewOnly() {}

// Deliver implements Delivery.
func (d *CaptureDelivery) Deliver(ctx context.Context, email, challengeID, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = append(d.messages, CapturedMessage{Email: email, ChallengeID: challengeID, Code: code})
	return nil
}

// Messages returns a copy of every captured delivery.
func (d *CaptureDelivery) Messages() []CapturedMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]CapturedMessage(nil), d.messages...)
}
