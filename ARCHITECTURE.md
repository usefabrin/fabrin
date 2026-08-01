# Architecture

How Fabrin is put together, and why. For the working agreement see
[AGENTS.md](AGENTS.md); for the roadmap see [docs/TODO.md](docs/TODO.md).

## The one thing that shapes everything else

**Fabrin is a library, not an application.** That single fact inverts instincts
that are correct in application repositories:

| In an application | In Fabrin |
|---|---|
| `internal/` holds the real code | `internal/` is **invisible** — Go forbids users importing it |
| Refactoring an exported name is free | Every exported symbol is a promise to strangers |
| Layering rules are the key boundary | **API-surface discipline** is the key boundary |
| Dependencies are your business | Your dependencies leak into every consumer's `go.sum` |

Anything a user needs is a **root-level package**. Putting a user-facing type in
`internal/` is not a style mistake, it is a bug — no import path can reach it.

## Package map

```
fabrin/                  package fabrin — App, Module, Router, Context/HandlerFunc
├── cli/                 Command + Dispatch        (Django: manage.py commands)
├── config/              layered settings          (Django: settings.py)
├── health/              liveness + readiness      (Django: system checks)
├── logging/             slog setup, request ids
├── fabrintest/          test client for Fabrin apps
├── orm/                 model metadata — no DB handle, no driver
├── migrate/             migration engine          (F2)
├── auth/                users, sessions, perms    (F3)
├── admin/               auto-CRUD admin site      (F4)
├── render/              templates, static files   (F5)
├── forms/               binding and validation    (F5)
├── signals/             event bus                 (F6)
├── tasks/               background jobs, cron      (F6)
├── cmd/fabrin/          the CLI — `new`, `startapp`, `version`
│
├── internal/scaffold/   go:embed project templates — unimportable by users
├── examples/            runnable apps, built and smoked by `just check`
├── api/fabrin.txt       checked-in snapshot of the exported surface
├── specs/               behaviour spec + test matrix
└── tools/               SEPARATE Go module — dev tooling
```

### Why `tools/` is its own module

`apicheck` needs `golang.org/x/tools/go/packages` for type information. If it
lived in the main module, **every Fabrin consumer** would carry `x/tools` in
their `go.sum` to run a tool they will never invoke. A library's dependency list
is part of its cost to adopt, so dev tooling gets its own module.

Anything under `tools/` that a user might want at runtime is in the wrong place.

### Why `orm/` holds no database handle

The map entry is deliberate and depguard enforces it: `orm` may not import
`database/sql`, and no driver appears anywhere near it. The package describes
tables and columns; it opens nothing.

That is what lets the admin (F4) and forms (F5) render a schema with no database
running, and it is why this package's tests finish in microseconds instead of
waiting on a container. It is also what keeps the ORM swappable — the admin reads
**Fabrin** metadata, so it never becomes GORM-shaped. The query API is
`database/sql` or whichever ORM the application chose, reached through an
interface the module declares for itself; Fabrin names neither. See
[ADR 0002](docs/adr/0002-database-sql-is-the-orm-seam.md).

## Request path

```
Your app's main()                    wires modules, satisfies their ports
        │
        ▼
fabrin.App                           registry, lifecycle, graceful shutdown
        │  installs the default middleware, then mounts each module's routes
        ▼
gin.Recovery                         a panic becomes a 500, not a dead process
        │
        ▼
logging.RequestID                    X-Request-ID on the context and the response
        │
        ▼
logging.Logger                       one structured line per completed request
        │
        ├──────────────▶ /healthz    liveness — consults nothing
        ├──────────────▶ /readyz     readiness — every mounted module's checks
        │
        ▼
Module.Routes(Router)                your code
        │
        ▼
gin.Engine                           blessed and public — every Gin middleware works
```

There is no adapter layer between your handler and Gin. That is deliberate.

**The middleware order is load-bearing.** `Recovery` is outermost so a panic in
either of the others still produces a response. `RequestID` precedes `Logger`
because `Logger` reads the id off the request context — reversed, every request is
logged without one and *nothing fails*, which is the kind of defect that survives
a refactor. The health routes are mounted before module routes and without any
module opting in, so an orchestrator's probes work against a stock app; a module
that declares `/healthz` itself panics at construction, which is Gin's
duplicate-route behaviour and the right moment to find out.

