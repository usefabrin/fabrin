# Auth-focused v1 delivery and release evidence

This is the canonical v1 scope and progress record. Implementation proceeds in
small tested commits directly on `main`, without PRs, feature branches, or
additional worktrees, as requested by the maintainer. That workflow remains in
force until v1 is stable.

## Product contract

Fabrin v1 is an authentication and authorization release. It ships email OTP,
production SMTP delivery, secure browser-cookie and native-bearer sessions,
invitation-only signup, stable identities, groups, permissions, ownership
authorization, audit records, cleanup, and explicit administrator bootstrap.
Passwords, social login and passkeys are deferred.

Redis owns ephemeral auth state: challenges, abuse budgets, verification leases,
browser pre-authentication state, sessions, rotation and revocation. PostgreSQL
owns durable identities, invitations, groups, permissions, audit records and
relations to application profiles. Redis v1 supports one standalone primary;
Sentinel and Cluster follow later. The decision and its failure model are in
[ADR 0008](adr/0008-redis-owns-ephemeral-auth-state.md).

Fabrin remains one Go module, so the v1 tag stabilizes every exported package.
Existing core, CLI, configuration, health, logging, metadata, migration,
generated-data and capture-mail capabilities remain available and must pass API
review. They receive correctness and security fixes, but no new non-auth product
scope before v1. `admin` still exports no public API.

## Milestones

| Milestone | Acceptance | Current evidence |
|---|---|---|
| Auth core (#105, #110) | Protected OTPs, neutral delivery behavior, bounded shared abuse controls and verified identity creation | Local core plus shared Redis challenge/budget tests implemented |
| Redis and identity persistence (#111) | Redis OTP/session state plus PostgreSQL identities with retry-safe verification recovery | Both adapters implemented; live CI covers concurrency and lease recovery |
| Sessions and transport (#112) | Native and browser login, CSRF, rotation, revocation, expiry, CORS and cleanup | Native/browser handlers, exact-origin CORS, CSRF, identity-wide logout and challenge binding implemented; privilege revocation and cleanup pending |
| Authorization (#113) | Invitations, groups, deny-by-default operation/field policy, ownership and administrator bootstrap | Planned |
| Production email (#116) | Replaceable SMTP with TLS, deadlines, sanitized errors and tested ambiguous delivery behavior | Capture backend implemented; SMTP pending |
| Release candidate (#108) | Deployed auth reference, recovery evidence, whole-module API/security review, support docs and no blockers | Planned; no stable-v1 claim |

Non-auth feature epics #104, #106, #107 and their unfinished children move after
v1 unless an item is required to operate or validate auth. The earlier generated
owned-resource preview in #111 is superseded by an auth reference application.

## Implementation order

1. Refactor `authpg` to durable identity/eligibility persistence and add the
   Redis verification/session adapter without exposing its client type. **Done.**
2. Complete transport operations with privilege-change revocation, cleanup and
   dependency health checks; native/browser login, CSRF, exact CORS and
   revoke-all are implemented.
3. Add invitation, group, permission, ownership and field-policy APIs with an
   explicit administrator bootstrap and no implicit superuser signup.
4. Add the TLS-required SMTP backend, secret-free audit events and bounded
   cleanup commands.
5. Exercise the auth reference deployment, review the complete public surface,
   fix release blockers, publish support/recovery/upgrade guides and tag v1.

## Stable-v1 gate

Record commands and results for:

- OTP malformed-input, expiry, replay, resend, concurrency, enumeration, shared
  address/source limits, delivery ambiguity and Redis/PostgreSQL outage windows.
- Browser bootstrap/login/logout CSRF, cookie flags, exact origins, native/browser
  purpose separation, body bounds and no-store responses.
- Session fixation, rotation, idle/absolute expiry, single/revoke-all behavior,
  privilege-change barriers, restart and cleanup.
- Invitation and disabled-identity races; group, operation, record ownership and
  field policies denying before protected persistence.
- SMTP TLS/authentication, timeouts, accepted-message ambiguity and credential-safe
  errors; secret-free audit output.
- Empty/existing PostgreSQL migrations, rollback/recovery, Redis restart, supported
  Go/PostgreSQL/Redis versions, `just check`, `just race` and performance evidence.
- Whole-module API and dependency review, SECURITY guidance, installation,
  configuration, upgrade/recovery guides and no unresolved release blocker.

No calendar date substitutes for these gates. Direct `main` delivery ends only
after the stable tag; later work returns to the repository's normal contribution
workflow.

## Current implementation record

The approved [authentication contract](AUTH_CONTRACT.md) defines nine threat-led
behavior groups. The core generates cryptographic challenge IDs and eight-digit
codes, binds them with length-prefixed HMAC-SHA-256, uses exclusive five-minute
expiry and five attempts, sanitizes store/delivery errors, and creates an opaque
digest-only initial session. `mail.Capture` is bounded and available only to
tests or explicitly local tools.

Commit `0ad3552` added a PostgreSQL implementation of the complete auth store.
The v1 Redis decision superseded that persistence shape before any release:
`authpg` now retains durable identity and eligibility data, while `authredis`
owns challenges, shared rolling budgets, verification leases and sessions. Live
integration tests exercise PostgreSQL identity serialization and Redis
cross-client concurrency, retry after a transient identity failure, session
authentication and revocation. Existing preview databases may be reset; there
is no released migration compatibility promise yet.

The generated schema work and its proposed ADR remain in the repository, but
the auth-focused release does not extend them. The unfinished local preview test
is preserved while its no-auto-migration and no-public-inbox assertions are
carried into the auth reference application.
