#!/usr/bin/env bash
#
# agents.sh — render and verify native agent adapters from the canonical role
# catalog. Adapters are projections; docs/agents is the source of truth.
#
set -euo pipefail

cd "$(dirname "$0")/.."
mode="${1:-check}"
[[ "$mode" == "check" || "$mode" == "write" ]] || {
  echo "usage: scripts/agents.sh [check|write]" >&2
  exit 2
}

catalog="docs/agents/catalog.tsv"
[[ -f "$catalog" ]] || { echo "✗ agents: $catalog is missing" >&2; exit 1; }

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
mkdir -p "$out/.claude/agents" "$out/.codex/agents" "$out/.cursor/agents"

cat >"$out/.codex/config.toml" <<'EOF'
[agents]
max_concurrent_threads_per_session = 3
EOF

count=0
while IFS=$'\t' read -r id access capabilities description extra; do
  [[ -z "$id" || "$id" == \#* ]] && continue
  if [[ -n "${extra:-}" || ! "$id" =~ ^[a-z][a-z0-9-]*$ ]]; then
    echo "✗ agents: invalid catalog row for id '$id'" >&2
    exit 1
  fi
  if [[ "$access" != "read-only" && "$access" != "owned-write" ]]; then
    echo "✗ agents: $id has invalid access '$access'" >&2
    exit 1
  fi
  [[ "$capabilities" =~ ^[a-z]+(,[a-z]+)*$ ]] || {
    echo "✗ agents: $id has invalid capabilities '$capabilities'" >&2
    exit 1
  }
  IFS=',' read -r -a capability_list <<<"$capabilities"
  for capability in "${capability_list[@]}"; do
    case "$capability" in
      read|search|shell|edit) ;;
      *) echo "✗ agents: $id declares unknown capability '$capability'" >&2; exit 1 ;;
    esac
  done
  [[ -n "$description" ]] || { echo "✗ agents: $id has no description" >&2; exit 1; }
  [[ "$description" != *'"'* && "$description" != *'\'* ]] || {
    echo "✗ agents: $id description contains an unsafe quote or backslash" >&2
    exit 1
  }
  [[ -f "docs/agents/roles/$id.md" ]] || {
    echo "✗ agents: canonical charter docs/agents/roles/$id.md is missing" >&2
    exit 1
  }

  claude_tools=""
  [[ ",$capabilities," == *,read,* ]] && claude_tools="Read"
  [[ ",$capabilities," == *,search,* ]] && claude_tools="${claude_tools:+$claude_tools, }Grep, Glob"
  [[ ",$capabilities," == *,shell,* ]] && claude_tools="${claude_tools:+$claude_tools, }Bash"
  has_edit=0
  [[ ",$capabilities," == *,edit,* ]] && has_edit=1
  claude_mode="permissionMode: plan"
  codex_sandbox="read-only"
  cursor_readonly="true"
  if [[ "$access" == "owned-write" ]]; then
    [[ "$has_edit" -eq 1 ]] || { echo "✗ agents: owned-write role $id lacks edit capability" >&2; exit 1; }
    claude_tools="${claude_tools:+$claude_tools, }Write, Edit"
    claude_mode=$'permissionMode: default\nisolation: worktree'
    codex_sandbox="workspace-write"
    cursor_readonly="false"
  elif [[ "$has_edit" -eq 1 ]]; then
    echo "✗ agents: read-only role $id declares edit capability" >&2
    exit 1
  fi

  cat >"$out/.claude/agents/$id.md" <<EOF
---
name: $id
description: "$description"
tools: $claude_tools
model: inherit
$claude_mode
---

This is a native Claude Code adapter, not the source of truth.

Before acting, read \
\`AGENTS.md\`, \
\`docs/agents/ORCHESTRATION.md\`, and \
\`docs/agents/roles/$id.md\`.

Accept only a complete task packet whose runtime is \
\`claude\`. Never invoke another agent platform, never delegate, and never
mutate Git or GitHub state. Follow the packet's access and owned-path
restrictions. Return a result conforming to
\`docs/agents/schemas/result-packet.schema.json\`.
EOF

  cat >"$out/.codex/agents/$id.toml" <<EOF
name = "$id"
description = "$description"
sandbox_mode = "$codex_sandbox"
developer_instructions = """
This is a native Codex adapter, not the source of truth.

Before acting, read AGENTS.md, docs/agents/ORCHESTRATION.md, and docs/agents/roles/$id.md.

Accept only a complete task packet whose runtime is codex. Never invoke another
agent platform, never delegate, and never mutate Git or GitHub state. Follow the
packet's access and owned-path restrictions. Return a result conforming to
docs/agents/schemas/result-packet.schema.json.
"""
EOF

  cat >"$out/.cursor/agents/$id.md" <<EOF
---
name: $id
description: "$description"
model: inherit
readonly: $cursor_readonly
---

This is a native Cursor adapter, not the source of truth.

Before acting, read \
\`AGENTS.md\`, \
\`docs/agents/ORCHESTRATION.md\`, and \
\`docs/agents/roles/$id.md\`.

Accept only a complete task packet whose runtime is \
\`cursor\`. Never invoke another agent platform, never delegate, and never
mutate Git or GitHub state. Follow the packet's access and owned-path
restrictions. Return a result conforming to
\`docs/agents/schemas/result-packet.schema.json\`.
EOF
  count=$((count + 1))
done <"$catalog"

[[ "$count" -gt 0 ]] || { echo "✗ agents: catalog declares no roles" >&2; exit 1; }

# Extra canonical charters are as dangerous as missing adapters: they create a
# role that one platform may discover while another cannot.
charter_count="$(find docs/agents/roles -maxdepth 1 -type f -name '*.md' | wc -l | tr -d ' ')"
[[ "$charter_count" -eq "$count" ]] || {
  echo "✗ agents: catalog has $count roles but roles/ has $charter_count charters" >&2
  exit 1
}

if [[ "$mode" == "write" ]]; then
  mkdir -p .claude/agents .codex/agents .cursor/agents
  for existing in .claude/agents/*.md .codex/agents/*.toml .cursor/agents/*.md; do
    [[ -f "$existing" ]] || continue
    if grep -Eq 'native (Claude Code|Codex|Cursor) adapter, not the source of truth' "$existing"; then
      rm -f "$existing"
    fi
  done
  cp "$out/.claude/agents/"*.md .claude/agents/
  cp "$out/.codex/agents/"*.toml .codex/agents/
  cp "$out/.cursor/agents/"*.md .cursor/agents/
  cp "$out/.codex/config.toml" .codex/config.toml
  echo "✓ agents: rendered $count roles for Claude Code, Codex, and Cursor."
  exit 0
fi

status=0
for platform in .claude/agents .codex/agents .cursor/agents; do
  if [[ ! -d "$platform" ]]; then
    echo "✗ agents: $platform is missing; run bash scripts/agents.sh write" >&2
    status=1
    continue
  fi
  diff -ru "$out/$platform" "$platform" || status=1
done
if [[ ! -f .codex/config.toml ]]; then
  echo "✗ agents: .codex/config.toml is missing" >&2
  status=1
elif ! diff -u "$out/.codex/config.toml" .codex/config.toml; then
  status=1
fi

set +e
platform_invocations="$(grep -ERn \
  '(codex|claude|cursor)[[:space:]]+(exec|run|agent)' \
  .claude/agents .codex/agents .cursor/agents)"
grep_status=$?
set -e
case "$grep_status" in
  0)
    echo "✗ agents: an adapter appears to invoke an agent platform" >&2
    echo "$platform_invocations" >&2
    status=1
    ;;
  1) ;;
  *)
    echo "✗ agents: could not inspect native adapters for platform invocation" >&2
    status=1
    ;;
esac

[[ "$status" -eq 0 ]] || {
  echo "✗ agents: native adapters drifted; run bash scripts/agents.sh write" >&2
  exit 1
}
echo "✓ agents: $count canonical roles match all three native platforms."
