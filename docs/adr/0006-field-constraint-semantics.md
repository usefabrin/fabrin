# 0006. Field constraint semantics — NOT NULL by default, named constraints

- **Status:** Proposed
- **Date:** 2026-08-23
- **Deciders:** Fabrin contributors
- **Requirement / issue:** FR-ORM-1, [#79](https://github.com/usefabrin/fabrin/issues/79)

## Context

`orm.Field` exported `Nullable`, `Unique`, and `Index` while nothing consumed
them. Every generated migration rendered all columns nullable, and the recorded
state format deliberately withheld the three flags (#56) so no semantics could
freeze in by accident. The differ's nullability detection was explicitly absent,
waiting on this decision.

The question could not stay open: after v0.1, whatever users assumed about these
flags becomes a compatibility promise. The differ and emitters need to consume
the metadata everywhere in the next slice, which makes deciding urgent rather
than premature.

Two hard constraints shaped the answer. First, `orm.Field` is an exported struct:
adding fields can break unkeyed literals, so the composite/named-constraint space
could not be grown casually. Second, TODO warned that `Model.PrimaryKey []string`
beside `Field.PrimaryKey` would leave two permanent ways to declare a primary
key, with the field-level one unable to express the composite case it sits next
to.

## Decision

**Columns are NOT NULL unless `Nullable: true`.** Django's polarity, not Go's
zero-value habit. A silently nullable column hides bugs until data corrupts; an
explicit opt-out matches Fabrin's error-not-silent ethos. Primary keys are NOT
NULL by definition, so `PrimaryKey` + `Nullable` is rejected at registration.

**`Unique: true` makes the column UNIQUE.** UNIQUE already implies an index on
every database Fabrin renders for (PostgreSQL and SQLite both), so there is no
second unique-index object. `Unique + Index` is rejected as a misunderstanding,
not accepted as a tuning hint.

**`Index: true` creates one plain single-column index.** Primary keys already
imply uniqueness and an index, so `PrimaryKey + Unique` and
`PrimaryKey + Index` are rejected too.

**Generated database objects are named, bounded, and collision-resistant.** A
plain index is `idx_<readable>_<digest>`, a unique constraint is
`uq_<readable>_<digest>`, and a primary-key constraint is
`pk_<readable>_<digest>`. The complete name is at most PostgreSQL's 63-byte
identifier limit. `<readable>` is the lower-ASCII table/column name with other
runs replaced by `_`, truncated to make room; `<digest>` is the first 16 hex
characters of SHA-256 over the kind plus length-delimited original identifiers.
The digest is not decoration: concatenating `table_column` alone makes `a_b.c`
collide with `a.b_c`, and truncating long names makes unrelated objects collide
again. The exact derivation is part of the migration-format contract because a
later migration must be able to drop an object an earlier version created.

All table, column, index, and constraint names are SQL identifiers, never SQL
fragments. Dialects quote them according to their own rules; interpolation as
raw SQL is not an extension mechanism.

**Impossible or redundant combinations are validated at registration**, where
every other metadata mistake already fails: `PrimaryKey + Nullable`,
`PrimaryKey + Unique`, `PrimaryKey + Index`, and `Unique + Index`.

**Deferred, with representation reserved but unwritten:** composite primary keys
(`Model.PrimaryKey []string`), named indexes, and multi-column UNIQUE. The
deferred shapes stay out of code so `Field.PrimaryKey` remains the ONLY way to
declare a key until something — the admin's relation screens, most plausibly —
needs composites badly enough to introduce `Model.PrimaryKey` and deprecate the
field-level flag in the same change. Deciding the shape now prevents discovering
it later under compatibility pressure; implementing it now would ship surface
nothing consumes.

### Alternatives rejected

- **Nullable by default.** Friendlier to Go zero values, and wrong: the metadata
  describes database columns, not Go variables, and the failure mode of a wrong
  default here is silent data corruption instead of a loud compile error.
- **`NotNull *bool` or `Atomic`-style inverted naming.** Pointer flags make
  three states out of two and unkeyed literals worse. `Nullable` keeps the bool
  polarity honest precisely because its zero value would have been dangerous —
  which is why the guard test asserting flag-encoding behavior flips
  deliberately in #79's second slice rather than silently.
- **Implementing composites now.** Nothing consumes them; shipping
  `Model.PrimaryKey` before any consumer exists is how two representations
  become permanent.

## Consequences

- Generated migrations gain `NOT NULL` and named primary/unique constraints in
  the wiring slice. Nullability, primary-key, uniqueness, and index changes must
  each become visible to the differ; simultaneous changes must not mask one
  another.
- The state format gains a version marker. Unversioned pre-#79 files are decoded
  using the semantics that actually produced them — non-primary columns were
  nullable and all three withheld flags were false — so the first new diff is
  accurate rather than a forced rewrite. Unknown marked versions fail closed.
- SQLite cannot alter nullability in place; its stated refusal covers the new
  operation exactly as it covers type changes, consistent with MIG-024. The same
  rule applies to constraint changes SQLite cannot express without rebuilding a
  table: refuse during render, before a migration file is written.
- PostgreSQL primary-key removal resolves the actual single-column key through
  `pg_constraint` and `pg_attribute`. New keys still receive the deterministic
  name above, but catalog identity keeps unversioned migrations reversible:
  their inline primary keys received server-assigned names before this naming
  contract existed.
