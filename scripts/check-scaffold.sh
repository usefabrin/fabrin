#!/usr/bin/env bash
#
# check-scaffold.sh — the generator's output builds, tests, boots, and survives
# `startapp`.
#
# Prevents: a scaffold that emits code which no longer compiles against the
# framework it scaffolds. Every change to fabrin.New, config.Standard, or the
# Module interface can break the templates, and nothing else in `just check`
# would notice — the templates are .tmpl text, invisible to the compiler.
#
# # Why a temporary directory rather than a checked-in examples/ entry
#
# The generated project has its own go.mod, so under examples/ it would be a
# NESTED MODULE: `go build ./examples/scaffold` fails, and build-examples.sh
# globs examples/*/ expecting packages of the root module. Committing a copy
# instead would let it drift from the templates it is supposed to prove, which is
# the one thing this gate exists to catch.
#
# So it is generated fresh here, on every run, and thrown away. There is no
# directory to be silently absent — this script either does the whole thing or
# fails.
#
# # Why it builds against this checkout
#
# Resolving Fabrin from the module proxy would test the LAST PUBLISHED COMMIT,
# not the working tree — a change that breaks the scaffold would go green until
# it was merged, which inverts what this gate is for. Hence the `replace`
# directive below, which is the guarantee that matters.
#
# What this gate does NOT promise is a fully offline run, and the reason is worth
# writing down because GOPROXY=off was tried first and failed in CI:
#
#   Go 1.17+ prunes the module graph, so Fabrin's own cache never contains
#   golang.org/x/sys@v0.6.0's go.mod — an edge reachable only through
#   go-isatty's unpruned requirements. The generated project starts with an empty
#   require list, so resolving it walks that edge and needs the metadata. It
#   passes locally, where years of unrelated work have warmed the cache, and
#   fails on a runner whose cache holds exactly what Fabrin needs.
#
# The alternatives were to copy Fabrin's whole require block into the generated
# go.mod — which would mean the gate no longer builds the go.mod the scaffold
# actually emits — or to carry a retry. Fetching a few go.mod files from a proxy
# that caches them aggressively is the cheaper trade, and the `replace` keeps the
# guarantee that this is the working tree being tested.
#
set -euo pipefail

cd "$(dirname "$0")/.."
root="$PWD"
go="${GO:-go}"
port="${SCAFFOLD_PORT:-18090}"
deadline_secs=20

# No skip branch. cmd/fabrin exists, and a gate that can quietly do nothing is
# the failure mode the whole gates/ directory is written against.
[[ -d cmd/fabrin ]] || { echo "✗ scaffold: cmd/fabrin not found" >&2; exit 1; }

work="$(mktemp -d)"
app_pid=""
cleanup() {
  [[ -n "$app_pid" ]] && { kill "$app_pid" 2>/dev/null || true; wait "$app_pid" 2>/dev/null || true; }
  rm -rf "$work"
}
trap cleanup EXIT

# From the WORKING TREE. `go run ./cmd/fabrin` would do, but building once keeps
# the four invocations below from recompiling the tool each time.
"$go" build -o "$work/fabrin" ./cmd/fabrin
fabrin="$work/fabrin"

"$fabrin" new demo -dir "$work" -skip-tidy >/dev/null

cd "$work/demo"
"$go" mod edit -replace "github.com/usefabrin/fabrin=$root"

# -mod=mod lets the build write the require lines it needs. Fabrin itself comes
# from the replace above, so no version of it is ever fetched.
export GOFLAGS=-mod=mod

step() { echo "  · $1"; }

# Capture output and show it only on failure. `go build` narrates every require
# it adds, which is noise on a green run and the whole diagnosis on a red one.
run() {
  local out
  if ! out="$("$@" 2>&1)"; then
    echo "✗ scaffold: $* failed:" >&2
    sed 's|^|    |' <<<"$out" >&2
    exit 1
  fi
}

