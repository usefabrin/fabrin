# Issue to pull request

1. Confirm a scoped issue and acceptance criteria exist.
2. Create a short-lived branch and isolated worktree; record its base SHA.
3. Produce the expected-red test or injected gate violation.
4. Implement the smallest change that makes the targeted acceptance pass.
5. Run an independent review appropriate to risk.
6. Update docs and specs last.
7. Run targeted checks, then exclusive `just check` and `just race` as Lead.
8. Commit intentionally, open one PR for the issue, and store evidence there.

Workers do not perform steps that mutate branch, index, remote, issue, or PR
state; those remain Lead operations under the orchestration contract.
