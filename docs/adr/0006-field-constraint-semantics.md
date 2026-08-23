# 0006. Field constraint semantics — NOT NULL by default, per-field flags only

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
flags becomes a compatibility promise. The differ and the emitters now consume
the metadata everywhere, which is what made deciding urgent rather than
premature.

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

**`Unique: true` makes the column UNIQUE**, inline. UNIQUE already implies an
index on every database Fabrin renders for (PostgreSQL and SQLite both), so
there is no separate unique-index object and `Unique` + `Index` on one column is
rejected as a misunderstanding, not accepted as a tuning hint.

**`Index: true` creates a plain index** named `idx_<table>_<column>` —
deterministic, collision-free within a table by the existing duplicate-column
rule, and greppable in schema dumps for the same reason `migrate.Table` is
exported.

**Impossible combinations are validated at registration**, where every other
metadata mistake already fails: `PrimaryKey + Nullable`, `Unique + Index`.

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

- Generated migrations gain `NOT NULL`/`UNIQUE` immediately in the wiring slice;
  nullability changes become diffable operations.
- Pre-#79 state files encode all-nullable columns and are **rejected, not
  reinterpreted**: the format gains a version marker, and files without it fail
  loudly pointing at this ADR. Zero releases exist, so nothing real breaks; the
  alternative — inferring legacy semantics forever — bakes a second meaning
  into the parser permanently.
- SQLite cannot alter nullability in place; its stated refusal covers the new
  operation exactly as it covers type changes, consistent with MIG-024.
