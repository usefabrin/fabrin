package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/usefabrin/fabrin/health"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func serve(t *testing.T, r *health.Registry, path string) *httptest.ResponseRecorder {
	t.Helper()
	e := gin.New()
	health.Mount(e, r)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) health.Report {
	t.Helper()
	var rep health.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rep
}

// ── HLT-001: liveness consults nothing ──────────────────────────────────────

func TestLiveness_DoesNotConsultChecks(t *testing.T) {
	t.Parallel()

	var probed atomic.Int64
	r := health.NewRegistry()
	if err := r.Register("db", health.Named("conn", func(context.Context) error {
		probed.Add(1)
		return errors.New("database is on fire")
	})); err != nil {
		t.Fatalf("Register: %v", err)
	}

	rec := serve(t, r, health.LivenessPath)

	// The check FAILS, and liveness must still be 200. Restarting is the only
	// remedy a liveness failure has, and restarting cannot fix a database — so a
	// dependency-aware liveness probe converts every blip into a restart loop that
	// also drops in-flight requests.
	if rec.Code != http.StatusOK {
		t.Errorf("liveness = %d, want 200 even with a failing dependency", rec.Code)
	}
	if probed.Load() != 0 {
		t.Errorf("liveness probed %d check(s); it must consult none", probed.Load())
	}
}

// ── HLT-002: readiness fails closed and names the failure ───────────────────

func TestReadiness_FailsClosedAndNamesTheFailingCheck(t *testing.T) {
	t.Parallel()

	r := health.NewRegistry()
	mustRegister(t, r, "db", health.Named("conn", func(context.Context) error { return nil }))
	mustRegister(t, r, "cache", health.Named("ping", func(context.Context) error {
		return errors.New("connection refused")
	}))

	rec := serve(t, r, health.ReadinessPath)

	// Reporting ready while a dependency is unreachable takes traffic the process
	// cannot serve, and hides the problem behind a load balancer.
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness = %d, want 503 when a check fails", rec.Code)
	}

	rep := decode(t, rec)
	if rep.Status != health.StatusDown {
		t.Errorf("Status = %q, want %q", rep.Status, health.StatusDown)
	}

	// Whoever is paged needs to know WHICH module and WHICH check, from the
	// response alone.
	body := rec.Body.String()
	for _, want := range []string{"cache", "ping", "connection refused"} {
		if !strings.Contains(body, want) {
			t.Errorf("readiness body must contain %q; got %s", want, body)
		}
	}

	var down int
	for _, c := range rep.Checks {
		if c.Status == health.StatusDown {
			down++
			if c.Module != "cache" || c.Check != "ping" {
				t.Errorf("wrong check reported down: %+v", c)
			}
		}
	}
	if down != 1 {
		t.Errorf("%d checks down, want exactly 1", down)
	}
}

func TestReadiness_UpWhenAllChecksPass(t *testing.T) {
	t.Parallel()

	r := health.NewRegistry()
	mustRegister(t, r, "db", health.Named("conn", func(context.Context) error { return nil }))
	mustRegister(t, r, "cache", health.Named("ping", func(context.Context) error { return nil }))

	rec := serve(t, r, health.ReadinessPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if rep := decode(t, rec); rep.Status != health.StatusUp || len(rep.Checks) != 2 {
		t.Errorf("report = %+v, want up with 2 checks", rep)
	}
}

func TestReadiness_UpWithNoChecksRegistered(t *testing.T) {
	t.Parallel()

	// An app with no dependencies is ready. Failing closed on an EMPTY set would
	// mean every minimal app is permanently unready — failing closed is about
	// unknown check outcomes, not about having nothing to check.
	rec := serve(t, health.NewRegistry(), health.ReadinessPath)
	if rec.Code != http.StatusOK {
		t.Errorf("readiness with no checks = %d, want 200", rec.Code)
	}
}

func TestReadiness_TimesOutRatherThanHanging(t *testing.T) {
	t.Parallel()

	r := health.NewRegistry()
	r.Timeout = 50 * time.Millisecond
	mustRegister(t, r, "slow", health.Named("hang", func(ctx context.Context) error {
		// A check that ignores ctx entirely is the realistic bad case.
		<-ctx.Done()
		return ctx.Err()
	}))

	start := time.Now()
	rec := serve(t, r, health.ReadinessPath)
	elapsed := time.Since(start)

	// Unbounded, this handler outlives the orchestrator's own probe timeout and is
	// reported as a failure with no detail about which check hung.
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness = %d, want 503 on timeout", rec.Code)
	}
	if elapsed > 2*time.Second {
		t.Errorf("readiness took %v; the timeout must bound it", elapsed)
	}
	if !strings.Contains(rec.Body.String(), "hang") {
		t.Errorf("the timed-out check must be named; got %s", rec.Body.String())
	}
}

func TestEvaluate_RunsChecksConcurrently(t *testing.T) {
	t.Parallel()

	const n = 8
	const delay = 60 * time.Millisecond

	r := health.NewRegistry()
	for i := range n {
		mustRegister(t, r, "m", health.Named(string(rune('a'+i)), func(ctx context.Context) error {
			select {
			case <-time.After(delay):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}))
	}

	start := time.Now()
	rep := r.Evaluate(context.Background())
	elapsed := time.Since(start)

	if rep.Status != health.StatusUp {
		t.Fatalf("report = %+v, want up", rep)
	}
	// Serially this would take n*delay, which for real dependencies blows the
	// probe timeout for reasons unrelated to any single one of them. Generous
	// bound: the point is "not N times", not a precise figure on a shared runner.
	if elapsed > delay*4 {
		t.Errorf("evaluating %d checks took %v; serial would be ~%v, so they are not concurrent",
			n, elapsed, n*delay)
	}
}

func TestEvaluate_SortsResultsDeterministically(t *testing.T) {
	t.Parallel()

	r := health.NewRegistry()
	mustRegister(t, r, "zebra", health.Named("b", ok), health.Named("a", ok))
	mustRegister(t, r, "alpha", health.Named("z", ok))

	// Unsorted, a diff between two readiness responses shows map iteration order
	// rather than a real change.
	rep := r.Evaluate(context.Background())
	var got []string
	for _, c := range rep.Checks {
		got = append(got, c.Module+"/"+c.Check)
	}
	want := "alpha/z,zebra/a,zebra/b"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestRegister_RejectsUnusableChecks(t *testing.T) {
	t.Parallel()

	r := health.NewRegistry()

	// An anonymous failing check tells whoever is paged nothing about where to look.
	if err := r.Register("m", health.Named("", ok)); err == nil {
		t.Error("a check with no name must be rejected")
	}
	if err := r.Register("m", nil); err == nil {
		t.Error("a nil check must be rejected rather than panicking at probe time")
	}
	if r.Len() != 0 {
		t.Errorf("rejected checks must not be registered; Len = %d", r.Len())
	}
}

func TestCheckFunc_WithNoFunctionFailsRatherThanPanics(t *testing.T) {
	t.Parallel()

	// A zero-valued CheckFunc is a plausible mistake; a nil-map-style panic inside
	// the readiness handler would take the endpoint down instead of reporting.
	c := health.CheckFunc{CheckName: "empty"}
	if err := c.Probe(context.Background()); err == nil {
		t.Error("a CheckFunc with no Fn must return an error")
	}
}

func ok(context.Context) error { return nil }

func mustRegister(t *testing.T, r *health.Registry, module string, checks ...health.Check) {
	t.Helper()
	if err := r.Register(module, checks...); err != nil {
		t.Fatalf("Register(%s): %v", module, err)
	}
}
