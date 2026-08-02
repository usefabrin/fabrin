# 0002. `database/sql` is the ORM seam; GORM is not blessed

- **Status:** Accepted
- **Date:** 2026-08-01
- **Deciders:** Fabrin contributors
- **Requirement / issue:** INV-1, FR-ORM-1, FR-ORM-2, NFR-4

## Context

F0 proposed "GORM behind a thin `fabrin/orm` seam, with Fabrin owning its own
model-metadata registry". The durable decision here is who owns the metadata and
whether a third-party handle is blessed; it does not claim an unwritten GORM
adapter is currently shipped.

The question is forced by three things that are already true:

**Hard rule 1.** `apicheck`'s allowlist holds exactly one entry, and
`TestAllowlist_HoldsOnlyGin` fails on a silent second. Anything blessed becomes
part of Fabrin's semver contract permanently, and lands in every consumer's
`go.sum` whether they use it or not.

**[ADR 0001](0001-gin-as-a-type-alias.md) blessed Gin, and said why that is not
a precedent.** Gin's `Context` appears in every handler a user writes; the
ecosystem is the feature. Neither is true of a database handle.

**NFR-4: every battery sits behind an interface a user can replace.** An ORM is
the battery most likely to be replaced — by `sqlc`, by `pgx`, by hand-written
SQL, by a store the user already has.

## Decision

**No GORM type appears in any Fabrin exported signature.** The allowlist stays at
one entry.

The seam is **`database/sql`**, which is standard library and therefore permitted
without an allowlist change:

- `fabrin/migrate` operates on `*sql.DB`.
- `fabrin/orm` owns model metadata — descriptors, the registry, the `Modeler`
  collection — and holds no database handle at all.
- A module that needs data **declares the interface it needs in its own package**
  and receives an implementation, exactly as `greet` declares `Clock`. That
  interface is the same extraction seam hard rule 3 is about, applied to the data
  layer.

`main` is the only place any ORM is named:

```go
// the module declares what it requires
package orders

type Store interface {
    Find(ctx context.Context, id int64) (*Order, error)
}

func New(s Store) *Module { return &Module{store: s} }

// main.go — the application chooses and owns the adapter
db, err := sql.Open("sqlite", dsn)
app, err := fabrin.New(opts, orders.New(sqlstore.New(db)))
```

The worked example uses `database/sql` and SQLite. Fabrin currently ships no
GORM adapter and names no default query API. Choosing one later is a separate
pre-v0 decision; it does not alter the rule that no third-party database handle
enters Fabrin's exported signatures.

## Consequences

**What this buys.** The allowlist stays reviewable at one entry. GORM's version
never enters Fabrin's semver contract, and never enters the `go.sum` of a user
who chose `pgx`. Swapping the ORM is a change in one `main.go` and one adapter,
not a change to Fabrin. Migrations run against anything with a `database/sql`
driver, which is every database Go can talk to.

**What it costs, and this is the real cost.** A user gets less for free than
Django gives them. There is no `Model.objects`, no ambient handle, no
`fabrin.DB()`. Wiring a store is work Fabrin will not do for them — and for a
batteries-included framework that is a genuine subtraction, not a neutral trade.
The mitigation is that the wiring is one obvious line in `main` and the pattern
is demonstrated in `examples/`, not that the cost is imaginary.

**What it forecloses.** Fabrin cannot later offer `app.DB()` returning a GORM
handle without a new ADR superseding this one, a second allowlist entry, and a
major version.

**A second-order consequence worth stating.** Because migrations take `*sql.DB`
and Fabrin ships no driver, Fabrin's own tests need one. It enters `go.mod` as a
test-only dependency — Go does not distinguish those — so it reaches consumers'
`go.sum` while never being linked into their binaries. That is a real cost of
this decision, accepted, and revisited if it grows.

## Alternatives considered

### Bless GORM: `fabrin/orm` exposes `*gorm.DB`

Rejected. It is the most convenient option and the one a user arriving from
Django would expect, which is exactly why it needs an argument rather than a
default.

It puts GORM's v1 API inside Fabrin's semver contract permanently, for a battery
NFR-4 says must be replaceable. Gin earned that treatment because its types are
in every handler and its middleware ecosystem is the reason to choose Fabrin.
GORM's types would be in Fabrin's API for the convenience of not writing an
adapter — a much smaller benefit for the same permanent price. It also lands GORM
and its drivers in the `go.sum` of every consumer, including those who chose
something else.

### A Fabrin-defined `orm.DB` interface that GORM satisfies

Rejected, and this is the near miss. It sounds like the ports-not-imports pattern
and is its opposite: a *consumer* declaring the narrow set of methods it needs is
the pattern; a *framework* declaring a wide database interface for everyone is
the "narrow interface" alternative [ADR 0001](0001-gin-as-a-type-alias.md)
rejected, one layer further down.

Either the interface reproduces enough of GORM to be useful — at which point it
is a wrapper with extra steps, and Fabrin owns maintaining it against GORM's
releases forever — or it does not, and users hit a wall the first time they need
`Preload` or a raw query. It also inverts the cost the same way: cheap for
Fabrin to change, expensive for users to write against.

`database/sql` is the interface that already exists, that the whole ecosystem
implements, and that Fabrin does not have to maintain.

### Fabrin ships its own query builder

Rejected for v0, and probably forever. It is the only option that makes the data
API entirely Fabrin's, and it costs a query builder — years of edge cases in
dialects, nulls, and joins — to solve a problem GORM and sqlc do not have.
Fabrin's scarce resource is the batteries, not the SQL.

### The GORM adapter in a separate module

Considered and deferred rather than rejected. `github.com/usefabrin/fabrin-gorm`
would keep GORM out of Fabrin's `go.mod` entirely, which is strictly better than
a test-only dependency.

It is not needed yet: under this decision Fabrin's own code never imports GORM,
so there is nothing to extract. Revisit if the worked example grows enough to
want its own release cycle.
