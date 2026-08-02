#!/usr/bin/env bash
#
# run-all.sh — run every hygiene gate, reporting all failures rather than the
# first.
#
# Prevents: a fail-fast loop hiding gates 2..N behind gate 1, so a contributor
# fixes one thing, re-runs, finds another, and pays a full round trip per gate.
#
# Gates live in this directory, one file each, ordered below by how much the
# failure they catch hurts when it slips through. They must stay fast — the
# pre-commit hook runs this target, and a slow hook is a bypassed hook. Nothing
# here may require Docker, network, or a build.
#
set -uo pipefail

cd "$(dirname "$0")"

# Ordered deliberately, not alphabetically.
gates=(
  check-depguard-coverage.sh
  check-examples.sh
  check-agent-docs.sh
  check-docs-freshness-policy.sh
  check-hook-worktrees.sh
  check-action-pins.sh
)

status=0
for gate in "${gates[@]}"; do
  if [[ ! -f "$gate" ]]; then
    echo "✗ gates: $gate is listed in run-all.sh but does not exist" >&2
    status=1
    continue
  fi
  bash "$gate" || status=1
done

# Any gate script present but not listed above would never run. That is the
# failure mode this repo has to assume: a gate nobody invokes looks identical to
# a gate that passes.
shopt -s nullglob
for f in check-*.sh; do
  found=0
  for gate in "${gates[@]}"; do
    [[ "$f" == "$gate" ]] && found=1 && break
  done
  if [[ "$found" -eq 0 ]]; then
    echo "✗ gates: $f exists but is not listed in run-all.sh — it never runs" >&2
    status=1
  fi
done
shopt -u nullglob

exit "$status"
