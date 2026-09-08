# 0007. Generate PostgreSQL data access from Go schemas

- **Status:** Proposed
- **Date:** 2026-09-08
- **Deciders:** Fabrin contributors; product direction approved by the maintainer
- **Requirement / issue:** FR-DATA-1, #104, #109

## Context

The v1 product direction requires ordinary tables to work from convenient code
configuration. The application-owned store pattern in ADR 0002 remains a useful
escape hatch, but does not provide that default. The maintainer chose Go schema
builders and generated typed code over struct reflection or hand-written mappings.

## Decision

Add `schema`, an explicit Go declaration and source generator. It emits ordinary
Go records, PostgreSQL stores over `database/sql`-compatible handles, and existing
`orm.Model` metadata. Metadata remains independent of connections. Registration
is explicit; no package scanning, runtime reflection, global registry, or GORM
handle enters the public API. Generated code is checked in by applications and
reviewed alongside schema changes. Generation does not connect to a database or
apply migrations. Initial-table SQL uses the same `migratediff.Postgres` renderer
as later migrations; the generator does not maintain a second SQL dialect. PostgreSQL is the production target; existing SQLite support
is retained without a v1 completeness promise.

The first slice supports supplied primary keys, scalar fields, create/get and
nullable values. It does not claim relationships, generated IDs, general query
building, REST, or admin. Those land as independently tested capabilities.

This proposes updating ADR 0002's no-default-query decision while retaining its
stdlib handle, replaceability, and third-party API restrictions. ADR 0002 is not
marked superseded until this proposal receives the required human API review.

## Consequences

Users gain a typed default without writing stores for ordinary persistence.
Generated stores can satisfy module-owned ports, and `*sql.Tx` can substitute for
`*sql.DB`. SQL remains inspectable and custom stores remain possible.

Fabrin now owns generator compatibility, naming, null semantics, and supported
PostgreSQL operations. Generated source is an application artifact that must be
regenerated deliberately. Public schema and generated API shapes remain
provisional until reviewed; a preview is not an API freeze.

## Alternatives considered

- Keep user-written stores only: preserves the current boundary but fails the
  accepted convenience goal.
- Runtime reflection over struct tags: fewer generation steps, but moves mapping
  errors to runtime and was not the selected authoring experience.
- Expose GORM: adds another third-party semver promise and couples metadata to it.
- Build a universal ORM/query language: expands dialect and query scope before
  a working application validates the minimal API.
