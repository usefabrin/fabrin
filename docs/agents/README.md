# Fabrin agent orchestration

This directory is the vendor-neutral source of truth for Fabrin's specialist
agents. `AGENTS.md` remains the working agreement for every human and agent;
these files define how one native platform session delegates bounded work.

- [ORCHESTRATION.md](ORCHESTRATION.md) defines scheduling, ownership, packets,
  integration, and human-review boundaries.
- [catalog.tsv](catalog.tsv) is the machine-readable role inventory.
- `roles/` contains canonical specialist charters.
- `schemas/` defines task and result packets.
- `procedures/` contains reusable, platform-neutral workflows.

The adapters in `.claude/agents/`, `.codex/agents/`, and `.cursor/agents/` are
generated projections. Run `bash scripts/agents.sh check` to verify parity or
`bash scripts/agents.sh write` after changing the catalog or renderer.

One run selects exactly one native runtime family. A Codex lead delegates only
to Codex agents, a Claude Code lead only to Claude agents, and a Cursor lead only
to Cursor agents. Cross-platform invocation is forbidden.
