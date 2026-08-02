# Fabrin requirements

Numbered so that [docs/TODO.md](../TODO.md), `specs/system-behavior.yaml`, and
commit messages can cite a stable ID instead of restating intent. An ID never
changes meaning; if the requirement changes, it gets a new ID and the old one is
marked superseded.

**Prefixes.** `FR-<AREA>-n` functional · `NFR-n` non-functional ·
`INV-n` invariant (something that must never become false).

Status values: `planned` · `in progress` · `done` · `superseded`.

---

## FR-CORE — application core

| ID | Requirement | Status |
|---|---|---|
| FR-CORE-1 | An application is composed from a set of modules passed to `fabrin.New`. A module supplies a name and mounts routes; everything else is optional. | done |
| FR-CORE-2 | Registering two modules with the same name is an error at construction, not at first request. | done |
| FR-CORE-3 | `App.Run` serves until its context is cancelled or the process receives SIGINT/SIGTERM, then shuts down gracefully within a bounded window. | done |
| FR-CORE-4 | A module may declare owned resources via `Lifecycle`. `Start` runs in caller-supplied registration order and `Stop` in reverse. Fabrin infers no dependency graph; `main` owns an order that keeps required resources alive. | done |
| FR-CORE-5 | A module may declare health checks via `Checker`. The app aggregates them. | done |
| FR-CORE-6 | Optional module interfaces are discovered by type assertion at registration, and the registry reports which ones each module matched — a mistyped method name otherwise fails silently. | done |

## FR-MODULES — composition and deployment shapes

| ID | Requirement | Status |
|---|---|---|
| FR-MODULES-1 | `Options.Modules` selects which already-constructed registered modules mount routes and optional capabilities; empty means all. It does not suppress dependency construction performed before `fabrin.New`. `fabrin/config` populates it from `FABRIN_MODULES`. | done (core) |
| FR-MODULES-2 | A selected name matching no registered module is a startup **error**, and the error lists what *is* registered. A typo that silently serves nothing is worse than a crash, because the process passes its liveness probe while doing no work. | done |
| FR-MODULES-3 | A module declares its dependencies as interfaces in its own package and receives them as arguments. Fabrin provides no service locator or global registry to fetch another module from. | done |
| FR-MODULES-4 | `fabrin.NewFromFactories` validates the complete named factory catalogue and `Options.Modules` selection before invoking a builder, then builds only selected modules in factory registration order. A factory name must match the module it returns. Dependencies remain explicit, compiler-checked values captured by each builder; Fabrin introduces no service locator. | done |

## FR-CONFIG — settings

| ID | Requirement | Status |
|---|---|---|
| FR-CONFIG-1 | Settings resolve in the order defaults ← file ← environment ← flags, with later layers winning. | done |
| FR-CONFIG-2 | Every resolved value reports which layer set it. A misconfigured deploy is otherwise undebuggable — you can see the wrong value but not where it came from. | done |
| FR-CONFIG-3 | An unparseable or unknown-typed value fails at load, not at first use. Failing late means failing in production after a green deploy. | done |
| FR-CONFIG-4 | `FABRIN_ADDR` is the listen address, default `:8080`. Named as a requirement because `scripts/smoke-examples.sh` depends on it to give each example its own port. | done |
| FR-CONFIG-5 | Settings load without constructing an HTTP stack, so the CLI, tests, and a migrate-only process can read them. | done |

## FR-HEALTH — liveness and readiness

| ID | Requirement | Status |
|---|---|---|
| FR-HEALTH-1 | `/healthz` reports process liveness and consults **no** dependencies. A liveness probe that fails on a slow database causes a restart loop that cannot fix anything. | done |
| FR-HEALTH-2 | `/readyz` aggregates every mounted module's checks and **fails closed**: unknown or errored means not ready. Reporting ready while a dependency is unreachable takes traffic the process cannot serve. | done |
| FR-HEALTH-3 | A failing readiness check names which module and check failed. | done |
| FR-HEALTH-4 | Readiness returns within its deadline even when a check ignores cancellation, fails closed for that check, and starts at most one outstanding invocation per registered check. | done |

## FR-LOG — observability

| ID | Requirement | Status |
|---|---|---|
| FR-LOG-1 | Structured logging via `log/slog`, JSON or text by configuration. | done |
| FR-LOG-2 | Every request carries a request id, available on the context and returned in a response header, so a user-reported error can be found in the logs. | done |
| FR-LOG-3 | A recovered handler panic produces a structured completed-request log with its request id, actual committed response status, explicit panic marker, and error severity; recovery writes 500 only when the response is not already committed. | done |

## FR-CLI — the `fabrin` command (F1)

