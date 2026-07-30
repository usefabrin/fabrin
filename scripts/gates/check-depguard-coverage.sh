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
#   3. A manifest entry has no `# boundary: <name> — <decision>` line in
#      .golangci.yml. The rules file must record the decision even when the
#      decision is "no rules needed", because "we thought about it and it's fine"
#      and "we forgot" are otherwise indistinguishable six months later.
#
#      Check 3 originally grepped for the bare package name "mentioned anywhere"
#      in the config. That fails open on a substring: `orm` matches `formatters:`,
#      so fabrin/orm would have landed with no rule recorded, in the very gate
#      written to prevent unguarded packages. The structured marker below cannot
#      be satisfied by prose, and the name must be followed by a terminator so
#      `config` does not match a hypothetical `configx`.
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

# 3. Has a recorded boundary decision in the rules file.
#
# The marker is `# boundary: <name> — <decision>`. The root package is "." on disk
# and `root` in the inventory, since "." reads as nothing in a comment.
for l in ${listed[@]+"${listed[@]}"}; do
  marker="$l"
  [[ "$l" == "." ]] && marker="root"

  # Anchor the name and require a terminator, so `config` cannot be satisfied by
  # `configx` and no substring of unrelated prose can satisfy anything.
  if ! grep -qE "^[[:space:]]*#[[:space:]]*boundary:[[:space:]]+${marker}([[:space:]]|$)" "$config"; then
    echo "✗ depguard-coverage: $config has no boundary decision for '$l'" >&2
    echo "    Add a line to the inventory:" >&2
    echo "        # boundary: $marker — <the rule, or why none is needed>" >&2
    status=1
  fi
done

[[ "$status" -eq 0 ]] || exit 1

echo "✓ depguard-coverage: ${#discovered[@]} public package(s), all recorded."
