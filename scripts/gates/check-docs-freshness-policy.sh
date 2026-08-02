#!/usr/bin/env bash
#
# Prevents: docs freshness accepting unrelated or deleted documentation as proof
# that a governed surface was updated.
#
set -euo pipefail

cd "$(dirname "$0")/../.."
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

expect_pass() {
  printf '%s\n' "$1" >"$tmp"
  bash scripts/check-docs-freshness.sh --files-from "$tmp" >/dev/null
}

expect_fail() {
  printf '%s\n' "$1" >"$tmp"
  if bash scripts/check-docs-freshness.sh --files-from "$tmp" >/dev/null 2>&1; then
    echo "✗ docs-policy: violation unexpectedly passed: $1" >&2
    exit 1
  fi
}

expect_fail $'M\t.golangci.yml\nM\tdocs/TODO.md'
expect_pass $'M\t.golangci.yml\nM\tCONTRIBUTING.md'
expect_fail $'M\t.github/workflows/ci.yml\nM\tREADME.md'
expect_pass $'M\t.github/workflows/ci.yml\nM\tCONTRIBUTING.md'
expect_fail $'M\tapi/fabrin.txt\nD\tCHANGELOG.md'
expect_pass $'M\tapi/fabrin.txt\nM\tCHANGELOG.md'
expect_fail $'C100\tREADME.md\t.github/workflows/copied.yml'
expect_pass $'M\t.github/workflows/ci.yml\nC100\tREADME.md\tCONTRIBUTING.md'
expect_fail $'R100\tconfig/options.go\tinternal/options.go'
expect_pass $'R100\tconfig/options.go\tinternal/options.go\nM\tARCHITECTURE.md'
expect_fail $'D\tconfig/old.go'
expect_pass $'D\tinternal/old.go'
expect_pass $'M\tconfig/options.go\nM\tspecs/system-behavior.yaml'

if bash scripts/check-docs-freshness.sh refs/heads/does-not-exist HEAD >/dev/null 2>&1; then
  echo "✗ docs-policy: an invalid Git range was accepted as no changes" >&2
  exit 1
fi

echo "✓ docs-policy: destination-specific updates required; deletions cannot satisfy them."
