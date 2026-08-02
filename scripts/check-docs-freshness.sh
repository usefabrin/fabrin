#!/usr/bin/env bash
#
# check-docs-freshness.sh — governed changes update the documentation that owns
# their contract.
#
# Prevents: an unrelated documentation touch satisfying a changed public surface,
# or a deleted document being counted as an update. This gate checks destinations,
# not prose semantics; the docs-syncer charter owns the semantic review.
#
# Usage:
#   check-docs-freshness.sh
#   check-docs-freshness.sh BASE HEAD
#   check-docs-freshness.sh --staged
#   check-docs-freshness.sh --files-from FILE  # test seam: "M<TAB>path"
#
set -euo pipefail

cd "$(dirname "$0")/.."

changed_files() {
  case "${1:-}" in
    --staged)
      git diff --cached --no-renames --name-status --diff-filter=ACMRD
      ;;
    --files-from)
      cat "${2:?--files-from needs a file}"
      ;;
    "")
      if git rev-parse --verify --quiet HEAD~1 >/dev/null; then
        git diff --no-renames --name-status --diff-filter=ACMRD HEAD~1 HEAD
      else
        git ls-tree -r --name-only HEAD | sed $'s/^/A\t/'
      fi
      ;;
    *)
      git diff --no-renames --name-status --diff-filter=ACMRD "$1" "${2:?BASE given without HEAD}"
      ;;
  esac
}

changes="$(changed_files "$@")"
if [[ -z "$changes" ]]; then
  echo "✓ docs-freshness: no files changed."
  exit 0
fi

# Git diff runs with rename detection disabled, so real ranges describe a move as
# deletion plus addition. The test seam still accepts name-status rename/copy
# records: a rename's source and destination both changed, while only a copy's
# destination changed. Deletions may trigger policy but can never satisfy it.
all_files="$(awk -F '\t' '{ if ($1 ~ /^R/) { print $2; print $3 } else if ($1 ~ /^C/) print $3; else print $2 }' <<<"$changes")"
updated_files="$(awk -F '\t' '$1 !~ /^D/ { if ($1 ~ /^[RC]/) print $3; else print $2 }' <<<"$changes")"

has_changed() { grep -qE "$1" <<<"$all_files"; }
has_updated() { grep -qE "$1" <<<"$updated_files"; }

status=0
require_update() {
  local surface="$1" destinations="$2" hint="$3"
  if ! has_updated "$destinations"; then
    echo "✗ docs-freshness: $surface changed without updating its owning documentation." >&2
    echo "    $hint" >&2
    status=1
  fi
}

if has_changed '^api/fabrin\.txt$'; then
  require_update "the public API snapshot" '^CHANGELOG\.md$' \
    "Update CHANGELOG.md; a v0 may move, but never silently."
fi

if has_changed '^\.golangci\.yml$'; then
  require_update "boundary rules" '^CONTRIBUTING\.md$' \
    "Update CONTRIBUTING.md and prove the changed rule bites."
fi

if has_changed '^justfile$'; then
  require_update "the command surface" '^(AGENTS|CONTRIBUTING)\.md$' \
    "Update AGENTS.md or CONTRIBUTING.md where the recipe contract is documented."
fi

if has_changed '^(scripts/|\.github/workflows/)'; then
  require_update "validation or CI behavior" '^CONTRIBUTING\.md$' \
    "Update CONTRIBUTING.md where contributors learn what the gates enforce."
fi

if has_changed '^(\.claude/|\.codex/|\.cursor/|docs/agents/)'; then
  require_update "agent orchestration" '^AGENTS\.md$' \
    "Update AGENTS.md, the canonical cross-platform working agreement."
fi

# Public package implementation only. Tests, internal tooling, examples, and the
# dev-only tools module do not independently promise user-visible behavior.
if grep -E '\.go$' <<<"$all_files" |
  grep -vE '^(internal|tools|examples)/' |
  grep -vE '_test\.go$' |
  grep -q .; then
  require_update "a public package" \
    '^(CHANGELOG\.md|README\.md|ARCHITECTURE\.md|docs/(DJANGO_PARITY\.md|TODO\.md|requirements/FABRIN_REQUIREMENTS\.md)|specs/(system-behavior\.yaml|test-matrix\.md))$' \
    "Update the relevant contract, requirement, behavior spec, architecture, or changelog."
fi

[[ "$status" -eq 0 ]] || exit 1
echo "✓ docs-freshness: governed changes update their owning documentation."
