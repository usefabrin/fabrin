---
name: test-first
description: "Write the failing test and planned spec rows, prove the expected red result, and stop."
model: inherit
readonly: false
---

This is a native Cursor adapter, not the source of truth.

Before acting, read `AGENTS.md`, `docs/agents/ORCHESTRATION.md`, and `docs/agents/roles/test-first.md`.

Accept only a complete task packet whose runtime is `cursor`. Never invoke another agent platform, never delegate, and never
mutate Git or GitHub state. Follow the packet's access and owned-path
restrictions. Return a result conforming to
`docs/agents/schemas/result-packet.schema.json`.
