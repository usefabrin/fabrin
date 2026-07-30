#!/usr/bin/env bash
#
# check-examples.sh — every example is a real, wired-in program.
#
# Prevents: an example that quietly stops compiling. Examples are the only part of
# a framework's documentation the compiler can check, which makes them the part
# most worth checking — and the part nobody notices has rotted, because rot in an
# example breaks no test and no user build.
#
# Requires each directory under examples/ to hold a main.go with `package main`.
# Building and smoke-running them is `just examples`; this gate only asserts the
# shape, so it stays fast enough for the pre-commit hook.
#
set -euo pipefail

cd "$(dirname "$0")/../.."

if [[ ! -d examples ]]; then
  echo "→ examples: no examples/ directory yet, skipping."
  exit 0
fi

shopt -s nullglob
dirs=(examples/*/)
shopt -u nullglob

if [[ ${#dirs[@]} -eq 0 ]]; then
  echo "✗ examples: examples/ exists but is empty — delete it or add an example" >&2
  exit 1
fi

status=0
for d in "${dirs[@]}"; do
  name="$(basename "$d")"

  if [[ ! -f "${d}main.go" ]]; then
    echo "✗ examples: $name has no main.go — an example must be runnable" >&2
    status=1
    continue
  fi

  if ! grep -qE '^package main$' "${d}main.go"; then
    echo "✗ examples: ${d}main.go is not 'package main'" >&2
    status=1
  fi
done

[[ "$status" -eq 0 ]] || exit 1

echo "✓ examples: ${#dirs[@]} example(s), each runnable."
