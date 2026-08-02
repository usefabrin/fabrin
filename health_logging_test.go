package fabrin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/health"
	"github.com/usefabrin/fabrin/logging"
)

// checkerModule implements the optional Checker interface.
type checkerModule struct {
	testModule
	checks []health.Check
}

func (m checkerModule) Checks() []health.Check { return m.checks }

func failing(name string, err error) health.Check {
	return health.Named(name, func(context.Context) error { return err })
}

func passing(name string) health.Check {
	return health.Named(name, func(context.Context) error { return nil })
}

// decodeReport reads a readiness response body, failing the test if it is not a
// well-formed report — the body IS the diagnostic, so a malformed one is a bug.
func decodeReport(t *testing.T, rec *httptest.ResponseRecorder) health.Report {
	t.Helper()
	var report health.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("readiness body must be a health.Report, got %q: %v", rec.Body.String(), err)
	}
	return report
}

// ── HLT-001: liveness consults nothing ──────────────────────────────────────

func TestHealthz_StaysUpWhileAModuleCheckIsFailing(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{}, checkerModule{
		testModule: testModule{name: "db"},
		checks:     []health.Check{failing("primary", errors.New("connection refused"))},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := get(t, app, health.LivenessPath)

	// This is the whole point of separating the two endpoints. Restarting is the
	// only remedy a liveness failure has, and restarting cannot reach a database
	// that is refusing connections — it just drops the requests this process was
	// still serving and starts a restart loop.
	if rec.Code != http.StatusOK {
		t.Errorf("liveness = %d, want 200: /healthz must consult no module checks", rec.Code)
	}
}

func TestHealthz_IsMountedWithoutAnyModuleAskingForIt(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Batteries included: an orchestrator's probe must work against a stock app,
	// with no module having opted in.
	if rec := get(t, app, health.LivenessPath); rec.Code != http.StatusOK {
		t.Errorf("liveness = %d, want 200 on an app whose modules contribute nothing", rec.Code)
	}
	if rec := get(t, app, health.ReadinessPath); rec.Code != http.StatusOK {
		t.Errorf("readiness = %d, want 200 when no module contributes a check", rec.Code)
	}
}

// ── HLT-002: readiness aggregates and fails closed ──────────────────────────

func TestReadyz_FailsClosedAndNamesTheFailingModuleAndCheck(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{},
		checkerModule{
			testModule: testModule{name: "db"},
			checks:     []health.Check{failing("primary", errors.New("connection refused"))},
		},
		checkerModule{
			testModule: testModule{name: "cache"},
			checks:     []health.Check{passing("redis")},
		},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := get(t, app, health.ReadinessPath)

	// Fails closed: serving traffic this process cannot handle is worse than
	// leaving the pool, because a ready-but-broken instance hides the fault behind
	// a load balancer.
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness = %d, want 503 when a check fails", rec.Code)
	}

	report := decodeReport(t, rec)
	if report.Status != health.StatusDown {
		t.Errorf("report status = %q, want %q", report.Status, health.StatusDown)
	}

	// The response has to be the diagnostic. "not ready" without naming which
	// dependency is down sends whoever is paged to the logs to find out.
	var found bool
	for _, res := range report.Checks {
		if res.Module == "db" && res.Check == "primary" {
			found = true
			if res.Status != health.StatusDown {
				t.Errorf("db/primary status = %q, want %q", res.Status, health.StatusDown)
			}
			if !strings.Contains(res.Error, "connection refused") {
				t.Errorf("db/primary error = %q, want the probe's own message", res.Error)
			}
		}
	}
	if !found {
		t.Errorf("report must name the failing module and check, got %+v", report.Checks)
	}

	// The passing check is still reported: knowing what is healthy is half of
	// knowing where to look.
	if len(report.Checks) != 2 {
		t.Errorf("report must cover every registered check, got %d", len(report.Checks))
	}
}

