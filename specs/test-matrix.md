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

These three are enforced by `apicheck` rather than by a Go test, so their "test"
is the gate invocation plus the injected-violation evidence recorded in the PR
that lands them. A gate counts as coverage only once it has been proven to fail on
a violation *and* to pass its negative control.

| ID | Behaviour | Test |
|----|-----------|------|
| API-001 | Unblessed third-party type in exported signature fails | _planned_ |
| API-002 | Surface change without regenerated snapshot fails | _planned_ |
| API-003 | Aliases recorded unexpanded; Gin bump does not churn snapshot | _planned_ |
