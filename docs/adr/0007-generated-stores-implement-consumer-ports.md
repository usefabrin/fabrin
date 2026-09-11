# 0007. Generated PostgreSQL stores implement consumer-owned ports

- **Status:** Proposed
- **Date:** 2026-09-11
- **Deciders:** Fabrin contributors
- **Requirement / issue:** FR-ORM-2, FR-ORM-6, #109

## Context

[ADR 0002](0002-database-sql-is-the-orm-seam.md) chose `database/sql` as the
framework seam and consumer-owned store interfaces as the module boundary. It
also rejected a Fabrin query builder for v0 because its maintenance cost was not
justified while the framework had no stable data-access direction.

The v1 direction now requires a strong default: developers declare a schema in
Go and receive typed PostgreSQL data access. Leaving every SQL adapter
hand-written preserves replaceability but fails the batteries-included goal.
Blessing pgx or a runtime ORM would solve the convenience problem by making a
third party part of Fabrin's public compatibility contract.

The schema already has a database-independent home in `orm.Model`. Generating
from another declaration would let migration metadata and data access disagree,
which is the failure code generation is meant to remove.

## Decision

`orm.Model` and `orm.Field` carry optional `GoName` values alongside their SQL
names. Metadata-only users may omit them. Migration snapshots omit them because
they describe source code rather than database state.

`fabrin/ormgen.Generate` accepts a Go package name and those same models, then
returns one deterministic, formatted source file. Before producing bytes it
rejects invalid or colliding Go names, invalid or colliding SQL names,
unsupported model shapes, and anything other than one primary key per model.

Generated code contains:

- typed row structs, using pointers for nullable scalar and time columns;
- a `DBTX` method set satisfied without adapters by `*sql.DB`, `*sql.Tx`, and
  `*sql.Conn`;
- a `Queries` value with context-aware `Create<Model>` and `Get<Model>` methods;
- PostgreSQL placeholders and quoted identifiers; and
- wrapped errors that retain `context.Canceled` and `sql.ErrNoRows` for
  `errors.Is`.

The generated `Queries` is an application adapter. A module continues to declare
the narrow store interface it consumes and receives a value that satisfies it at
the wiring boundary. Fabrin exposes no ambient database and stores no handle in
model metadata.

Generation is an offline library call that returns bytes. A project may invoke
it from `go generate`, a small project command, or another build tool and decides
where and how to write the file. Fabrin does not scan packages or mutate a
project implicitly.

The first surface supports caller-supplied primary keys and Create/Get only.
Database-generated keys, relationships, listing, pagination, updates, deletes,
and transaction helpers require explicit later schema and API decisions.

## Consequences

One declaration now drives migration metadata and typed data access. Generated
queries remain ordinary readable Go and ordinary SQL, context cancellation
crosses every call, and users can drop to `database/sql` beside them. No new
third-party type enters Fabrin's exported API.

The cost is checked-in generated code and two source-only name fields in public
metadata. Renaming a Go type or field changes application source even when it
does not change the database, so those names stay out of migration snapshots.
Generated row types may differ from a module's domain types; the wiring adapter
owns that conversion when the module deliberately keeps its port independent.

The conservative SQL-name grammar rejects PostgreSQL identifiers that quoting
could technically represent. That restriction makes generated Go names,
diagnostics, and later relationship references unambiguous. It can be relaxed
additively once a concrete schema needs more.

## Alternatives considered

### Reverse ADR 0002 and expose a runtime ORM

Rejected. It would make the ORM's types and release cycle part of Fabrin's v1
contract, add runtime reflection, and make generated metadata unnecessary while
weakening the extraction seam modules already use.

### Bless pgx and generate against its native interfaces

Rejected. pgx is the PostgreSQL test driver and a good application choice, but
its types do not need to enter Fabrin's API. The standard `database/sql` method
set covers the required Create/Get behavior and works with connections and
transactions without an adapter.

### Maintain a second schema declaration for generation

Rejected. Two declarations can drift while both compile. Adding optional Go
names to Fabrin's existing metadata is a smaller public promise and keeps DDL,
admin metadata, and generated data access anchored to one source.

### Generate module store interfaces as well as implementations

Rejected. The module is the consumer and owns the smallest interface it needs.
A framework-generated wide interface would reverse the ports rule and make
tests and remote adapters implement methods the module never calls.
