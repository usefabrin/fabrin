# Test matrix

Every behaviour in [`system-behavior.yaml`](system-behavior.yaml) has a row here,
and every row here has an entry there. `just specs` enforces both directions —
the matrix must not accumulate rows for behaviours that were renamed or dropped,
and a behaviour must not exist without a discoverable test.

A row's **Test** column is where a reader looks to find the coverage. `_planned_`
means the behaviour is specified and not yet built; the spec entry stays
`status: planned` until the test exists.

## Core

| ID | Behaviour | Test |
|----|-----------|------|
| CORE-001 | Duplicate module name fails at construction | `module_test.go::TestNew_RejectsDuplicateModuleNames` |
| CORE-002 | Optional interfaces discovered and reported | `module_test.go::TestApp_ReportsOptionalInterfacesEachModuleMatched` |
| CORE-003 | Graceful shutdown on cancel and on signal | `fabrin_test.go::TestApp_RunReturnsWhenContextCancelled` |
| CORE-004 | `Lifecycle.Stop` runs in reverse order | `module_test.go::TestApp_StopsLifecycleModulesInReverseRegistrationOrder` |

Also covered, without a spec entry of their own because each is a consequence of
the four above rather than an independent claim: an empty module name rejected, a
failed `Start` unwinding what it already started, `Run` refusing a second call, and
the Gin aliases proving identical in both directions.

## Modules and deployment shapes

| ID | Behaviour | Test |
|----|-----------|------|
| MOD-001 | `FABRIN_MODULES` mounts only named modules | `module_test.go::TestNew_MountsOnlySelectedModules` |
| MOD-002 | Unknown module name is a startup error | `module_test.go::TestNew_RejectsSelectionNamingUnregisteredModule` |
| MOD-003 | Cross-module dependency is a locally declared interface | `examples/hello/hello_test.go::TestModules_NeverImportEachOther` |

## Config

| ID | Behaviour | Test |
|----|-----------|------|
| CFG-001 | Layer precedence: defaults → file → env → flags | `config/config_test.go::TestLoad_EachLayerWinsOverThePreviousOne` |
| CFG-002 | Each value reports its source layer | `config/config_test.go::TestLoad_ReportsWhichLayerSetEachValue` |
| CFG-003 | Unparseable value fails at load, key named | `config/config_test.go::TestLoad_RejectsUnparseableValueNamingTheKey` |
| CFG-004 | `FABRIN_ADDR` sets listen address, default `:8080` | `config/config_test.go::TestLoad_DefaultsAddrToDocumentedValue` |
| CFG-005 | No sources is an error, not a silent defaults-only load | `config/config_test.go::TestLoad_RejectsAnEmptySourceList` |
| CFG-006 | `Standard()` keeps per-layer provenance | `config/config_test.go::TestStandard_KeepsPerLayerProvenance` |

## Health and logging

| ID | Behaviour | Test |
|----|-----------|------|
| HLT-001 | `/healthz` consults no dependencies | `health_logging_test.go::TestHealthz_StaysUpWhileAModuleCheckIsFailing` |
| HLT-002 | `/readyz` fails closed, names the failure | `health_logging_test.go::TestReadyz_FailsClosedAndNamesTheFailingModuleAndCheck` |
| HLT-003 | Readiness consults only *mounted* modules | `health_logging_test.go::TestReadyz_OnlyConsultsMountedModules` |
| LOG-001 | Request id on context and response header | `health_logging_test.go::TestRequestID_ReachesTheHandlerContext` |
| LOG-002 | Inbound `X-Request-ID` honoured across a hop | `logging/logging_test.go::TestRequestID_HonoursInboundIDSoTracesSurviveAHop` |
| LOG-003 | Hostile inbound id discarded, fresh one issued | `logging/logging_test.go::TestRequestID_RejectsHostileInboundValues` |
| LOG-004 | `FABRIN_LOG_FORMAT` / `FABRIN_LOG_LEVEL` reach the request logger | `health_logging_test.go::TestNew_BuildsTheLoggerFromLogLevel` |

The `health` and `logging` packages carry their own unit tests for the pieces
these wiring tests exercise end to end — check timeout and concurrency
(`health/health_test.go`), format and level selection (`logging/logging_test.go`).
The rows above deliberately name the *wiring* test, because a package that behaves
correctly while nothing mounts it is the failure mode these IDs exist to prevent.

## Public API discipline

| ID | Behaviour | Test |
|----|-----------|------|
| API-001 | Unblessed third-party type in exported signature fails | `tools/apicheck/apicheck_test.go::TestLeak_FindsUnblessedTypesInEveryPosition` |
| API-002 | Surface change without regenerated snapshot fails | `scripts/api.sh` (gate; see below) |
| API-003 | Aliases recorded unexpanded; Gin bump does not churn snapshot | `tools/apicheck/apicheck_test.go::TestDescribe_RecordsAliasesUnexpanded` |

