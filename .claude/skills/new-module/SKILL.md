---
name: new-module
description: Scaffold a new Fabrin module — the package, its failing tests, its ports, and the spec and doc rows that keep `just check` green. Use when adding a module to Fabrin itself or to an app built on it, and whenever a new package needs its boundary decision recorded.
---

# Scaffold a Fabrin module

Read `AGENTS.md` and `ARCHITECTURE.md` first. A module is Fabrin's answer to
Django's `INSTALLED_APPS`: one required method, plus optional interfaces that are
type-asserted at registration, so a module pays only for what it uses.

Work in this order. Steps 1–3 come before any implementation, and step 6 before
any commit.

## 1. The port, before the package

Ask what this module needs from **other** modules. Every answer becomes an
interface **declared in this package** — never an import of the module that
provides it. That interface is the extraction seam; a direct import welds the two
together permanently, and the weld is invisible until someone tries to split
them.

Keep it to the methods the consumer actually calls. A wide interface copied from
the provider is a direct import wearing a disguise: it makes the seam expensive
to satisfy any other way, which is the one thing it exists to make cheap.

`examples/hello/greet` is the reference — `Clock` is one method, and `main.go` is
the only place that knows who satisfies it.

## 2. The failing tests

Before the implementation. Not after. See the `test-first` agent for why, and use
it if the behaviour is subtle.

Three that a module almost always needs:

- **Its routes answer.** Mount it on an `App` and assert on the response, not on
  a handler called directly — a handler that works while nothing mounts it is the
  failure mode integration tests exist to catch.
- **Its port is a port.** If this module depends on another, assert the negative
  by reading the **import graph**, not behaviour. `TestModules_NeverImportEachOther`
  in `examples/hello/hello_test.go` is the pattern; it catches even a blank
  import, which compiles cleanly and which no behavioural test can distinguish
  from correct code.
- **Its checks fail closed**, if it implements `Checker`. A check that reports
  ready when its dependency is down is worse than no check.

Run them. Quote the failure.

## 3. The spec rows — `planned`, not `implemented`

If the module makes a load-bearing behavioural claim, add it to
`specs/system-behavior.yaml` now:

```yaml
  - id: MOD-00N
    what: <the behaviour, in one sentence>
    why: <what breaks without it>
    requirement: FR-...
    status: planned
```

and a row in `specs/test-matrix.md` with `_planned_` in the Test column.

**Leave off the `test:` field while the status is `planned`.** `just specs`
parses YAML structurally, checks both spec/matrix directions and requirement IDs,
and once you write `status: implemented` resolves the exact top-level Go test
function through the AST. Writing `implemented` with a test that does not exist
yet turns `just check` red for everyone.

## 4. The package

```go
// Package <name> is a Fabrin module that <does the thing>.
package <name>

type Module struct{ /* dependencies, unexported */ }

// New takes its dependencies rather than reaching for them, so a missing one is
// a compile error at the call site in main, not a nil dereference on the first
// request.
func New(deps ...) *Module { return &Module{...} }

func (m *Module) Name() string           { return "<name>" }
func (m *Module) Routes(r fabrin.Router) { ... }
```

`Name()` is not decoration — it is how `FABRIN_MODULES` selects this module for a
process, and how a failing readiness check names it. An unknown name in that
variable is an error, never a silent no-op.

Then add only the optional interfaces this module genuinely uses:

| Interface | Method | Add when |
|---|---|---|
| `Checker` | `Checks() []health.Check` | it owns a dependency whose absence should take this process out of the load balancer |
| `Lifecycle` | `Start(ctx) / Stop(ctx)` | it owns a resource that must be opened and closed |
| `Modeler` | `Models() []orm.Model` | F2 |
| `Migrator` | `Migrations() []migrate.M` | F2 |
| `Commander` | `Commands() []cli.Command` | F1 |
| `Subscriber` | `Subscribe(b signals.Bus)` | F6 |

A `Checker` check belongs to `/readyz` and never to `/healthz`. Readiness asks
"should I get traffic"; liveness asks "would restarting help", and restarting
cannot reach a database.

## 5. If this is a new *public package* in Fabrin itself

Two more things, both gated:

- Add a `# boundary: <name> — <decision>` line in `.golangci.yml`. "No rules
  needed" is a valid decision; it just has to be written down, so **considered**
  and **forgotten** stay distinguishable. `scripts/gates/check-depguard-coverage.sh`
  checks the manifest against the filesystem in both directions.
- Add it to `scripts/gates/public-packages.txt`. `apicheck` reads that same
  manifest, so a new public package cannot reach `api/fabrin.txt` without a
  recorded boundary decision — deliberately one list, not two that can drift.

Then `just api` regenerates the snapshot, in the same commit, with the reason in
the commit body.

## 6. Close the loop

- Flip the spec entry to `status: implemented` and add `test: <file>::<TestFunc>`.
  Replace `_planned_` in the matrix with the same reference.
- `CHANGELOG.md` if the public surface moved — link references defined at the
  bottom of the file.
- `just check`.
