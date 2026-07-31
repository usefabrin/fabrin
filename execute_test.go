package fabrin_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/health"
)

// ── CLI-004: route attribution ──────────────────────────────────────────────

func TestApp_RoutesAttributesEachRouteToItsModule(t *testing.T) {
	t.Parallel()

	// "Which module owns this URL" is the question `routes` exists to answer, and
	// it cannot be answered by reading the engine alone: Gin records a handler
	// name, which for a closure is an unhelpful `pkg.func1`.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		route("blog", "/posts"),
		route("shop", "/cart"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	owner := map[string]string{}
	for _, r := range app.Routes() {
		owner[r.Method+" "+r.Path] = r.Module
	}

	if got := owner["GET /posts"]; got != "blog" {
		t.Errorf("GET /posts owned by %q, want blog", got)
	}
	if got := owner["GET /cart"]; got != "shop" {
		t.Errorf("GET /cart owned by %q, want shop", got)
	}

	// Liveness and readiness are mounted before any module and without one opting
	// in, so attributing them to whichever module happened to mount first would be
	// a lie that only shows up when someone goes looking for the owner.
	for _, p := range []string{health.LivenessPath, health.ReadinessPath} {
		if got, ok := owner["GET "+p]; !ok {
			t.Errorf("%s missing from Routes()", p)
		} else if got != "" {
			t.Errorf("%s attributed to module %q, want the framework (empty)", p, got)
		}
	}
}

func TestApp_RoutesIsOrderedStably(t *testing.T) {
	t.Parallel()

	// Gin builds Routes() by walking one tree per HTTP method, so its order is
	// neither registration order nor sorted. Unstable output makes `routes`
	// useless for diffing two deployments against each other.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		testModule{name: "m", routes: func(r fabrin.Router) {
			r.POST("/zebra", func(*fabrin.Context) {})
			r.GET("/apple", func(*fabrin.Context) {})
			r.DELETE("/apple", func(*fabrin.Context) {})
		}},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := app.Routes()
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.Path > cur.Path || (prev.Path == cur.Path && prev.Method > cur.Method) {
			t.Fatalf("Routes() not sorted by path then method: %v then %v", prev, cur)
		}
	}
}

func TestApp_RoutesReflectsProcessSlicing(t *testing.T) {
	t.Parallel()

	// A module this process did not mount registered nothing, so it owns nothing.
	// `routes` must describe THIS process, not the binary's full catalogue —
	// otherwise it is actively misleading in exactly the deployment shape process
	// slicing exists to serve.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", Modules: []string{"blog"}},
		route("blog", "/posts"),
		route("shop", "/cart"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, r := range app.Routes() {
		if r.Module == "shop" || r.Path == "/cart" {
			t.Errorf("unmounted module's route present: %+v", r)
		}
	}
}

// ── CLI-005: Execute ────────────────────────────────────────────────────────

func TestApp_ExecuteRoutesListsRoutesWithOwningModule(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		route("blog", "/posts"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out bytes.Buffer
	if err := app.Execute(t.Context(), &out, []string{"routes"}); err != nil {
		t.Fatalf("Execute routes: %v", err)
	}

	for _, want := range []string{"GET", "/posts", "blog", health.LivenessPath} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("routes output missing %q:\n%s", want, out.String())
		}
	}
}

func TestApp_ExecuteWithNoArgumentsServes(t *testing.T) {
	t.Parallel()

	// The default has to stay `serve`. Anything else silently changes what an
	// existing `./myapp` does the moment its main() switches to Execute — a
	// container that used to serve would print usage and exit 0, which every
	// orchestrator reads as a successful run.
	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx, io.Discard, nil) }()

	if _, err := waitForAddr(app); err != nil {
		t.Fatalf("Execute with no arguments never listened: %v", err)
	}
	cancel()

	if err := <-done; err != nil {
		t.Errorf("clean shutdown must return nil, got %v", err)
	}
}

func TestApp_ExecuteTreatsALeadingFlagAsASettingAndServes(t *testing.T) {
	t.Parallel()

	// `./myapp -addr :9000` worked before Execute existed: config.Standard()
	// parses os.Args for it, and Go's flag package stops at the first positional
	// so a subcommand's own flags are never in its way. Execute must not turn
	// that invocation into `unknown command "-addr"`.
	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx, io.Discard, []string{"-addr", "127.0.0.1:0"}) }()

	if _, err := waitForAddr(app); err != nil {
		t.Fatalf("a leading flag must still serve: %v", err)
	}
	cancel()

	if err := <-done; err != nil {
		t.Errorf("clean shutdown must return nil, got %v", err)
	}
}