API-002 is the one row here whose test is a script rather than a Go test, and
deliberately: the mechanism that fails is `diff -u` inside `scripts/api.sh`, so a
Go test wrapping it would be testing `diff`. A gate counts as coverage only once
it has been proven to fail on a violation *and* to pass its negative control —
the transcript for this one (`func Sneaky() string` added, snapshot untouched,
gate red; reverted, gate green) is recorded in the PR that landed it.

What `diff` cannot check is whether the snapshot it compares is *faithful*: a
symbol the renderer silently drops can be deleted from the API without api-check
saying a word. `apicheck_test.go::TestDescribe_RecordsEveryExportedKindAndNothingUnexported`
asserts the whole rendered output for a package holding one of every kind, which
is why it compares exactly rather than checking that particular lines appear.

`tools/` is a separate Go module, so `go test ./...` from the root does not reach
these tests. `just test`, `just cover`, and `just lint` each invoke it explicitly.

## The command surface

| ID | Behaviour | Test |
|----|-----------|------|
| CLI-001 | Unknown command is an error naming the closest match | `cli/cli_test.go::TestDispatch_RejectsUnknownCommandNamingTheClosestMatch` |
| CLI-002 | Each command's flags are its own; nothing registered globally | `cli/cli_test.go::TestDispatch_ParsesFlagsIntoTheCommandsOwnSet` |
| CLI-003 | `Dispatch` writes to no stream it does not own | `cli/cli_test.go::TestDispatch_WritesNothingToStderrOnAFlagParseError` |
| CLI-004 | Every route attributed to its module; framework routes marked | `execute_test.go::TestApp_RoutesAttributesEachRouteToItsModule` |
| CLI-005 | `Execute` with no arguments serves; a leading flag is a setting | `execute_test.go::TestApp_ExecuteWithNoArgumentsServes` |
| CLI-006 | Gin's debug output silenced unless `Debug`; `GIN_MODE` wins | `execute_test.go::TestNew_SilencesGinsDebugOutputUnlessDebugIsSet` |
| CLI-007 | `Commander` commands collected from *mounted* modules only | `commander_test.go::TestNew_CollectsCommandsFromMountedModulesOnly` |
| CLI-008 | A colliding command name fails at construction, naming both | `commander_test.go::TestNew_RejectsAModuleCommandThatShadowsABuiltIn` |
| CLI-009 | Flags parsed wherever they appear; `--` passes the rest through | `cli/cli_test.go::TestDispatch_ParsesFlagsWhereverTheyAppear` |
| CLI-010 | `fabrin new` writes a project that builds, tests, and boots | `internal/scaffold/scaffold_test.go::TestGenerate_WritesEveryFileTheProjectNeeds` |
| CLI-011 | `fabrin startapp` writes the module *and* wires it in | `internal/scaffold/module_test.go::TestModule_WiresItselfIntoNewApp` |
| CLI-012 | The edit keeps the user's formatting and stays gofmt-clean | `internal/scaffold/module_test.go::TestModule_LeavesMainGofmtClean` |
| CLI-013 | The scaffold's output builds, tests, boots — against this checkout | `scripts/check-scaffold.sh` (gate; see below) |

CLI-001…003 cite FR-CLI-4, because `Commander` is why `fabrin/cli` exists as a
package at all — a module contributing a subcommand is what forces the command
type to be Fabrin's own, and forces the package to stand alone from `App`.
CLI-004/005 cite FR-CLI-3; CLI-006 cites NFR-7; CLI-007/008/009 cite FR-CLI-4
again, this time as the thing itself rather than as the reason the package
exists; CLI-010 cites FR-CLI-1; CLI-011/012 cite FR-CLI-2; CLI-013 cites NFR-5.

CLI-013 is the second row in this file whose test is a script rather than a
Go test, and for the same reason as API-002: what fails is `go build` inside a
generated project, and a Go test wrapping that would be testing the toolchain.
Its four injected-violation transcripts are in the PR that landed it — a
template that does not compile, one that panics, one that compiles and passes
its tests but cannot start, and an import inserted out of sorted order.

CLI-012 compares against `format.Source` rather than asserting the imports are
sorted, because the two failures are not the same size. An unsorted import is
not a compile error — it is a file `gofmt -l` flags on the user's next commit,
blaming their edit rather than the tool's. The parse check alone stays green
through it, which is how the bug reached a manual run before a test caught it.

CLI-006 is here rather than under Config because the CLI is where it bites: Gin's
construction-time banner and route table land on **stdout**, four lines above the
answer `routes` was asked for. The same output has always been in a Fabrin app's
container log — the command is what made it impossible to ignore.

