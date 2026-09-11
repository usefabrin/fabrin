# Email OTP authentication contract

Status: **approved by the maintainer for implementation**, September 10, 2026. Tracking: #80,
#105, #110, #112, #113. This document specifies intended behavior; it does not
claim production readiness. Approval was given after review of commit `a19c4d7`
under the direct-main workflow. Capture mail, the private challenge primitive,
and a public local OTP core are implemented. The local core reserves challenges,
enforces bounded process-local budgets, cleans up known delivery failures, and
atomically resolves stable identities plus initial opaque sessions. Durable shared
storage, eligibility policy, browser/native transport, broader session revocation,
and authorization remain planned. The earlier `authpg` all-in-one store is being
split before v1: Redis will own ephemeral challenges, budgets and sessions;
PostgreSQL will retain durable identities and authorization. This is approved
architecture, not approval of the incomplete HTTP or production stack.

## Goal, trust boundaries and limits

An application wires built-in email-code signup/login in Go and can extend the
minimal identity with related profile tables. No password table is required.
Default signup creates an identity only after successful verification; invitation
mode admits only invited addresses. Ordinary signup never grants admin privilege.
Email possession is the proof: a compromised mailbox, mail provider, application
process or privileged database writer is outside the protection this mechanism
provides. Email OTP is not phishing-resistant MFA or proof of a person's identity.

Untrusted inputs include email, code, challenge ID, client metadata, headers,
redirect destinations, cookies, bearer tokens and resource IDs. Trusted components
are configured application code, key material, the transactional auth store and
mail adapter. Request IDs and proxy-derived IPs are observability/abuse signals,
not authentication factors. The default trusted-proxy behavior remains unchanged.

Fabrin owns identity, challenge and session persistence. Business modules consume
locally declared identity/permission ports; they do not import each other. A
Redis adapter is the production default for ephemeral auth state and `authpg`
is the durable identity/authorization default. A memory adapter may be supplied
only as an explicitly selected, bounded development/test option. Capture mail
must never become a production delivery fallback.

## Challenge lifecycle

Approved defaults are Fabrin decisions, not claims that OWASP mandates these
specific values. Changes require matching acceptance tests and documentation.

- Generate an unpredictable 32-byte challenge ID and uniformly sampled eight
  decimal digits with `crypto/rand`. Preserve leading zeroes. Never derive a code
  from time, an email hash, a request ID or a general-purpose random source.
- A code expires five minutes after reservation. Expiry is exclusive: a code is
  invalid when `now >= expires_at`. Delivery does not extend its lifetime.
- Keep one active challenge per canonical address and purpose. A resend replaces
  it; it never revives an expired challenge or resets the address's abuse budget.
- Store only an HMAC-SHA-256 verifier binding purpose, challenge ID, canonical
  email and code with unambiguous length-prefix encoding. Compare in constant
  time. Keep the HMAC key outside the database; require at least 32 random bytes.
  A database read alone must not permit offline enumeration of eight-digit codes.
- The request's email parser accepts a single bounded bare address, rejects
  controls/display names, preserves local-part case, and lowercases the ASCII
  domain. Do not strip dots, plus suffixes or apply provider-specific aliases.
  Non-ASCII addresses are rejected in this first contract. Policy changes need an
  identity migration plan, because canonicalization changes account identity.
- Verify by challenge ID plus code, never by code alone. Invalid, expired,
  replaced, spent and unknown challenges produce the same public auth failure.
  Internal diagnostics may distinguish categories without logging credentials.

Challenge reservation and per-address/per-source rate-budget updates are atomic.
Proposed defaults: at least 60 seconds between sends per address, at most five
send reservations per address per rolling hour, five verification attempts per
challenge, and at most twenty failed verifications per address per rolling hour.
A configurable trusted source key also limits send reservations (default twenty
per rolling hour) and verification attempts (default one hundred per rolling
hour). Limits are shared across instances in production. Address budgets include
rejected or replaced challenges; a resend cannot buy five unlimited new guesses.
Budgets expire independently from challenge data. Memory adapters must bound
both challenge and budget entries and fail closed at capacity.

Source-level checks happen before address-specific work. Invalid challenge IDs
still consume source-level verification budget. Missing or forged proxy headers
must not disable source limits. Deliberate denial of service by targeting an
address remains possible; bounded temporary limits are preferable to permanent
account lockout, and successful login must not silently reset abuse history.

## Delivery and transaction failures

Reserve a challenge and consume the send budget before invoking delivery. Do not
hold database locks while calling a mail provider. Successful provider acceptance
is not a guarantee of inbox delivery. A known delivery failure invalidates that
exact challenge without invalidating a newer resend, retains the abuse budget,
and returns a retryable internal failure. Do not automatically resend on an
ambiguous timeout: the provider may already have accepted the email.

