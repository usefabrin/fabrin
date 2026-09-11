# Email OTP authentication

Fabrin now exposes the first email-code authentication core. It reserves an
eight-digit, five-minute challenge, sends it through an explicit `auth.Sender`,
and atomically consumes it through an `auth.Store`. The included
`auth.MemoryStore` supports tests and local development. In production,
`authredis.Store` provides ephemeral Redis persistence and delegates verified
identity resolution to `authpg.Store`. `mail.Capture` remains test/local delivery
only.

Successful verification atomically creates a minimal opaque server-side session.
Native bearer and secure browser-cookie handlers now cover login, current
identity, logout and logout-all. Production mail, privilege-change revocation,
cleanup, authorization and deployment evidence remain to be implemented.
`auth.WithProduction` rejects the included memory and capture backends so they
cannot be selected directly as production defaults.

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
authentication, err := service.Verify(ctx, challenge.ID, "Alice@EXAMPLE.COM", code, auth.PurposeNative, sourceKey)
if err != nil {
    return err
}
identity := authentication.Identity
session := authentication.Session.Credential // return once over a no-store native response
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

The session credential contains independent 32-byte random ID and secret parts.
The store sees only the ID and SHA-256 digest. `auth.SessionManager.Current`
checks and atomically refreshes the 24-hour idle window without extending the
seven-day absolute expiry. `Logout` revokes the stored credential before success.
All malformed, unknown, expired and revoked credentials return `auth.ErrSession`.

## Native bearer HTTP

`authhttp.Native` provides five Gin handlers without choosing URL paths for the
application:

```go
native, err := authhttp.NewNative(service, sessions)
if err != nil {
    return err
}
r.POST("/auth/native/request", native.RequestCode)
r.POST("/auth/native/verify", native.VerifyCode)
r.GET("/auth/native/current", native.Current)
r.POST("/auth/native/logout", native.Logout)
r.POST("/auth/native/logout-all", native.LogoutAll)
```

Request and verification bodies are strict JSON capped at 4 KiB. Every response
uses `Cache-Control: no-store`. Verification accepts only native-purpose
challenges and returns the bearer token once in JSON; the handlers never read or
set authentication cookies and never accept query-string tokens. `Current` and
`Logout` require exactly one `Authorization: Bearer <credential>` header, and
logout revokes the server record before returning 204.
`LogoutAll` verifies the presented session and atomically revokes every session
indexed to the same identity.

Known delivery failures and send-budget suppression return the same 202 shape as
an accepted request, using a non-verifiable random challenge ID. Store outages
return a sanitized unavailable response. The default abuse-budget source is the
direct TCP peer and ignores forwarding headers. After establishing a trusted
proxy boundary, an application can opt into its own bounded key with
`authhttp.WithSource`; request-controlled headers alone are unsafe.

Protect native application routes with `native.RequireAuth()`. A successful
check stores the verified `auth.Identity` in the standard request context:

```go
r.GET("/account", native.RequireAuth(), func(c *gin.Context) {
    identity, ok := auth.IdentityFromContext(c.Request.Context())
    // ok is true after RequireAuth.
})
```

## Browser cookie HTTP

`authhttp.Browser` provides the parallel browser flow without choosing URL
paths. Configure exact HTTPS origins and mount its CORS middleware on the auth
route group:

```go
browser, err := authhttp.NewBrowser(service, sessions, preAuth, []string{
    "https://app.example.com",
})
if err != nil {
    return err
}
web := r.Group("/auth/browser")
web.Use(browser.CORS())
web.GET("/bootstrap", browser.Bootstrap)
web.POST("/request", browser.RequestCode)
web.POST("/verify", browser.VerifyCode)
web.GET("/current", browser.Current)
web.POST("/logout", browser.Logout)
web.POST("/logout-all", browser.LogoutAll)
```

Bootstrap sets `__Host-fabrin_preauth` with `Secure`, `HttpOnly`, `Path=/` and
`SameSite=Lax`, then returns its independent CSRF token once in no-store JSON.
Send that token in `X-CSRF-Token` on request and verification. Successful
verification consumes pre-auth state, sets `__Host-fabrin_session` with the same
cookie protections, and returns a fresh session CSRF token without returning the
session credential. Use the fresh token for logout and logout-all.

