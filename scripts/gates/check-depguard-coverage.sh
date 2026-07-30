#!/usr/bin/env bash
#
# check-depguard-coverage.sh — every public package has a recorded boundary decision.
#
# Prevents: a new public package landing with nobody having considered whether it
# needs boundary rules. depguard's `files:` and `pkg:` entries are prefix matches,
# not globs — there is no way to write "every root-level package" — so the rules
# in .golangci.yml are a hand-written enumeration. A hand-written enumeration
# FAILS OPEN: the package nobody remembered to add is simply unguarded, silently,
# and the lint run stays green while reporting on a subset of the tree.
#
# So the enumeration is itself checked. scripts/gates/public-packages.txt is the
# manifest of Fabrin's public (user-importable) packages. This gate fails when:
#
#   1. A public package exists on disk but is missing from the manifest.
#   2. The manifest lists a package that no longer exists.
#   3. A manifest entry is not mentioned anywhere in .golangci.yml — the rules
#      file must record the decision, even when the decision is "no rules needed",
#      because "we thought about it and it's fine" and "we forgot" are otherwise
#      indistinguishable six months later.
#
set -euo pipefail

cd "$(dirname "$0")/../.."

manifest="scripts/gates/public-packages.txt"
config=".golangci.yml"

[[ -f "$manifest" ]] || { echo "✗ depguard-coverage: $manifest not found" >&2; exit 1; }
[[ -f "$config" ]] || { echo "✗ depguard-coverage: $config not found" >&2; exit 1; }

# Public = the root package plus any root-level directory holding a .go file that
# users can import. internal/ is unimportable by language rule; the rest are not
# library surface.
not_public='^(internal|tools|examples|scripts|docs|specs|perf|api|web|\..*)$'

discovered=()
if compgen -G "*.go" >/dev/null; then
  discovered+=(".")
fi
for d in */; do
  d="${d%/}"
  [[ "$d" =~ $not_public ]] && continue
  compgen -G "$d/*.go" >/dev/null || continue
  discovered+=("$d")
done

listed=()
while IFS= read -r line; do
  line="${line%%#*}"
  line="$(tr -d '[:space:]' <<<"$line")"
  [[ -n "$line" ]] && listed+=("$line")
done <"$manifest"

status=0

# 1. On disk but not in the manifest.
for pkg in "${discovered[@]}"; do
  found=0
  for l in ${listed[@]+"${listed[@]}"}; do
    [[ "$pkg" == "$l" ]] && found=1 && break
  done
  if [[ "$found" -eq 0 ]]; then
    echo "✗ depguard-coverage: public package '$pkg' is not in $manifest" >&2
    echo "    Add it, then record its boundary decision in $config —" >&2
    echo "    a depguard rule, or a comment saying why none is needed." >&2
    status=1
  fi
done

# 2. In the manifest but gone from disk.
for l in ${listed[@]+"${listed[@]}"}; do
  found=0
  for pkg in "${discovered[@]}"; do
    [[ "$pkg" == "$l" ]] && found=1 && break
  done
  if [[ "$found" -eq 0 ]]; then
    echo "✗ depguard-coverage: $manifest lists '$l', which has no Go files" >&2
    echo "    Remove the entry and its rules in $config." >&2
    status=1
  fi
done

# 3. Mentioned in the rules file.
for l in ${listed[@]+"${listed[@]}"}; do
  # The root package is named "." on disk; .golangci.yml refers to it by module
  # path or as "root", so accept either spelling.
  if [[ "$l" == "." ]]; then
    grep -qE 'root package|github\.com/usefabrin/fabrin"' "$config" && continue
    echo "✗ depguard-coverage: $config never mentions the root package" >&2
    status=1
    continue
  fi
  if ! grep -q "$l" "$config"; then
    echo "✗ depguard-coverage: $config never mentions '$l'" >&2
    echo "    Add a rule for it, or a comment recording why it needs none." >&2
    status=1
  fi
done

[[ "$status" -eq 0 ]] || exit 1

echo "✓ depguard-coverage: ${#discovered[@]} public package(s), all recorded."
