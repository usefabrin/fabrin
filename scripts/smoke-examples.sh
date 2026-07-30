#!/usr/bin/env bash
#
# smoke-examples.sh — boot every example and prove it answers /healthz.
#
# Prevents: an example that compiles but cannot start. Compilation catches a
# renamed symbol; it does not catch a nil dependency, a config key that no longer
# exists, or a module that panics during registration — all of which are exactly
# what a framework's wiring changes break, and all of which a user hits within
# five seconds of trying the example.
#
# Each example is started on its own port, polled until it answers or the deadline
# passes, then terminated. Skips cleanly before examples/ exists.
#
# FABRIN_ADDR is part of the config contract, not a convention invented here: this
# script needs each example on its own port or they collide, and a smoke test that
# passes because only the first example bound is worse than no smoke test. The key
# is named in the acceptance criteria of the config loader (#7) and the hello
# example (#9) so the three cannot drift. If you rename it, all three move
# together. See #14.
#
set -euo pipefail

cd "$(dirname "$0")/.."

go="${GO:-go}"
deadline_secs=10
port_base="${SMOKE_PORT_BASE:-18080}"

if [[ ! -d examples ]]; then
  echo "→ smoke-examples: no examples/ directory yet, skipping."
  exit 0
fi

shopt -s nullglob
dirs=(examples/*/)
shopt -u nullglob

[[ ${#dirs[@]} -gt 0 ]] || { echo "→ smoke-examples: examples/ is empty, skipping."; exit 0; }

out="$(mktemp -d)"
pids=()
cleanup() {
  for pid in ${pids[@]+"${pids[@]}"}; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$out"
}
trap cleanup EXIT

status=0
i=0
for d in "${dirs[@]}"; do
  name="$(basename "$d")"
  port=$((port_base + i))
  i=$((i + 1))
  log="$out/$name.log"

  "$go" build -o "$out/$name" "./$d"

  FABRIN_ADDR=":$port" "$out/$name" >"$log" 2>&1 &
  pid=$!
  pids+=("$pid")

  ok=0
  for _ in $(seq $((deadline_secs * 5))); do
    if ! kill -0 "$pid" 2>/dev/null; then
      break # exited early; the log below will say why
    fi
    if curl -fsS -o /dev/null "http://127.0.0.1:$port/healthz" 2>/dev/null; then
      ok=1
      break
    fi
    sleep 0.2
  done

  if [[ "$ok" -eq 1 ]]; then
    echo "  ✓ examples/$name answered /healthz on :$port"
  else
    echo "✗ smoke-examples: examples/$name never answered /healthz on :$port within ${deadline_secs}s" >&2
    echo "    ── its output ──" >&2
    sed 's|^|    |' "$log" >&2 || true
    status=1
  fi
done

[[ "$status" -eq 0 ]] || exit 1

echo "✓ smoke-examples: ${#dirs[@]} example(s) booted and healthy."