func TestApp_ExecuteServeCommandServes(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx, io.Discard, []string{"serve"}) }()

	if _, err := waitForAddr(app); err != nil {
		t.Fatalf("serve never listened: %v", err)
	}
	cancel()

	if err := <-done; err != nil {
		t.Errorf("clean shutdown must return nil, got %v", err)
	}
}

func TestApp_ExecuteVersionReportsTheModulePathAndVersion(t *testing.T) {
	t.Parallel()

	// Read from build info rather than a constant. A hardcoded version string is
	// wrong from the first commit after someone forgets to bump it, and nothing
	// fails when it is.
	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out bytes.Buffer
	if err := app.Execute(t.Context(), &out, []string{"version"}); err != nil {
		t.Fatalf("Execute version: %v", err)
	}
	if !strings.Contains(out.String(), "fabrin") {
		t.Errorf("version output should name Fabrin, got: %q", out.String())
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Error("version printed nothing")
	}
}

func TestApp_ExecuteRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("m", "/x"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out bytes.Buffer
	err = app.Execute(t.Context(), &out, []string{"rout"})
	if err == nil {
		t.Fatal("unknown command must be an error, or a typo exits 0 having done nothing")
	}
	if !strings.Contains(err.Error(), "routes") {
		t.Errorf("error should suggest the closest built-in, got: %v", err)
	}
}

// ── CLI-006: Gin's debug output must not land in a command's output ─────────

func TestNew_SilencesGinsDebugOutputUnlessDebugIsSet(t *testing.T) {
	// Not parallel: gin's mode and default writer are process-global, which is
	// also why New only sets the mode when GIN_MODE is unset.
	origMode, origWriter := gin.Mode(), gin.DefaultWriter
	t.Cleanup(func() { gin.SetMode(origMode); gin.DefaultWriter = origWriter })

	tests := []struct {
		name      string
		debug     bool
		wantNoise bool
	}{
		// Gin prints a warning banner and its whole route table on construction.
		// In a `routes` listing that is four lines of garbage above the answer; in
		// a container log it is a needless disclosure of internal handler paths.
		{"quiet by default", false, false},
		{"Debug opts back in", true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Gin's own zero configuration, which is what a user gets before
			// Fabrin has an opinion.
			gin.SetMode(gin.DebugMode)

			var buf bytes.Buffer
			gin.DefaultWriter = &buf

			if _, err := fabrin.New(
				fabrin.Options{Addr: "127.0.0.1:0", Debug: tc.debug},
				route("m", "/x"),
			); err != nil {
				t.Fatalf("New: %v", err)
			}

			if noisy := buf.Len() > 0; noisy != tc.wantNoise {
				t.Errorf("gin wrote %d bytes, want noise=%v:\n%s", buf.Len(), tc.wantNoise, buf.String())
			}
		})
	}
}

func TestNew_LeavesGinModeAloneWhenGINMODEIsSet(t *testing.T) {
	// A caller who set the environment variable has said something more specific
	// than an application default. gin.SetMode is process-global, so overriding
	// it would make one App's construction change another's.
	origMode := gin.Mode()
	t.Cleanup(func() { gin.SetMode(origMode) })

	t.Setenv(gin.EnvGinMode, gin.TestMode)
	gin.SetMode(gin.TestMode)

	// Debug: true would otherwise force DebugMode.
	if _, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0", Debug: true}, route("m", "/x")); err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := gin.Mode(); got != gin.TestMode {
		t.Errorf("GIN_MODE=test was overridden to %q", got)
	}
}

// ── Documented behaviour: a route collision is Gin's panic ──────────────────

func TestNew_PanicsWhenTwoModulesClaimTheSamePath(t *testing.T) {
	t.Parallel()

	// Not a feature — a record of what happens today, so the attribution code
	// above is not silently relying on something that might change. Gin panics at
	// registration, which is the right moment to find out but names only the path,
	// not the two modules that collided. Improving that is #40, deliberately not
	// this change: converting it to an error would reverse a decision New already
	// documents for the /healthz case.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("two modules claiming one path must not be silently accepted")
		}
		if !strings.Contains(toString(r), "/posts") {
			t.Errorf("the panic should name the colliding path, got: %v", r)
		}
	}()

	_, _ = fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		route("blog", "/posts"),
		route("shop", "/posts"),
	)
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return ""
}
