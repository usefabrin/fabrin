# Authentication and session threat model

- **Status:** Approved for implementation
- **Date:** 2026-09-11
- **Issue:** [#80](https://github.com/usefabrin/fabrin/issues/80)
- **Applies to:** v1 email OTP, browser sessions, native bearer sessions,
  authorization, recovery, and future password support

This document is the approved security contract auth code must satisfy. It names the
assets, trust boundaries, attacker capabilities, defaults, failure behavior, and
the executable acceptance rows that hold each claim up. Issue #110 implements
the email-OTP core; production storage, transport, sessions, authorization, and
the remaining acceptance rows still require their own implementation and review.

## Scope and assets

Fabrin protects:

- identity records and the binding between an identity and a verified email;
- one-time challenge secrets, protected verifiers, attempt/send budgets, and
  expiry;
- browser and native session credentials, server-side session state, CSRF
  secrets, and revocation state;
- group, permission, and resource-ownership decisions;
- recovery and invitation state;
- signing, hashing, and encryption keys; and
- audit records that show security-relevant events without containing secrets.

V1 authenticates with email OTP. Passwords, social login, and passkeys are
deferred. Password threats remain specified because #80 requires the future
default to be decided before a password API can appear; they do not authorize a
password implementation in v1.

Application profile data is outside the framework identity row. Applications
relate their own profile table to the stable Fabrin identity identifier. A
verified email is an authentication identifier, not permission to access an
application resource.

## Trust boundaries and attacker model

Untrusted inputs include every HTTP field and header, email address, OTP value,
cookie, bearer token, forwarded client address, redirect target, resource key,
store result, delivery error, and configuration value originating outside the
process.

The attacker may:

- send concurrent, reordered, replayed, and malformed requests;
- know whether an email belongs to a real person and try to learn whether it has
  an account;
- read a delivered OTP from a compromised mailbox and race its owner;
- set cookies before login, steal a session credential, or submit a cross-site
  browser request;
- distribute attempts across processes and addresses;
- trigger store, delivery, clock, and key-provider failures;
- inspect ordinary application responses and timing at network granularity; and
- control resource identifiers while authenticated as another identity.

Fabrin does not claim to protect a host where the application process, database,
or active signing keys are fully compromised. It does limit the value of a
database-only disclosure by storing digests of OTP and bearer secrets, and it
supports key rotation and session revocation for recovery after a secret leak.
Email delivery confidentiality is bounded by the configured provider and the
recipient mailbox.

## Security defaults

### Email OTP

Challenge secrets come from `crypto/rand`. The delivered code has at least 64
bits of entropy; short decimal codes are not an acceptable default. Only a
domain-separated keyed digest is stored. Logs, errors, metrics, and audit events
never contain the code, verifier, session credential, raw bearer token, or
secret key.

A challenge has one normalized email, purpose, issued time, expiry, send count,
attempt count, consumed time, and invalidated time. The default lifetime is ten
minutes, with at most five verification attempts and three sends in that
window. Applications may tighten these bounds. Increasing them requires an
explicit option and documentation because it weakens brute-force resistance.

Issuing and resending use a distributed store budget keyed by normalized email,
purpose, and a privacy-preserving network bucket. A resend rotates the secret
and invalidates the prior verifier. It does not reset the first-issued time,
attempt count, or send count. Concurrent verify calls atomically compare,
consume, and create or resolve the identity in one store transaction. Exactly
one caller can succeed. No identity is created before successful consumption.

Request and verify responses are deliberately uniform for known, unknown,
invited, blocked, and already-registered addresses where revealing the state is
not required to continue. Status, response schema, and externally observable
work stay equivalent at network granularity. Delivery failure and storage
failure return a generic unavailable response and do not claim a code was sent.
Whether operators receive the underlying cause is an audit/logging decision,
never response content.

The capture delivery and in-memory store are test and local-preview only.
Construction in production mode fails if either is selected.

### Browser sessions and CSRF

Successful authentication always creates a new session identifier; a
pre-authentication identifier is never promoted. Privilege changes rotate the
identifier again. Session identifiers contain at least 256 random bits and only
a keyed digest is stored server-side.

The default browser cookie is `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`, has
no `Domain`, and uses the `__Host-` prefix. Its lifetime never exceeds the
server-side session expiry. Production construction fails when transport
settings would require sending this cookie over cleartext HTTP. Cross-site use
requires an explicit secure-cookie and CSRF policy; it cannot be enabled by
silently weakening SameSite.

Every state-changing cookie-authenticated request requires a CSRF token bound to
the session and checked with a constant-time comparison. Safe methods do not
mutate auth state. Login, logout, recovery, and invitation acceptance receive
the same CSRF treatment as application writes. Origin/Referer validation is a
defense in depth check, not the only token mechanism.

Logout atomically revokes the server-side session before expiring the cookie.
Global logout and security-sensitive identity changes revoke every session for
the identity. A store outage fails authentication closed; cached success may not
outlive its explicit, short validation lifetime.

### Native bearer sessions

Native clients receive a high-entropy opaque bearer credential once. Only its
key identifier and keyed digest are stored. Bearer credentials never appear in
URLs or cookies. Rotation returns a new credential and atomically revokes the
old one. Logout, global logout, expiry, and identity disablement revoke it
server-side. Browser cookie and native bearer credentials have separate parsing
and CSRF rules; accepting either must not create an ambiguous downgrade path.

### Authorization

Authentication establishes identity only. Authorization is denied when a
permission, group, ownership callback, store lookup, or dependency errors or is
absent. REST and admin use the same authorization service and resource ownership
decision; admin is not a bypass. List queries constrain rows before pagination,
and object checks run before serialization, binding, or mutation so forbidden
resource contents and validation details are not disclosed.

Missing and forbidden resources may intentionally share a 404 response when
existence is sensitive. The decision is consistent for one endpoint and the
underlying deny reason remains available to a secret-free audit event.

### Recovery and invitations

Recovery uses a separate purpose, verifier namespace, budget, and short expiry.
Successful recovery atomically consumes the challenge, rotates the current
session, and revokes other sessions. Recovery responses resist account
enumeration in the same way as login.

Invitation-only mode creates no identity from an unverified request. An invite
is single-use, scoped to an email and application, expires, and is atomically
consumed with email verification. Resending or replacing an invite invalidates
the previous secret without resetting abuse budgets.

### Passwords, when a future release adds them

The default password verifier uses a memory-hard KDF with per-password random
salt, a versioned parameter envelope, and a server-side pepper obtained from a
replaceable key provider. Parameters have a documented minimum and are bounded
on decode so an attacker-controlled verifier cannot force unbounded memory or
CPU work. A successful login upgrades stale parameters atomically without
changing the observable login response. Password, verifier, salt, and pepper
never enter logs or audit attributes. Breached-password screening, reset-token
semantics, and a concrete KDF parameter set require a new reviewed contract
before password APIs ship.

## Keys and rotation

Every stored digest and signed value carries a non-secret key identifier.
Verification accepts the active key plus explicitly configured retiring keys;
new values use only the active key. Unknown key identifiers fail closed. Key
material comes through a narrow provider, is never returned by auth APIs, and is
not logged on load failure.

Removing a retiring key invalidates credentials that depend on it. Operators
must be able to list the affected credential kind and maximum remaining lifetime
before removal. Emergency rotation combines a new active key with global
session revocation and challenge invalidation.

## Store and delivery failure contract

Auth ports take `context.Context` first. Cancellation is preserved through
wrapping. Store methods required for consume, identity creation, session
rotation, revocation, and budget updates are atomic operations at the port
boundary; the core never assembles a security transaction from independent
read/write calls.

Timeout, unavailable, corrupt-record, conflict, and not-found outcomes are
distinct sentinels or typed results. Corrupt or unknown-version records fail
closed and emit an operator-visible event. A timeout is never reinterpreted as
not-found. Delivery begins only after durable challenge state exists. A failed
delivery records the failed send attempt and does not make an unverifiable
success claim to the caller. Retrying uses the existing budget.

All retryable operations are idempotent by an explicit request key or atomic
store primitive. The core does not retry an ambiguous store timeout after a
consume or identity-creation attempt unless the port can report the committed
result for the same idempotency key.

## Audit contract

The audit sink receives structured events for challenge requested, delivery
attempted, verification succeeded/failed/locked, identity created/disabled,
session created/rotated/revoked, logout, recovery, invitation consumption,
authorization denied, rate-limit rejection, key rotation, and store corruption.

Events contain event name, timestamp, request id, public identity id when known,
session/challenge opaque record id when safe, purpose, outcome, and bounded
reason code. They exclude email by default and always exclude OTPs, verifiers,
cookies, bearer tokens, CSRF tokens, password material, and keys. Sink failure
cannot turn an authentication denial into success. For state-changing success,
the default is fail closed when the required durable audit event cannot be
recorded; deployments choosing best-effort audit must do so explicitly and the
trade is documented.

## Executable acceptance map

The canonical behavior text and future exact tests are `AUTH-001` through
`AUTH-018` in `specs/system-behavior.yaml` and `specs/test-matrix.md`:

| Threat | Acceptance rows |
|---|---|
| Weak, leaked, replayed, expired, or brute-forced OTP | AUTH-001…005 |
| Enumeration and dependency failure disclosure | AUTH-006, AUTH-017 |
| Session fixation, cookie theft, CSRF, and revocation gaps | AUTH-007…010 |
| Bearer-token theft and rotation races | AUTH-011 |
| Authorization bypass and resource disclosure | AUTH-012 |
| Recovery and invitation abuse | AUTH-013 |
| Secret leakage in audit and audit failure | AUTH-014 |
| Key compromise and rotation | AUTH-015 |
| Password downgrade or resource exhaustion in a future release | AUTH-016 |
| Capture backend used in production | AUTH-018 |

An implementation row moves from `planned` to `implemented` only in the change
that adds its exact test. Unit tests use deterministic clocks and stores;
concurrency tests use barriers rather than sleeps; PostgreSQL atomicity and race
claims run against a real server. Security, API, and ADR changes require human
review before production release.

## Explicit non-goals for v1

V1 does not ship passwords, social login, passkeys, SMS OTP, multi-factor
authentication, tenancy, an identity-provider protocol, or proof against a
compromised application host/database pair. Rate limits reduce online abuse;
they do not make email OTP phishing-resistant. Applications needing a stronger
assurance level must use a separately reviewed authentication method.
