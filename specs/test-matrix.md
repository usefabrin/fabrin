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
| MOD-004 | Factory selection happens before construction | `module_test.go::TestNewFromFactories_DoesNotBuildUnselectedModules` |
| MOD-005 | A greet-only example never opens the orders database | `examples/hello/hello_test.go::TestSlicing_DoesNotOpenAnUnselectedModulesResources` |

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
| HLT-004 | Deadline bounds non-cooperative checks; one invocation outstanding | `health/health_test.go::TestReadiness_TimesOutRatherThanHanging` |
| LOG-001 | Request id on context and response header | `health_logging_test.go::TestRequestID_ReachesTheHandlerContext` |
| LOG-002 | Inbound `X-Request-ID` honoured across a hop | `logging/logging_test.go::TestRequestID_HonoursInboundIDSoTracesSurviveAHop` |
| LOG-003 | Hostile inbound id discarded, fresh one issued | `logging/logging_test.go::TestRequestID_RejectsHostileInboundValues` |
| LOG-004 | `FABRIN_LOG_FORMAT` / `FABRIN_LOG_LEVEL` reach the request logger | `health_logging_test.go::TestNew_BuildsTheLoggerFromLogLevel` |
| LOG-005 | Recovered panic is error-logged with actual wire status; uncommitted response becomes 500 | `health_logging_test.go::TestRecoveredPanic_IsRequestLoggedWithFailureStatus` |

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
| CLI-014 | Leading help exits cleanly; invalid leading flags do not panic | `scripts/check-scaffold.sh` (gate; see below) |

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
| ORM-010 | A module names no database handle — read off the import graph | `examples/hello/hello_test.go::TestOrders_ImportsNoDatabaseHandleNorAnythingOutsideFabrin` |
| ORM-011 | One `Store` port, two implementations — in-memory in tests, the real one in `main` | `examples/hello/orders/orders_test.go::TestModule_ReachesItsDataOnlyThroughTheStoreItWasGiven` |
| ORM-012 | A primary key marked `Nullable` is rejected at registration | `orm/orm_test.go::TestRegistry_RejectsAModelWithNothingToMigrate` |
| ORM-013 | Redundant `Unique`/`Index` flags, including on a primary key, are rejected | `orm/orm_test.go::TestRegistry_RejectsAModelWithNothingToMigrate` |

