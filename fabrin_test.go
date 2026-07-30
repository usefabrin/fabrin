package fabrin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/usefabrin/fabrin"
)

func TestMain(m *testing.M) {
	// Gin's debug mode writes a banner and per-route lines to stderr on every
	// engine construction, which buries real test output.
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// ── Gin is public: the alias must be the same type, not a conversion ─────────

func TestContext_IsGinContextItself(t *testing.T) {
	t.Parallel()

	// If these were wrappers rather than aliases, none of the four conversions
	// below would compile, and every Gin middleware would need an adapter. That
	// they do compile — in BOTH directions, with no conversion syntax — is the
	// whole reason Gin is blessed publicly. Assigning nil would not prove it: only
	// passing one type where the other is required does.
	var (
		_ = func(c *gin.Context) *fabrin.Context { return c }
		_ = func(c *fabrin.Context) *gin.Context { return c }
		_ = func(h gin.HandlerFunc) fabrin.HandlerFunc { return h }
		_ = func(h fabrin.HandlerFunc) gin.HandlerFunc { return h }
	)

	app, err := fabrin.New(fabrin.Options{}, testModule{
		name: "m",
		routes: func(r fabrin.Router) {
			// A stock Gin middleware, used unmodified.
			r.Use(func(c *gin.Context) { c.Header("X-From-Gin", "yes"); c.Next() })
			r.GET("/x", func(c *fabrin.Context) { c.JSON(http.StatusOK, fabrin.H{"ok": true}) })
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := get(t, app, "/x")
	if rec.Header().Get("X-From-Gin") != "yes" {
		t.Error("an unmodified gin.HandlerFunc must work as Fabrin middleware")
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("gin JSON rendering must work through the alias, got %q", rec.Body.String())
	}
}

// ── CORE-003: graceful shutdown ─────────────────────────────────────────────

func TestApp_RunReturnsWhenContextCancelled(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		route("m", "/x"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	// Wait for a real listener rather than sleeping: a fixed sleep is either
	// slower than needed or flaky on a loaded runner, usually both.
	if _, err := waitForAddr(app); err != nil {
		t.Fatalf("server never listened: %v", err)
	}

	cancel()

	select {
	case err := <-done:
		// A cancelled context is an ordinary stop, not a failure — returning
		// ctx.Err() here would make every clean shutdown look like an error to
		// the caller's exit-code logic.
		if err != nil {
			t.Errorf("Run must return nil on a clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of cancellation: shutdown must be bounded")
	}
}

func TestApp_RunStopsLifecycleModulesOnShutdown(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var log []string

	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		lifecycleModule{testModule: testModule{name: "res"}, log: &log, mu: &mu},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	if _, err := waitForAddr(app); err != nil {
		t.Fatalf("server never listened: %v", err)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}

	mu.Lock()
	got := strings.Join(log, ",")
	mu.Unlock()

	// Run owns the lifecycle: a module that only gets Start when the caller
	// remembers to call it separately will leak in every real program.
	if got != "start:res,stop:res" {
		t.Errorf("Run must start and stop lifecycle modules, got %q", got)
	}
}

func TestApp_RunRejectsSecondCall(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	if _, err := waitForAddr(app); err != nil {
		t.Fatalf("server never listened: %v", err)
	}

	// Running twice would start a second listener and double-Start every module.
	if err := app.Run(context.Background()); !errors.Is(err, fabrin.ErrAlreadyRunning) {
		t.Errorf("a second Run must fail with ErrAlreadyRunning, got %v", err)
	}

	cancel()
	<-done
}

func TestApp_RunServesRequests(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	addr, err := waitForAddr(app)
	if err != nil {
		t.Fatalf("server never listened: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	cancel()
	<-done
}

// waitForAddr blocks until the app is listening and returns its resolved address.
// Addr "127.0.0.1:0" asks the kernel for a free port, so tests never collide.
func waitForAddr(app *fabrin.App) (string, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if addr := app.Addr(); addr != "" {
			return addr, nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return "", errors.New("timed out waiting for listener")
}

// ── Options ─────────────────────────────────────────────────────────────────

func TestNew_DefaultsAddrWhenUnset(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := app.Options().Addr; got != fabrin.DefaultAddr {
		t.Errorf("Addr = %q, want the documented default %q", got, fabrin.DefaultAddr)
	}
}

func TestNew_RequiresAtLeastOneModule(t *testing.T) {
	t.Parallel()

	// An app with no modules serves nothing. Almost always a wiring mistake, and
	// silently starting is how it reaches production.
	if _, err := fabrin.New(fabrin.Options{}); err == nil {
		t.Fatal("New with no modules must fail")
	}
}

// ── Benchmarks: the cost of Fabrin over the router it wraps ──────────────────

func BenchmarkRawGin_OneRoute(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	benchServe(b, e)
}

func BenchmarkFabrin_OneRoute(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	app, err := fabrin.New(fabrin.Options{}, route("m", "/x"))
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	benchServe(b, app.Handler())
}

func benchServe(b *testing.B, h http.Handler) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
}
