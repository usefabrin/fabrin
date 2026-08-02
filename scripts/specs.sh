#!/usr/bin/env bash
#
# specs.sh — every documented behaviour is traceable to a test.
#
# Prevents: a behaviour claimed in specs/system-behavior.yaml with nothing
# executable behind it. A spec nobody tests is a wish, and the gap is invisible —
# the spec reads as a description of the system while describing an intention.
#
# The parser lives in tools/ so YAML support never enters the framework module.
# It validates the document structure, exact matrix rows, requirement references,
# and exact Go test functions through the AST. A grep for `func TestFoo` accepts
# comments and TestFooLonger; neither is executable evidence for TestFoo.
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

go -C tools run ./speccheck -root ..