What this costs per request is measured, itemised, and defended in
[`perf/BASELINE.md`](perf/BASELINE.md) rather than assumed to be free.

## Gin is public, on purpose

`fabrin.Context` is a **type alias** for `gin.Context`; `fabrin.HandlerFunc` for
`gin.HandlerFunc`. Not a wrapper — an alias, so they are the *same type*.

**What this buys:** every Gin middleware in the ecosystem works unmodified.
`c.ShouldBindJSON`, `c.Param`, `c.SSEvent` — all of it, no ceremony, nothing to
learn twice, and nothing for Fabrin to keep in sync as Gin evolves.

**What it costs:** Gin's v1 API is part of Fabrin's semver contract. If Gin ever
ships v2 with a changed `Context`, Fabrin has a breaking change it did not
choose. That cost is bounded — Gin has been on v1 since 2015 — and it was
accepted deliberately, because "built on Gin" is a reason people would choose
Fabrin, not an implementation detail to hide.

**What it is not:** a licence to bless anything else. Gin is the only third-party
package permitted in an exported signature, and
[`apicheck`](#the-api-surface-gate)'s allowlist is the single reviewable record
of that. A second entry needs an ADR.

The decision, and the four alternatives it beat — a wrapper struct, a narrow
interface, Fabrin's own router, and an import-level restriction — is recorded in
[ADR 0001](docs/adr/0001-gin-as-a-type-alias.md).

### Why containment is not an import rule

An early draft restricted which *packages* may import Gin — "only the root
package." That rule is unimplementable: `health`'s handlers and `logging`'s
middleware **are** `gin.HandlerFunc` by definition, so it would have made both
packages unwritable.

The invariant that actually matters is narrower: Gin may appear in an **exported
signature** because it is allowlisted; nothing else may. That needs type
information, so `apicheck` enforces it and depguard does not.

## Modules — Fabrin's `INSTALLED_APPS`

The required interface is one method. Everything else is an **optional**
interface, type-asserted at registration, so a module pays only for what it uses:

```go
type Module interface {
    Name() string
    Routes(r Router)
}

// Optional — asserted at registration:
type Checker    interface { Checks() []health.Check }             // system checks
type Lifecycle  interface { Start(ctx) error; Stop(ctx) error }    // owned resources
type Modeler    interface { Models() []orm.Model }                // F2
type Migrator   interface { Migrations() []migrate.M }            // F2
type Commander  interface { Commands() []cli.Command }            // subcommands
type Subscriber interface { Subscribe(b signals.Bus) }            // F6
```

Optional interfaces rather than a fat one with stub methods: a module that owns
no resources should not have to write an empty `Start`. The cost is that a typo
in a method name means the interface silently is not satisfied — which is why the
registry logs which optional interfaces each module matched.

## Ports, not imports

**A module never imports another module.** It declares the interface it needs in
its own package, and the wiring passes in whatever satisfies it:

```go
// inside the blog module — blog does not know or care who provides this
type Clock interface{ Now() time.Time }

func New(clock Clock) *Blog { return &Blog{clock: clock} }
```

```go
// main.go — the only place that knows both sides exist
clock := systemClock{}
app, err := fabrin.New(cfg, blog.New(clock), reports.New(clock))
```

That interface is the **extraction seam**. Nothing in `blog` changes when its
dependency moves to another process — only `main.go` does.

A direct import welds the two modules together permanently, and the weld is
invisible until someone tries to split them. By then it is load-bearing.

## Deployment shapes

Fabrin is a **modular monolith by default, extractable by design**. Three
mechanisms, and deliberately no more.

**1. Ports, not imports** — above.

**2. Process slicing.** `FABRIN_MODULES` selects which registered modules this
process mounts:

```bash
fabrin serve                           # everything
FABRIN_MODULES=blog,auth fabrin serve  # the web tier
FABRIN_MODULES=reports fabrin serve    # the reports service
```

One binary, N deployment shapes. Splitting a monolith becomes a deploy-config
change rather than a rewrite. An unknown module name is an **error**, never a
silent no-op — a typo that quietly serves nothing is the worst possible outcome
here, because the process looks healthy.

**3. Swappable satisfaction.** A port satisfied in-process by a direct call can
instead be satisfied by an HTTP client adapter. The module cannot tell, and its
tests do not change.

### Explicit non-goals for v0

Fabrin ships **no** service discovery, service mesh, RPC framework, or
remote-client code generator. Those are solved elsewhere and solved better.
Fabrin ships the seam, plus service-ready defaults: structured logging,
liveness/readiness, config from the environment, graceful shutdown.

Adding any of the non-goals needs an ADR. "We're a microservice framework too" is
how batteries-included frameworks become un-learnable.

## Boundary rules

Enforced by depguard (`.golangci.yml`); documented for humans in
[CONTRIBUTING.md](CONTRIBUTING.md).

| Rule | Why |
|---|---|
| `config` must not import Gin or `net/http` | Settings must load from the CLI, from tests, and from a migrate-only process without constructing an HTTP stack |
| `config`, `logging`, `health`, `cli`, `orm` must not import the root package **or a sibling** | They sit *below* it. The rule's main work is catching a *sibling* import, which compiles cleanly and quietly makes a leaf depend on half the framework. Where the root package already imports the leaf, the root direction is additionally a cycle the compiler rejects — but not for `orm` yet, where this rule is the only thing stopping it |
| `orm` must not import `database/sql` | Metadata is a description; the handle belongs to the application. Otherwise the admin needs a database running to render a form, and this package's tests need one to run |
| `internal/**` must not import the root package | The dependency runs public → internal |

Every public package needs a `# boundary: <name> — <decision>` line in
`.golangci.yml`, checked by `scripts/gates/check-depguard-coverage.sh`. "No rules
needed" is a valid decision — it just has to be written down, so *considered* and
*forgotten* stay distinguishable.

## The API surface gate

`api/fabrin.txt` is a checked-in snapshot of every exported symbol. `just api`
regenerates it; `just api-check` fails when code and snapshot disagree. A
governed-surface change must also update `CHANGELOG.md`.

**Type aliases are recorded unexpanded** — `type Context = github.com/gin-gonic/gin.Context`
is one line, not Gin's forty-odd methods. Expanding them would churn the snapshot
on every Gin patch bump, turning `api-check` red on dependency updates that
change nothing about Fabrin. A gate that cries wolf gets ignored, and an ignored
gate is worse than none.

The tradeoff accepted: the snapshot will not show Gin's own breaking changes.
Gin's release notes cover that.

**The gate answers a second question.** Before comparing snapshots it fails on an
unblessed third-party type anywhere in an exported signature — result, parameter,
variadic, struct field, interface method, method signature, package variable, type
argument, or nested inside containers to any depth. That last part is the one
worth stating: `type Options struct { DB *gorm.DB }` puts GORM's version into
Fabrin's semver contract exactly as surely as a function returning one, and is far
likelier to happen, because a field gets added without anyone rereading the rules.

The two checks are different failures with different fixes. Drift means *record
the change*; a leak means *do not make it* — accept an interface you declare, or
return your own type. So the leak check runs first, and its message says so.

## Prove a gate bites

When you add or change a gate or rule: inject a throwaway violation, watch it
**fail**, then revert — and pair it with a **negative control** confirming the
legitimate case still **passes**.

Both halves are necessary. A gate that fails proves it can fail; only the
negative control proves it fails for the *right reason*. A rule rejecting
everything looks identical to a correct one, and
[#14](https://github.com/usefabrin/fabrin/issues/14) is what the missing half
costs: a coverage gate that matched substrings, verified with a string absent
from the file, so the test passed and the fail-open hid behind it.

## Testing

- **TDD.** The failing test comes first. A behavioural claim in a doc with no test
  behind it is a wish.
- **`specs/`** is the index: `system-behavior.yaml` lists behaviours by ID,
  `test-matrix.md` maps each to its test, and `just specs` fails when an
  `implemented` behaviour names a test that does not exist.
- **`examples/`** is the only documentation a compiler can check. `just check`
  builds every example *and boots it* — compilation catches a renamed symbol, but
  only booting catches a nil dependency or a module that panics during
  registration, which is what wiring changes actually break.
