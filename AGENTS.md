# AGENTS.md — working agreement for Fabrin

Canonical guide for AI coding agents (Claude Code, Codex, Cursor) and humans
working in this repository. Read this before making changes.

Do not put agent rules in `CLAUDE.md` — it only imports this file via
`@AGENTS.md`. Edit this file instead.

Also follow:

- [CONTRIBUTING.md](CONTRIBUTING.md) — TDD, trunk-based git, the validation gate
- [docs/coding-guidelines.md](docs/coding-guidelines.md) — engineering / style standards
- [ARCHITECTURE.md](ARCHITECTURE.md) — package map and the Module/port model
- `AGENTS.local.md` — **if this file exists, read it before starting.** It holds
  machine-local context: reference material available on this machine, local
  paths, anything specific to one developer's environment rather than to the
  project. It is gitignored, and deliberately so — never copy its contents into a
  tracked file. Most clones will not have one, and skipping it is correct then.

## What this is

**Fabrin** is a **batteries-included web framework for Go**, built on
[Gin](https://github.com/gin-gonic/gin) and inspired by **Django's development
philosophy**: convention over configuration, a strong default answer to every
common web problem, and an admin site you get for free from your models.

It is also **microservice-compatible by design** — see
[Deployment shapes](#deployment-shapes-the-microservice-answer).

**Fabrin is a library, not an application.** That inverts several instincts that
are correct in application repositories:

- `internal/` is **invisible to users** — Go forbids importing it. Anything users
  need is a root-level package (`fabrin`, `fabrin/config`, `fabrin/health`, …).
  Putting a user-facing type in `internal/` is not a style mistake, it is a bug.
- **Every exported symbol is a promise.** Adding one is cheap; removing one
  breaks strangers' builds. Prefer to ship less and add later.
- The most valuable boundary rule is not layering, it is **API-surface
  discipline** (hard rule 2 below).

## What to optimise for

**Security first, then performance, then everything else.** Both are top-tier
concerns and most changes serve both. When they genuinely conflict, **security
wins** — and the trade is written down rather than made quietly.

This ordering is sharper for a framework than it would be for an application. A
default here is inherited by every application built on Fabrin, and almost nobody
revisits a framework default. A slow framework costs its users milliseconds they
can measure and fix. An unsafe default costs every downstream application at
once, invisibly, and they have no reason to go looking.

### What it already means here

The rule is a description of decisions already made, not a new aspiration. Each
of these is documented at its own call site; this is the principle they share:

- **Defaults differ from the underlying library's when the library's is unsafe.**
  `TrustedProxies` defaults to *none*, because Gin trusts every proxy and that
  makes `ClientIP()` spoofable by any client sending `X-Forwarded-For`
  (`app.go`). `ReadHeaderTimeout` is 10s, because Go's `http.Server` zero value
  is *no timeout* (`config/options.go`).
- **Untrusted input that reaches a log file or a response header is rejected,
  not escaped.** An inbound request id is dropped unless it is already safe, and
  bounded at 64 characters — it is a header-injection and log-forging vector, and
  generating a fresh id is always a valid answer (`logging/logging.go`).
- **Fail closed.** `/readyz` reports not-ready when a check fails or times out.
  Serving traffic this process cannot handle is worse than leaving the pool
  (`health/health.go`).

### The canonical example

`crypto/rand` for request ids costs ~200 ns per request — roughly a fifth of the
middleware budget, and its single largest *time* component. `math/rand` would
recover nearly all of it.

It stays. Request ids reach logs and some systems that consume them treat them as
unguessable. That is the trade this section exists to make, and
[`perf/BASELINE.md`](perf/BASELINE.md) records both the cost and the reason, so
the next person optimising the request path finds the decision instead of
rediscovering the option.

### Performance is measured, never asserted

A framework that says "fast" without a number is marketing. `just bench` and
[`perf/BASELINE.md`](perf/BASELINE.md) hold the numbers; a regression lands with
a written justification, per that file's own rule. Allocations per request are
the tracked metric, because nanoseconds vary with the machine and an allocation
in the hot path shows up identically everywhere.

### When they conflict

A change that trades a security property for speed must **name the property it
weakens, and why that is acceptable, in the commit body.** Never silently. If you
cannot state the property clearly enough to write the sentence, that is the
answer: do not make the change.

## Hard rules (never violate)

### 1. Gin is blessed; nothing else is

`fabrin.Context` is a **type alias** for `gin.Context`, and `fabrin.HandlerFunc`
for `gin.HandlerFunc`. This is deliberate: every Gin middleware in the ecosystem
works in Fabrin unmodified, and "built on Gin" is a feature users choose Fabrin
for, not an implementation detail to hide.

The accepted cost is that **Gin's v1 API is part of Fabrin's semver contract.**
That cost is bounded because Gin has been on v1 since 2015. It is not a licence
to bless anything else: **no other third-party type may appear in an exported
signature.** `apicheck`'s allowlist is the single, reviewable record of what
Fabrin has committed to, and `github.com/gin-gonic/gin` is its only entry.
Adding a second entry is an architectural decision that needs an ADR, not a
line edit. The reasoning behind this rule, and the four alternatives that lost
to it, are in [ADR 0001](docs/adr/0001-gin-as-a-type-alias.md).

Note what this rule does *not* say: it does not restrict which packages may
*import* Gin. `health`'s handlers and `logging`'s middleware are
`gin.HandlerFunc` by definition. Containment is about the **exported surface**,
which is why `apicheck` enforces it and depguard does not.

### 2. The public API surface changes only on purpose

`api/fabrin.txt` is a checked-in snapshot of every exported symbol. If your
change moves the surface, regenerate it with `just api` **in the same commit**,
and say why in the commit body. `just api-check` fails otherwise.

Type aliases are recorded unexpanded (`type Context = github.com/gin-gonic/gin.Context`,
not Gin's forty-odd methods) so that a Gin patch bump does not churn the file.
A gate that cries wolf gets ignored, and an ignored gate is worse than none.

### 3. Modules never import each other

A module that needs something another module owns **declares the interface it
needs in its own package** and lets the wiring pass in whatever satisfies it.
That interface is the seam that makes the module extractable into its own
service later. A direct import welds the two together permanently, and the weld
is invisible until someone tries to split them.

### 4. Prove a gate bites before trusting it

When you add or change a gate, a depguard rule, or a check script: inject a
throwaway violation, watch the gate **fail**, then revert. A rule that silently
matches nothing looks identical to a rule that passes. This is not paranoia —
prefix-matched deny lists in particular fail open when a new package lands, which
is exactly why `check-depguard-coverage.sh` exists.

## Commands (everything via `just`)

| Command | What it does |
|---------|--------------|
| `just setup` | First-time: deps, pinned tools, git hooks |
| `just install-hooks` | Symlink `scripts/hooks/pre-commit` into `.git/hooks` |
| `just build` | Build the framework and every app under `examples/` |
| `just test` / `just cover` | `go test ./...`, with or without per-package coverage |
| `just lint` / `just format` | Check / apply style (gofmt, `go vet`, golangci-lint) |
| `just arch` | Boundary check (depguard) |
| `just api` / `just api-check` | Regenerate / verify the `api/fabrin.txt` snapshot |
| `just examples` | Build **and smoke** every app under `examples/` |
| `just specs` | Validate `specs/` against the test matrix |
| `just docs-check` | Docs-freshness gate on governed surfaces |
| `just gates` | Fast repo-hygiene gates (`scripts/gates/*.sh`) — also run by the pre-commit hook |
| `just bench` | Framework-overhead benchmarks vs raw Gin. Baseline: `perf/BASELINE.md` |
| `just check` | **All local gates — exactly the set CI runs** |
| `just ci` | Alias for `just check` |
| `just tools` | Install pinned dev tools (versions in the justfile) |

Run a single test: `go test . -run TestApp_MountsOnlySelectedModules -v`.

`GOFLAGS=-mod=mod` is exported by the justfile (not passed per command) so local
and CI resolve modules identically.

**`just check` is exactly what CI runs** — the workflow calls `just ci` rather
than re-listing the commands, so the two cannot drift. A green `check` means a
green CI; there is no second command to remember.

### Recipes skip, they do not fail

`check`'s recipe list is written **once** and never grows. A recipe whose target
does not exist yet prints a skip notice and exits 0. This keeps the invariant that
**every PR leaves `just check` green** without editing `check` in four separate
PRs — `examples`, `specs`, and `api-check` each spent part of F0 skipping, and
none of them do now.

Keep the mechanism when you add a recipe. The next milestone lands `cmd/fabrin`
and `fabrin/orm`, and their gates will need it for exactly the same reason.

Gates also report **every** failure rather than the first. A fail-fast loop hides
gates 2..N behind gate 1, so a contributor fixes one thing, re-runs, finds
another, and pays a full round trip per gate.

## Architecture

```
Your app's main()                       ← wires modules, satisfies their ports
        │
        ▼
fabrin.App                              ← registry, lifecycle, graceful shutdown
        │  mounts
        ▼
Module.Routes(Router)                   ← your code; handlers are gin.HandlerFunc
        │
        ▼
Gin engine (blessed, public)            ← every Gin middleware works unmodified
```

`fabrin.Module` is Fabrin's answer to Django's `INSTALLED_APPS`. The required
interface is one method; everything else is an **optional** interface
type-asserted at registration, so a module only pays for what it uses:

```go
type Module interface {
    Name() string
    Routes(r Router)
}

// Optional — asserted at registration:
type Checker    interface { Checks() []health.Check }            // system checks
type Lifecycle  interface { Start(ctx) error; Stop(ctx) error }   // owned resources
type Modeler    interface { Models() []orm.Model }                // F2
type Migrator   interface { Migrations() []migrate.M }            // F2
type Commander  interface { Commands() []cli.Command }            // F1
type Subscriber interface { Subscribe(b signals.Bus) }            // F6
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full package map.

## Deployment shapes (the microservice answer)

Fabrin is a **modular monolith by default, extractable by design**. Three
mechanisms, and no more:

1. **Ports, not imports** (hard rule 3). The interface a module declares for its
   dependency *is* the extraction seam.
2. **Process slicing.** `FABRIN_MODULES=blog,auth` mounts only the named modules
   in this process. One binary, N deployment shapes — splitting a monolith into
   services becomes a deploy-config change, not a rewrite. An unknown module name
   is an error, never a silent no-op.
3. **Swappable satisfaction.** A port satisfied in-process by a direct call can
   instead be satisfied by an HTTP client adapter; the signals bus is in-process
   by default and swappable. The module cannot tell the difference.

**Explicit non-goals for v0.** Fabrin ships **no** service discovery, service
mesh, RPC framework, or remote-client code generator. It ships the seam, plus
service-ready defaults: structured logging, liveness/readiness, config from env,
graceful shutdown. Do not add any of the non-goals without an ADR — "we're a
microservice framework too" is how batteries-included frameworks become
un-learnable.

## How we work

- **TDD.** Write the failing test first, then the code. A behavioural claim in a
  doc without a test behind it is a wish, not a feature.
- **Django parity is a design input, not a target.** Ask "what problem does
  Django solve here, and what is the *idiomatic Go* answer?" Fabrin should feel
  like Go that happens to be batteries-included — not like Python transliterated.
  Record the mapping in [docs/DJANGO_PARITY.md](docs/DJANGO_PARITY.md).
- **Trunk-based git.** Short-lived branches off `main`, merged frequently.
  Incomplete work hides behind a flag, not a long-running branch.
- **Conventional Commits.** Scopes: `core`, `router`, `config`, `orm`,
  `migrate`, `auth`, `admin`, `cli`, `modules`, `transport`, `render`, `tasks`,
  `docs`, `harness`, `examples`.
- **Issue first.** Open a GitHub issue before coding (except trivial
  typos/docs). Title with `[FEATURE]` or `[BUG]`; apply the type label plus an
  area label. Large work is an **epic** with child issues.
- **One small PR per issue** — link the issue, aim under ~400 LOC of meaningful
  change, **squash merge**. See [CONTRIBUTING.md](CONTRIBUTING.md).
- **Dev tooling stays out of the framework's `go.mod`.** `tools/` is a separate
  Go module for exactly this reason: `apicheck` needs `golang.org/x/tools`, and a
  library must not push dev-only dependencies into every consumer's `go.sum`.

## Keep docs current (enforced)

When you change a **governed surface** — the public API (`api/fabrin.txt`), a CLI
command, or anything a `*_CONTRACT.md` governs — update the relevant `docs/` or
`specs/` in the **same change**. The `just docs-check` gate (pre-commit hook +
CI) fails otherwise. When you add or change a load-bearing behaviour, update
`specs/system-behavior.yaml` **and** `specs/test-matrix.md`.

## Last step of every process

After implementation and tests, **update the docs as the last step** of every
change. Refresh `docs/`, `specs/`, `ARCHITECTURE.md`, `docs/TODO.md`,
`docs/DJANGO_PARITY.md`, and `CHANGELOG.md` when the work warrants it — do not
stop at green tests. Governed-surface updates are mandatory; other doc drift
should be fixed in the same change whenever you notice it.

## Specialised agents and skills

`.claude/agents/` holds **charters, not rules.** Rules live in this file, because
a rule must be visible to every agent and every human; a charter says what one
narrow job is for and when to stop doing it. Each file states its charter, its
tools, and an explicit **hand-back condition** — the last of those is the part
that matters, because an agent with no stated stopping point drifts into the next
job and nobody notices.

| Agent | For |
|---|---|
| `api-guardian` | Whether a public-surface change *should* ship — export-worthiness, naming, shapes that lock us in. Blocks on anything needing an ADR. |
| `test-first` | The failing test, and only that. Hands back red. |
| `django-parity` | "What problem does Django solve, and what is the idiomatic *Go* answer?" Owns `docs/DJANGO_PARITY.md`. |
| `boundary-auditor` | Proving a gate bites — one injection per mechanism, plus the negative control. |
| `docs-syncer` | The last step of every change, checking what `docs-check` cannot read. |
| `perf-sentinel` | Measuring, and attributing a regression to a cause rather than to "the framework". |

Three of these overlap a gate on purpose, and the charters say where the gate
stops seeing. `just docs-check` counts touched files and cannot read them;
`just api-check` knows the surface *moved* but not whether it should have; hard
rule 4 says prove a gate bites, and the half people skip is the negative control.

`.claude/skills/` holds procedures: `new-module` (port first, failing test, spec
rows, then the package) and `issue-to-pr` (the working agreement as a sequence).

## Where things live

- Roadmap / milestones: [docs/TODO.md](docs/TODO.md)
- Django feature → Fabrin package → status: [docs/DJANGO_PARITY.md](docs/DJANGO_PARITY.md)
- Requirements (by ID): `docs/requirements/FABRIN_REQUIREMENTS.md`
- Behaviour spec + test matrix: `specs/`
- Engineering / style standards: `docs/coding-guidelines.md`
- Architecture detail: [ARCHITECTURE.md](ARCHITECTURE.md)
- Contribution flow: [CONTRIBUTING.md](CONTRIBUTING.md)
- Consequential decisions: [docs/adr/](docs/adr/README.md) — what was decided,
  and what was rejected
- Public API snapshot: `api/fabrin.txt`
- Specialised agents: `.claude/agents/` — charters, not rules. Rules live here.
- Repeatable procedures: `.claude/skills/`
- Claude Code tool permissions: `.claude/settings.json` — **permissions only,
  never rules.** Anything that reads as a rule belongs in this file. The
  allowlist covers read-only and gate commands (`just check`, `go test`,
  `gh issue`, …); destructive ones (`git push`, `gh pr merge`) deliberately
  still prompt.

**Before committing:** `just check` must pass, and a governed-surface change must
carry its `docs/`/`specs/` update — the pre-commit hook enforces style and
docs-freshness.
