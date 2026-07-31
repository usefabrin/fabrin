# Architecture Decision Records

A decision that is expensive to reverse gets a file here. Everything else gets a
comment at its call site and a line in `CHANGELOG.md`.

`AGENTS.md` routes three kinds of decision to this directory. Those are the
current list, not a closed one:

1. **A second entry in `apicheck`'s allowlist.** Hard rule 1: adding one is "an
   architectural decision that needs an ADR, not a line edit". Whatever is added
   becomes something Fabrin cannot change without a major version, forever, and
   the cost is invisible at every call site that benefits from it.
   `TestAllowlist_HoldsOnlyGin` fails on a silent addition so this conversation
   actually happens.
2. **Any of the v0 microservice non-goals** — service discovery, a service mesh,
   an RPC framework, a remote-client generator. "We're a microservice framework
   too" is how batteries-included frameworks become un-learnable.
3. **Consequential decisions** generally: a choice with live alternatives, where
   someone six months from now would otherwise re-derive the trade and possibly
   reverse it by accident.

## What an ADR is not

**Not a changelog.** `CHANGELOG.md` records what changed and why, in the commit
that changed it. An ADR records what was *decided* and what was **rejected** —
the alternatives are the point, and they appear nowhere else.

**Not documentation.** `ARCHITECTURE.md` describes the system as it is. An ADR
describes a fork in the road and which branch was taken. If a reader needs the
ADR to understand how the code works today, the architecture doc has a gap.

**Not a retrospective ritual.** Do not backfill an ADR for every past decision.
Most of Fabrin's are adequately captured by a call-site comment plus a changelog
entry, and a directory of ADRs nobody needed teaches people to skip the ones that
matter.

## Format

One file, `NNNN-kebab-case-title.md`, numbered sequentially from `0001`. Numbers
are never reused, including for withdrawn ADRs.

```markdown
# NNNN. Title stating the decision, not the topic

- **Status:** Proposed | Accepted | Superseded by [NNNN](NNNN-….md)
- **Date:** YYYY-MM-DD
- **Deciders:** who
- **Requirement / issue:** INV-N, FR-…, #N

## Context
The forces. What is true that makes this a decision rather than an obvious call.

## Decision
What was chosen, stated as a commitment.

## Consequences
What this buys, what it costs, and what it forecloses. The cost section is
mandatory; a decision with no stated cost is one nobody has finished thinking
about.

## Alternatives considered
Each with why it lost. This is the section that makes the file worth keeping.
```

**Titles state the decision.** `0001-gin-as-a-type-alias.md`, not
`0001-router-choice.md` — a reader scanning filenames should learn what was
decided without opening anything.

## Amending

An accepted ADR is not edited to change its substance. Correcting a typo or a
broken link is fine; changing what was decided is not. Write a new ADR that
supersedes it, and set the old one's status to `Superseded by [NNNN]`.

The reason is the same one behind `api/fabrin.txt`: the value is in the record
being trustworthy. An ADR quietly rewritten to match what the code does now
cannot tell you the code once did something else — which is exactly the question
someone opens it to answer.

## Index

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-gin-as-a-type-alias.md) | Gin is blessed, and `fabrin.Context` is a type alias rather than a wrapper | Accepted |
