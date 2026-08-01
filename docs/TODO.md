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

      Nothing declares a model into it yet — `Modeler` is the next item, and
      FR-ORM-1 stays *in progress* until something reads the registry.
- [ ] GORM adapter behind the `fabrin/orm` seam. (FR-ORM-2)
- [ ] `Modeler` — modules declare their models; no package scanning. (FR-ORM-3)
- [ ] `fabrin migrate` / `makemigrations`; versioned files with a down step.
      (FR-ORM-4)
- [ ] Duplicate-version gate — two branches claiming one version is a matter of
      when, not if, and it otherwise surfaces at deploy time. (FR-ORM-5)
- [ ] Transactions, connection pooling, one place to configure pool limits.

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