ORM-001…006 cite FR-ORM-1; ORM-007…009 cite FR-ORM-3; ORM-010…011 cite FR-ORM-2,
which [ADR 0002](../docs/adr/0002-database-sql-is-the-orm-seam.md) reads as *a
documented adapter pattern and a worked example* rather than an exported Fabrin
type. The example is `examples/hello/orders`
([#60](https://github.com/usefabrin/fabrin/issues/60)).

ORM-010 is the row a behavioural test cannot hold up, for the reason ORM-006 and
MIG-006 give — except that here the reader is a Go test rather than depguard,
because the claim is about one example directory rather than a package boundary
the linter can name. Two things about it are worth keeping when it is edited. It
allowlists (standard library plus Fabrin) instead of denying ORMs by prefix: a
deny list fails open against the one nobody wrote a rule for, and "no ORM" is a
claim about all of them. And its floor counts **non-test** `.go` files, because
the module's own `_test.go` imports Fabrin — a floor that asked only whether any
Go was read would be satisfied by the test file alone, and the test would then
pass with no module on disk at all.

Both denies were injected and read before the tests landed: a fixture importing
`database/sql` and `modernc.org/sqlite` turned it red on both branches, a fixture
importing only Fabrin and the standard library turned it green, and removing the
fixture returned it to the vacuity floor.

ORM-011 claims two halves and its row names one test, for the reason MIG-010
gives: a spec entry may name only one. The in-memory half — the module answering
real requests with a map behind it, and the store recording exactly one write —
is the test in the table. The half that says the *same module* answers the same
requests against the store `main` wired in is
`examples/hello/hello_test.go::TestOrders_RoundTripsAnOrderThroughTheStoreMainWiredIn`,
which drives the example's real `newApp` and reads the order back in a second
request, because storage that outlives a request is what an echo cannot fake.
Neither test alone is the claim: one implementation is a wrapper wearing a
disguise, and the point is that the module is unchanged between the two.

`examples/hello/hello_test.go::TestOrders_DeclaresTheTableItOwnsSoAGeneratorHasSomethingToDiff`
has no spec entry of its own, being a consequence of ORM-007 — `Modeler` applied
to a real module — rather than an independent claim. It deliberately asserts a
model attributed to `orders` and nothing about the columns: pinning the schema
there would make every later column an edit to that test rather than to the
module.

ORM-003 and ORM-004 look contradictory and are not: table order is **sorted**
because registration order carries `FABRIN_MODULES` and the argument order in
`main` into the output, and field order is **preserved** because the declaration
is the author's intent about column layout. Both exist to make the generator's
output a function of the schema alone — a generator that emits a spurious diff on
a project nobody changed is one nobody trusts.

ORM-012 and ORM-013 are [ADR 0006](../docs/adr/0006-field-constraint-semantics.md)
landing: the three provisional flags have decided semantics (NOT NULL default,
named UNIQUE constraint, plain auto-named index), which made contradictory and
redundant combinations expressible — and therefore invalid. Generated database
object names carry a readable prefix plus a bounded digest, avoiding both
underscore ambiguity and PostgreSQL's silent identifier truncation. The
validation rows ride ORM-002's table-driven test, the same pattern as
MIG-004/MIG-007 sharing one table across two requirements.

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
| MIG-008 | A pre-merge gate rejects duplicate migration files; an empty set passes portably | `scripts/gates/check-migration-versions.sh` (gate) |
| MIG-009 | Versions that do not sort as written are rejected | `migrate/migrate_test.go::TestRun_RejectsVersionsThatDoNotSortAsWritten` |
| MIG-010 | `Up`/`Down` take a `Handle` — four frozen methods, satisfied unmodified by `*sql.Tx`, `*sql.DB`, `*sql.Conn` | `migrate/handle_test.go::TestHandle_MethodSetIsFrozenAtFourAndSatisfiedUnmodifiedByTxDBAndConn` |
| MIG-011 | Recorded state round-trips — tables, modules, declared field order intact | `orm/state_test.go::TestSnapshot_RoundTripsThroughEncodeAndParse` |
| MIG-012 | Encoding one schema twice produces identical bytes | `orm/state_test.go::TestSnapshot_EncodeIsDeterministic` |
| MIG-013 | Versioned state preserves ADR 0006's constraint flags | `orm/state_test.go::TestSnapshot_EncodesConstraintFlagsInVersionedState` |
| MIG-014 | Unreadable state is an error naming its source | `orm/state_test.go::TestParseSnapshot_ErrorsNameTheirSource` |
| MIG-015 | Unknown keys in recorded state are rejected, not dropped | `orm/state_test.go::TestParseSnapshot_RejectsKeysItDoesNotKnow` |
| MIG-016 | Parsed state is revalidated through registration's rules | `orm/state_test.go::TestParseSnapshot_RevalidatesWhatItReads` |
| MIG-017 | Replay carries hand-written steps forward, starts empty, reconstructs identically | `orm/state_test.go::TestReplayState_IsDeterministicAndCarriesHandWrittenStepsForward` |
| MIG-018 | A replay chain with repeating, descending, or empty versions is rejected | `orm/state_test.go::TestReplayState_RejectsBrokenSequences` |
| MIG-019 | The differ detects new/dropped tables, added/dropped fields, type and length changes | `migratediff/migratediff_test.go::TestDiff_DetectsEachShapeOfChange` |
| MIG-020 | Diffing a state against itself emits nothing | `migratediff/migratediff_test.go::TestDiff_EmitsNothingWhenStatesAgree` |
| MIG-021 | Deterministic op order — creates, alterations, drops last | `migratediff/migratediff_test.go::TestDiff_IsDeterministicAndOrdersItsOps` |
| MIG-022 | A pure field reorder produces no operations | `migratediff/migratediff_test.go::TestDiff_TreatsAReorderedFieldListAsNoChange` |
| MIG-023 | SQLite DDL executes against a real database and accepts rows | `migratediff/migratediff_test.go::TestSQLite_CreatesTablesThatAcceptRows` |
| MIG-024 | SQLite refuses drop/retype with a stated error before anything runs | `migratediff/migratediff_test.go::TestSQLite_RefusesColumnDropAndRetypeWithStatedErrors` |
| MIG-025 | PostgreSQL verified live when `FABRIN_TEST_PG_DSN` is set; skipped **with a notice** otherwise | `migratediff/migratediff_test.go::TestPostgres_RendersDDLThatALiveServerAccepts` |
| MIG-026 | A dropped column's SQL carries its data-loss warning naming the column | `migratediff/migratediff_test.go::TestDataLossIsStatedInTheEmittedSQL` |
| MIG-027 | `Migrator` declares a module's migrations; mounted modules only; reported in `Capabilities` | `migrator_test.go::TestApp_ReportsMigratorCapability` |
| MIG-028 | Two modules claiming one version fail at construction, naming both | `migrator_test.go::TestNew_RejectsTwoModulesClaimingOneMigrationVersion` |
| MIG-029 | Mixed version widths across modules fail at construction | `migrator_test.go::TestNew_RejectsMigrationsOfMixedWidthsAcrossModules` |
| MIG-030 | `migrate` applies pending, prints what ran, says "up to date" on nothing | `migrator_test.go::TestExecute_MigrateAppliesPendingMigrationsAndSaysSo` |
| MIG-031 | `-to` moves forward or rolls back to an exclusive target, direction stated first | `migrator_test.go::TestExecute_MigrateToRollsBackToAnExclusiveTarget` |
| MIG-032 | Migration commands refuse on a sliced process, naming registered vs mounted | `migrator_test.go::TestExecute_MigrateRefusesWhenTheProcessIsSliced` |
| MIG-033 | `makemigrations` writes compiling, gofmt-clean per-module Go + state + manifest + generated `all.go` | `makemigrations_test.go::TestExecute_MakemigrationsGeneratesFilesForANewTable` |
| MIG-034 | Unchanged project: "no changes", no files rewritten | `makemigrations_test.go::TestExecute_MakemigrationsTwiceSaysNoChanges` |
| MIG-035 | Corrupt recorded state fails naming the file, wrapping `ErrBadState` | `makemigrations_test.go::TestExecute_MakemigrationsRefusesUnparsableRecordedState` |
| MIG-036 | Hand-written steps carry the last known state forward | `makemigrations_test.go::TestExecute_MakemigrationsCarriesHandWrittenStepsForward` |
| MIG-037 | Two changed modules → two files, two distinct versions | `makemigrations_test.go::TestExecute_MakemigrationsGivesEachOwningModuleItsOwnMigration` |
| MIG-038 | `makemigrations` refuses on a sliced process | `makemigrations_test.go::TestExecute_MakemigrationsRefusesWhenTheProcessIsSliced` |
| MIG-039 | Legacy unversioned state decodes using its actual all-nullable semantics | `orm/state_test.go::TestParseSnapshot_DecodesLegacyConstraintSemantics` |
| MIG-040 | Unknown marked state versions fail closed, naming source and version | `orm/state_test.go::TestParseSnapshot_RejectsUnknownStateVersion` |
| MIG-041 | Stable two-method `Dialect`; one operation may render ordered statements | `migratediff/migratediff_test.go::TestDialect_HasOneStableRenderMethodForAllOperations` |
| MIG-042 | `Apply` preflights every operation before the first schema mutation | `migratediff/migratediff_test.go::TestApply_PreflightsEveryOperationBeforeMutating` |
| MIG-043 | Dialects quote metadata as identifiers, including embedded quotes | `migratediff/migratediff_test.go::TestDialects_QuoteIdentifiersInsteadOfTreatingThemAsSQL` |
| MIG-044 | Simultaneous type/nullability/constraint changes are all emitted | `migratediff/migratediff_test.go::TestDiff_EmitsEveryIndependentChangeOnAField` |
| MIG-045 | Primary-key and index transitions are explicit and dependency-ordered | `migratediff/migratediff_test.go::TestDiff_DetectsIndexAndPrimaryKeyChanges` |
| MIG-046 | Generated object names are bounded and collision-resistant | `migratediff/migratediff_test.go::TestGeneratedObjectNamesAreBoundedAndCollisionResistant` |
| MIG-047 | PostgreSQL creates complete constraints for new tables and columns | `migratediff/migratediff_test.go::TestPostgres_RendersCompleteConstraintsForNewTablesAndColumns` |
| MIG-048 | PostgreSQL renders constraint transitions and catalog-resolved legacy PK drops | `migratediff/migratediff_test.go::TestPostgres_RendersConstraintTransitionsWithStableNames` |
| MIG-049 | SQLite creates supported constraints/indexes and refuses rebuild-only additions | `migratediff/migratediff_test.go::TestSQLite_RendersInitialConstraintsAndRefusesUnsupportedAdditions` |
| MIG-050 | pgx generation preserves multi-statement Up groups and reverses Down groups | `makemigrations_test.go::TestExecute_MakemigrationsRendersIndependentPostgresChangesAndReversesGroups` |
| MIG-051 | Same-second migration generation advances beyond the recorded version | `makemigrations_internal_test.go::TestNextVersionAdvancesPastARecordedVersionFromTheCurrentSecond` |
| MIG-052 | Every generated sidecar records that version's cumulative application schema | `makemigrations_test.go::TestExecute_MakemigrationsRecordsCumulativeStateAtEachGeneratedVersion` |
| MIG-053 | Explicit SQLite/PostgreSQL column rename; SQLite preserves data | `migratediff/migratediff_test.go::TestSQLite_RenamesAColumnWithoutLosingItsData` |
| MIG-054 | Confirmed compatible pair generates one reversible rename | `makemigrations_internal_test.go::TestRunMakemigrations_ConfirmedRenameGeneratesReversibleSQL` |
| MIG-055 | Declined rename remains drop/add with its data-loss warning | `makemigrations_internal_test.go::TestRunMakemigrations_DeclinedRenameGeneratesDropAndAdd` |
| MIG-056 | `-no-input` refuses a possible rename before writing files | `makemigrations_test.go::TestExecute_MakemigrationsNoInputRefusesPossibleRenameBeforeWriting` |
| MIG-057 | Ambiguous compatible pairs are asked one at a time | `makemigrations_internal_test.go::TestResolveRenames_AsksAmbiguousCandidatesOneAtATime` |
| MIG-058 | Confirmed constrained rename requires a hand-written migration | `makemigrations_internal_test.go::TestResolveRenames_ConfirmedConstrainedColumnRequiresHandWrittenMigration` |

MIG-027…032 land the command half of #59's first slice: the `Migrator`
interface (the counterpart of `Modeler` — models say what the schema IS,
migrations say how it got there) and the built-in `migrate [-to]` command.
Cross-module wiring mistakes are **construction** errors — duplicate versions
(MIG-028) and mixed widths (MIG-029) — because two branches generating the same
timestamped migration are green in isolation and collide only when their modules
meet in one binary. Within-module mistakes stay with the engine's validation,
which names the migration precisely.

MIG-032 is the fail-closed answer to slicing: FABRIN_MODULES is route selection,
never schema selection. A sliced process migrating would half-migrate the shared
database; makemigrations run the same way would propose dropping every table
whose module was selected out.

`migrate -to` decides direction by reading the applied-state table through
`migrate.Ensure` + a plain SELECT before anything runs; every mutation still
goes through the engine, which owns the transactional guarantees. 
Forward-to
filters the subset at or below the target and hands it to `Run` unchanged — no
new engine mode, so MIG-003's ordering and idempotence guarantees apply as-is.

MIG-033…038 complete #59's second slice: the generator and the on-disk format.
The format's load-bearing property is that **everything downstream can read it
without compiling** — filename prefix equals version (what #55's gate will
check), and the state sidecar is data in the #56 codec. `all.go` is regenerated
from the manifest rather than scanned from source, so regeneration cannot
silently drop a hand-written migration; MIG-036 pins the carry-forward rule that
makes hand-written and generated migrations able to mix at all.

The Go half is held to the stronger, complementary property: the test compiles
the generated package in a temporary module. Parsing alone accepted a migration
that referenced `migrate.M` without importing `migrate`, leaving `just check`
green and the user's next build red (#93).

MIG-042 pins the boundary between preflight and execution: every operation must
render successfully before the first statement executes. This prevents a known
dialect limitation from partially changing a schema; it does not claim that a
later database execution error makes the whole operation list transactional.

MIG-041 stabilizes the extension seam before the constraint vocabulary grows:
`Dialect` has `Name` plus one `Render(Operation) []string` entry point, so a new
operation does not add another required method to every custom dialect. The
statement list also removes the false one-operation/one-statement assumption;
`TestApply_ExecutesEveryStatementReturnedForOneOperation` is its execution
negative control. Built-in dialects return `ErrUnsupported` for unknown intent.

MIG-043 treats model names as identifiers rather than trusted SQL. Both shipped
dialects use ANSI double-quote escaping, verified by executing a table and
column whose names contain spaces and an embedded quote against real SQLite and
by checking PostgreSQL's rendered statement.

MIG-044…051 complete ADR 0006's migration wiring. Field properties are diffed
independently, then dependency-ranked so indexes and constraints come off before
the columns they guard change and return afterwards. The named-object digest is
part of the migration format; PostgreSQL primary-key drops additionally resolve
catalog identity so a key created by the legacy inline renderer remains
reversible even though PostgreSQL chose its old name. SQLite emits everything it
can execute honestly and refuses table-rebuild-only changes during preflight.

The generator preserves each operation's statement order while reversing the
operation groups for `Down`; flattening first would reverse the internals of a
multi-statement operation. Its pgx detection uses the driver's package path,
because the displayed concrete name is only `stdlib.Driver`, and its version
clock advances past equality so two genuine changes in one second do not
collide.

MIG-052 pins the time dimension of recorded state. A multi-module run still
emits one migration per owner, so its sidecars cannot all claim the run's final
snapshot. The generator starts from replayed state and advances only the tables
changed by each ordered operation group; untouched and not-yet-migrated tables
carry forward. Replaying those cumulative records therefore describes schemas
that really existed after each version and reaches the declared final state.

Two graduation notes. The `migratediff` seam went public here (#59 consuming it
is exactly the deliberate moment ADR 0005 anticipated), with a `$`-exact
depguard deny pinning root-import out while leaving the orm import in — the
mutation check for THAT gate surfaced as an import-cycle typecheck error, since
root already imports migratediff; the depguard rule stands as documentation and
backstop. And SQLite's stated refusal met its limit honestly: an `Up` with a
drop/retype still refuses generation ("hand-write this migration"), but a
generated DOWN whose inverse hits the refusal falls back to the plain statement
with the caveat riding in the file — without that fallback even adding a column
would be ungeneratable, since its rollback is a column drop.

MIG-011…018 are the recorded-state mechanism
([#56](https://github.com/usefabrin/fabrin/issues/56)): the "before" that
`makemigrations` diffs the live registry against, reconstructed by replaying
what each migration recorded — no database involved, because generating a
migration has to work on a laptop with nothing running. They live in package
`orm` rather than `migrate`, on purpose: both packages are leaves under the
boundary rules, the state *is* model metadata, and #57's differ needs exactly
this type. Nothing reads a directory yet, so the on-disk layout — file names,
where the state travels relative to the Go file — stays #59's decision; what
ships today is the codec and the replay rule.

MIG-013, MIG-039, and MIG-040 are ADR 0006's state transition. New records carry
an explicit version and preserve every decided flag. Unversioned records decode
with the schema semantics that generated them — non-primary columns nullable,
with the withheld flags false — while unknown marked versions fail closed. This
makes the first new diff accurate without treating an unknown future format as
legacy.

MIG-015 turns `encoding/json`'s default inside out. Ignoring unknown fields is
the polite choice for an RPC payload and exactly wrong for state a future diff
runs against: a key this version does not understand would be silently dropped,
and the next generated migration would diff against an impoverished schema.
Unknown-key rejection plus MIG-016's revalidation means anything that parses can
be trusted as far as anything registered directly.

MIG-019…026 began as the private differ proof (#57) and graduated to the public
`migratediff` package with #59. It lives at the root rather than `internal/`
because `internal/`'s boundary forbids sibling imports and this package exists
to read `orm` metadata. Nullability detection remains absent in this decision
slice: the flags stay withheld from recorded state (MIG-013) until ADR 0006's
versioned codec and operation wiring land together.

Two dialects ship together on purpose. SQLite keeps the gate hermetic
(MIG-023 runs against an in-process database); PostgreSQL is what people deploy,
and MIG-025 skips with a notice rather than passing silently when no DSN is
configured — the skip is visible in verbose output, so an always-skipped check
cannot masquerade as green forever. MIG-024's refusal is the honest half of
SQLite support: emitting drop/retype SQL that fails mid-migration is the worst
place to learn a dialect limit, so the refusal names itself before anything
executes. The table-rebuild dance that would un-refuse it needs multi-statement
operations, which is #59's decision to make deliberately.

The pgx driver enters `go.mod` as a test-only dependency for MIG-025, measured
like its sqlite predecessor: `go list -deps .` reaches zero `jackc/*` modules;
only `-test` reach does (16). Without it, a configured DSN would skip on
"unknown driver" and the live check could never truly run — which is exactly the
silently-never-passing outcome this row forbids.

MIG-010 is a type widening, so its tests read the **shape** of `M`'s fields by
reflection rather than exercising a behaviour: the load-bearing half of ADR 0003
is that the method set can never grow, and no behavioural test can see a fifth
method. The plausible fifth — `Query`, `Exec`, `QueryRow`, `Stmt` — is one
`*sql.Tx` already has, so a compile-time `var _ Handle = (*sql.Tx)(nil)` keeps
compiling on the day it lands and every user's fake breaks instead. Same reason
`TestModules_NeverImportEachOther` reads the import graph. `api/fabrin.txt`
records the same set, but a snapshot diff is reviewed by a human; this fails with
the reason attached.

The cell names the frozen-set test because a spec entry may name only one. The
other half — that `Up` and `Down` are the **same** named interface type, so one
user helper serves both directions — is
`migrate/handle_test.go::TestM_UpAndDownTakeAHandleRatherThanATransaction`.

MIG-001 is **unchanged** by it, and deliberately not re-tested: the engine still
opens a transaction and still writes the body and the bookkeeping row inside it,
which `TestRun_LeavesNothingBehindWhenAMigrationFails` asserts behaviourally,
without reference to what the handle's dynamic type is. ADR 0003's clause that
the dynamic type is **not** part of the contract has no test and cannot have one:
asserting the handle *is* a `*sql.Tx` would enshrine exactly what the clause
de-contracts, and asserting it is *not* one would be false today and forbid the
implementation the ADR mandates. It is held by a doc comment, and by Fabrin's own
tests never asserting on it.

MIG-008 is blocked, and on something outside the migration engine: there is no
on-disk migration file format yet. The engine takes migrations as values and
nothing reads a directory (`docs/TODO.md`, F2), so a gate has no files to read
and no naming scheme to hold a filename to. Writing one now would mean inventing
the format that `fabrin makemigrations` has to live with afterwards. The row
stays here, planned, so the gap is discoverable from the matrix rather than only
from the issue.

MIG-009 arrived via issue #55 but cites **FR-ORM-4**, for the reason the
paragraph below gives: two distinct versions, neither claiming the other's, is an
ordering property of the engine — MIG-002's family, not MIG-007's. `M` documents
lexicographic ordering and therefore a fixed-width version — "9 sorts after 10" —
and `prepare` never checks it. Mutual width consistency across the set is the
only violation detectable without a version scheme, and Fabrin documents none:
the engine's own tests use `001`, and a timestamp is recommended rather than
required. `prepare` now rejects such a set, wrapping `ErrInvalidMigration` rather
than gaining a distinct sentinel: the set is unusable for the same reason a
missing `Down` makes one unusable, and an exported sentinel is a permanent
promise that nothing yet needs to branch on.

Checked by mutation, because the assertion that both versions are named is the
half most easily satisfied by accident. Deleting the check turns it red on "a set
that sorts as [10 9] must be rejected"; keeping the check but dropping the two
`%q` from the message turns it red on `the error must name "9"`. The message
deliberately contains no literal `9` or `10` of its own — an illustrative "9 sorts
after 10" in the error text would have satisfied the test without naming the
versions the caller actually passed.

The assertion that nothing was applied is stronger than its siblings': validation
runs before `ensureTable`, so a set rejected here leaves the applied-state table
**uncreated**, not merely empty. Reading `versions()` would fail on a missing
table rather than prove the point, which is why the rejection tests above assert
`tableExists` instead.

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

## Admin seam proof

| ID | Behaviour | Test |
|----|-----------|------|
| ADM-001 | One private, reflection-free resource flows from metadata and forms through CRUD persistence | `admin/admin_test.go::TestResource_CRUDFlowsFromMetadataFormsToPersistence` |
| ADM-002 | Unsafe CRUD validates CSRF then authorization before binding or persistence | `admin/admin_test.go::TestResource_UnsafeActionsFailClosedBeforeBindingOrPersistence` |
| ADM-003 | Metadata and typed adapters retain field errors and skip invalid persistence | `admin/admin_test.go::TestResource_FormErrorsStayWithMetadataFieldsAndSkipPersistence` |

## Generated data access

| ID | Behaviour | Test |
|----|-----------|------|
| DATA-001 | Deterministic compiled PostgreSQL stores and metadata | `schema/schema_test.go::TestGenerate_CompilesAndUsesPostgres` |
| DATA-002 | Reject invalid or colliding schema names | `schema/schema_test.go::TestGenerate_RejectsInvalidNames` |

## Mail

| ID | Behaviour | Test |
|----|-----------|------|
| MAIL-001 | Bounded independent snapshots and drain | `mail/mail_test.go::TestCapture_BoundedSnapshotAndDrain` |
| MAIL-002 | Invalid headers and cancellation reject before capture | `mail/mail_test.go::TestCapture_RejectsInvalidAndCancelledMessages` |

## Authentication (proposed contract)

| ID | Behaviour | Test |
|----|-----------|------|
| AUTH-001 | Codes bind to purpose, email and challenge ID, expire at the boundary and have protected verifiers. | _planned_ |
| AUTH-002 | Challenge consumption and attempt accounting are atomic; replay and replaced codes fail. | _planned_ |
| AUTH-003 | Shared address and source abuse budgets survive resends and fail closed on capacity or store errors. | _planned_ |
| AUTH-004 | Delivery failure creates no authenticated result and cleanup cannot revoke a newer challenge. | _planned_ |
| AUTH-005 | Verified signup uniquely resolves identity with invitation and disabled-account policies before granting a session. | _planned_ |
| AUTH-006 | Session rotation, expiry and revocation are enforced by the store on every authenticated request. | _planned_ |
| AUTH-007 | Cookie and native authentication enforce CSRF, credential separation, bounded bodies and no-store responses. | _planned_ |
| AUTH-008 | Authorization covers lists, records and fields and denies access on policy or store failures. | _planned_ |
| AUTH-009 | Public auth responses resist enumeration and audit output excludes credentials; key rotation and cleanup preserve validity rules. | _planned_ |

## Private OTP proof

| ID | Behaviour | Test |
|----|-----------|------|
| OTP-001 | Private challenge verification binds purpose, ID, canonical email and code using protected verifiers and enforces the exact expiry boundary. | `auth/challenge_test.go::TestChallenge_BindsCredentialsAndConsumesOnce` |
| OTP-002 | Private challenge consumption allows at most five attempts and one concurrent successful use. | `auth/challenge_test.go::TestChallenge_ConcurrentVerificationHasOneWinner` |
| OTP-003 | Email canonicalization rejects controls and non-ASCII input, preserving local-part case and aliases while lowercasing the domain. | `auth/challenge_test.go::TestChallenge_RejectsInvalidInputsAndCanonicalizesEmail` |

Expiry and attempt boundaries also run in `auth/challenge_test.go::TestChallenge_ExpiryAndAttemptBoundary`; verifier encoding is covered by `auth/challenge_test.go::TestChallenge_VerifierUsesUnambiguousFieldEncoding`. These private tests do not complete the planned end-to-end AUTH rows.
