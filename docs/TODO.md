# Roadmap

Milestones **F0 – F8**, ordered by dependency rather than by appeal. This is
close to Django's own build order, and for the same reason: you cannot generate an
admin site from models you do not have, and you cannot have models without
settings and migrations.

Each milestone ends with `just check` green. Items cite requirement IDs from
[requirements/FABRIN_REQUIREMENTS.md](requirements/FABRIN_REQUIREMENTS.md).

Legend: `[ ]` not started · `[~]` in progress · `[x]` done · ◐ milestone partly done

---

## F0 — Repo, harness, agent system, runnable core ◐

Tracked by [#1](https://github.com/usefabrin/fabrin/issues/1).

- [x] Licence (MIT, "Fabrin contributors"), README, `AGENTS.md` + `CLAUDE.md`
      pointer, `CONTRIBUTING.md`, a module that compiles. (INV-4, NFR-2)
- [x] `justfile` harness — `just check` is the one gate and exactly what CI runs.
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
- [x] Process slicing via `Options.Modules`, unknown name is an error.
      (FR-MODULES-1,2, MOD-001,002)
- [x] `fabrin/config` — layered settings with provenance; owns the `Options`
      declaration the root package aliases. (FR-CONFIG-1…5, CFG-001…004)
- [x] `fabrin/health` — `/healthz` liveness, `/readyz` failing closed and
      consulting only *mounted* modules. (FR-HEALTH-1…3, HLT-001…003)
- [x] `fabrin/logging` — slog setup + request ids, installed by default.
      (FR-LOG-1,2, LOG-001…004)
- [x] `examples/hello` — two modules, ports not imports, slicing demonstrated by a
      test rather than by prose. (FR-MODULES-3, MOD-003)
- [x] `apicheck` in the `tools/` module + `api/fabrin.txt` + the gate — snapshot
      drift *and* unblessed types in a signature. (INV-1,2, API-001…003, NFR-3)
- [x] Agent charters in `.claude/agents/` and repo skills — six charters, each
      with a hand-back condition, plus `new-module` and `issue-to-pr`. The
      `release` skill is deferred until there is a release policy to document.
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

## F2 — Models, metadata, migrations

- [x] **`fabrin/orm` — the metadata registry**, the load-bearing piece. Admin,
      forms and migrations all read Fabrin metadata, not GORM's, so the ORM stays
      swappable and the admin does not become GORM-shaped. It holds no database
      handle and cannot import `database/sql`, which is what lets a schema be
      read with nothing running. (FR-ORM-1, ORM-001…006)

      FR-ORM-1 stays *in progress* until the admin and forms read it, which is
      the clause its text actually promises.
- [ ] GORM as the shipped default adapter — a documented pattern and a worked
      example, **not** an exported Fabrin type returning `*gorm.DB`.
      ([ADR 0002](adr/0002-database-sql-is-the-orm-seam.md), FR-ORM-2)
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
- [ ] Duplicate-version gate — two branches claiming one version is a matter of
      when, not if, and it otherwise surfaces at deploy time. (FR-ORM-5)
- [ ] Transactions, connection pooling, one place to configure pool limits.

### Open before v0.1 — decisions that get expensive at the tag

Nothing here is a defect. Each is a shape that is free to change now and
breaking afterwards, so it needs an answer rather than a discovery.

- [ ] **Does a migration ever need to run outside a transaction?**
      `M.Up`/`M.Down` are `func(ctx, *sql.Tx) error`, so the answer is currently
      *no* — by construction, and silently. `CREATE INDEX CONCURRENTLY` is the
      case that forces it: PostgreSQL refuses it inside a transaction block, and
      it is how you index a large table without locking writes. Django ships
      `Migration.atomic = False` for exactly this.

      **Yes** means changing the signature *now*, to an interface both `*sql.Tx`
      and `*sql.DB` satisfy — `ExecContext`, `QueryContext`, `QueryRowContext`.
      That also closes a second hole: `*sql.Tx` exposes `Commit`/`Rollback`, so a
      user calling `tx.Rollback()` inside `Up` breaks the engine's own promise
      that the body and the bookkeeping row commit together.

      **No** means recording the foreclosure. [ADR 0002](adr/0002-database-sql-is-the-orm-seam.md)
      argued about `*sql.DB` — a handle Fabrin is *given* — and never about
      `*sql.Tx`, which appears in every migration a **user writes**. Different
      blast radius, unrecorded. Per [the ADR policy](adr/README.md#amending) an
      accepted ADR is not edited for substance, so this is a new ADR, not a
      footnote. (FR-ORM-4)
- [ ] **Three `orm.Field` fields have no semantics.** `Nullable`, `Unique` and
      `Index` are exported and read by nothing — not `validate`, not `clone`.
      There is no answer to whether `Index: true` on a `Unique: true` field is
      redundant or additive, and after v0.1 the answer has to stay compatible
      with whatever users assumed. Either give them meaning in the generator
      (#57) or withhold them until it needs them.

      Related: `validate` rejects composite keys today, so when they land they
      need `Model.PrimaryKey []string` — leaving two permanent ways to say the
      same thing, with `Field.PrimaryKey` unable to express the composite case.
      Same for multi-column `UNIQUE` and named indexes. Moving all three to
      `Model` costs nothing while nothing reads them. (FR-ORM-1)
- [ ] **`orm`'s type constants break the repo's only enum precedent.** `health`
      uses `StatusUp`/`StatusDown`; `orm` uses bare `String`, `Int`, `Time`.
      `orm.Time` sits one letter from `time.Time` in code that imports both.
      (FR-ORM-1)

### Gate holes, proven by injection

`just specs` and `just docs-check` bite on everything they advertise — each was
put through the hard-rule-4 loop. What they do **not** cover was proven the same
way, and is written down here rather than rediscovered:

- [ ] **`specs.sh`: a `status:` typo silently disables the traceability check.**
      `status: implemented` is its sole trigger and the vocabulary is
      unvalidated, so `status: done` removes an entry from the check while the
      gate still counts it and still reports it as traceable to a test.
- [ ] **`specs.sh` greps, it does not match structurally.** A deleted matrix
      *row* is satisfied by any prose mention of the id elsewhere in the file,
      and an unanchored `grep -q "func $fn"` passes when a test is renamed to a
      *longer* name. A commented-out entry still counts — and removing its
      orphaned matrix row then makes the gate fail on a behaviour that exists
      only as comment text.
- [ ] **`requirement:` is read by nothing.** No script parses it, in either
      direction. A spec entry may cite a requirement that does not exist, or the
      wrong one — which has already happened once and was caught by hand.
- [ ] **`specs/system-behavior.yaml` is not validated as YAML.** It parsed as
      none for the whole of F0–F2; `specs.sh` is line-based by design and reads
      it happily. The parser reports only the first error, so a fix reveals the
      next.

## F3 — Auth

- [ ] User model + memory-hard password hashing. (FR-AUTH-1)
- [ ] Server-side sessions, pluggable store. (FR-AUTH-2)
- [ ] Permissions and groups, checkable in handler and template. (FR-AUTH-3)
- [ ] Replaceable user model — an app with its own is not forced into Fabrin's.
      (FR-AUTH-4)
- [ ] Login/logout, `RequireAuth` middleware, CSRF.

## F4 — The admin site

The reason people want a Django-like framework. Everything above exists to make
this possible.

- [ ] CRUD screens generated from the metadata registry, no per-model code.
      (FR-ADMIN-1)
- [ ] `html/template` + htmx, `go:embed`ded — **no Node or Bun toolchain for
      Fabrin's users.** Tailwind compiled at Fabrin release time. (FR-ADMIN-2)
- [ ] Per-model overrides: list columns, filters, form fields, search. (FR-ADMIN-3)
- [ ] Every write permission-checked; the admin is not a bypass. (FR-ADMIN-4)
- [ ] Pagination that does not `SELECT *` a whole table to count it.

## F5 — Rendering, forms, static files

- [ ] `fabrin/render` — template loading, layouts, per-module template namespaces.
- [ ] `fabrin/forms` — binding and validation with errors a template can render
      field by field.
- [ ] Static file serving, embedding, cache headers, content hashing.

## F6 — Signals and background work

- [ ] `fabrin/signals` — in-process bus; `Subscriber` on modules. Django's signals,
      minus the action-at-a-distance: subscriptions are declared, not registered
      from anywhere.
- [ ] `fabrin/tasks` — background jobs and cron, with a durable store option.

## F7 — Remote ports and observability

Where the extractable-by-design claim gets its second half.

- [ ] HTTP adapter so a port can be satisfied across a process boundary without
      the module noticing.
- [ ] Bus backends (NATS, Redis) behind the `signals` interface.
- [ ] OpenTelemetry traces and metrics.
- [ ] Still **no** service discovery, mesh, or RPC framework. Adding one needs an
      ADR.

## F8 — The remaining batteries

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
