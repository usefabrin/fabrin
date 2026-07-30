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
- [ ] `fabrin.App`, module registry, router, graceful shutdown, reverse-order
      `Stop`. (FR-CORE-1…6, CORE-001…004)
- [ ] Process slicing via `FABRIN_MODULES`, unknown name is an error.
      (FR-MODULES-1,2, MOD-001,002)
- [ ] `fabrin/config` — layered settings with provenance. (FR-CONFIG-1…5, CFG-001…004)
- [ ] `fabrin/health` — `/healthz` liveness, `/readyz` failing closed.
      (FR-HEALTH-1…3, HLT-001,002)
- [ ] `fabrin/logging` — slog setup + request ids. (FR-LOG-1,2, LOG-001)
- [ ] `examples/hello` — two modules, ports not imports, slicing demonstrated by a
      test rather than by prose. (FR-MODULES-3, MOD-003)
- [ ] `apicheck` in the `tools/` module + `api/fabrin.txt` + the gate.
      (INV-1,2, API-001…003, NFR-3)
- [ ] Agent charters in `.claude/agents/` and repo skills.
- [ ] `perf/BASELINE.md` filled with real numbers vs raw Gin. (NFR-1)

## F1 — The `fabrin` CLI

The entry point to everything else, so it comes before the ORM: `fabrin new` needs
a project shape to generate, and F0 defines that shape.

- [ ] `fabrin new <name>` — runnable project scaffold. (FR-CLI-1)
- [ ] `fabrin startapp <name>` — module scaffold, wired in. (FR-CLI-2)
- [ ] `fabrin routes` — mounted routes with owning module. (FR-CLI-3)
- [ ] `fabrin version`, `fabrin serve`.
- [ ] `Commander` — modules contribute subcommands. Django's management commands.
      (FR-CLI-4)
- [ ] Scaffold output is itself an `examples/` entry, so `just check` proves the
      generator emits something that builds and boots.

## F2 — Models, metadata, migrations

- [ ] **Fabrin's own model-metadata registry** — the load-bearing piece. Admin,
      forms and migrations all read Fabrin metadata, not GORM's, so the ORM stays
      swappable and the admin does not become GORM-shaped. (FR-ORM-1)
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
