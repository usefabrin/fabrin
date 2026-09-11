# Email OTP core preview

Fabrin now exposes the first email-code authentication core. It reserves an
eight-digit, five-minute challenge, sends it through an explicit `auth.Sender`,
and atomically consumes it through an `auth.Store`. The included
`auth.MemoryStore` and `mail.Capture` make this flow runnable in tests and local
development.

This is not a login endpoint or a production authentication stack. Browser and
native HTTP routes, pre-auth CSRF state, durable PostgreSQL storage, invitations,
disabled-identity policy, sessions, production mail and authorization remain to
be implemented. `auth.WithProduction` rejects the included memory and capture
backends so they cannot be selected directly as production defaults.

## Run the local flow

```go
key := configuredAuthKey // at least 32 random bytes, loaded outside source control
store, err := auth.NewMemoryStore(1_000)
if err != nil {
    return err
}
inbox, err := mail.NewCapture(100)
if err != nil {
    return err
}
service, err := auth.New(store, inbox, "local-1", key)
if err != nil {
    return err
}

challenge, err := service.Request(ctx, "Alice@EXAMPLE.COM", auth.PurposeNative, sourceKey)
if err != nil {
    return err
}
message := inbox.Messages()[0]
// Test code extracts the eight digits from message.Text.
identity, err := service.Verify(ctx, challenge.ID, "Alice@EXAMPLE.COM", code, auth.PurposeNative, sourceKey)
```

The example assumes imports for `fabrin/auth` and `fabrin/mail`. Use a stable,
nonempty source key derived from trusted server configuration and connection
metadata. Request headers alone are not a trusted source identity. Keep the HMAC
key outside the database and supply at least 32 random bytes. The key ID is stored
with reservations for explicit rotation; this slice accepts the active key only.

`mail.Capture` satisfies `auth.Sender` directly. Production adapters implement
the same single-method interface with a deadline-aware `Send` method. The service
adds a ten-second delivery deadline, stores only the HMAC verifier, invalidates an
exact challenge after a known provider rejection, and leaves an ambiguously timed
out delivery verifiable until it expires.

## Memory-store behavior

The memory store is concurrency-safe and bounded by the capacity passed to
`NewMemoryStore`. It enforces the approved local defaults: one send per address
per minute, five sends per address per rolling hour, twenty sends per source per
rolling hour, five attempts per challenge, twenty failed verifications per
address per rolling hour, and one hundred verification attempts per source per
rolling hour. Address budgets span browser and native purposes. A resend replaces
the active challenge for that address and purpose without clearing budgets.

Unknown challenge IDs consume source verification budget. Replaced, expired,
spent, malformed, wrong-purpose and wrong-code attempts all return
`auth.ErrAuthentication`; callers cannot distinguish them. Store failures become
`auth.ErrUnavailable`, delivery failures become `auth.ErrDelivery`, and raw
provider/store errors are not returned.

The capacity is a bound for each internal collection rather than a user count.
When a new challenge or budget key cannot be represented, the store fails closed.
State is local to one process and disappears on restart, so its limits are not
shared across replicas. Use it only for tests or an explicitly local tool.

## Store contract and remaining transaction

Applications may implement `auth.Store` using durable storage. `Reserve` must
atomically consume address/source send budgets and replace the prior active
challenge. `Verify` must atomically consume source/address attempt budgets,
compare the protected verifier, allow one successful consumer, and resolve one
stable identity for the canonical email. `Invalidate` must affect only its named
challenge so cleanup cannot revoke a newer resend.

The approved [authentication contract](../AUTH_CONTRACT.md) additionally requires
identity eligibility and session creation in the same transaction as challenge
consumption. This preview stops before sessions, so it is not enough to implement
a production store or expose `Request`/`Verify` directly as public HTTP handlers.
The next integration slice supplies that complete transaction and the transport's
generic public response rules.
