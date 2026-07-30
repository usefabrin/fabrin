#!/usr/bin/env bash
#
# check-docs-freshness.sh — a governed-surface change carries its docs update.
#
# Prevents: documentation drifting behind the code it describes. For a framework
# this is not cosmetic — the docs ARE the product for anyone who has not read the
# source, and a wrong doc costs a user more than a missing one, because they trust
# it and debug in the wrong place.
#
# "Governed surface" means a change a user can observe and that documentation
# promises something about:
#
#   api/fabrin.txt   the exported API — CHANGELOG.md must record the move
#   the root package and public packages — docs/ or specs/ must move with them
#   justfile         the command surface — AGENTS.md and CONTRIBUTING.md name it
#   .golangci.yml    boundary rules — CONTRIBUTING.md documents them
#
# Usage:
#   check-docs-freshness.sh                 compare HEAD against its parent
#   check-docs-freshness.sh BASE HEAD       compare a range (CI, on pull requests)
#   check-docs-freshness.sh --staged        compare the index (the pre-commit hook)
#
set -euo pipefail

cd "$(dirname "$0")/.."

changed_files() {
  case "${1:-}" in
    --staged)
      git diff --cached --name-only --diff-filter=ACMR
      ;;
    "")
      # A repo with a single commit has no parent to diff against.
      if git rev-parse --verify --quiet HEAD~1 >/dev/null; then
        git diff --name-only --diff-filter=ACMR HEAD~1 HEAD
      else
        git ls-tree -r --name-only HEAD
      fi
      ;;
    *)
      git diff --name-only --diff-filter=ACMR "$1" "${2:?BASE given without HEAD}"
      ;;
  esac
}

files="$(changed_files "$@" || true)"

if [[ -z "$files" ]]; then
  echo "✓ docs-freshness: no files changed."
  exit 0
fi

has() { grep -qE "$1" <<<"$files"; }

# Documentation that can satisfy a governed-surface change.
docs_touched=0
has '^(docs/|specs/|README\.md$|ARCHITECTURE\.md$|AGENTS\.md$|CONTRIBUTING\.md$|CHANGELOG\.md$)' && docs_touched=1

status=0
require_docs() {
  local what="$1" hint="$2"
  if [[ "$docs_touched" -eq 0 ]]; then
    echo "✗ docs-freshness: this change touches $what but updates no documentation." >&2
    echo "    $hint" >&2
    status=1
  fi
}

# The API snapshot moving is the one case with a specific required destination:
# a v0 that breaks people without listing the break is a v0 nobody can upgrade.
if has '^api/fabrin\.txt$'; then
  if ! has '^CHANGELOG\.md$'; then
    echo "✗ docs-freshness: api/fabrin.txt moved but CHANGELOG.md was not updated." >&2
    echo "    The public surface changed. Record it — v0 may break users, but not silently." >&2
    status=1
  fi
fi

# Go changes in public packages. internal/, tools/, examples/ and _test.go files
# are excluded: they are not surface a document promises anything about.
if grep -E '\.go$' <<<"$files" |
  grep -vE '^(internal|tools|examples)/' |
  grep -vE '_test\.go$' |
  grep -q .; then
  require_docs "a public package" \
    "Update docs/, specs/, or the parity table. If nothing needed changing, touch the doc that says so."
fi

has '^justfile$' && require_docs "the justfile" \
  "The command table in AGENTS.md and the gate table in CONTRIBUTING.md name these recipes."

has '^\.golangci\.yml$' && require_docs "boundary rules" \
  "CONTRIBUTING.md documents the depguard rules. Also prove the rule bites before trusting it."

[[ "$status" -eq 0 ]] || exit 1

echo "✓ docs-freshness: governed-surface changes carry their docs."
