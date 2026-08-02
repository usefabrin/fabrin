# Platform-local multi-agent orchestration

## Roles and authority

The top-level native session is the **Lead**. There is no spawnable orchestrator
role: specialists never delegate, and making orchestration a child would create
an unnecessary nesting boundary.

The Lead alone:

- selects one runtime: `codex`, `claude`, or `cursor`;
- builds the task graph and issues complete task packets;
- creates and removes isolated worktrees and allocates ports;
- owns branches, the Git index, commits, remotes, issues, pull requests, and merges;
- rejects overlapping write sets and stale results;
- integrates accepted results one at a time; and
- runs final validation on the integrated tree.

A worker never spawns another agent and never mutates Git or GitHub state. It may
inspect `status`, `diff`, `show`, and `log`. It writes only when its packet says
`owned-write`, its current directory is the assigned isolated worktree, and every
changed path is owned by that packet.

## Scheduling

- Trivial risk: use no subagent.
- Normal risk: use one or two independent workers when the work is separable.
- High risk: one implementer, then two independent adversarial reviewers.
- At most three workers may be concurrent.
- Parallelize read-heavy exploration, triage, tests, and review freely.
- Never run writers over overlapping path sets.
- When isolated writable worktrees cannot be guaranteed, delegate reads only.
- Specialists may not delegate or create nested teams.

Full `just check`, `just race`, examples, scaffold smoke tests, benchmarks, and
final integration are exclusive Lead operations. If another isolated validation
is necessary, its packet receives disjoint `SMOKE_PORT_BASE` and `SCAFFOLD_PORT`
values.

## Task packets

Every delegation uses `schemas/task-packet.schema.json`. The packet fixes the
task ID, runtime, role, issue, objective, risk, base SHA, absolute worktree,
access mode, owned paths, inputs, invariants, acceptance commands, exclusive
resources, and optional ports.

Before dispatch, the Lead runs:

```text
go -C tools run ./cmd/agentcheck task --repo .. --runtime codex --base BASE_SHA TASK.json [TASK.json...]
```

`owned_paths` entries are normalized, slash-separated repository-relative
prefixes: `docs/agents` owns that path and its descendants, while `app.go` owns
that exact file. Globs, backslashes, `.`, parent traversal, absolute paths, and
`.git` are forbidden. Worktree paths may be absolute POSIX or Windows paths.

The Lead rejects a packet before dispatch when:

- its runtime differs from the Lead's native platform;
- its write set overlaps another active writer;
- `read-only` contains an owned path;
- `owned-write` has no owned path or no isolated worktree;
- its base SHA is not the integration base; or
- its commands would use another agent platform.

## Results and fan-in

Every worker returns `schemas/result-packet.schema.json`; prose may accompany it,
but may not replace it. The Lead verifies task ID, runtime, role, assigned base,
changed paths, command outcomes, blockers, and hand-back condition.

Before accepting a result, the Lead runs:

```text
go -C tools run ./cmd/agentcheck result --repo .. --runtime codex --base BASE_SHA TASK.json RESULT.json
```

The validator rejects strict-JSON/type errors, unknown roles, access that differs
from the role catalog, cross-runtime or different-base fanout, overlapping
writable prefixes, stale SHAs, worker-created commits, read-only mutations, and
changed paths outside ownership.

Both observed SHAs must equal the assigned base unless the Lead explicitly
issued a refreshed packet. A stale result is rejected and rerun, never silently
rebased. A read-only result with changed paths, a worker-created commit or branch,
or a path outside ownership is rejected.

Integration is serialized. After applying one accepted result, the Lead runs its
targeted acceptance commands before considering the next. Final validation runs
only after all accepted work is integrated.

## Failure, retry, and cancellation

- `failed`: the worker could execute but the objective or validation failed.
- `blocked`: required authority, input, or external state is unavailable.
- `completed`: the hand-back condition is satisfied and blockers are empty.

The Lead may retry once with a corrected or refreshed packet. It cancels work
that becomes stale, overlaps newly accepted ownership, or is no longer needed.
Cancellation never grants permission to discard another worktree's changes.

## Durable evidence

Runtime state belongs in the native platform. Durable evidence belongs in the
issue or pull request: expected-red TDD output, injected gate failures, negative
controls, review findings, validation summaries, and accepted risks. Do not add
tracked per-run logs.

## Human review

Human review is required before merging changes to:

- public Go API or the Gin allowlist;
- ADRs or v0 non-goals;
- authentication, authorization, session, or security defaults;
- potentially destructive data migrations;
- orchestration contracts or native adapters; and
- CI or validation gates.

Green automation remains necessary but is not sufficient for these categories.
