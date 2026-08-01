# 0003. Migrations take a `Handle`, not a `*sql.Tx`

- **Status:** Accepted
- **Date:** 2026-08-01
- **Deciders:** Fabrin contributors
- **Requirement / issue:** FR-ORM-4, [#67](https://github.com/usefabrin/fabrin/issues/67)

## Context

`fabrin/migrate` shipped in F2 with `M.Up` and `M.Down` typed
`func(ctx context.Context, tx *sql.Tx) error`. That signature answers a question
nobody asked: **may a migration run outside a transaction?** It answers *no*, by
construction, silently.

`CREATE INDEX CONCURRENTLY` is the case that forces the question. It is how you
index a large table without locking writes, and **PostgreSQL refuses it inside a
transaction block.** It is not exotic; it is the standard answer for any table
big enough to matter. Django ships `Migration.atomic = False` for exactly this,
and Django parity is a design input here.

This is not a decision that was made and recorded. Nothing in `docs/`, `specs/`,
`migrate/` or `DJANGO_PARITY.md` mentioned `atomic`, `CONCURRENTLY`, or
non-transactional migrations. The foreclosure was a side effect of picking the
obvious type.

**Why it is urgent and not merely open.** `*sql.Tx` appears in signatures the
**user authors**. Every migration anyone ever writes is typed against it.
Nothing is tagged yet, so today the change costs two field types and two test
helpers; `migrate` is imported by nothing outside its own package.

[ADR 0002](0002-database-sql-is-the-orm-seam.md) reasoned about `*sql.DB` — a
handle Fabrin is *given*, once, in `main`. It never mentions `*sql.Tx`. Different
blast radius, and unrecorded.

**Hard rule 1 is not in play.** `database/sql` is standard library; the snapshot
already carries `*database/sql.DB`, and `apicheck`'s allowlist stays at its
single Gin entry. This ADR exists under category 3 of
[the ADR policy](README.md) — a choice with live alternatives that someone would
otherwise re-derive and possibly reverse — not because a type was blessed.

## Decision

**`M.Up` and `M.Down` take a `migrate.Handle`**, an exported interface satisfied
unmodified by `*sql.Tx`, `*sql.DB` and `*sql.Conn`:

```go
type Handle interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}
```

Four things are settled here, each independently permanent.

### 1. The type changes now; the feature does not

Today the engine always passes a `*sql.Tx`, and the version row still commits in
the same transaction as the body. **MIG-001 stays true.** What changes is that
the *type* no longer promises it, so `NonAtomic` can land later as an opt-in with
no signature change and no edit to anyone's migration.

The doc comment states today's guarantee; the type reserves the capability. A
type that permits more than the current implementation delivers is normal and
honest. Prose that hedges about a mode which does not exist is not — so the
package documentation is **not** pre-emptively weakened.

### 2. The method set is four, and it is frozen

Users will implement `Handle` with recording fakes in their own tests. **Adding a
fifth method after v0.1 breaks every one of them.** This is the only part of the
change that is genuinely irreversible, so the set is decided once, here.

`PrepareContext` is in because a data migration backfilling a million rows wants
it, and it is the one excluded method a legitimate migration cannot synthesise
from the others. `BeginTx` is *not* in — it is absent from `*sql.Tx`, so its
exclusion is forced rather than chosen (see Consequences).

Through the migration parameter the engine and its tests currently use
`ExecContext` only. Three of the four are therefore speculative — accepted
deliberately, because a set that can never grow is sized once for the uses it
must eventually serve.

### 3. The future atomicity field is `NonAtomic bool`, not `Atomic bool`

Bound now, before anything depends on it.

`M` is a struct of exported fields — permanent name, permanent polarity.
`Atomic bool` zero-values to `false`, so the day it lands **every existing
migration silently becomes non-atomic.** That is a data-loss-class default, it is
the inverse of Django's, and it is precisely the fail-closed rule `AGENTS.md` is
built on. The zero value must mean "wrapped in a transaction".

### 4. The dynamic type is not part of the contract

`Handle`'s documentation says Fabrin may pass a wrapper, and that a migration
must not assert on the dynamic type.

Without that sentence, users write `h.(*sql.Tx)` for savepoints, Hyrum's law
makes it load-bearing, and Fabrin can never pass a wrapper again — no dry-run
mode, no statement logging, no `SET LOCAL lock_timeout` injection. The whole
value of this change is optionality; an undeclared dynamic type buys the option
and pre-spends it.

## Consequences

**What this buys.** The `CONCURRENTLY` case stops being unreachable. Users write
one helper shape — `func(context.Context, migrate.Handle) error` — that works in
either mode. And `tx.Rollback()` inside `Up`, which breaks the engine's own
promise that the body and the bookkeeping row commit together, stops sitting in
autocomplete.

**What it does not buy, and this correction matters.** A narrow interface
**restricts nothing.** `h.(*sql.Tx).Rollback()` compiles. The interface converts
an *accident* into a *deliberate act*; it does not remove the ability. Anyone
reasoning from "the interface makes it impossible" is reasoning from a false
premise — this was claimed on #67 and is wrong.

**What it costs.** Between now and whenever `NonAtomic` lands, every user reads a
parameter type that says "this may not be a transaction" and receives a
transaction one hundred percent of the time. The signature is less precise today
in exchange for an option that may never be exercised. That is the real trade,
and if `NonAtomic` never ships it never pays out.

**A hole this design opens, named rather than discovered.** `BeginTx` is on
`*sql.DB` but not on `*sql.Tx`, so it can never be in `Handle`. Django's
non-atomic migrations use `transaction.atomic()` blocks internally — the risky
statement runs bare, the rest is wrapped. Under this design that is unreachable
without a type assertion. Survivable, because an optional-capability interface
asserted later is purely additive and breaks nobody. But it is a known
consequence.

**This is an atomicity seam, not a portability seam.** `Handle`'s methods return
`sql.Result`, `*sql.Rows`, `*sql.Row` and `*sql.Stmt` — concrete `database/sql`.
Nothing pgx-native, nothing outside `database/sql`, can ever satisfy it. Anyone
selling this as future driver flexibility is mistaken.

**What it forecloses.** The method set. See Decision 2.

## Alternatives considered

### Keep `*sql.Tx`, and add `UpNonAtomic` later

The strongest alternative, and the one that nearly won, because it defeats the
urgency argument outright: `M` is a struct of exported fields, which `AGENTS.md`
says "may gain fields forever". `UpNonAtomic func(ctx, *sql.DB) error` is a
**purely additive** post-v0.1 change. So "changing it later breaks every user
migration" is not true of the feature — only of the type.

Rejected on four counts, in order of weight:

1. **Two signatures means no shared helper.** A user's
   `func addIndex(name string) func(context.Context, migrate.Handle) error` works
   in either mode; with two incompatible `Up` types their own helpers fork
   permanently, and the test helpers in `migrate_test.go` already demonstrate the
   pattern people will copy.
2. **The non-atomic body would receive `*sql.DB`** — a pool handle carrying
   `Close`, `SetMaxOpenConns` and `BeginTx`. Strictly *more* exposure than today.
3. **Four fields plus a validation matrix, permanently** ("exactly one of
   `Up`/`UpNonAtomic`…"), on the type users write most often, whose entire virtue
   is being readable at a glance.
4. **Asymmetric cost of being wrong.** Wrong now: two field types, two test
   helpers, nothing tagged. Wrong later: the four-field scar forever.

### An exported concrete wrapper struct

The only design that actually delivers what the interface is often *claimed* to
deliver: `Commit`/`Rollback` genuinely unreachable, and a method set free to grow
later without breaking anyone — because users cannot implement a struct.

Rejected on ergonomics rather than on principle. Users need a constructor to
build one in their own tests, which is more surface and more Fabrin machinery;
and it loses the property that makes this change clean, that `*sql.Tx`, `*sql.DB`
and `*sql.Conn` satisfy it **unmodified, with no adapter**. Recorded because the
trade is genuinely close, and because anyone who wants restriction rather than
discouragement should know this is where it lives.

### An unexported constraint, or an anonymous interface literal on the field

Technically available — a field typed with an anonymous interface literal
compiles, and users can spell it. Rejected: it forces every user to re-spell four
method signatures verbatim at every migration, and renders as a wall in godoc and
in `api/fabrin.txt`. Checked rather than assumed.

### Naming: `DB`, `Execer`, `Querier`, `Conn`, `Tx`, `DBTX`

`Handle` was chosen because **this repository names roles, never method sets** —
`Router`, `Module`, `Checker`, `Lifecycle`, `Modeler`, `Commander`,
`config.Source`, `health.Check`. A `-er` name would be the sole exception and the
symbol users misremember. The package already uses the word: "the handle belongs
to the caller".

- **`DB`** — `Run` and `Rollback` already take a `db *sql.DB` in this same
  package, so `migrate.DB` would put two different `db`s of two different types
  on one godoc page. That it is also the name of ADR 0002's rejected alternative
  is a footnote, not the argument.
- **`Execer` / `Querier` / `ExecQuerier`** — each half-true; the set both execs
  and queries.
- **`Conn`** — `*sql.DB` is a pool, and `*sql.Conn` already means something else.
- **`Tx`** — the entire point is that it may not be one.
- **`Executor` / `Runner`** — read as the engine, in a package exporting `Run`.
- **`DBTX`** — sqlc's name, recognisable to sqlc users and nobody else.

The cost owned: `migrate.Handle` can misread as a function by analogy with
`Router.Handle`. At the only site it appears — a parameter type — it is
unambiguous, and that was judged cheaper than the `DB` ambiguity.