func TestReadyz_ReportsUpWhenEveryCheckPasses(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{}, checkerModule{
		testModule: testModule{name: "db"},
		checks:     []health.Check{passing("primary")},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := get(t, app, health.ReadinessPath)
	if rec.Code != http.StatusOK {
		t.Errorf("readiness = %d, want 200 when every check passes", rec.Code)
	}
	if report := decodeReport(t, rec); report.Status != health.StatusUp {
		t.Errorf("report status = %q, want %q", report.Status, health.StatusUp)
	}
}

func TestReadyz_OnlyConsultsMountedModules(t *testing.T) {
	t.Parallel()

	// Process slicing must extend to readiness. A module this process did not mount
	// owns nothing here, so gating this process's traffic on its dependency would
	// make a deployment shape fail for a reason it has no stake in.
	app, err := fabrin.New(
		fabrin.Options{Modules: []string{"cache"}},
		checkerModule{
			testModule: testModule{name: "db"},
			checks:     []health.Check{failing("primary", errors.New("connection refused"))},
		},
		checkerModule{
			testModule: testModule{name: "cache"},
			checks:     []health.Check{passing("redis")},
		},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := get(t, app, health.ReadinessPath)
	if rec.Code != http.StatusOK {
		t.Errorf("readiness = %d, want 200: an unmounted module's failing check must not gate this process", rec.Code)
	}
	if report := decodeReport(t, rec); len(report.Checks) != 1 {
		t.Errorf("report must cover only mounted modules, got %+v", report.Checks)
	}
}

func TestNew_RejectsAnUnnamedHealthCheck(t *testing.T) {
	t.Parallel()

	// An anonymous failing check tells whoever is paged nothing about where to
	// look, and New already fails on wiring mistakes rather than warning about them.
	_, err := fabrin.New(fabrin.Options{}, checkerModule{
		testModule: testModule{name: "db"},
		checks:     []health.Check{health.Named("", func(context.Context) error { return nil })},
	})
	if err == nil {
		t.Fatal("a check with no name must be rejected at construction")
	}
	if !strings.Contains(err.Error(), "db") {
		t.Errorf("error must name the offending module, got %q", err)
	}
}

// ── LOG-001: every request carries a request id ─────────────────────────────

func TestRequestID_IsReturnedOnEveryResponse(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := get(t, app, "/x")

	// The header is the half a user can quote from a failed request. Without it,
	// "find the logs for this error" is a text search over timestamps.
	if rec.Header().Get(logging.HeaderRequestID) == "" {
		t.Errorf("every response must carry %s", logging.HeaderRequestID)
	}
}

func TestRequestID_ReachesTheHandlerContext(t *testing.T) {
	t.Parallel()

	var fromContext string
	app, err := fabrin.New(fabrin.Options{}, testModule{
		name: "m",
		routes: func(r fabrin.Router) {
			r.GET("/x", func(c *fabrin.Context) {
				fromContext = logging.RequestIDFromContext(c.Request.Context())
				c.String(http.StatusOK, "ok")
			})
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := get(t, app, "/x")

	// The context half is what lets every log line from this request carry the same
	// id — including lines written by a handler that never sees the header.
	if fromContext == "" {
		t.Fatal("the request id must be on the handler's request context")
	}
	if got := rec.Header().Get(logging.HeaderRequestID); got != fromContext {
		t.Errorf("header id %q and context id %q must match, or correlating them is guesswork", got, fromContext)
	}
}

func TestRequestID_HonoursAnInboundHeader(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(logging.HeaderRequestID, "upstream-trace-1")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	// A trace started by an upstream proxy or another service must survive the hop,
	// or a request is uncorrelatable the moment it crosses a service boundary.
	if got := rec.Header().Get(logging.HeaderRequestID); got != "upstream-trace-1" {
		t.Errorf("request id = %q, want the inbound value preserved", got)
	}
}

func TestRecoveredPanic_IsRequestLoggedWithFailureStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		beforePanic func(*fabrin.Context)
		wantStatus  int
	}{
		{name: "before response commitment", beforePanic: func(*fabrin.Context) {}, wantStatus: http.StatusInternalServerError},
		{name: "after response commitment", beforePanic: func(c *fabrin.Context) { c.String(http.StatusOK, "partial") }, wantStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			app, err := fabrin.New(fabrin.Options{
				Logger: slog.New(slog.NewJSONHandler(&out, nil)),
			}, testModule{
				name: "m",
				routes: func(r fabrin.Router) {
					r.GET("/panic", func(c *fabrin.Context) {
						tc.beforePanic(c)
						panic("boom")
					})
				},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			rec := get(t, app, "/panic")
			if rec.Code != tc.wantStatus {
				t.Fatalf("panic response = %d, want actual committed status %d", rec.Code, tc.wantStatus)
			}

			var line map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &line); err != nil {
				t.Fatalf("panic request log is not one JSON line: %v (%s)", err, out.String())
			}
			if line["msg"] != logging.MsgRequest || line["status"] != float64(tc.wantStatus) {
				t.Errorf("panic request log = %v, want actual status %d", line, tc.wantStatus)
			}
			if line["level"] != "ERROR" || line["panic_recovered"] != true {
				t.Errorf("panic request must be explicit error evidence: %v", line)
			}
			if line[logging.LogKeyRequestID] != rec.Header().Get(logging.HeaderRequestID) {
				t.Errorf("panic request log must carry the response request id: %v", line)
			}
		})
	}
}

// ── The logger is built from LogFormat and LogLevel ─────────────────────────

func TestNew_BuildsTheLoggerFromLogLevel(t *testing.T) {
	t.Parallel()

	// Regression guard. Options.WithDefaults used to fill Logger with
	// slog.Default(), which meant LogFormat and LogLevel were plumbed all the way
	// from FABRIN_LOG_FORMAT / FABRIN_LOG_LEVEL through config and then silently
	// discarded. A settings key that resolves, validates, and does nothing is the
	// same defect class as one referenced but never implemented.
	app, err := fabrin.New(fabrin.Options{LogLevel: "error"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	log := app.Options().Logger
	if log == nil {
		t.Fatal("New must leave Options().Logger usable")
	}
	ctx := context.Background()
	if log.Enabled(ctx, slog.LevelInfo) {
		t.Error("LogLevel \"error\" must reach the logger: info is still enabled")
	}
	if !log.Enabled(ctx, slog.LevelError) {
		t.Error("LogLevel \"error\" must still enable error")
	}
}

func TestNew_KeepsACallerSuppliedLogger(t *testing.T) {
	t.Parallel()

	// A consumer who passes a logger has already decided where Fabrin's output
	// goes; overriding it from LogFormat would silently redirect their logs.
	mine := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := fabrin.New(fabrin.Options{Logger: mine, LogLevel: "error"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app.Options().Logger != mine {
		t.Error("New must not replace a caller-supplied logger")
	}
}
