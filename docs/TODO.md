# Roadmap

Milestones **F0 – F8**, ordered by dependency rather than by appeal. This is
close to Django's own build order, and for the same reason: you cannot generate an
admin site from models you do not have, and you cannot have models without
settings and migrations.

Each milestone ends with `just check` green. Items cite requirement IDs from
[requirements/FABRIN_REQUIREMENTS.md](requirements/FABRIN_REQUIREMENTS.md).

Legend: `[ ]` not started · `[~]` in progress · `[x]` done · ◐ milestone partly done

---

## F0 — Repo, harness, agent system, runnable core

Tracked by [#1](https://github.com/usefabrin/fabrin/issues/1).

- [x] Licence (MIT, "Fabrin contributors"), README, `AGENTS.md` + `CLAUDE.md`
      pointer, `CONTRIBUTING.md`, a module that compiles. (INV-4, NFR-2)
- [x] `justfile` harness — `just check` is the local quality gate and exactly
      what CI's quality job runs; docs, race, and main-only benchmarks are
      explicit additional jobs.
      (NFR-6)
- [x] Hygiene gates with a recorded boundary decision per public package.
      (INV-5, INV-6)
- [x] Pre-commit hook (fast subset only), resolving the hooks dir correctly in
      worktrees.
- [x] CI calling `just ci`, `docs-guard` on the PR range, `bench` on `main`.
      (NFR-5, NFR-6)
- [x] Issue forms (feature/bug/epic) + PR template.
- [x] `ARCHITECTURE.md`, this roadmap, `DJANGO_PARITY.md`, coding guidelines,
      requirements, `specs/`, `CHANGELOG.md`.
- [x] `fabrin.App`, module registry, router, graceful shutdown, reverse-order
      `Stop`. (FR-CORE-1…6, CORE-001…004)
- [x] Route/capability slicing via `Options.Modules`, unknown name is an error.
      `New` retains eager semantics; `NewFromFactories` validates before callbacks
      and constructs only the selected set in registration order.
      ([ADR 0004](adr/0004-module-factories-select-before-construction.md),
      FR-MODULES-1…4, MOD-001…005, #77)
- [x] `fabrin/config` — layered settings with provenance; owns the `Options`
      declaration the root package aliases. (FR-CONFIG-1…5, CFG-001…004)
- [x] `fabrin/health` — `/healthz` liveness, `/readyz` failing closed,
      deadline-bounded even for non-cooperative checks, and consulting only
      *mounted* modules. (FR-HEALTH-1…4, HLT-001…004)
- [x] `fabrin/logging` — slog setup + request ids, installed by default.
      (FR-LOG-1,2, LOG-001…004)
- [x] `examples/hello` — two modules, ports not imports, slicing demonstrated by a
      test rather than by prose. (FR-MODULES-3, MOD-003)
- [x] `apicheck` in the `tools/` module + `api/fabrin.txt` + the gate — snapshot
      drift *and* unblessed types in a signature. (INV-1,2, API-001…003, NFR-3)
- [x] Canonical orchestration in `docs/agents/` with task/result schemas,
      ownership and fan-in rules, six role charters, and generated native
      adapters for Claude Code, Codex, and Cursor. Same-platform delegation only;
      adapter parity is gated. (NFR-8, INV-8)
- [x] `perf/BASELINE.md` filled with real numbers vs raw Gin — **Fabrin's own
      abstractions add zero allocations**; the default observability stack adds
      13, itemised and justified there. (NFR-1)

## F1 — The `fabrin` CLI

The entry point to everything else, so it comes before the ORM: `fabrin new` needs
a project shape to generate, and F0 defines that shape.

- [x] `fabrin/cli` — `Command` and `Dispatch` over stdlib `flag`. A leaf: the root
      package imports it to declare `Commander`, so it can never take an `*App`.
      (CLI-001…003)
- [x] `fabrin new <name>` — runnable project scaffold that builds, tests, and
      boots. Templates live in `internal/scaffold`, so none of them is a public
      promise. (FR-CLI-1, CLI-010)
- [x] `fabrin startapp <name>` — module scaffold, wired into `newApp` by locating
      the `fabrin.New` call with `go/ast` and splicing at the offsets it reports.
      (FR-CLI-2, CLI-011…012)
- [x] `./myapp routes` — mounted routes with owning module. Answered by the app's
      own binary, because Go compiles and no separate tool can introspect an
      application it did not build. (FR-CLI-3, CLI-004…005)
- [x] `version`, `serve`, and `App.Execute` as the entry point a `main` hands
      `os.Args[1:]` to. No arguments still means serve.
- [x] `Commander` — modules contribute subcommands. Django's management commands.
      Collected from *mounted* modules only, and a colliding name fails at
      construction. (FR-CLI-4, CLI-007…008)
- [x] `just check` generates a project, builds it, runs its tests, extends it
      with `startapp`, and boots it — against the working tree rather than the
      published module. Not an `examples/` entry after all: a generated project
      has its own `go.mod`, which under `examples/` would be a nested module the
      other two steps cannot build. (NFR-5, CLI-013)

## Stabilization gate before new F2–F5 work

New model/migration, rendering, auth, and admin feature work pauses until the
stabilization epic's runtime, harness, orchestration, and contract corrections
are green. Public API, ADR, security, migration, orchestration, CI, and gate
changes require human review.

The stabilization epic is complete. Lazy selection-before-construction is
resolved by [#77](https://github.com/usefabrin/fabrin/issues/77) and proposed
[ADR 0004](adr/0004-module-factories-select-before-construction.md). Remaining
pre-v0 decisions are the admin CRUD/type seam
([#78](https://github.com/usefabrin/fabrin/issues/78)), provisional ORM
constraints ([#79](https://github.com/usefabrin/fabrin/issues/79)), and the auth
threat model ([#80](https://github.com/usefabrin/fabrin/issues/80)).

## F2 — Models, metadata, migrations ◐

- [x] **`fabrin/orm` — the metadata registry**, the load-bearing piece. Admin,
      forms and migrations all read Fabrin metadata, not GORM's, so the ORM stays
      swappable and the admin does not become GORM-shaped. It holds no database
      handle and cannot import `database/sql`, which is what lets a schema be
      read with nothing running. (FR-ORM-1, ORM-001…006)

      FR-ORM-1 stays *in progress* until the admin and forms read it, which is
      the clause its text actually promises.
- [x] Consumer-owned data port pattern and worked example, **not** an exported
      Fabrin query API or third-party handle.
      ([ADR 0002](adr/0002-database-sql-is-the-orm-seam.md), FR-ORM-2)

      The **pattern and the worked example** landed as `examples/hello/orders`
      (#60, ORM-010/011): the module declares the two-method `Store` it needs,
      `main.go` is the only file that names SQL, and there are two
      implementations of the port because an interface with exactly one forever
      is a wrapper wearing a disguise. It is over `database/sql` and SQLite, not
      GORM — which is the seam ADR 0002 chose, so the pattern is the same
      whatever sits behind it.

      Fabrin ships no GORM adapter today. Whether a default query adapter should
      exist is a separate pre-v0 API decision; documentation must not call an
      unwritten adapter the default.
- [x] `Modeler` — modules declare their models; no package scanning. Collected
      from **mounted** modules only, so a sliced process is handed only the
      schema it owns, and two modules claiming one table fails at construction.
      `App.Models()` is what the generator will read. (FR-ORM-3, ORM-007…009)
- [x] `fabrin/migrate` — the engine. Versioned, ordered, reversible; each
      migration and the row recording it commit in **one** transaction, and an
      unapplied migration sorting before an applied one is an error. Takes a
      `*sql.DB`, imports no driver, no Gin, no `net/http`, so it runs from a
      process that mounts no routes. (FR-ORM-4, MIG-001…006)
- [ ] `fabrin migrate` / `makemigrations` as commands, and migrations as files on
      disk. The engine above takes them as values; nothing reads a directory yet.
      (FR-ORM-4)
- [~] Duplicate-version gate — two branches claiming one version is a matter of
      when, not if, and it otherwise surfaces at deploy time. (FR-ORM-5)

      The **engine** half is done: two migrations at one version are rejected
      (MIG-007), as is a set whose versions do not sort as written (MIG-009).
      The **pre-merge gate** is blocked, and on something outside the engine —
      its acceptance criterion is that it reads migration *files*, so a
      colliding migration in a branch that does not compile is still caught, and
      there is no on-disk format to read yet. Writing one now would invent the
      format `fabrin makemigrations` must then live with. (MIG-008)
- [ ] Transactions, connection pooling, one place to configure pool limits.

### Open before v0.1 — decisions that get expensive at the tag

Nothing here is a defect. Each is a shape that is free to change now and
breaking afterwards, so it needs an answer rather than a discovery.

- [x] **Does a migration ever need to run outside a transaction?** Answered
      **yes**, and the *type* moved for it while the *feature* did not:
      `M.Up`/`M.Down` are now `func(ctx, migrate.Handle) error`, over an exported
      interface of four frozen methods that `*sql.Tx`, `*sql.DB` and `*sql.Conn`
      satisfy unmodified. `CREATE INDEX CONCURRENTLY` is the case that forced it
      — PostgreSQL refuses it inside a transaction block, and it is how you index
      a large table without locking writes.

      **The engine still passes a transaction every time**, and the version row
      still commits with the body, so MIG-001 is unchanged. Django's
      `Migration.atomic = False` has **no** equivalent yet; what the change
      bought is that `NonAtomic bool` can land later with no edit to anyone's
      migration. Its polarity is bound now, because `Atomic bool` zero-values to
      `false` and would silently make every existing migration non-atomic.

      It does **not** close the `Commit`/`Rollback` hole, and the reverse was
      claimed while this was open: a narrow interface restricts nothing —
      `h.(*sql.Tx).Rollback()` still compiles. It converts an accident into a
      deliberate act. What holds the wrapper option open is the doc comment
      saying the dynamic type is not part of the contract, not the interface.
      ([ADR 0003](adr/0003-migrations-take-a-handle-not-a-transaction.md), #67,
      FR-ORM-4, MIG-010)
- [ ] **Three `orm.Field` fields have no semantics.** `Nullable`, `Unique` and
      `Index` are exported and read by nothing — not `validate`, not `clone`.
      There is no answer to whether `Index: true` on a `Unique: true` field is
      redundant or additive, and after v0.1 the answer has to stay compatible
      with whatever users assumed. Either give them meaning in the generator
      (#57) or withhold them until it needs them.

      Related: `validate` rejects composite keys today, so when they land they
      need `Model.PrimaryKey []string` — leaving two permanent ways to say the
      same thing, with `Field.PrimaryKey` unable to express the composite case.
      Same for multi-column `UNIQUE` and named indexes. Resolve the representation
      intentionally rather than assuming an exported struct can grow for free.
      ([#79](https://github.com/usefabrin/fabrin/issues/79), FR-ORM-1)
- [ ] **`orm`'s type constants break the repo's only enum precedent.** `health`
      uses `StatusUp`/`StatusDown`; `orm` uses bare `String`, `Int`, `Time`.
      `orm.Time` sits one letter from `time.Time` in code that imports both.
      (FR-ORM-1)

### Gate hardening

- [x] `speccheck` parses YAML structurally, validates the status vocabulary and
      requirement IDs, matches exact matrix rows, and resolves real `_test.go`
      functions with Go test signatures through the AST. Its previously
      false-positive cases are regression-tested in the tools module. (NFR-9)
- [x] Docs freshness includes deletions and maps API, boundaries, command,
      validation, public-package, and orchestration changes to their owning
      documents. It fails closed on bad ranges and checks both sides of renames;
      it deliberately does not claim to understand prose.
- [x] `agentcheck` compiles the packet schemas and enforces platform/base
      identity, catalog access, disjoint ownership/worktrees/resources, and
      stale or out-of-scope result rejection at dispatch and fan-in. (NFR-8)

## F3 — Rendering, forms, static, and CSRF foundation

This minimal vertical foundation comes before auth and admin because both need
safe form errors, templates, and CSRF integration rather than private one-off
versions.

- [ ] `fabrin/render` — template loading, layouts, per-module namespaces.
      (FR-RENDER-1)
- [ ] `fabrin/forms` — binding and validation with field and non-field errors.
      (FR-RENDER-2)
- [ ] Static serving, embedding, cache headers, and content hashing.
      (FR-RENDER-3)
- [ ] CSRF middleware plus template/form integration. (FR-RENDER-4)

## F4 — Auth

- [ ] User model + memory-hard password hashing. (FR-AUTH-1)
- [ ] Server-side sessions, pluggable store. (FR-AUTH-2)
- [ ] Permissions and groups, checkable in handler and template. (FR-AUTH-3)
- [ ] Replaceable user model — an app with its own is not forced into Fabrin's.
      (FR-AUTH-4)
- [ ] Login/logout, `RequireAuth` middleware, CSRF.
- [ ] Threat model before API freeze: rotation and fixation, cookie defaults,
      enumeration, password upgrades, recovery, audit, and fail-closed authz.
      ([#80](https://github.com/usefabrin/fabrin/issues/80))

## F5 — The admin site

The reason people want a Django-like framework. Everything above exists to make
this possible.

- [ ] CRUD screens generated from the metadata registry, no per-model code.
      (FR-ADMIN-1)
- [ ] `html/template` + htmx, `go:embed`ded — **no Node or Bun toolchain for
      Fabrin's users.** Tailwind compiled at Fabrin release time. (FR-ADMIN-2)
- [ ] Per-model overrides: list columns, filters, form fields, search. (FR-ADMIN-3)
- [ ] Every write permission-checked; the admin is not a bypass. (FR-ADMIN-4)
- [ ] Pagination that does not `SELECT *` a whole table to count it.
- [ ] Prove one internal vertical CRUD slice from metadata to persistence before
      freezing any public admin seam; current metadata has no generic CRUD or Go
      type link. ([#78](https://github.com/usefabrin/fabrin/issues/78))

## F6 — Signals and background work

Exploratory theme, not a committed feature set until requirements and acceptance
criteria are written.

- [ ] `fabrin/signals` — in-process bus; `Subscriber` on modules. Django's signals,
      minus the action-at-a-distance: subscriptions are declared, not registered
      from anywhere.
- [ ] `fabrin/tasks` — background jobs and cron, with a durable store option.

## F7 — Remote ports and observability

Exploratory theme. This is where the extraction seam gains a remote
implementation; current process slicing does not make extraction deploy-only.

- [ ] HTTP adapter so a port can be satisfied across a process boundary without
      the module noticing.
- [ ] Bus backends (NATS, Redis) behind the `signals` interface.
- [ ] OpenTelemetry traces and metrics.
- [ ] Still **no** service discovery, mesh, or RPC framework. Adding one needs an
      ADR.

## F8 — The remaining batteries

Exploratory theme, not a committed feature set until requirements and acceptance
criteria are written.

- [ ] `fabrin/cache` — interface plus in-memory and Redis.
- [ ] `fabrin/mail` — backends including a test one that captures instead of sends.
- [ ] i18n and l10n.
- [ ] Rate limiting and throttling.

---

## Not planned

Recording these saves re-litigating them:

- **A template DSL that reimplements control flow.** `html/template` plus Go
  functions, or `templ` if a user prefers it. No magic a debugger cannot step
  through.
- **Reflection-driven routing from struct tags.** Routes are code, so they are
  greppable and the compiler checks them.
- **An ORM that hides SQL.** `fabrin/orm` is a seam over a real ORM, and dropping
  to SQL is always available.
- **Service discovery, mesh, RPC framework.** See F7.
- **A JS build step imposed on Fabrin's users.** See F4.
