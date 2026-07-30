#!/usr/bin/env bash
#
# build-examples.sh — compile every app under examples/ to a throwaway binary.
#
# Skips cleanly before examples/ exists so `just check` stays green across the
# PRs of a multi-PR epic (see the justfile header).
#
set -euo pipefail

cd "$(dirname "$0")/.."

go="${GO:-go}"

if [[ ! -d examples ]]; then
  echo "→ build-examples: no examples/ directory yet, skipping."
  exit 0
fi

shopt -s nullglob
dirs=(examples/*/)
shopt -u nullglob

[[ ${#dirs[@]} -gt 0 ]] || { echo "→ build-examples: examples/ is empty, skipping."; exit 0; }

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

for d in "${dirs[@]}"; do
  name="$(basename "$d")"
  "$go" build -o "$out/$name" "./$d"
  echo "  ✓ built examples/$name"
done

echo "✓ build-examples: ${#dirs[@]} example(s) compiled."