step "building the generated project"
run "$go" build ./...

step "running its tests"
run "$go" test ./...

# #37 verified the wiring edit by re-parsing, which cannot know whether the
# result compiles. This is where it does.
step "startapp, then building again"
run "$fabrin" startapp billing
run "$go" build ./...
run "$go" test ./...

# An import inserted in the wrong sorted position is not a compile error — it is
# a file `gofmt -l` flags on the user's next commit, blaming their edit.
step "gofmt over the edited project"
unformatted="$(gofmt -l . 2>/dev/null || true)"
if [[ -n "$unformatted" ]]; then
  echo "✗ scaffold: the generator emitted unformatted Go:" >&2
  sed 's|^|    |' <<<"$unformatted" >&2
  exit 1
fi

step "routes lists both modules"
if ! routes="$("$go" run . routes 2>&1)"; then
  # This is also where "compiles, and its tests pass, but the binary cannot
  # start" surfaces: the generated tests exercise newApp, and only main calls
  # config.MustLoad. Reported here rather than left as a bare panic.
  echo "✗ scaffold: the generated binary failed to run:" >&2
  sed 's|^|    |' <<<"$routes" >&2
  exit 1
fi

for want in "/ " "/billing" "/healthz" "/readyz"; do
  grep -q -- "$want" <<<"$routes" || {
    echo "✗ scaffold: routes output is missing '$want':" >&2
    sed 's|^|    |' <<<"$routes" >&2
    exit 1
  }
done

step "help exits cleanly throughout the settings prefix"
for help_args in \
  '-h' \
  '-h=true' \
  '-addr 127.0.0.1:1 -h'; do
  read -r -a help_argv <<<"$help_args"
  if ! help="$("$go" run . "${help_argv[@]}" 2>&1)"; then
    echo "✗ scaffold: generated application help failed for $help_args:" >&2
    sed 's|^|    |' <<<"$help" >&2
    exit 1
  fi
  grep -q 'Usage:' <<<"$help" || {
    echo "✗ scaffold: generated application help printed no usage for $help_args:" >&2
    sed 's|^|    |' <<<"$help" >&2
    exit 1
  }
done

step "unknown leading flags fail without panicking"
if unknown="$("$go" run . --version 2>&1)"; then
  echo "✗ scaffold: unknown --version flag exited successfully" >&2
  exit 1
fi
grep -q 'flag provided but not defined' <<<"$unknown" || {
  echo "✗ scaffold: unknown --version flag did not report the parse error:" >&2
  sed 's|^|    |' <<<"$unknown" >&2
  exit 1
}
if grep -q 'panic:' <<<"$unknown"; then
  echo "✗ scaffold: unknown --version flag panicked instead of returning an error:" >&2
  sed 's|^|    |' <<<"$unknown" >&2
  exit 1
fi

# Loopback, not a wildcard bind, for the reason smoke-examples.sh documents: the
# poll below is loopback-specific, and anything else already holding that address
# would make this gate report on a process it did not start.
step "booting it and polling /healthz"
log="$work/app.log"
FABRIN_ADDR="127.0.0.1:$port" "$go" run . >"$log" 2>&1 &
app_pid=$!

ok=0
for _ in $(seq $((deadline_secs * 5))); do
  if ! kill -0 "$app_pid" 2>/dev/null; then
    break # exited early; the log below says why
  fi
  if curl -fsS -o /dev/null "http://127.0.0.1:$port/healthz" 2>/dev/null; then
    ok=1
    break
  fi
  sleep 0.2
done

if [[ "$ok" -ne 1 ]]; then
  echo "✗ scaffold: the generated app never answered /healthz on :$port within ${deadline_secs}s" >&2
  echo "    ── its output ──" >&2
  sed 's|^|    |' "$log" >&2 || true
  exit 1
fi

echo "✓ scaffold: generated, built, tested, extended with startapp, and booted."