Also covered without a spec entry, each being a consequence of the six above
rather than an independent claim: help requests succeed rather than fail,
`Flags` being optional is not a nil dereference, a command with no `Run` is
rejected, duplicate names are rejected regardless of which command was asked for,
the context reaches the command so a blocking one can be cancelled, `Routes()` is
sorted stably, an unmounted module contributes no routes, and `version` reads
build info rather than a constant.

`TestNew_PanicsWhenTwoModulesClaimTheSamePath` has no spec entry on purpose: it
records what Gin does today rather than a behaviour Fabrin promises. Improving
that panic to name both modules is [#40](https://github.com/usefabrin/fabrin/issues/40).

## Models and metadata

| ID | Behaviour | Test |
|----|-----------|------|
| ORM-001 | Duplicate table rejected, the error naming both modules | `orm/orm_test.go::TestRegistry_RejectsTwoModelsClaimingOneTable` |
| ORM-002 | An unmigratable model is rejected at registration, not at DDL time | `orm/orm_test.go::TestRegistry_RejectsAModelWithNothingToMigrate` |
| ORM-003 | `Models()` ordered by table, not by registration | `orm/orm_test.go::TestRegistry_ModelsIsOrderedByTableRatherThanRegistration` |
| ORM-004 | Field order preserved exactly as declared | `orm/orm_test.go::TestRegistry_KeepsFieldOrderAsDeclared` |
| ORM-005 | `Models()` returns a deep copy | `orm/orm_test.go::TestRegistry_ModelsReturnsACopy` |
| ORM-006 | `orm` imports no `database/sql` and no sibling package | `.golangci.yml` — `orm-is-standalone` (gate; see below) |
| ORM-007 | A module declares tables via `Modeler`; nothing is scanned for | `modeler_test.go::TestApp_ModelsCollectsEachModulesTablesWithItsName` |
| ORM-008 | Models collected from **mounted** modules only | `modeler_test.go::TestNew_CollectsModelsFromMountedModulesOnly` |
| ORM-009 | Two modules claiming one table fail at construction | `modeler_test.go::TestNew_RejectsTwoModulesDeclaringOneTable` |

ORM-001…006 cite FR-ORM-1; ORM-007…009 cite FR-ORM-3.

ORM-003 and ORM-004 look contradictory and are not: table order is **sorted**
because registration order carries `FABRIN_MODULES` and the argument order in
`main` into the output, and field order is **preserved** because the declaration
is the author's intent about column layout. Both exist to make the generator's
output a function of the schema alone — a generator that emits a spurious diff on
a project nobody changed is one nobody trusts.

ORM-002 is one row over a table-driven test plus three siblings —
`TestRegistry_RejectsAModelWithNoPrimaryKey`,
`TestRegistry_RejectsMaxLenOnSomethingThatIsNotAString`, and
`TestField_TypesAreFabrinsOwn`. They are the same claim (a model the generator
could not act on never enters the registry) rather than four, and splitting them
would suggest a caller has four cases to handle when it has one.

ORM-006 is the third row whose test is not a Go test, for the reason API-002 and
CLI-013 give: the thing that must fail is a **compile that succeeds**. A sibling
import — `fabrin/health`, say — builds cleanly and is not an import cycle, so no
Go test can distinguish it from correct code; only the import graph can, and
depguard is what reads it.

All three denies were injected and read before this landed: the sibling import,
the root import, and `database/sql`. The last is the load-bearing one, because
nothing else stops this package opening a connection, and the moment it can, the
admin needs a database running to render a form.

The root-import deny is worth a note, because what it does changed **between two
merged commits**. When `orm` landed in
[#52](https://github.com/usefabrin/fabrin/issues/52) nothing imported it, so
`orm` → root compiled cleanly — verified, not assumed — and this rule was the only
thing rejecting it. `Modeler` in [#53](https://github.com/usefabrin/fabrin/issues/53)
made the root package import `orm`, and the same injection now fails with
`import cycle not allowed`. A leaf that ships before its consumer is unguarded by
the compiler for exactly that window, which is the argument for writing the rule
when the package lands rather than when the consumer does.

ORM-009 looks like a duplicate of ORM-001 and tests something else. ORM-001 proves
`orm.Registry` rejects the conflict; ORM-009 proves the root package **propagates**
it — that `New` fails rather than logging, that both module names survive the wrap,
and that `errors.Is(err, orm.ErrDuplicateTable)` still matches through it. A
`fmt.Errorf` with `%v` instead of `%w` passes ORM-001 and breaks ORM-009.

ORM-008 is the row that would pass without being tested, if written carelessly.
`collectModels` iterates `reg.modules`, which is *already* the mounted set, so a
test that registers two modules with no selection goes green whether or not the
rule is honoured. The test sets `Options.Modules` to a subset, and was checked by
mutation: pointing `collectModels` at `New`'s raw arguments turns it red with
`Models() = [invoices orders]`.

ORM-007 claims two things and its row names one test. The collection half — a
module hands its tables over, and nothing is scanned for — is the test in the
table; the reporting half, that `Modeler` shows up in `App.Capabilities()` like
`Checker`, `Lifecycle`, and `Commander`, is
`modeler_test.go::TestApp_ReportsModelerAmongAModulesCapabilities`. That test
asserts the negative too, because a mistyped `Models()` otherwise fails silently:
the module simply never contributes, and nothing says so.

Also covered without a spec entry, each a consequence of the three above: an app
whose modules declare no models has an empty schema rather than an error, and
`App.Models()` returns a deep copy — the root-package half of ORM-005, since
handing out `*orm.Registry` would hand out `Register` with it.

## Migrations

| ID | Behaviour | Test |
|----|-----------|------|
| MIG-001 | Body and applied-state row commit in **one** transaction | `migrate/migrate_test.go::TestRun_LeavesNothingBehindWhenAMigrationFails` |
| MIG-002 | A migration ordered before an applied one is an error | `migrate/migrate_test.go::TestRun_RejectsAMigrationOrderedBeforeOneAlreadyApplied` |
| MIG-003 | Version order, not slice order; applying is idempotent | `migrate/migrate_test.go::TestRun_AppliesPendingMigrationsInVersionOrder` |
| MIG-004 | An unusable set is rejected before anything runs | `migrate/migrate_test.go::TestRun_RejectsAnUnusableMigrationSet` |
| MIG-005 | Rollback runs `Down` newest-first, to an exclusive target | `migrate/migrate_test.go::TestRollback_UndoesInReverseOrder` |
| MIG-006 | The engine imports no driver, no Gin, no `net/http` | `.golangci.yml` — `migrate-is-standalone` (gate; see below) |
| MIG-007 | Two migrations claiming one version are rejected | `migrate/migrate_test.go::TestRun_RejectsAnUnusableMigrationSet` |

MIG-001…006 cite FR-ORM-4; **MIG-007 cites FR-ORM-5**, and the split matters
because the two are easy to conflate. FR-ORM-5 is *two migrations claiming one
version*; MIG-002 is *one version arriving late*, which is an ordering property of
the engine. They fail differently, are caught by different checks, and a reader
tracing FR-ORM-5 to MIG-002 would find the wrong test.

MIG-004 and MIG-007 name the same table-driven test on purpose: it has four
subtests and they answer to two requirements. `duplicate_version` is FR-ORM-5's;
the other three — no version, no `Up`, no `Down` — are FR-ORM-4's.

MIG-001 is the row the whole package is built around, and the one a plausible
implementation gets wrong. Writing the applied-state row *outside* the
transaction leaves a half-applied migration marked as finished, which every later
run then skips. Mutation-checked: moving that `INSERT` out of the transaction
turns this test red.

The test asserts **both** halves, because each can pass alone. After a migration
whose `Up` issues DDL and then fails: the table it created must be gone, *and*
its version must be absent from `fabrin_migrations` — while the migration before
it stays committed, since it already succeeded.

MIG-006 is the fourth row whose test is a gate rather than a Go test, and its
negative control is the interesting half. `migrate_test.go` imports
`modernc.org/sqlite` while the rule denies it; the gate is green because of the
`!**/*_test.go` exclusion. So `just arch` passing *with that import present* is
the proof the exclusion works, not an absence of evidence. All four denies were
injected and read: a sibling package, Gin, `net/http`, and the driver — each
compiled cleanly first, which is what leaves depguard the only thing that could
catch them.

What that rule cannot do is stated where it lives: depguard matches prefixes, so
"no driver" is inexpressible against dozens of drivers. It names the one in
`go.mod`, which is the one a slip could actually reach for.

MIG-003 and MIG-005 each state a claim wider than the single test their row
names, and the rest of it is in a sibling rather than missing. MIG-003's
idempotence half — an already-applied migration is skipped, not re-run — is
`TestRun_IsIdempotent`; the ordering test says nothing about it. MIG-005's
**exclusive** target is `TestRollback_StopsAtTheTargetVersion`, and its refusal to
skip a recorded version it holds no `Down` for is
`TestRollback_ReportsAnAppliedVersionItCannotUndo`. The rows name the test that
would fail first, but a reader tracing either claim needs all three.

Also covered without a spec entry: a failing `Down` leaves its version recorded
rather than deleted, and the applied-state table records name and time alongside
the version.
