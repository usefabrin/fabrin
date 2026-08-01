# Contributing to Fabrin

AI agent instructions (Claude Code, Codex, Cursor): [AGENTS.md](AGENTS.md).
`CLAUDE.md` only imports that file — edit `AGENTS.md`, not `CLAUDE.md`.
Engineering / style standards: [docs/coding-guidelines.md](docs/coding-guidelines.md).

## Principles

- **Test-Driven Development.** Write the failing test first; make it pass;
  refactor. A behavioural claim in a doc with no test behind it is a wish.
- **Fabrin is a library.** Every exported symbol is a promise to strangers.
  Adding one is cheap, removing one breaks their builds. Ship less; add later.
- **Trunk-based development.** One long-lived branch: `main`. Work on
  short-lived branches, merge frequently, and hide incomplete work behind
  feature flags rather than long-running branches.

## Local setup

```bash
just setup     # deps, pinned tools, git hooks
just check     # the full local gate
```

You need Go (version in `go.mod`) and [just](https://github.com/casey/just).
`just tools` installs the pinned linters; the justfile records the versions CI
uses, so local and CI run the same ruleset.

## The validation gate

`just check` runs, in order:

| Gate | Command | Enforces |
|------|---------|----------|
| Hygiene | `just gates` (`scripts/gates/*.sh`) | Repo invariants no compiler checks |
| Style | `just lint` (gofmt, `go vet`, golangci-lint) | Formatting and vet checks |
| Tests | `just test` | Behaviour |
| Boundaries | `just arch` | depguard layering rules |
| API surface | `just api-check` | The public API changed only on purpose |
| Scaffold | `just examples` | The generator emits a project that builds, tests, and **boots** — built against the working tree, not the published module |
| Examples | `just examples` | Every example still builds and serves |
| Specs | `just specs` | Every behaviour has a matrix row and a test |

**`just check` is exactly what CI runs** — the workflow calls `just ci`, an alias
for `check`, rather than re-listing the commands. A green `check` means a green
CI. Two things run outside it, because both need a git range a bare recipe cannot
supply:

- **docs-freshness** (`just docs-check`) — the pre-commit hook runs it on staged
  files; CI runs it as a separate `docs-guard` job.
- **benchmarks** (`just bench`) — CI runs these on `main` only.

`just gates` must stay fast enough for the pre-commit hook — keep the whole
target under a couple of seconds, and require no Docker, network, or build. Add
new hygiene checks as a script in `scripts/gates/`, list it in
`scripts/gates/run-all.sh`, and **give it a header comment saying which failure it
prevents.** A check whose purpose nobody remembers is the first one deleted.

### The gate scripts

| Script | Prevents |
|--------|----------|
| `scripts/gates/check-depguard-coverage.sh` | A new public package landing with nobody having decided whether it needs boundary rules |
| `scripts/install-hooks.sh` | A pre-commit hook that reports installed and enforces nothing |
| `scripts/gates/check-agent-docs.sh` | Rules accumulating in `CLAUDE.md`, giving Fabrin two working agreements that disagree |
| `scripts/gates/check-examples.sh` | An example quietly ceasing to be a runnable program |
| `scripts/gates/run-all.sh` | A gate script that exists but is not listed, and so never runs |
| `scripts/check-gofmt.sh` | `gofmt -l` printing filenames and exiting 0, which it does by design |
| `scripts/check-docs-freshness.sh` | Documentation drifting behind the code it describes |
| `scripts/specs.sh` | A documented behaviour with nothing executable behind it |
| `scripts/smoke-examples.sh` | An example that compiles but cannot start |
| `scripts/api.sh` | A breaking change to the exported surface landing unnoticed |

`run-all.sh` fails on a gate script that is present but unlisted, because a gate
nobody invokes looks identical to a gate that passes.

The pre-commit hook runs only the fast, local subset — `gofmt`, `gates`, and
docs-freshness on the staged change. Tests, lint, arch, and the API snapshot are
`just check`'s job before you push. A hook slow enough to be annoying is a hook
bypassed with `--no-verify`, and a bypassed hook enforces nothing.

### Recipes skip, they do not fail

`check`'s recipe list is written once and never grows. A recipe whose target does
not exist yet prints a skip notice and exits 0. This is what lets every PR leave
`just check` green without editing `check` in four separate PRs.

### Prove a gate bites

When you add or change a gate or a depguard rule, inject a throwaway violation,
confirm the gate **fails**, then revert. A rule that matches nothing is
indistinguishable from a rule that passes. Prefix-matched deny lists in
particular fail open when a new package lands — that is why
`scripts/gates/check-depguard-coverage.sh` exists.

**Pair every violation with a negative control.** A gate that fails is only half
the evidence; you also need to know it fails for the *right reason*. `net/http`
imported from `config/` must fail **and** the same import in `health/` must pass —
otherwise a rule that rejects everything looks exactly like a correct one.

This is not hypothetical. The coverage gate shipped in #13 asked "is this package
mentioned in `.golangci.yml`?" with a substring grep, and `orm` matches
`formatters:`. It was verified with a name genuinely absent from the file, so the
test passed and the fail-open hid behind it (#14). The structured
`# boundary:` marker replaced it, and the regression test is the case the original
suite could not have caught: a package in the manifest with nothing recorded.

## Boundary rules (enforced by depguard, `.golangci.yml`)

- `fabrin/config` must not import Gin or `net/http`. Settings must load from the
  CLI, from tests, and from a migrate-only process without booting a server.
- `fabrin/config`, `fabrin/logging`, `fabrin/health`, `fabrin/cli`, and
  `fabrin/orm` must not import the root package **or each other**. The root
  package imports all five, so that direction is a cycle the compiler rejects and
  the rule is belt-and-braces there. It exists for the *sibling* import, which
  compiles cleanly and quietly makes a leaf depend on half the framework.

  Write the rule when the package lands, not when its consumer does: `orm`
  shipped one PR before anything imported it, and in that window `orm` → root
  compiled fine and this rule was the only thing rejecting it.
- `fabrin/orm` must not import `database/sql` either. It describes models and
  opens nothing: that is what lets the admin and forms read a schema with no
  database running, and what keeps its tests in microseconds. The query API is
  `database/sql` or the user's own ORM, named in neither place — see
  [ADR 0002](docs/adr/0002-database-sql-is-the-orm-seam.md).
- `fabrin/migrate` must not import Gin, `net/http`, or a database driver.
  Migrations run from a process that mounts no routes. The engine takes a
  `*sql.DB` and the application supplies the driver; the SQLite driver in
  `go.mod` exists for this package's own tests and the `!**/*_test.go` exclusion
  is what keeps it out of the shipped engine.
- `internal/**` must not import the root package.

`fabrin/cli` is a leaf for a concrete reason: the root package imports it to
declare `Commander` (`Commands() []cli.Command`), and a CLI that cannot print its
own help without first constructing an `App` is a CLI nobody can test —
`fabrin new` runs in a directory where no `App` exists at all.

**Gin containment is not a depguard rule.** `health`'s handlers and `logging`'s
middleware are `gin.HandlerFunc` by definition, so restricting which packages may
*import* Gin would make them unwritable. The real invariant is about the
**exported surface**, and `just api-check` enforces it: Gin may appear in
exported signatures because `apicheck`'s allowlist says so; nothing else may.
That allowlist is a `map` in `tools/apicheck/main.go` with one entry, and a test
asserts it has exactly one — so a second entry fails the build until the ADR that
hard rule 1 requires actually happens.

## When you change a governed surface

If your change touches the **public API**, a **CLI command**, or anything a
`*_CONTRACT.md` governs, you must also:

1. Regenerate the API snapshot (`just api`) in the **same commit**, and say why
   the surface moved in the commit body.
2. Update the relevant `docs/`, and if a load-bearing behaviour changed, update
   `specs/system-behavior.yaml` **and** `specs/test-matrix.md`, then run
   `just specs`.

The `just docs-check` gate (pre-commit hook + CI) fails commits that change a
governed surface without a docs/specs update. Treat updating docs as the **last
step** of every change, after implementation and tests.

## Adding a module or a package

1. Add requirements (with IDs) to `docs/requirements/FABRIN_REQUIREMENTS.md`.
2. Add the behaviour to `specs/system-behavior.yaml` + `specs/test-matrix.md`
   with `status: planned`.
3. Write the failing test.
4. Implement.
5. If it is a new **public** package, add it to
   `scripts/gates/public-packages.txt` **and** add a
   `# boundary: <name> — <decision>` line to the inventory in `.golangci.yml`.
   "No rules needed" is a valid decision; it just has to be written down, so that
   *considered* and *forgotten* stay distinguishable. `just gates` names the
   missing entry, because the hand-enumeration would otherwise fail open.
6. If it is a new **module**, remember hard rule 3: declare the interfaces you
   need in your own package. Never import another module.
7. Flip the spec entry to `status: implemented`; run `just ci`.
8. Update `docs/DJANGO_PARITY.md` if this moves a parity row.

## Consequential decisions

Choices that are expensive to reverse — blessing another dependency in the public
API, adding a v0 non-goal, changing the data layer — go in
[`docs/adr/`](docs/adr/README.md) as a dated record of the decision and its
alternatives. Do not open an ADR for a routine change; do open one before
anything a future contributor would otherwise reasonably undo.

The **alternatives** section is what makes the file worth keeping. `CHANGELOG.md`
already records what changed and why; only the ADR records what was rejected, and
that is the part a future contributor needs before undoing something.
[ADR 0001](docs/adr/0001-gin-as-a-type-alias.md) is the worked example.

## Issues first

Open a GitHub issue **before** writing code (except trivial typos/docs). Use the
**Feature**, **Bug**, or **Epic** forms under New Issue — they set the title
prefix and type label; then apply the matching area label.

- **Title prefix:** `[FEATURE]` for new capability, `[BUG]` for defects.
- **Labels:** type (`enhancement` / `bug` / `epic`) plus an area label (`core`,
  `router`, `config`, `orm`, `auth`, `admin`, `cli`, `harness`, `docs`, …).
- **Epics:** large or multi-slice work is an epic issue tracking child issues.
  Do not implement an epic as one PR.
- **One small PR per issue:** each issue gets its own focused PR that links it.
  Aim under ~400 LOC of meaningful change; split further if review would suffer.

## Commits

Conventional Commits. Scopes: `core`, `router`, `config`, `orm`, `migrate`,
`auth`, `admin`, `cli`, `modules`, `transport`, `render`, `tasks`, `docs`,
`harness`, `examples`.

```
type(scope): short imperative summary

What changed and why. Note public-API, behavioural, or operational impact.
Mention test/spec coverage when relevant.
```

Let pre-commit hooks run; do not bypass them with `--no-verify` without a
documented reason.

## Pull requests

Keep PRs small and focused — **one issue per PR**, and link the issue.
Self-review the diff before merging. **Squash merge**; the PR title becomes the
squash commit, so it must follow Conventional Commits.

**The author may merge their own PR once CI is green.** Review is not a gate —
the green checks are. This is a small repo with a heavy agent-assisted workflow,
and a mandatory-review rule would only ever be satisfied by the same person who
wrote the change. Request review when a change is genuinely risky or you want a
second opinion, and resolve review conversations before merging.

What *is* a gate: `just check` locally, green CI, and a governed-surface change
carrying its `docs/`/`specs/` update. Do not merge red.

## Pull request checklist

- [ ] Linked issue opened first with `[FEATURE]` or `[BUG]` (+ labels).
- [ ] Followed [docs/coding-guidelines.md](docs/coding-guidelines.md).
- [ ] `just check` passes locally.
- [ ] New or changed behaviour is test-covered (TDD).
- [ ] Public-API change regenerated `api/fabrin.txt` in the same commit, and the
      commit body says why the surface moved.
- [ ] No new third-party type in an exported signature (Gin is the only
      allowlisted entry — a second one needs an ADR).
- [ ] No module imports another module.
- [ ] New or changed gate proven to fail on an injected violation.
- [ ] Governed-surface changes updated `docs/`/`specs/` (`just docs-check` green).
- [ ] `docs/TODO.md` and `docs/DJANGO_PARITY.md` reflect progress.
