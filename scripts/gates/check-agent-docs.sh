#!/usr/bin/env bash
#
# check-agent-docs.sh — one working agreement and one canonical role catalog.
#
# Prevents: rules accumulating in CLAUDE.md. The moment it holds a rule of its
# own, Fabrin has two working agreements that disagree, and which one an agent
# obeys depends on which tool it is running under. That failure is silent — both
# files read as authoritative — and it gets worse the longer it goes unnoticed.
#
# So: CLAUDE.md may contain only comments, blank lines, and the @AGENTS.md
# import. Native role adapters must match docs/agents through scripts/agents.sh.
#
set -euo pipefail

cd "$(dirname "$0")/../.."

status=0

for f in AGENTS.md CLAUDE.md; do
  [[ -f "$f" ]] || { echo "✗ agent-docs: $f is missing" >&2; status=1; }
done
[[ "$status" -eq 0 ]] || exit 1

if ! grep -qx '@AGENTS.md' CLAUDE.md; then
  echo "✗ agent-docs: CLAUDE.md must import the working agreement with a bare '@AGENTS.md' line" >&2
  status=1
fi

# Strip HTML comments and blanks; the only survivor may be the import.
leftover="$(grep -vE '^[[:space:]]*(<!--.*-->)?[[:space:]]*$' CLAUDE.md | grep -vx '@AGENTS.md' || true)"
if [[ -n "$leftover" ]]; then
  echo "✗ agent-docs: CLAUDE.md contains content other than the @AGENTS.md import:" >&2
  sed 's|^|    |' <<<"$leftover" >&2
  echo "    Move it to AGENTS.md — CLAUDE.md is a pointer, not a rulebook." >&2
  status=1
fi

[[ "$status" -eq 0 ]] || exit 1

echo "✓ agent-docs: CLAUDE.md is a pointer to AGENTS.md."

# Native platform files are generated from docs/agents, never edited as a second
# set of role rules. Byte parity catches both missing platforms and silent drift.
bash scripts/agents.sh check
