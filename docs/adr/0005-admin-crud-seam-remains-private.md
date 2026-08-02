# 0005. The first admin CRUD seam remains private

- **Status:** Proposed
- **Date:** 2026-08-02
- **Deciders:** Fabrin contributors
- **Requirement / issue:** FR-ADMIN-5, [#78](https://github.com/usefabrin/fabrin/issues/78)

## Context

`orm.Model` describes a table but does not identify or construct a Go type.
Consumer-owned stores deliberately expose only the operations their module
needs, not a framework-wide CRUD repository. Those choices keep persistence
swappable, but they leave a real gap between metadata and a generated admin.

The gap cannot be designed safely from interfaces alone. A public model link,
form abstraction, or repository would become a semver promise before Fabrin has
proved pagination, transactions, filtering, nullable values, conflicts, or the
runtime dispatch needed for heterogeneous models. The authentication, CSRF, and
rendering foundations are also still planned.

## Decision

The first metadata-to-form-to-persistence CRUD vertical lives in the root-level
`admin` package, but every symbol in the vertical remains unexported. The package
is user-importable because a future admin is user-facing; its current empty
exported surface deliberately promises nothing.

Each private resource binds one concrete Go type to an existing `orm.Registered`
value using:

- an explicit constructor;
- an explicit primary-key reader;
- explicit read/write adapters for every non-primary-key metadata field; and
- resource-specific list, get, create, update, and delete callbacks.

Metadata determines field membership, order, and existing constraints such as
`MaxLen`. The explicit adapters own conversion between submitted strings and the
concrete value. Unknown submitted fields are ignored, so metadata is also the
mass-assignment allowlist. Fabrin does not add a Go-type field to `orm.Model`, use
reflection, scan packages, or infer field access from tags.

The persistence callbacks are evidence for this one vertical, not a public
repository or query contract. A reusable framework-owned CRUD/query abstraction
would revisit [ADR 0002](0002-database-sql-is-the-orm-seam.md) and requires a new
ADR before it is exported.

Every resource requires both an authorization callback and a CSRF validator.
Reads authorize before persistence. Unsafe actions validate CSRF first and then
authorize the module, model, action, and optional record key before binding or
persistence. Authentication may later place a principal in `context.Context`,
but this proof does not define a user, session, permission, token, middleware,
route, or response contract. The real F3/F4 security foundations must replace
these private callbacks before any usable admin handler ships.

No third-party type enters an exported signature, `orm` metadata is unchanged,
and `api/fabrin.txt` does not move.

## Consequences

The vertical proves that metadata can drive a form and CRUD lifecycle without
reflection, a blessed ORM, or a prematurely generic repository. It also makes
the fail-closed security order executable rather than aspirational.

The cost is deliberate per-resource adaptation code. The proof does not yet
solve heterogeneous runtime registration, pagination, transactions, filters,
search, uniqueness, nullable values, optimistic conflicts, templates, routes,
or reusable form errors. It is not a usable admin site, and none of its private
shapes should be copied into public API merely because the vertical passes.

Keeping the code in `admin` rather than `internal/admin` also means consumers can
import a package with no exported identifiers. That temporary oddity preserves
the correct future import path: Go's `internal` rule would make a user-facing
admin API unreachable and would also reverse this repository's public-to-
internal dependency rule if the proof imported `orm` from there.

## Alternatives considered

### Add Go construction and field links to exported ORM metadata

Rejected because exported fields are permanent surface, reflection-shaped
construction erases compile-time type checking, and the still-provisional
nullable/unique/index semantics belong to #79. One CRUD proof is not evidence
for a model contract every persistence adapter must carry.

### Discover records with reflection, struct tags, package scanning, or `init`

Rejected because runtime discovery turns renames and type changes into startup
or request-time failures, hides registration in side effects, and recreates
Django's dynamic machinery in a language where explicit typed wiring is the
idiomatic seam.

### Export a generic repository or a wide framework-owned CRUD interface

Rejected because it would make Fabrin own query and persistence semantics that
ADR 0002 leaves with the application. The apparent five CRUD methods omit the
hard decisions: paging, filtering, transactions, not-found behavior, conflicts,
partial updates, and bulk operations.

### Pass type-erased row maps through the admin

Rejected because maps move conversion and validation errors to runtime, permit
unknown-field assignment unless every caller is perfect, and cannot express the
relationship between metadata and a concrete application type.

### Bless a third-party ORM's model or repository API

Rejected because third-party types may not enter Fabrin's exported signatures
without an ADR and an `apicheck` allowlist decision. It would also make the admin
ORM-shaped, defeating the reason Fabrin owns metadata.
