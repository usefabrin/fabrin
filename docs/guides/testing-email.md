# Test email without sending it

`fabrin/mail` currently provides plain-text email values and a bounded in-memory
capture backend. **It does not send real email, implement SMTP, or provide login.**
Use it to test code that sends messages through a locally declared interface.
See [v1 progress](../V1_PLAN.md) for production email and auth status.

## Wire a capture backend

In the module that needs mail, declare only the method it uses:

```go
type Sender interface {
    Send(context.Context, mail.Message) error
}
```

Pass a capture backend from your test or application wiring:

```go
inbox, err := mail.NewCapture(10)
if err != nil {
    return err
}
err = inbox.Send(ctx, mail.Message{
    To:      "person@example.com",
    Subject: "Sign in",
    Text:    "This is a test message.",
})
if err != nil {
    return err
}
messages := inbox.Drain()
// Assert recipients, subjects and body contents in tests.
```

These snippets assume imports of `context` and
`github.com/usefabrin/fabrin/mail`, and an error-returning function where shown.
The executable [package example](../../mail/example_test.go) demonstrates the
complete capture lifecycle. No app registration or configuration environment
variable is required; capture is an explicitly passed dependency.

## Limits and errors

Choose a positive inbox capacity. Once full, `Send` returns `mail.ErrFull` without
evicting messages or pretending delivery succeeded. Drain the inbox to reclaim
capacity, or create a new capture for each test. Allocation grows as messages
arrive. Each message is bounded to:

- One bare recipient address, at most 254 bytes; display names are rejected.
- A nonblank subject, at most 998 bytes, with no control characters.
- A nonempty valid UTF-8 text body, at most 1 MiB, without NUL bytes.

Recipient controls and invalid UTF-8 are rejected. Body line breaks are allowed.
The capture validates syntax but does not check whether a mailbox exists or can
receive mail. This is a plain-text single-recipient API: no attachments, HTML,
CC/BCC, sender override, or arbitrary headers are implemented.

`errors.Is(err, mail.ErrInvalidMessage)` identifies validation errors, and
`errors.Is(err, mail.ErrFull)` identifies capacity exhaustion. Validation errors
name the invalid part without echoing recipient, subject or body content.
Cancelled contexts return their context error without insertion when cancellation
is observed before recording; cancellation can race with a successful send.

## Concurrency and secrets

`Send`, `Messages`, and `Drain` are safe for concurrent use. Do not copy `Capture`
after first use. `Messages` returns an independent snapshot; editing it does not
change the inbox. `Drain` atomically removes and returns the entire current inbox.
Concurrent sends are ordered by insertion, not by goroutine creation order.
The zero value has no capacity and returns `ErrFull` for otherwise valid sends.

Capture never sends over the network, writes logs, persists messages or mounts
HTTP routes. A captured OTP would still be a secret in process memory. Do not
expose this inbox over a public HTTP endpoint, print captured OTPs in production
logs, or fall back to capture when a production mail transport fails. Returned
snapshots/drained messages retain their strings; draining is not secure erasure.

Run `go test -race ./mail` from the framework checkout to exercise bounds,
validation, cancellation, concurrent send/snapshot behavior, and the example.