| ID | Requirement | Status |
|---|---|---|
| FR-CLI-1 | `fabrin new <name>` scaffolds a runnable project. | done |
| FR-CLI-2 | `fabrin startapp <name>` scaffolds a module wired into the project. | done |
| FR-CLI-3 | `fabrin routes` lists mounted routes with their module, so "which module owns this URL" is answerable without reading the source. | done |
| FR-CLI-4 | A module may contribute subcommands via `Commander` (Django's management commands). | done |
| FR-CLI-5 | `-h`, `-help`, and `--help` anywhere in the initial configuration-flag prefix print application usage and exit successfully; invalid leading flags return an ordinary startup error rather than panic. | done |

## FR-ORM — data layer (F2)

| ID | Requirement | Status |
|---|---|---|
| FR-ORM-1 | Fabrin owns a model-metadata registry independent of the ORM. The admin, forms, and migrations read **Fabrin** metadata, so swapping the ORM does not rewrite them. | in progress |
| FR-ORM-2 | Data access crosses a consumer-owned port. `examples/hello/orders` demonstrates the same module with an in-memory test store and a `database/sql` + SQLite application adapter. Fabrin currently ships no GORM adapter or default query API. See [ADR 0002](../adr/0002-database-sql-is-the-orm-seam.md). | done |
| FR-ORM-3 | A module declares its models via `Modeler`; Fabrin does not scan packages for them. | done |
| FR-ORM-4 | Migrations are versioned, ordered files with an explicit down step. | in progress |
| FR-ORM-5 | Two migrations may not claim the same version. With several branches in flight this is a matter of when, not if, and it surfaces at deploy time otherwise. | in progress |

## FR-RENDER — rendering, forms, and static foundation (F3)

| ID | Requirement | Status |
|---|---|---|
| FR-RENDER-1 | Server-rendered templates have layouts, per-module namespaces, safe escaping, and an explicit function surface. | planned |
| FR-RENDER-2 | Form binding and validation produce field and non-field errors that templates can render without reflection-driven control flow. | planned |
| FR-RENDER-3 | Static assets may be embedded and served with content types, cache headers, and content hashing. | planned |
| FR-RENDER-4 | CSRF protection has a middleware contract and a template/form integration point before auth or admin depend on it. | planned |

## FR-AUTH — authentication and authorisation (F4)

| ID | Requirement | Status |
|---|---|---|
| FR-AUTH-1 | A default user model with password hashing using a memory-hard KDF. | planned |
| FR-AUTH-2 | Server-side sessions with a pluggable store. | planned |
| FR-AUTH-3 | Permissions and groups, checkable in a handler and in a template. | planned |
| FR-AUTH-4 | The user model is replaceable — an app with its own is not forced into Fabrin's. | planned |

## FR-ADMIN — the admin site (F5)

| ID | Requirement | Status |
|---|---|---|
| FR-ADMIN-1 | CRUD screens generated from the model-metadata registry, with no per-model code required. | planned |
| FR-ADMIN-2 | Rendered server-side with `html/template` + htmx and `go:embed`ded. Fabrin's users must not need a Node or Bun toolchain to get an admin. | planned |
| FR-ADMIN-3 | Per-model overrides for list columns, filters, and form fields. | planned |
| FR-ADMIN-4 | Every admin write is permission-checked; the admin is not a bypass. | planned |

---

## NFR — non-functional

| ID | Requirement | Status |
|---|---|---|
| NFR-1 | Framework overhead over raw Gin is measured, recorded in `perf/BASELINE.md`, and re-measured on every push to `main`. A "fast framework" claim with no number behind it is marketing. | done |
| NFR-2 | Fabrin builds on the Go version declared in `go.mod`, not merely the newest release. | done |
| NFR-3 | Dev tooling dependencies never enter the framework's `go.mod`. | done |
| NFR-4 | Every battery sits behind an interface a user can replace. Defaults exist so you need not choose, not so you cannot. | planned |
| NFR-5 | Every example compiles **and boots** in CI. | done |
| NFR-6 | `just check` is the complete local quality gate and exactly what CI's `quality` job runs. Range-aware docs freshness, `just race`, and main-only benchmarks run as explicit additional CI jobs. | done |
| NFR-7 | Where an underlying library's default is unsafe or noisy, Fabrin's differs and the reason is written at the call site. A setting that resolves and validates must change behaviour — one that does not is the same defect as one referenced but never implemented. | done |
| NFR-8 | Multi-agent delegation uses one native platform per run, schema- and semantics-validated task/result packets, isolated non-overlapping writers, and serialized integration. Cross-platform and nested delegation are forbidden. | done |
| NFR-9 | Behavior specs are parsed structurally, cite existing requirement IDs, and map exact matrix rows to exact executable test functions or files. | done |

## INV — invariants

| ID | Invariant | Status |
|---|---|---|
| INV-1 | Gin is the only third-party package permitted in an exported signature. Enforced by `apicheck`'s allowlist; a second entry requires an ADR. | done |
| INV-2 | The exported surface changes only deliberately: `api/fabrin.txt` is regenerated in the same commit and `CHANGELOG.md` records it. | done |
| INV-3 | No module imports another module. `examples/hello` demonstrates the ports-not-imports pattern, and `TestModules_NeverImportEachOther` reads the import graph to prove it. Nothing enforces it repo-wide for user code; review carries that. | planned |
| INV-4 | `CLAUDE.md` contains no rules — only the `@AGENTS.md` import. Two working agreements that disagree is worse than one that is incomplete. | done |
| INV-5 | Every public package has a recorded boundary decision in `.golangci.yml`. | done |
| INV-6 | Every gate has been proven to fail on an injected violation **and** to pass its negative control. | done |
| INV-7 | `/healthz` never consults a dependency; `/readyz` always fails closed. | done |
| INV-8 | Canonical specialist charters live in `docs/agents`; Claude Code, Codex, and Cursor adapters are generated and parity-checked. | done |
