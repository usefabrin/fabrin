#!/usr/bin/env bash
#
# specs.sh — every documented behaviour is traceable to a test.
#
# Prevents: a behaviour claimed in specs/system-behavior.yaml with nothing
# executable behind it. A spec nobody tests is a wish, and the gap is invisible —
# the spec reads as a description of the system while describing an intention.
#
# Three checks:
#
#   1. Every `- id:` in specs/system-behavior.yaml has a row in
#      specs/test-matrix.md. The matrix is where a reader looks to find the test;
#      an id missing from it is a behaviour with no discoverable coverage.
#   2. Every id marked `status: implemented` names a `test:` file that exists.
#   3. Every id in the matrix exists in the spec — the matrix must not accumulate
#      rows for behaviours that were renamed or dropped.
#
# The spec is kept flat and greppable on purpose: this gate needs no YAML parser,
# so it stays fast enough for the pre-commit hook and has no dependencies.
#
set -euo pipefail

cd "$(dirname "$0")/.."

spec="specs/system-behavior.yaml"
matrix="specs/test-matrix.md"

if [[ ! -d specs ]]; then
  echo "→ specs: no specs/ directory yet, skipping."
  exit 0
fi

for f in "$spec" "$matrix"; do
  [[ -f "$f" ]] || { echo "✗ specs: $f not found" >&2; exit 1; }
done

status=0

# Collect ids, and the id → (status, test) pairing. The spec's shape is:
#   - id: CORE-001
#     status: implemented
#     test: module_test.go::TestApp_RejectsDuplicateModuleNames
ids=()
cur_id=""
cur_status=""
cur_test=""

flush() {
  [[ -z "$cur_id" ]] && return 0
  ids+=("$cur_id")

  if ! grep -q "$cur_id" "$matrix"; then
    echo "✗ specs: $cur_id has no row in $matrix" >&2
    status=1
  fi

  if [[ "$cur_status" == "implemented" ]]; then
    if [[ -z "$cur_test" ]]; then
      echo "✗ specs: $cur_id is 'implemented' but names no test:" >&2
      status=1
    else
      file="${cur_test%%::*}"
      if [[ ! -f "$file" ]]; then
        echo "✗ specs: $cur_id names test file '$file', which does not exist" >&2
        status=1
      elif [[ "$cur_test" == *"::"* ]]; then
        fn="${cur_test##*::}"
        if ! grep -q "func $fn" "$file"; then
          echo "✗ specs: $cur_id names '$fn' in $file, which has no such function" >&2
          status=1
        fi
      fi
    fi
  fi

  cur_id=""; cur_status=""; cur_test=""
}

while IFS= read -r line; do
  case "$line" in
    *"- id:"*)
      flush
      cur_id="$(sed -E 's/.*- id:[[:space:]]*//; s/[[:space:]]*(#.*)?$//' <<<"$line")"
      ;;
    *"status:"*) cur_status="$(sed -E 's/.*status:[[:space:]]*//; s/[[:space:]]*(#.*)?$//' <<<"$line")" ;;
    *"test:"*) cur_test="$(sed -E 's/.*test:[[:space:]]*//; s/[[:space:]]*(#.*)?$//' <<<"$line")" ;;
  esac
done <"$spec"
flush

if [[ ${#ids[@]} -eq 0 ]]; then
  echo "✗ specs: $spec declares no behaviours (no '- id:' entries)" >&2
  exit 1
fi

# 3. Matrix rows with no spec entry. Matrix ids are the leading cell of a table
# row, e.g. "| CORE-001 | ... |".
while IFS= read -r mid; do
  [[ -n "$mid" ]] || continue
  found=0
  for id in "${ids[@]}"; do
    [[ "$id" == "$mid" ]] && found=1 && break
  done
  if [[ "$found" -eq 0 ]]; then
    echo "✗ specs: $matrix has a row for '$mid', which is not in $spec" >&2
    status=1
  fi
done < <(grep -oE '^\|[[:space:]]*[A-Z]+-[0-9]+' "$matrix" | sed -E 's/^\|[[:space:]]*//' || true)

[[ "$status" -eq 0 ]] || exit 1

echo "✓ specs: ${#ids[@]} behaviour(s), each in the matrix and traceable to a test."
