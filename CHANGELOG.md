# Changelog

Notable changes to Fabrin. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0 and breaking changes

**Fabrin is pre-1.0. The public API is unstable and will break.**

SemVer permits that in `0.y.z`, but permission is not licence to break people
quietly. So the rule here is stricter than SemVer requires:

> **Every change to the exported surface is listed in this file, in the same
> commit that makes it.** `api/fabrin.txt` moving without a `CHANGELOG.md` update
> fails the `docs-guard` gate.

A v0 that breaks users without telling them is a v0 nobody can upgrade — they
learn what changed from a compile error and have to reverse-engineer the intent.
Breaking changes are fine; unannounced ones are not.

Once v1 lands, breaking changes wait for a major version like anywhere else.

---

## [Unreleased]

Milestone **F0** — repository, harness, agent system, and runnable core.
Tracked by [#1](https://github.com/usefabrin/fabrin/issues/1).

### Added

- Working agreement (`AGENTS.md`) with `CLAUDE.md` as a pointer to it, contribution
  flow, MIT licence, and a Go module that compiles from the first commit. ([#12])
- `just check` as the single validation gate — the same set CI runs — plus hygiene
  gates, boundary rules, and a fast pre-commit hook. ([#13])
- CI calling `just ci` rather than re-listing steps, `docs-guard` over the PR
  range, benchmarks on `main`, issue forms, and a PR template. ([#16])
- `ARCHITECTURE.md`, roadmap, Django parity table, coding guidelines, numbered
  requirements, and `specs/` with a validating gate.

- `examples/hello` — two modules, ports rather than imports, and process slicing,
  each covered by an executable test rather than by prose. ([#9])

  `greet` needs the current time and never imports the module that owns it: it
  declares a one-method `Clock` interface and `main.go` passes in whatever
  satisfies it. `TestModules_NeverImportEachOther` reads the **import graph**
  rather than behaviour, which is the only way to prove that negative — it
  catches even a blank import, which compiles cleanly and which no behavioural
  test could distinguish from correct code.

### Fixed

- **The examples smoke gate bound and polled different addresses.** It started
  each example on `:$port` (wildcard) and polled `127.0.0.1:$port`. A wildcard
  bind normally accepts loopback, so it usually worked — but if anything on the
  machine already held that loopback address, `curl` reached *that* server and
  the gate reported on a process it did not start. Seen for real on `:8080`
  against an unrelated local app. Now binds loopback explicitly, which also stops
  CI examples being briefly reachable off-box. ([#9])
- **`FABRIN_LOG_FORMAT` and `FABRIN_LOG_LEVEL` were inert.** Both resolved from
  the environment and validated, and then `Options.WithDefaults` overwrote
  `Logger` with `slog.Default()` — so neither ever reached a handler. Same defect
  class as `FABRIN_ADDR` below: a settings key the surface advertises and nothing
  implements. `WithDefaults` now leaves `Logger` nil, documented, and
  `fabrin.New` builds the logger from both fields. ([#8])
- Three fail-open defects in the harness, all of which reported success while
  enforcing nothing. ([#15], [#14])
  - The public-package coverage gate matched **substrings**: `orm` matched
    `formatters:`, so `fabrin/orm` would have landed with no boundary rule
    recorded — a fail-open in the gate written to prevent exactly that. Replaced
    with an anchored `# boundary: <name>` marker that prose cannot satisfy.
  - `just install-hooks` created a **relative** symlink under `.git/hooks`,
    assuming `.git` is a directory. In a worktree it is a file, so the link
    dangled and git ran nothing while the recipe printed success.
  - `FABRIN_ADDR` was referenced by `smoke-examples.sh` but implemented nowhere,
    making a shell script the de-facto owner of part of the config contract.

### Public API

First exported surface. `api/fabrin.txt` and its gate land with #10; until then
this section is the record.

Added to package `fabrin`:

- `App`, `New(Options, ...Module) (*App, error)`, `Options`, `Module`, `Lifecycle`,
  `Router`.
- `App` methods: `Run`, `Start`, `Stop`, `Handler`, `Engine`, `Addr`, `Modules`,
  `Capabilities`, `Options`.
- **Gin aliases** — `Context = gin.Context`, `HandlerFunc = gin.HandlerFunc`,
  `H = gin.H`. Aliases, not wrappers: a `*fabrin.Context` and a `*gin.Context` are
  the same type, so every Gin middleware works unmodified. This is what puts Gin's
  v1 API inside Fabrin's semver contract, deliberately.
- Sentinels: `ErrDuplicateModule`, `ErrUnknownModule`, `ErrNoModules`,
  `ErrAlreadyRunning`. Exported so callers branch with `errors.Is` rather than
  matching message text.
- Defaults: `DefaultAddr`, `DefaultShutdownTimeout`, `DefaultReadHeaderTimeout`.

Two defaults differ from the underlying library's, both toward safety:

- `TrustedProxies` defaults to **none**. Gin trusts every proxy by default, which
  makes `ClientIP()` spoofable by any client sending `X-Forwarded-For`.
- `ReadHeaderTimeout` defaults to 10s. Go's `http.Server` zero value is *no
  timeout*, so a client can hold a goroutine open indefinitely by sending headers
  slowly.

`Options` is a plain struct rather than functional options because a config loader
produces it directly. Consequence: its fields may be **added**, never removed or
retyped, without a breaking change.

**`Options` will move to `fabrin/config` in #7, keeping `fabrin.Options` as an
alias.** Declaring it in the root package puts two commitments in direct conflict:
`FR-CONFIG` says the loader produces `Options`, while the `config-is-standalone`
boundary rule forbids `fabrin/config` from importing the root package — so
`config.Load() (fabrin.Options, error)` is both the obvious signature and a rule
violation. Settings are a lower-level concern than the application, so the type
belongs in `config` and the dependency runs root → config, which is the allowed
direction. The alias keeps every caller and the public surface unchanged.

Added — package `fabrin/config`:

- `Load(...Source) (Options, error)`, `MustLoad`, `Resolve(...Source) (*Resolved, error)`.
- Sources: `FromEnv(Lookup)`, `FromFile(path)`, `FromRequiredFile(path)`,
  `FromFlags(args)`. `Lookup`, `MapLookup`, `OSEnv()`.
- `Resolved` with `Get`, `SourceOf`, `String` — **provenance**, because on a
  misconfigured deploy you can see the wrong value but not where it came from, and
  that is most of the debugging time.
- `Standard() []Source` — the conventional stack (`.env` ← environment ← flags)
  as one value: `config.MustLoad(config.Standard()...)`. A **slice**, not a
  composite source, so per-layer provenance survives; collapsing the three would
  report every value as coming from "standard" and discard the answer to *which
  layer set this*. Reads `os.Args`, so it belongs in `main` — in a test binary the
  flag layer rejects `go test`'s own `-test.*` flags.
- `Defaults() Source` — a source supplying nothing, so "defaults only" is
  something a caller can say out loud.
- Settings keys as constants (`KeyAddr`, `KeyModules`, …), sentinels
  (`ErrInvalidValue`, `ErrUnknownKey`, `ErrBadFile`, `ErrNoSources`), and the
  `Default*` constants.
- `Options` **moved here** from the root package (see above), gaining `Debug`,
  `LogFormat`, and `LogLevel`, plus an exported `WithDefaults`.

Behaviour worth knowing:

- **`Load()` and `Resolve()` with NO sources are an error** (`ErrNoSources`),
  not a defaults-only load. ([#22])

  This is the one place the package's own "explicit sources" rule was working
  against its users. `config.MustLoad()` is the shape a reader reaches for first;
  it compiled, started, served, and ignored every `FABRIN_` variable with no
  diagnostic anywhere. Running the README's own example with
  `FABRIN_ADDR=127.0.0.1:18099` bound `:8080` instead — the only symptom being a
  port collision with an unrelated process.

  Sources stay explicit, because "nothing reads the environment behind your back"
  is worth keeping and a test needs a configuration the machine cannot influence.
  What changed is that the empty call now *says so*, and its error names both
  fixes. That matches how this codebase already treats an unknown settings key and
  an unknown module name: an error, never a silent no-op.

- **Later layers win**: defaults ← file ← env ← flags.
- **An unknown `FABRIN_`-prefixed key is an error**, with a near-miss suggestion.
  A silently ignored typo is how a setting "does not work" with no diagnostic
  anywhere. Unprefixed keys are ignored, since a real environment and a shared
  `.env` are full of unrelated variables.
- **A missing `FromFile` is not an error**; a missing `FromRequiredFile` is. The
  usual deployment has no settings file at all, so requiring one would make the
  common case the error case — but a path the user named explicitly should not be
  ignored.
- **Only flags actually passed contribute.** A flag layer writing its zero values
  over everything beneath it would let `FromFlags` erase the environment merely by
  being present.

Added — package `fabrin/health`:

- `Check` (the interface a module implements), `CheckFunc`, `Named(name, fn)`.
- `Registry` with `NewRegistry`, `Register`, `Len`, `Evaluate`; `Report`,
  `Result`, `Status` with `StatusUp` / `StatusDown`.
- `LivenessHandler()`, `ReadinessHandler(*Registry)`, `Mount(gin.IRouter, *Registry)`.
- `LivenessPath` (`/healthz`), `ReadinessPath` (`/readyz`), `DefaultTimeout` (2s).

**Liveness and readiness answer different questions, and conflating them is how a
deployment becomes a restart loop.** `/healthz` consults *nothing*: restarting is
the only remedy a liveness failure has, so the probe must fail only when
restarting would help. `/readyz` consults every mounted module's checks and
**fails closed** — a ready-but-broken instance takes traffic it cannot serve and
hides the fault behind a load balancer.

Added — package `fabrin/logging`:

- `New(io.Writer, format, level) *slog.Logger` — returned, never installed into a
  package-level global, so a consumer can silence or redirect Fabrin without
  affecting anything else in their process.
- `RequestID()` and `Logger(*slog.Logger)` middleware.
- `RequestIDFromContext(ctx)`, `ContextWithRequestID(ctx, id)`.
- `HeaderRequestID` (`X-Request-ID`), `LogKeyRequestID` (`request_id`),
  `MsgRequest` (`request`).

Added — package `fabrin`:

- `Checker` — the optional `Module` interface contributing `[]health.Check` to
  readiness. Reported by `App.Capabilities` like `Lifecycle`.

### Changed

- **`fabrin.New` now installs three middleware and mounts two routes by default:**
  `gin.Recovery`, `logging.RequestID`, `logging.Logger`, plus `/healthz` and
  `/readyz`. Batteries included — an orchestrator's probes work against a stock
  app, and every response carries a request id a user can quote. A module that
  declares `/healthz` itself now panics at construction, which is Gin's duplicate-
  route behaviour and the right moment to find out.
- **Readiness consults only the modules this process *mounted*.** With
  `FABRIN_MODULES` in effect, an unmounted module's failing dependency does not
  gate this process — process slicing extends to readiness.
- **The request log message is now the constant `"request"`**, with method and
  path as attributes, rather than an interpolated `"GET /users/123"`. An
  interpolated message makes every distinct path its own message string, which
  defeats grouping and alerting in every aggregator.
- **`RequestID` no longer calls `c.Set(LogKeyRequestID, id)`.** It allocated Gin's
  `Keys` map on every request to hold a second copy of a string already reachable
  through `RequestIDFromContext`. `LogKeyRequestID` remains exported — it is the
  slog attribute key.
- **`config.Options.WithDefaults` leaves `Logger` nil.** Its default is not a
  constant but a logger built from `LogFormat` and `LogLevel`, and
  `config-is-standalone` forbids `fabrin/config` from importing `fabrin/logging`.
  `fabrin.New` fills it. A caller using `config.Load()` *without* going through
  `fabrin.New` must now supply a logger or check for nil before using
  `Options.Logger`.
- `fabrin.Options`, `fabrin.DefaultAddr`, `fabrin.DefaultShutdownTimeout`, and
  `fabrin.DefaultReadHeaderTimeout` are now **aliases** of the `fabrin/config`
  declarations. Not a breaking change — an alias is the same type and the same
  constant — and existing code keeps compiling unchanged.

### Performance

- The per-request baseline moved from **9 allocations to 22**, recorded with
  itemised attribution in [`perf/BASELINE.md`](perf/BASELINE.md). The framework's
  own machinery still adds **zero** — `BenchmarkFabrin_OneRoute` and
  `BenchmarkRequestIDAndLogger` land on the same 22 allocs and 1794 B, which is
  the evidence that the registry, route groups, and capability map all resolve at
  construction time. The 13 belong to request ids and structured logging, which
  are per-request work by definition. Attribution benchmarks now live in
  `logging/` so a future move is a reading rather than a bisect.

  This supersedes the earlier entry for #18, which read "zero extra allocations
  and zero extra bytes per request (9 allocs/op, 1040 B/op for both)". That was
  true when the only thing between a request and a handler was the router; it
  stopped being true the moment request ids and a log line became defaults. The
  half of it that survives is stated above and is now the tracked invariant.

[#8]: https://github.com/usefabrin/fabrin/issues/8
[#9]: https://github.com/usefabrin/fabrin/issues/9
[#12]: https://github.com/usefabrin/fabrin/pull/12
[#13]: https://github.com/usefabrin/fabrin/pull/13
[#14]: https://github.com/usefabrin/fabrin/issues/14
[#15]: https://github.com/usefabrin/fabrin/pull/15
[#16]: https://github.com/usefabrin/fabrin/pull/16
[#22]: https://github.com/usefabrin/fabrin/issues/22