Every unsafe browser request requires one allowlisted non-null `Origin`, exactly
one relevant cookie and exactly one CSRF header. CORS echoes only an exact
configured origin, enables credentials, and rejects unlisted origins and
unapproved preflight headers. Bodies are strict JSON capped at 4 KiB and all
responses are no-store. The default budget source is the direct peer; after a
trusted proxy boundary is configured, use `authhttp.WithBrowserSource` to supply
a bounded application-controlled key.

For an HTTP loopback development server only, pass
`authhttp.WithInsecureLoopback()`. The constructor then rejects every
non-loopback origin and switches to `fabrin_dev_preauth` and
`fabrin_dev_session` cookies without `Secure`. Debug mode does not enable this
behavior, and production origins remain HTTPS-only.

Use `browser.RequireAuth()` for cookie-authenticated application routes. Add
`browser.RequireCSRF()` to POST, PUT, PATCH and DELETE routes so the same exact
Origin and session token checks used by logout complete before the handler runs.
The identity is available through `auth.IdentityFromContext` as in the native
example.

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

## Production Redis and PostgreSQL stores

Applications may implement `auth.Store` using durable storage. `Reserve` must
atomically consume address/source send budgets and replace the prior active
challenge. `Verify` must atomically consume source/address attempt budgets,
compare the protected verifier, allow one successful consumer, resolve one stable
identity for the canonical email, and persist `Verification.Session` for that
identity. `Invalidate` must affect only its named
challenge so cleanup cannot revoke a newer resend.

Construct the PostgreSQL identity store first, then pass it to the Redis store.
Neither constructor connects. Register the PostgreSQL migration explicitly and
use `Ping` as a Redis readiness check. The Redis URL supports `redis://` and
TLS-enabled `rediss://`; production deployments should use transport protection
appropriate to their network and provider.

```go
identities, err := authpg.New(db, authpg.WithInvitationsRequired())
if err != nil {
    return err
}
store, err := authredis.New(redisURL, identities)
if err != nil {
    return err
}
defer store.Close()

service, err := auth.New(store, smtpSender, keyID, key, auth.WithProduction())
if err != nil {
    return err
}
```

`authredis.Store` uses Redis server time for five-minute challenges, rolling
address/source budgets, the 30-second verification lease, and session expiry.
Keys use SHA-256 digests for email and source lookups; sessions persist only the
secret digest. Independently constructed stores share the same budgets when they
use the same prefix. Use `authredis.WithPrefix` only for a non-secret deployment
namespace. V1 supports one standalone Redis primary.

Custom browser transports must bind both request and verification to the same
high-entropy pre-authentication credential with `auth.WithBinding`. The core
hashes the value before passing it to a store; memory and Redis reject a missing
or different binding as the same authentication failure. Fabrin's built-in
browser handlers manage this option with their pre-auth cookie and CSRF
state; callers should not use a user identifier or other guessable value.

`auth.NewPreAuthManager(store)` creates the built-in pre-authentication
state used before a browser session exists. `Bootstrap` returns an opaque
credential and independent CSRF token with a ten-minute expiry. Persist only the
record passed through `auth.PreAuthStore`: both secrets are already SHA-256
digests. `Authenticate` validates a cookie/token pair without consuming it so
request-code and verify-code can share one state; `Consume` validates and removes
it once login succeeds. Redis shares a twenty-per-source rolling-hour bootstrap
budget, while the memory store also fails closed at its configured capacity.

`authpg.New(db)` now supplies `auth.IdentityStore` for PostgreSQL without
connecting or changing schema. Register `authpg.Migration()` with the
application's migrations and run `./yourapp migrate` as a separate deploy step
before serving. The application imports and selects its PostgreSQL driver and
owns the `*sql.DB` lifecycle.

```go
migrations := []migrate.M{authpg.Migration()}
```

The adapter serializes resolution by canonical email, returns an existing
eligible identity on retry, consumes an invitation in the same transaction as
first identity creation, and denies disabled identities. Set
`FABRINTEST_PG_DSN` to run its live concurrency test; without that variable the
test reports an explicit skip.

The verification lease prevents concurrent winners while PostgreSQL resolves the
identity. A transient durable-store error releases the lease for retry; a worker
crash leaves it reclaimable after 30 seconds. Redis completion is idempotent so
an ambiguous client response cannot revive a consumed code. Identity-policy
rejection consumes the challenge.

Set `FABRINTEST_REDIS_URL` and `FABRINTEST_PG_DSN` to run the live adapter
tests. Privilege-change revocation, cleanup, production mail and resource
authorization remain required by the approved
[authentication contract](../AUTH_CONTRACT.md).
