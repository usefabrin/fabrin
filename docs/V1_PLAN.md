# V1 delivery and release evidence

This is the canonical v1 scope and progress record. Implementation proceeds in
small tested commits directly on `main`, without PRs, feature branches, or
additional worktrees, as requested by the maintainer. Repository and user docs
are part of each completed change. Existing issue links are references, not a
requirement to use a PR workflow.

## Product contract

Fabrin is a backend framework. Developers define schemas in Go, generate typed
PostgreSQL models and data access, and explicitly enable REST CRUD and embedded
admin. Email OTP is the first authentication method. Framework-owned identities
are extended with related application profiles. Browser cookie and native bearer
sessions are v1 requirements. Signup creates an account only after verification;
invitation-only mode is configurable. The only built-in frontend is admin.

PostgreSQL is the supported production target. Preserve existing SQLite behavior,
but defer SQLite completeness. R2 is the primary object storage adapter. Django
is a reference, not a feature checklist or API constraint.

## Milestones

| Milestone | Acceptance | Current evidence |
|---|---|---|
| September 10, 2026 preview (#103) | Runnable generated PostgreSQL create/get backend, email-code verification before signup, authenticated owned resource, capture-only test mail, reproducible guide | In progress; generator and bounded capture mail implemented. Not yet a runnable auth preview. |
| Data foundation (#104) | Typed CRUD, keys/defaults/nulls/relations/indexes, transactions, pagination, safe PostgreSQL migrations and upgrade concurrency | Scalar create/get generator implemented; ADR 0007 remains proposed. Existing migrations continue separately. |
| Auth (#105, #80) | OTP, delivery/attempt limits, atomic consumption, cookie/native sessions, revocation, invitation policy, groups and ownership authorization | Contract approved; private OTP challenge proof implemented. No login API shipped. |
| REST and admin (#106) | Explicit enablement, field/operation policies, scoped list and record access, bounded queries, embedded admin | Existing private admin proof only. |
| Operations (#107) | Production email, private R2 access, PostgreSQL jobs/scheduling, memory/Redis cache, distributed rate limits, local signals, tracing/metrics | Capture-only test mail implemented; production adapters remain planned. |
| Release candidate (#108) | Deployed reference backend, security/API review, upgrade/recovery evidence, gates/races, measured overhead, accurate support docs | Planned; no stable-v1 claim. |

September 10 is a **developer-preview target**, not a stable release deadline.
Incomplete acceptance criteria remain visibly incomplete if that date arrives.
The preview excludes generated REST, usable admin, native sessions, and cloud
uploads. Its auth is capture-only and cannot be represented as production-ready.

## Dependency order and work queue

1. Reconcile migration state and constraints (#79, #95, #101); retain their tests.
2. Review the schema/data-access decision and harden generated create/get (#109).
3. Define the email-OTP threat model (#80); implement OTP and capture delivery
   (#110), then integrate the runnable PostgreSQL preview (#111).
4. Finish PostgreSQL CRUD/relations/migration capabilities; add production
   sessions (#112), authorization (#113), and email (#116).
5. Build explicitly enabled REST and embedded admin over those shared contracts.
6. Add private R2 storage (#114), durable jobs/scheduling (#115), caches and
   distributed limits (#117), and local signals/observability (#118).
7. Exercise a deployed release candidate, resolve blockers, and freeze the
   reviewed public API before tagging stable v1.

Issue [#102](https://github.com/usefabrin/fabrin/issues/102) links the original
tracking scope. Individual feature changes remain small and use Conventional
Commits even though there are no PRs. A proposed ADR is not silently marked
accepted because code compiles. Maintainer API/security review can happen on a
concrete commit; it does not require a PR.

## Stable-v1 gate

The reference backend must demonstrate verified signup, browser/native login,
owned-resource CRUD, admin permissions, an authorized R2 upload, and a durable
job. Record commands and results for:

- Reproducible generation and compilation of generated applications.
- PostgreSQL empty/existing database migrations, supported rollback, concurrent
  execution, and upgrade/recovery scenarios.
- OTP expiry/replay/race/abuse and outage tests; session revocation and CSRF;
  denied list/read/write and field-exposure tests.
- Worker crash, retry, cancellation, dependency failure, and graceful shutdown.
- Real R2 integration and unauthorized/oversized object operations.
- `just check`, `just race`, governed docs checks, and performance measurements.
- Security and API review, supported version policy, user setup/upgrade guides,
  and no unresolved release-blocking defects.

Defer passwords/social/passkeys, i18n, distributed signal backends, service
mesh/discovery, organization/tenant management, and additional production DBs.
No calendar date substitutes for these gates.

## User documentation

Start with [generated PostgreSQL data](guides/generated-data.md). Guides must
state what ships today, show executable configuration, explain ownership and
security boundaries, and name preview limitations. Do not present planned auth,
REST, admin, or cloud adapters as available functionality.

## Generator validation evidence — September 8, 2026

Unit tests cover deterministic source and rejected schemas. The generated project
compiles and checks constructor nil rejection and exact agreement between initial
SQL and migration metadata. A live PostgreSQL 17 run passed create/get, null,
length, cancellation and not-found checks. Boundary probes independently rejected
root, Gin, HTTP and SQL imports in the generator; an `orm` import passed as the
negative control. Temporary probes were removed. API review found the constructor
validation issue, which was fixed with a failing-then-passing regression test.
This is generator evidence, not evidence that the complete preview or v1 ships.

## September 10 status

The preview target has arrived with the generator and bounded capture email
available. OTP login and the runnable PostgreSQL auth example are **not complete**;
no preview release or stable release is claimed. [AUTH_CONTRACT.md](AUTH_CONTRACT.md)
has been approved for implementation; its nine end-to-end behavior rows remain planned. It replaces
password-first assumptions with email OTP and minimal identity plus profiles.
[Testing email](guides/testing-email.md) documents the implemented capture API.
Next work remains #110 OTP core, then #111 integrated preview; passing mail tests
does not satisfy either issue's complete acceptance criteria.

Mail validation on September 10: `just check` and `just race` passed. Tests cover
bounded insertion, cancellation/invalid input, independent snapshots, and
concurrent producers/drainers returning each message exactly once. Root and
sibling import probes failed the new mail boundary; the stdlib negative control
passed. Read-only review findings were resolved in tests and the proposed auth
contract. The maintainer subsequently approved the contract for implementation;
passing capture tests is not auth security evidence.

## Approved contract and private OTP proof

The maintainer approved `AUTH_CONTRACT.md` on September 10. The private challenge
state machine now uses cryptographic IDs/eight-digit codes, length-prefixed HMAC
binding, constant-time verifier comparison, exclusive five-minute expiry and five
attempts with one concurrent winner. ASCII email canonicalization preserves the
local part and lowercases the domain. The API snapshot remains unchanged because
`auth` exports no symbols. No process-global auth state or incomplete public store
interface has been introduced.

Next: transactional shared abuse budgets, challenge reservation and delivery
cleanup, then atomic identity/session creation. The private proof cannot grant
sessions or enforce those distributed guarantees. See
[authentication status](guides/authentication.md).

Validation for the private OTP proof: `just check` and `just race` passed. Two
independent static reviews identified test-evidence gaps; tests now exercise
well-formed wrong codes, malformed codes, concurrent successful consumption and
concurrent failure exhaustion. Separate root/HTTP/SQL boundary probes failed as
expected and the cryptographic stdlib negative control passed. No public API
snapshot changes were needed. These checks do not mark the full AUTH contract
rows implemented.
