package fabrin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/usefabrin/fabrin"
)

// testModule is the minimum a module must be: a name and some routes.
type testModule struct {
	name   string
	routes func(r fabrin.Router)
}

func (m testModule) Name() string { return m.name }

func (m testModule) Routes(r fabrin.Router) {
	if m.routes != nil {
		m.routes(r)
	}
}

// route builds a module serving 200 at the given path, so tests can assert on
// which modules got mounted by asking the router rather than by inspecting
// internals.
func route(name, path string) fabrin.Module {
	return testModule{name: name, routes: func(r fabrin.Router) {
		r.GET(path, func(c *fabrin.Context) { c.String(http.StatusOK, name) })
	}}
}

func get(t *testing.T, app *fabrin.App, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// ── CORE-001 ────────────────────────────────────────────────────────────────

func TestNew_RejectsDuplicateModuleNames(t *testing.T) {
	t.Parallel()

	_, err := fabrin.New(fabrin.Options{},
		route("blog", "/a"),
		route("blog", "/b"),
	)

	if err == nil {
		t.Fatal("registering two modules named \"blog\" must fail: one module's routes would silently shadow the other's")
	}
	if !errors.Is(err, fabrin.ErrDuplicateModule) {
		t.Errorf("error must be identifiable as ErrDuplicateModule so callers can branch on it, got %v", err)
	}
	if !strings.Contains(err.Error(), "blog") {
		t.Errorf("error must name the offending module, got %q", err)
	}
}

func TestNew_RejectsEmptyModuleName(t *testing.T) {
	t.Parallel()

	_, err := fabrin.New(fabrin.Options{}, testModule{name: ""})
	if err == nil {
		t.Fatal("an unnamed module cannot be selected by FABRIN_MODULES or reported in a health check, so it must be rejected at construction")
	}
}

// ── CORE-002 ────────────────────────────────────────────────────────────────

// lifecycleModule implements the optional Lifecycle interface.
type lifecycleModule struct {
	testModule
	log      *[]string
	mu       *sync.Mutex
	startErr error
}

func (m lifecycleModule) Start(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.log = append(*m.log, "start:"+m.name)
	return m.startErr
}

func (m lifecycleModule) Stop(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.log = append(*m.log, "stop:"+m.name)
	return nil
}

func TestApp_ReportsOptionalInterfacesEachModuleMatched(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var log []string

	app, err := fabrin.New(fabrin.Options{},
		lifecycleModule{testModule: testModule{name: "withLifecycle"}, log: &log, mu: &mu},
		route("plain", "/plain"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A mistyped optional method name means the interface is silently not
	// satisfied — the module simply never starts. The registry must be able to
	// say what it matched, so that failure is discoverable.
	caps := app.Capabilities()

	if got := caps["withLifecycle"]; !slices.Contains(got, "Lifecycle") {
		t.Errorf("module withLifecycle implements Lifecycle; Capabilities reported %v", got)
	}
	if got := caps["plain"]; slices.Contains(got, "Lifecycle") {
		t.Errorf("module plain does not implement Lifecycle; Capabilities reported %v", got)
	}
}

// ── CORE-004: the ORDER is the behaviour ────────────────────────────────────

func TestApp_StopsLifecycleModulesInReverseRegistrationOrder(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var log []string

	mk := func(name string) fabrin.Module {
		return lifecycleModule{testModule: testModule{name: name}, log: &log, mu: &mu}
	}

	app, err := fabrin.New(fabrin.Options{}, mk("first"), mk("second"), mk("third"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	mu.Lock()
	got := strings.Join(log, ",")
	mu.Unlock()

	// Asserting the full sequence, not merely that Stop ran: a module built after
	// its dependency must shut down before it, or it tears down against something
	// already gone. A test that only checked "Stop was called" would pass on an
	// implementation that stops in forward order.
	want := "start:first,start:second,start:third,stop:third,stop:second,stop:first"
	if got != want {
		t.Errorf("lifecycle order wrong\n got: %s\nwant: %s", got, want)
	}
}

func TestApp_StartFailureStopsAlreadyStartedModules(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var log []string
	boom := errors.New("boom")

	app, err := fabrin.New(fabrin.Options{},
		lifecycleModule{testModule: testModule{name: "ok"}, log: &log, mu: &mu},
		lifecycleModule{testModule: testModule{name: "bad"}, log: &log, mu: &mu, startErr: boom},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := app.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start must return the module's error, got %v", err)
	}

	mu.Lock()
	got := strings.Join(log, ",")
	mu.Unlock()

	// Leaving "ok" started after the app failed to come up leaks whatever it
	// owns — a pool, a goroutine, a file handle — with nothing left holding a
	// reference to close it.
	want := "start:ok,start:bad,stop:ok"
	if got != want {
		t.Errorf("failed Start must unwind what it started\n got: %s\nwant: %s", got, want)
	}
}

// ── MOD-001 / MOD-002: process slicing ──────────────────────────────────────

func TestNew_MountsOnlySelectedModules(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(
		fabrin.Options{Modules: []string{"greet"}},
		route("greet", "/greet"),
		route("clock", "/time"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := get(t, app, "/greet").Code; got != http.StatusOK {
		t.Errorf("/greet: selected module must be mounted, got %d", got)
	}
	// This 404 is the process-slicing claim. One binary, N deployment shapes.
	if got := get(t, app, "/time").Code; got != http.StatusNotFound {
		t.Errorf("/time: unselected module must not be mounted, got %d", got)
	}
}

func TestNew_MountsEverythingWhenNoSelection(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{},
		route("greet", "/greet"),
		route("clock", "/time"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, path := range []string{"/greet", "/time"} {
		if got := get(t, app, path).Code; got != http.StatusOK {
			t.Errorf("%s: an empty selection means all modules, got %d", path, got)
		}
	}
}

func TestNew_RejectsSelectionNamingUnregisteredModule(t *testing.T) {
	t.Parallel()

	_, err := fabrin.New(
		fabrin.Options{Modules: []string{"greet", "typo"}},
		route("greet", "/greet"),
	)

	// A typo that silently serves nothing is the worst outcome available: the
	// process starts, passes its liveness probe, and does no work.
	if err == nil {
		t.Fatal("a selection naming an unregistered module must fail at startup, not silently mount nothing")
	}
	if !errors.Is(err, fabrin.ErrUnknownModule) {
		t.Errorf("error must be identifiable as ErrUnknownModule, got %v", err)
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Errorf("error must name the unmatched selection, got %q", err)
	}
	if !strings.Contains(err.Error(), "greet") {
		t.Errorf("error should list what IS registered, so the typo is obvious; got %q", err)
	}
}

func TestApp_ModulesReportsOnlyMountedModules(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(
		fabrin.Options{Modules: []string{"greet"}},
		route("greet", "/greet"),
		route("clock", "/time"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := app.Modules()
	if len(got) != 1 || got[0] != "greet" {
		t.Errorf("Modules must report what is mounted, not what was registered; got %v", got)
	}
}

func TestNewFromFactories_DoesNotBuildUnselectedModules(t *testing.T) {
	t.Parallel()

	built := make(map[string]int)
	factory := func(name, path string) fabrin.ModuleFactory {
		return fabrin.LazyModule(name, func(context.Context) (fabrin.Module, error) {
			built[name]++
			return route(name, path), nil
		})
	}

	app, err := fabrin.NewFromFactories(
		t.Context(),
		fabrin.Options{Modules: []string{"greet"}},
		factory("greet", "/greet"),
		factory("clock", "/time"),
	)
	if err != nil {
		t.Fatalf("NewFromFactories: %v", err)
	}

	if built["greet"] != 1 {
		t.Errorf("selected factory built %d times, want once", built["greet"])
	}
	if built["clock"] != 0 {
		t.Errorf("unselected factory built %d times, want zero", built["clock"])
	}
	if got := get(t, app, "/greet").Code; got != http.StatusOK {
		t.Errorf("/greet: selected factory's module must be mounted, got %d", got)
	}
	if got := get(t, app, "/time").Code; got != http.StatusNotFound {
		t.Errorf("/time: unselected factory's module must not be mounted, got %d", got)
	}
}

func TestNewFromFactories_RejectsUnknownSelectionBeforeBuilding(t *testing.T) {
	t.Parallel()

	built := 0
	_, err := fabrin.NewFromFactories(
		t.Context(),
		fabrin.Options{Modules: []string{"greet", "typo"}},
		fabrin.LazyModule("greet", func(context.Context) (fabrin.Module, error) {
			built++
			return route("greet", "/greet"), nil
		}),
	)

	if !errors.Is(err, fabrin.ErrUnknownModule) {
		t.Fatalf("unknown selection must return ErrUnknownModule, got %v", err)
	}
	if built != 0 {
		t.Errorf("selection must be validated before any factory runs, built %d module(s)", built)
	}
}

func TestNewFromFactories_RejectsDuplicateCatalogBeforeBuilding(t *testing.T) {
	t.Parallel()

	built := 0
	factory := func(path string) fabrin.ModuleFactory {
		return fabrin.LazyModule("blog", func(context.Context) (fabrin.Module, error) {
			built++
			return route("blog", path), nil
		})
	}

	_, err := fabrin.NewFromFactories(t.Context(), fabrin.Options{},
		factory("/a"),
		factory("/b"),
	)

	if !errors.Is(err, fabrin.ErrDuplicateModule) {
		t.Fatalf("duplicate factory names must return ErrDuplicateModule, got %v", err)
	}
	if built != 0 {
		t.Errorf("factory catalog must be validated before any factory runs, built %d module(s)", built)
	}
}

func TestNewFromFactories_PropagatesBuildErrorAndContext(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "request-scoped")
	boom := errors.New("open resource")

	_, err := fabrin.NewFromFactories(ctx, fabrin.Options{},
		fabrin.LazyModule("orders", func(got context.Context) (fabrin.Module, error) {
			if got.Value(contextKey{}) != "request-scoped" {
				t.Error("factory did not receive the construction context")
			}
			return nil, boom
		}),
	)

	if !errors.Is(err, boom) {
		t.Fatalf("factory error must remain identifiable through wrapping, got %v", err)
	}
	if !strings.Contains(err.Error(), "orders") {
		t.Errorf("factory error must name the module that failed, got %q", err)
	}
}

func TestNewFromFactories_RejectsFactoryNameMismatch(t *testing.T) {
	t.Parallel()

	_, err := fabrin.NewFromFactories(t.Context(), fabrin.Options{},
		fabrin.LazyModule("declared", func(context.Context) (fabrin.Module, error) {
			return route("returned", "/x"), nil
		}),
	)

	if err == nil {
		t.Fatal("a factory returning a differently named module must fail")
	}
	for _, name := range []string{"declared", "returned"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("name mismatch error must contain %q, got %q", name, err)
		}
	}
}

func TestNewFromFactories_PreservesFactoryOrderRatherThanSelectionOrder(t *testing.T) {
	t.Parallel()

	var built []string
	factory := func(name string) fabrin.ModuleFactory {
		return fabrin.LazyModule(name, func(context.Context) (fabrin.Module, error) {
			built = append(built, name)
			return route(name, "/"+name), nil
		})
	}

	app, err := fabrin.NewFromFactories(
		t.Context(),
		fabrin.Options{Modules: []string{"third", "first"}},
		factory("first"),
		factory("second"),
		factory("third"),
	)
	if err != nil {
		t.Fatalf("NewFromFactories: %v", err)
	}

	if got, want := strings.Join(built, ","), "first,third"; got != want {
		t.Errorf("factory build order = %q, want registration order %q", got, want)
	}
	if got, want := strings.Join(app.Modules(), ","), "first,third"; got != want {
		t.Errorf("mounted order = %q, want registration order %q", got, want)
	}
}

func TestNewFromFactories_RejectsInvalidFactory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		factory fabrin.ModuleFactory
	}{
		{name: "zero value", factory: fabrin.ModuleFactory{}},
		{name: "empty name", factory: fabrin.LazyModule("  ", func(context.Context) (fabrin.Module, error) {
			return route("unused", "/unused"), nil
		})},
		{name: "nil build", factory: fabrin.LazyModule("broken", nil)},
		{name: "nil module", factory: fabrin.LazyModule("broken", func(context.Context) (fabrin.Module, error) {
			return nil, nil
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := fabrin.NewFromFactories(t.Context(), fabrin.Options{}, tt.factory); err == nil {
				t.Fatal("invalid factory must fail construction")
			}
		})
	}
}
