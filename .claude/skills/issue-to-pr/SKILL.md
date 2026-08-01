---
name: issue-to-pr
description: Take a Fabrin issue from open to merged — branch, failing test, implementation, docs, gates, PR. Use when starting work on any issue, or when a change has grown past a typo and needs the full flow.
---

# Issue → PR

The repository's working agreement, as a sequence. Read `AGENTS.md` and
`CONTRIBUTING.md` for the reasoning; this is the order of operations.

## 0. There is an issue

Open one first if there is not — except for trivial typo and docs fixes. Title
`[FEATURE]` or `[BUG]`, apply the type label plus an area label. Large work is an
epic with child issues.

`gh issue view <n>` — the acceptance criteria in the body are the definition of
done, not your reading of the title.

## 1. Branch

```bash
git checkout main && git pull
git checkout -b <type>/<short-slug>
```

Trunk-based: short-lived branches off `main`, merged frequently. Incomplete work
hides behind a flag, not a long-running branch.

**Check `git branch --show-current` before your first commit.** Committing to
local `main` is recoverable — `git branch <name>`, `git checkout <name>`,
`git branch -f main origin/main` — but only if you notice before pushing.

## 2. Red

Write the failing test. Run it. Keep the output; the PR body wants it.

A test that has never failed proves only that it compiles. See the `test-first`
agent.

If the change adds or changes a **gate**, this step is different: inject a
violation, watch the gate fail, revert, then run the negative control. One
injection per mechanism — `apicheck`'s leak check needed four before it caught a
type nested inside `map[string][]bson.M`. See the `boundary-auditor` agent.

## 3. Green

Implement. Aim under **~400 LOC of meaningful change** — if it is heading past
that, the issue probably wanted splitting, and splitting it now is cheaper than
reviewing it later.

Two things to weigh while writing, in this order:

- **Security.** A default here is inherited by every application built on
  Fabrin, and almost nobody revisits a framework default. Where the underlying
  library's default is unsafe, differ from it and say why at the call site.
- **Performance.** Allocations per request in the hot path. Measured, never
  asserted.

When they conflict, security wins, and the trade gets named in the commit body.

## 4. Docs — the last step, not an afterthought

`AGENTS.md` makes this the last step of *every* change. `just docs-check` counts
files; it cannot read them, so run the `docs-syncer` agent — and **do not tell it
what you already checked.**

Working the list inline is the tempting shortcut, and it has now been measured.
Across #52, #53 and #54 it missed eight items — including a claim that the
compiler catches an import cycle it does not catch, written into `.golangci.yml`
one PR *after* that same false claim had been proven wrong by injection and
corrected in five files. An independent sweep, given no list of what to look for,
found all eight, with zero overlap against what the author had already found.

The value is not that the agent reads docs better. It is that it has **no memory
of writing them.** Pasting your own findings into the prompt destroys the one
property you invoked it for.

The list below is what to check on the occasions you genuinely cannot run it:

- `CHANGELOG.md` — the entry, and its `[#N]:` link reference actually defined at
  the bottom of the file.
- `specs/system-behavior.yaml` + `specs/test-matrix.md` — both directions.
- `docs/TODO.md`, `docs/requirements/FABRIN_REQUIREMENTS.md` — checkboxes and
  statuses.
- `docs/DJANGO_PARITY.md`, `ARCHITECTURE.md`, `README.md` — including whether
  their code examples still compile.

Governed-surface updates are mandatory. Other drift you notice gets fixed in the
same change.

## 5. `just check`

Exactly what CI runs — the workflow calls `just ci` rather than re-listing the
steps, so a green `check` means a green CI. There is no second command.

If it is red, read *all* the output: gates report every failure rather than the
first, so you can fix them in one pass instead of one round trip per gate.

## 6. Commit

Conventional Commits. Scopes: `core`, `router`, `config`, `orm`, `migrate`,
`auth`, `admin`, `cli`, `modules`, `transport`, `render`, `tasks`, `docs`,
`harness`, `examples`.

The body carries what the diff cannot: why the surface moved, which security
property a trade weakens, what you injected to prove a gate bites. A subject line
restating the diff and an empty body is a commit nobody can review in a year.

Never `--no-verify`. The pre-commit hook is the only thing between a
governed-surface change and undocumented drift.

## 7. PR

One small PR per issue, linked, **squash merge**. `Closes #<n>`.

The body is where evidence lives that has no other home — gate transcripts in
particular, because `specs/test-matrix.md` sends future readers to "the PR that
landed it" for coverage that is not a Go test.

Then `gh pr checks <n>`, and merge once green.

## Before you finish

Confirm the issue actually closed, `main` is up to date, and the working tree is
clean. An issue left open after its PR merged is a roadmap that lies.
