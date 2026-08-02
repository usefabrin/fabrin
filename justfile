# Fabrin harness — the single entry point for dev, quality gates, and verification.
# Run `just` or `just --list` for the recipe list.
#
# Two properties this file must keep:
#
#   1. `just check` is EXACTLY what CI's quality job runs. The workflow calls
#      `just ci`; it does not re-list the steps. Range-aware docs freshness, race
#      instrumentation, and main-only benchmarks are separate CI jobs.
#
#   2. `check`'s recipe list is written once and never grows. Recipes whose target
#      does not exist yet SKIP with a notice and exit 0 (see `specs`, `examples`,
#      `api-check`). That is what lets every PR in a multi-PR epic leave `check`
#      green without editing this list in four separate PRs.
#
# GOFLAGS is exported here rather than passed per-command so local and CI resolve
# modules identically.

export GOFLAGS := "-mod=mod"

go       := env("GO", "go")
golangci := env("GOLANGCI", "golangci-lint")

# Pin dev tools. An unpinned @latest means the lint ruleset can change with no
# commit to this repo — a build that breaks on a Tuesday for no local reason.
# `just tools` is the single install path, and CI uses it too.
golangci_version := env("GOLANGCI_VERSION", "v2.12.2")

# Show the recipe list
default:
    @just --list --list-heading $'Fabrin — available recipes:\n'

# ----------------------------------------------------------------------------
# Setup
# ----------------------------------------------------------------------------

# First-time setup: deps, pinned tools, git hooks
setup:
    {{ go }} mod download
    @just tools
    @just install-hooks

# Install pinned dev tools
tools:
    {{ go }} install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{ golangci_version }}

# Install the git pre-commit hook
#
# The hooks directory is resolved with `git rev-parse --git-path`, not assumed to
# be .git/hooks: in a worktree or a submodule, .git is a FILE pointing elsewhere,
# so a relative symlink into ".git/hooks" dangles outside the repo root. Git then
# finds no hook and runs nothing, while this recipe prints its success line. The
# `test -x` turns that silent no-op into a failure. See #14.
install-hooks:
    @bash scripts/install-hooks.sh

# ----------------------------------------------------------------------------
# Build & test
# ----------------------------------------------------------------------------

# Build the framework and every app under examples/
build:
    {{ go }} build ./...
    @just _build-examples

# Run the test suite
#
# tools/ is a SEPARATE module, so `go test ./...` from the root does not reach it
# — apicheck's tests would simply never run, in CI or locally, while this recipe
# printed success. Hence the second invocation. Same reason `lint` vets it too.
test:
    {{ go }} test ./...
    @if [ -d tools ]; then {{ go }} -C tools test ./...; else echo "→ tools: no tools/ module yet, skipping."; fi

# Run the race detector in both Go modules. CI runs this in a separate job so it
# cannot hide ordinary test diagnostics behind the slower instrumented build.
race:
    {{ go }} test -race ./...
    @if [ -d tools ]; then {{ go }} -C tools test -race ./...; else echo "→ tools: no tools/ module yet, skipping."; fi

# Run the test suite with coverage reported per package
cover:
    {{ go }} test -cover ./...
    @if [ -d tools ]; then {{ go }} -C tools test -cover ./...; else echo "→ tools: no tools/ module yet, skipping."; fi

# ----------------------------------------------------------------------------
# Quality gates
# ----------------------------------------------------------------------------

# Check style: gofmt, go vet, golangci-lint
lint:
    @bash scripts/check-gofmt.sh
    {{ go }} vet ./...
    @if [ -d tools ]; then {{ go }} -C tools vet ./...; else echo "→ tools: no tools/ module yet, skipping."; fi
    {{ golangci }} run

# Apply style fixes
#
# check-gofmt.sh walks the whole tree, so an unformatted file under tools/ fails
# `lint`. If this recipe only reached the root module, the advice that gate
# prints — "run `just format`" — would be a lie for exactly those files.
format:
    {{ go }} fmt ./...
    {{ go }} mod tidy
    @if [ -d tools ]; then {{ go }} -C tools fmt ./... && {{ go }} -C tools mod tidy; else echo "→ tools: no tools/ module yet, skipping."; fi

# Boundary check (depguard rules in .golangci.yml)
arch:
    {{ golangci }} run --enable-only depguard

# Fast repo-hygiene gates. Kept under a couple of seconds — the pre-commit hook
# runs this, and a slow hook is a bypassed hook.
gates:
    @bash scripts/gates/run-all.sh

# Docs-freshness: a governed-surface change must carry its docs/specs update.
# Takes an optional git range; the pre-commit hook passes staged files instead.
docs-check *args:
    @bash scripts/check-docs-freshness.sh {{ args }}

# ----------------------------------------------------------------------------
# Public API surface
# ----------------------------------------------------------------------------

# Regenerate api/fabrin.txt from the current exported surface
api:
    @bash scripts/api.sh write

# Fail if the committed api/fabrin.txt no longer matches the code
api-check:
    @bash scripts/api.sh check

# ----------------------------------------------------------------------------
# Examples & specs
# ----------------------------------------------------------------------------

# Build and smoke every app under examples/, and the scaffold's own output
#
# The third step is not an examples/*/ entry and deliberately so: a generated
# project has its own go.mod, which under examples/ would be a nested module the
# other two steps cannot build. It is generated fresh on every run and built
# against THIS checkout — see the header of scripts/check-scaffold.sh.
examples:
    @just _build-examples
    @bash scripts/smoke-examples.sh
    @bash scripts/check-scaffold.sh

# Validate specs/ against the test matrix
specs:
    @bash scripts/specs.sh

# ----------------------------------------------------------------------------
# Performance
# ----------------------------------------------------------------------------

# Framework-overhead benchmarks vs raw Gin. Baseline: perf/BASELINE.md
bench:
    {{ go }} test -run '^$' -bench . -benchmem ./...

# ----------------------------------------------------------------------------
# The gate
# ----------------------------------------------------------------------------

# All local gates used by CI's quality job. Range-aware docs freshness, race
# instrumentation, and main-only benchmarks are separate CI jobs.
check: gates lint test arch api-check examples specs
    @echo ""
    @echo "✓ check: all gates passed."

# Alias for `check`, so CI has one obvious entry point
ci: check

# ----------------------------------------------------------------------------
# Internals (prefixed `_`; not part of the public recipe surface)
# ----------------------------------------------------------------------------

# Build each example as its own package. Skips cleanly before examples/ exists.
_build-examples:
    @bash scripts/build-examples.sh
