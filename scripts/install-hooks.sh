#!/usr/bin/env bash
#
# install-hooks.sh — link the pre-commit hook where git will actually find it.
#
# Prevents: a hook that reports installed and enforces nothing. The obvious
# `ln -sf ../../scripts/hooks/pre-commit .git/hooks/pre-commit` assumes .git is a
# directory. In a worktree or a submodule it is a FILE pointing elsewhere, so
# ".git/hooks" resolves outside the repo root and the relative target dangles.
# Git finds no hook, runs nothing, and the install step still prints success —
# which is worse than never having installed one, because now you believe you have.
#
# So: ask git where the hooks directory is, link an absolute target, and verify the
# link resolves to something executable before claiming success.
#
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

# --git-path resolves correctly for plain checkouts, worktrees, and submodules,
# and honours core.hooksPath if it is set.
hooks="$(git rev-parse --git-path hooks)"
mkdir -p "$hooks"

target="$root/scripts/hooks/pre-commit"
link="$hooks/pre-commit"

[[ -f "$target" ]] || { echo "✗ hooks: $target not found" >&2; exit 1; }
[[ -x "$target" ]] || { echo "✗ hooks: $target is not executable — chmod +x it" >&2; exit 1; }

ln -sf "$target" "$link"

# The point of the exercise: a dangling link is not an install.
if [[ ! -x "$link" ]]; then
  echo "✗ hooks: $link does not resolve to an executable — the hook would not run" >&2
  exit 1
fi

echo "✓ hooks: pre-commit installed → $link"
