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

### Fixed

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

### Performance

Measured against raw `gin.Engine`: **zero extra allocations and zero extra bytes
per request** (9 allocs/op, 1040 B/op for both). Fabrin's registry, per-module
route group, and capability map all resolve at construction. See
[`perf/BASELINE.md`](perf/BASELINE.md).

[#12]: https://github.com/usefabrin/fabrin/pull/12
[#13]: https://github.com/usefabrin/fabrin/pull/13
[#14]: https://github.com/usefabrin/fabrin/issues/14
[#15]: https://github.com/usefabrin/fabrin/pull/15
[#16]: https://github.com/usefabrin/fabrin/pull/16
