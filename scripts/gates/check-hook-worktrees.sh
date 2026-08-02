#!/usr/bin/env bash
#
# Prevents: the shared hooks directory pointing at the last linked worktree that
# ran installation, then dangling when that worktree is removed.
#
set -euo pipefail

source_root="$(cd "$(dirname "$0")/../.." && pwd)"
# Git exports repository-local variables to hooks. This gate deliberately opens
# a different repository, so carrying the caller's index/worktree paths into it
# makes the fixture operate on the real repository (or fail before it starts).
# Clear every local variable Git documents before creating the isolated fixture.
unset $(git rev-parse --local-env-vars)
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
repo="$tmp/repo"
linked="$tmp/linked"

git init -q "$repo"
git -C "$repo" config user.email hooks@example.invalid
git -C "$repo" config user.name "Hook Gate"
mkdir -p "$repo/scripts/hooks"
printf '#!/usr/bin/env bash\necho main\n' >"$repo/scripts/hooks/pre-commit"
chmod +x "$repo/scripts/hooks/pre-commit"
git -C "$repo" add scripts/hooks/pre-commit
git -C "$repo" commit -qm initial
git -C "$repo" worktree add -q "$linked"

(cd "$repo" && bash "$source_root/scripts/install-hooks.sh" >/dev/null)
printf '#!/usr/bin/env bash\necho linked\n' >"$linked/scripts/hooks/pre-commit"
chmod +x "$linked/scripts/hooks/pre-commit"
(cd "$linked" && bash "$source_root/scripts/install-hooks.sh" >/dev/null)

hook="$(git -C "$repo" rev-parse --absolute-git-dir)/hooks/pre-commit"
[[ "$(cd "$repo" && "$hook")" == "main" ]] || {
  echo "✗ hook-worktrees: dispatcher did not use the main worktree hook" >&2
  exit 1
}
[[ "$(cd "$linked" && "$hook")" == "linked" ]] || {
  echo "✗ hook-worktrees: dispatcher did not use the linked worktree hook" >&2
  exit 1
}

git -C "$repo" worktree remove --force "$linked"
[[ "$(cd "$repo" && "$hook")" == "main" ]] || {
  echo "✗ hook-worktrees: removing a linked worktree broke the shared hook" >&2
  exit 1
}

echo "✓ hook-worktrees: shared dispatcher resolves each active worktree."
