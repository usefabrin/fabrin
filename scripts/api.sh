#!/usr/bin/env bash
#
# api.sh — regenerate or verify the public API snapshot.
#
# Prevents: a breaking change to the exported surface landing unnoticed. Fabrin is
# a library, so every exported symbol is a promise to strangers; a rename that
# passes tests and lint still breaks every build downstream. api/fabrin.txt is the
# checked-in record, and `check` mode fails when the code and the record disagree.
#
# Usage: api.sh write | check
#
# The real work is the apicheck tool, which lives in the tools/ module — it needs
# golang.org/x/tools for type information, and a library must not push dev-only
# dependencies into every consumer's go.sum.
#
# Skips cleanly before tools/ exists, so `just check` stays green across the PRs
# of a multi-PR epic (see the justfile header).
#
set -euo pipefail

cd "$(dirname "$0")/.."

mode="${1:-check}"
go="${GO:-go}"
snapshot="api/fabrin.txt"

case "$mode" in
  write | check) ;;
  *)
    echo "usage: api.sh write|check" >&2
    exit 2
    ;;
esac

if [[ ! -d tools ]]; then
  echo "→ api: apicheck not built yet (no tools/ module), skipping."
  exit 0
fi

run_apicheck() {
  (cd tools && "$go" run ./apicheck "$@")
}

if [[ "$mode" == "write" ]]; then
  mkdir -p "$(dirname "$snapshot")"
  run_apicheck -mode=snapshot >"$snapshot"
  echo "✓ api: wrote $snapshot"
  exit 0
fi

# check mode

# Leak detection first: an unblessed third-party type in an exported signature is
# a worse problem than a drifted snapshot, and reporting it first makes the cause
# obvious when both fire at once.
run_apicheck -mode=leak

if [[ ! -f "$snapshot" ]]; then
  echo "→ api: $snapshot does not exist yet, skipping snapshot comparison."
  echo "    Create it with \`just api\`."
  exit 0
fi

current="$(mktemp)"
trap 'rm -f "$current"' EXIT
run_apicheck -mode=snapshot >"$current"

if ! diff -u "$snapshot" "$current" >/dev/null; then
  echo "✗ api: the public surface no longer matches $snapshot:" >&2
  diff -u "$snapshot" "$current" | sed 's|^|    |' >&2 || true
  echo "" >&2
  echo "    If this change is intended, run \`just api\` and commit the result in" >&2
  echo "    the SAME commit, saying in the body why the surface moved." >&2
  exit 1
fi

echo "✓ api: public surface matches $snapshot."
