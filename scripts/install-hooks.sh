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
# Linked worktrees share one hooks directory, so an absolute symlink to the
# installer worktree is also wrong: the last installer wins, and deleting that
# worktree leaves every checkout with a dangling hook. Install a regular
# dispatcher that resolves the active worktree when Git invokes it.
#
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

# --git-path resolves correctly for plain checkouts, worktrees, and submodules,
# and honours core.hooksPath if it is set.
hooks="$(git rev-parse --git-path hooks)"
mkdir -p "$hooks"

link="$hooks/pre-commit"
target="$root/scripts/hooks/pre-commit"

[[ -f "$target" ]] || { echo "✗ hooks: $target not found" >&2; exit 1; }
[[ -x "$target" ]] || { echo "✗ hooks: $target is not executable — chmod +x it" >&2; exit 1; }

tmp="$(mktemp "${link}.tmp.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'root="$(git rev-parse --show-toplevel)"' \
  'exec "$root/scripts/hooks/pre-commit" "$@"' >"$tmp"
chmod +x "$tmp"
mv "$tmp" "$link"
trap - EXIT

# The point of the exercise: the installed dispatcher must be executable and
# independent of whichever worktree happened to run this installer last.
if [[ ! -x "$link" ]]; then
  echo "✗ hooks: $link does not resolve to an executable — the hook would not run" >&2
  exit 1
fi

echo "✓ hooks: worktree-aware pre-commit dispatcher installed → $link"