Every provider call receives a deadline-bounded context. Cancellation before a
committed reservation leaves no challenge; cancellation after reservation has
an explicitly recorded cleanup path. If invalidation fails, the challenge remains usable until consumed or expired:
verification proves knowledge of the correct code, not provider-confirmed
delivery. A timeout may have delivered mail, and verification can race cleanup.
The challenge-request operation itself creates no identity or session; successful
verification may still do so while that challenge is active. Operators receive
a sanitized failure event. Test this race explicitly rather than promising
that ambiguous delivery failure makes a code unusable.
A durable outbox, if added, must encrypt secret-bearing payloads, preserve the
original expiry, and have a bounded retry window. Plaintext OTPs must not enter
a generic durable job payload, error string, tracing attribute or access log.

## Atomic verification and identity creation

The store performs attempt accounting, expiry/purpose checks, verifier comparison,
challenge consumption, invitation/disabled-account policy, and identity resolution
in one transaction or equivalent atomic operation. Failed attempts commit their
budget increments even when no identity is returned. Unexpected store failure
returns no authenticated result. Only one concurrent request can succeed with
the same challenge. A replay never creates a second identity or session.

Identity IDs are random, opaque and stable. Canonical email has a unique database
constraint. Concurrent verified signup resolves to one identity, not duplicate
rows. Profile creation belongs to the application; failure must not leave a
successful response claiming that a required profile exists. Atomic profile
requirements use the same application-owned transaction or a documented
idempotent provisioning state. Built-in auth must not issue a privileged session
before those required provisioning checks complete.

Before sending the success response, persist the new session and its association
with the verified identity atomically with challenge consumption. If the client
loses the response, the used code remains spent: retrying verification cannot
return or reconstruct the session secret. The client starts another challenge.

## Sessions and HTTP integration

Use independent 32-byte cryptographic opaque session secrets, storing only their
SHA-256 digests. No JWT/access-refresh hierarchy is required for v1. Sessions
have an absolute seven-day maximum and a 24-hour idle timeout by default;
server-side checks enforce both. New authentication issues a fresh session secret; never promote a caller-provided
session ID into an authenticated session. Privilege changes and identity disabling
atomically revoke all affected sessions and require fresh authentication, including
when another administrator performs the change. Do not try to rotate secrets for
offline clients. Session checks beginning after revocation commits must reject the
revoked credential. Already authenticated in-flight requests may complete; operations
requiring a stronger boundary must recheck session/permission state in the same
transaction as the protected mutation. Tests synchronize checks, revocation and
mutations with barriers. No cross-request authentication cache may conceal a
committed revocation.

Browser mode uses a host-only `__Host-fabrin_session` cookie with `Secure`,
`HttpOnly`, `Path=/` and `SameSite=Lax`. A loopback-only development override is
explicit and must use a different cookie name; debug mode alone cannot disable
cookie security. Browser verification sets the cookie and does not return its
secret in JSON. Native mode returns a bearer secret only through an explicitly
configured native verification route, with `Cache-Control: no-store`. Secrets
never appear in URLs. Do not accept bearer credentials from query parameters.

All auth responses use no-store caching. JSON bodies have explicit byte bounds
and strict decoding. Return sanitized error codes, never raw store/provider
errors. Authentication does not trust request-controlled redirects; admin returns
to an allowlisted local path. CORS is denied unless exact origins are configured;
never combine wildcard origins with credentials. A browser first fetches an explicit no-store bootstrap endpoint, which sets a
short-lived secure host-only pre-authentication cookie and returns a CSRF token in
JSON. Its CORS policy permits only configured exact origins; browsers cannot read
the token from another origin. Pre-auth state is bounded server-side, expires after
ten minutes, and is rate-limited at bootstrap. Reject unsafe browser requests with
missing, null or untrusted Origin, or an absent/mismatched CSRF token. Bind browser
challenges to that pre-auth state and browser purpose; verification from another
browser context fails. On successful login, replace pre-auth state with a fresh
session and CSRF state. Logout and all other cookie-authenticated unsafe requests
require the same Origin/token checks. SameSite alone is not the CSRF mechanism.
Native challenge/verification routes use a distinct native purpose and never accept
browser-purpose challenges or fall back to cookies. Native routes must not set
browser auth cookies. Browser frontends use browser routes; this contract does not
pretend that Origin headers prove a caller is a native application.

For a syntactically valid request admitted by source and address limits, reserve
and attempt the same neutral verification email irrespective of existing, unknown,
disabled or uninvited identity state. Enforce invitation/disabled policy only at
atomic verification. This intentionally sends bounded verification messages to
ineligible addresses rather than making delivery an eligibility oracle. Do not
include account status or invitation details in that email. The public request
response remains generic accepted even when delivery fails or times out; failures
remain sanitized internal diagnostics and trigger the cleanup described above.
Store unavailability may produce a generic service-unavailable response, independent
of eligibility. Address-limit suppression uses the generic accepted shape; source
limits may return rate-limited. Tests compare eligible/ineligible paths with delayed,
failing and timed-out senders; never promise exact wall-clock equality. A
mailbox owner can learn that verification is denied, but receives no public
explanation distinguishing invitation, disabling, expiry or invalid credentials.
Resource endpoints distinguish unauthenticated from denied access according to
a documented transport policy; nonexistent and unauthorized record lookups must
not expose record existence through response bodies.

