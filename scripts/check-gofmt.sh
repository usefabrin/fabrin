#!/usr/bin/env bash
#
# check-gofmt.sh — fail on unformatted Go.
#
# Prevents: `gofmt -l` printing filenames and exiting 0, which is what it does by
# design. Wiring `gofmt -l .` straight into a lint recipe therefore reports
# problems and passes anyway — the check looks green while the tree is dirty.
#
set -euo pipefail

cd "$(dirname "$0")/.."

# Every module in the repo, so tools/ is covered too.
unformatted="$(gofmt -l . 2>/dev/null || true)"

if [[ -n "$unformatted" ]]; then
  echo "✗ gofmt: these files are not formatted:" >&2
  sed 's|^|    |' <<<"$unformatted" >&2
  echo "    Run \`just format\`." >&2
  exit 1
fi

echo "✓ gofmt: clean."
