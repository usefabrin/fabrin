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

CLI-001…003 cite FR-CLI-4, because `Commander` is why `fabrin/cli` exists as a
package at all — a module contributing a subcommand is what forces the command
type to be Fabrin's own, and forces the package to stand alone from `App`.
CLI-004/005 cite FR-CLI-3; CLI-006 cites NFR-7.

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