Logout revokes the current stored session before reporting success and clears
its cookie. Revocation-store failures are errors, not successful logout claims;
a client may still erase its local credential. Logout-all revokes all identity
sessions. Email change and account recovery need separately purpose-bound
verification of the new mailbox and recent authentication; they are deferred
until that flow is implemented and reviewed. Administrators have an explicit
bootstrap procedure with no public default credential or first-signup privilege.

## Authorization, audit, rotation and cleanup

Default authorization denies access. Groups and explicit per-operation policies
apply to admin, REST, list queries and individual records. Field allowlists apply
before binding and output serialization; a body cannot set owner, admin status or
internal auth fields. Ownership comes from the authenticated principal. Authorization checks complete before protected persistence operations start.
A policy error prevents those operations. Errors during a protected read return
no result data; errors during a mutation must follow its transaction rollback
contract rather than claiming the operation never started.

Audit challenge outcome, verification outcome, session creation/revocation,
identity disabling and privilege changes. Record event category, time and a
non-secret correlation reference. Never record codes, session tokens, HMAC keys,
message contents, full Authorization/Cookie headers or raw provider errors.
Email addresses are personal data and should not be default telemetry labels.

HMAC keys have explicit IDs. During planned rotation, retain the previous key
only through the maximum active challenge lifetime; compromise rotation revokes
all affected challenges immediately. Key lookup failure is an auth failure, not
an attempt to verify with a default key. Session digests need no signing key,
but store compromise requires revocation and incident handling. Background
cleanup deletes expired challenge/session rows and old budgets; correctness must
not depend on cleanup having run. Capture inboxes contain secrets and are
accessible only to test code or an explicitly local development tool. The OTP
service's production option rejects the included memory store and capture sender;
wrapping either does not make it production-safe.

## Threat-to-test acceptance matrix

All rows remain planned until executable evidence is linked in `specs/`.

| Behavior ID | Threat / required evidence |
|---|---|
| AUTH-001 | Random challenge/code; expiry boundary; purpose/email/ID binding; protected verifiers; malformed input rejection |
| AUTH-002 | Concurrent consume yields one success; replay, replacement, expired and spent code fail; failed attempts persist |
| AUTH-003 | Shared address/source budgets; resend preserves budget; unknown IDs consume source budget; capacity/outage fails closed |
| AUTH-004 | Provider error/timeout/cancellation; no account before verification; cleanup cannot revoke a newer challenge |
| AUTH-005 | Concurrent verified signup produces one identity; disabled/invitation policy checked transactionally; no implicit admin |
| AUTH-006 | Fresh sessions, privilege-change revocation, idle/absolute expiry, post-commit revocation boundary, synchronized in-flight behavior, logout-store failure, secret digests |
| AUTH-007 | Bounded pre-auth bootstrap, cross-browser binding, missing/null Origin rejection, login/logout CSRF, cookie flags, native-purpose separation, strict CORS, body bounds and no-store |
| AUTH-008 | Authorization applied to lists/records/fields; no mass assignment; store errors deny; admin cannot bypass policies |
| AUTH-009 | No account enumeration in public shape/processing; no code/token/mail content in logs/traces; key rotation and cleanup |

## References and deliberate choices

OWASP's [email recovery guidance](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html)
provides relevant principles for emailed codes: cryptographic randomness,
bounded validity, single use, protected storage and abuse controls. Fabrin applies
those principles to login; it does not claim login is a password-reset flow.

OWASP's [session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
informs the cookie, expiry, rotation and revocation requirements. Specific code
lengths, TTLs, budgets and persistence choices above are approved Fabrin defaults
requiring executable evidence and implementation security review before production.

Passwords, KDF upgrades, password-reset endpoints, social login and passkeys are
outside this v1 auth scope. The earlier password-first requirement is superseded;
there is no unused password-hashing API to freeze for a future feature.

## Review record

September 10 static review identified and resolved ambiguity in public delivery
failures, revocation on privilege changes, in-flight revocation boundaries,
failed-invalidation behavior, and browser pre-auth bootstrap. No auth implementation
was tested by that static review. The maintainer subsequently explicitly approved
the contract for implementation on September 10, satisfying #80's pre-implementation
review requirement. This does not accept implementation defects or waive production
security review. Private challenge tests prove the cryptographic, attempt, expiry
and single-use primitives. Public core tests additionally prove local
reservation/replacement, bounded budgets, sanitized delivery/store failures,
atomic identity/session creation, idle/absolute session expiry and logout
revocation. Conditional PostgreSQL tests exercise shared budgets, concurrent
single consumption, unique identity resolution, digest-only sessions and
revocation when `FABRIN_TEST_PG_DSN` is set; the constructor test proves wiring
does not connect or mutate schema. Invitation policy, identity-wide and
privilege-change revocation, cleanup, browser CSRF, HTTP enumeration resistance,
and production delivery remain unproved and unimplemented.
