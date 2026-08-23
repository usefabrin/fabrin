#!/usr/bin/env bash
#
# check-migration-versions.sh — two migration files may not claim one version.
#
# Prevents: the deploy-time collision MIG-008 exists for. Two branches each
# generating the same timestamped migration are both green in isolation and
# collide only when their modules meet in one binary — where the engine rejects
# the set at RUN time, against a database. The only cheap moment is before
# merge, and a gate that reads FILES catches it even on a branch that does not
# compile, which is the exact case a registry built by running Go code misses.
#
# What it reads: every <module>/migrations/*.go except all.go. The filename
# prefix IS the version (fixed-width YYYYMMDDHHMMSS) — that is the generator's
# contract (MIG-033), so filenames carry everything this gate needs.
#
# What it rejects:
#   - one version claimed by two files, anywhere in the tree (the engine applies
#     every module's migrations as ONE set)
#   - mixed version widths across the whole set (lexicographic ordering on
#     unequal widths applies them in an order nobody wrote)
#   - a filename whose prefix is not a 14-digit timestamp (a typo'd version is
#     invisible to both checks above until it is too late to fix cheaply)
#
# Usage: check-migration-versions.sh [root]
#   root defaults to the repository root; tests pass a fixture tree instead.
set -uo pipefail

root="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
status=0

declare -a seen_files=()
declare -a seen_versions=()

while IFS= read -r dir; do
  while IFS= read -r file; do
    base="$(basename "$file")"
    [[ "$base" == "all.go" ]] && continue
    version="${base%%_*}"

    if [[ ! "$version" =~ ^[0-9]{14}$ ]]; then
      echo "✗ $file: version prefix '$version' is not a fixed-width YYYYMMDDHHMMSS timestamp" >&2
      status=1
      continue
    fi

    for i in "${!seen_versions[@]}"; do
      if [[ "${seen_versions[$i]}" == "$version" ]]; then
        echo "✗ duplicate migration version $version:" >&2
        echo "    ${seen_files[$i]}" >&2
        echo "    ${file#"$root"/}" >&2
        status=1
      fi
    done

    seen_versions+=("$version")
    seen_files+=("${file#"$root"/}")
  done < <(find "$dir" -maxdepth 1 -name '*.go' | sort)

  # Width consistency is checked across the WHOLE collected set below, because
  # the engine applies every module's migrations as one union.
done < <(find "$root" -type d -name migrations -not -path '*/node_modules/*' 2>/dev/null | sort)

width=""
for v in "${seen_versions[@]}"; do
  if [[ -z "$width" ]]; then
    width="${#v}"
  elif [[ "${#v}" -ne "$width" ]]; then
    echo "✗ mixed version widths across migrations (${width} vs ${#v} chars): ordering is lexicographic, and a mixed set applies in an order nobody wrote" >&2
    status=1
    break
  fi
done

if (( status == 0 )); then
  n="${#seen_versions[@]}"
  echo "✓ migration-versions: ${n} migration file(s), no duplicate or malformed versions."
fi
exit "$status"
