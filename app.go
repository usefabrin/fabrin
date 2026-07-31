package fabrin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gin-gonic/gin"

	"github.com/usefabrin/fabrin/config"
	"github.com/usefabrin/fabrin/health"
	"github.com/usefabrin/fabrin/logging"
)

// Options configures an [App]. It is an ALIAS for [config.Options], not a
// separate type, so a value from [config.Load] passes straight to [New] with no
// mapping layer.
//
// The declaration lives in fabrin/config because the boundary rules forbid that
// package from importing this one — settings must load from a CLI, a test, or a
// migrate-only process without constructing an HTTP stack. Declaring Options here
// would make the obvious signature, config.Load() (fabrin.Options, error), a rule
// violation. Settings are a lower-level concern than the application, so the
// dependency runs root → config and the alias keeps this spelling working.
type Options = config.Options

// Defaults applied by [New] when the corresponding [Options] field is zero.
// Aliases of the config package's declarations, so there is one list rather than
// two that can disagree.
const (
	// DefaultAddr is the listen address. FABRIN_ADDR overrides it.
	DefaultAddr = config.DefaultAddr

	// DefaultShutdownTimeout bounds how long [App.Run] waits for in-flight
	// requests after a shutdown signal.
	DefaultShutdownTimeout = config.DefaultShutdownTimeout

	// DefaultReadHeaderTimeout guards against a client that opens a connection and
	// sends headers slowly, holding a goroutine indefinitely.
	DefaultReadHeaderTimeout = config.DefaultReadHeaderTimeout
)

// App is a Fabrin application: a set of mounted modules, an HTTP server, and the
// lifecycle that ties them together.
//
// Construct one with [New] and run it with [App.Run].
type App struct {
	opts     Options
	registry *registry
	engine   *gin.Engine

	mu      sync.Mutex
	running bool
	// addr is the resolved listen address, known only after the listener binds.
	// Options.Addr may be ":0" or "127.0.0.1:0", where the kernel picks the port —
	// which is what lets tests and the example smoke script run concurrently
	// without colliding.
	addr string
}

// New builds an App from opts and modules.
//
// It fails rather than warns when the module set cannot produce a working app:
// no modules, an empty or duplicated module name, or a selection naming something
// unregistered. Each of those is a wiring mistake whose only cheap moment of
// discovery is now — at first request, or worse, in production, the same mistake
// presents as a mysterious 404.
func New(opts Options, modules ...Module) (*App, error) {
	opts = opts.WithDefaults()

	// WithDefaults deliberately leaves Logger nil: its default depends on
	// LogFormat and LogLevel, and fabrin/config may not import fabrin/logging. This
	// is the layer that can see both, so it is the only place a logger is built.
	if opts.Logger == nil {
		// Stderr, matching what slog.Default() writes to, so redirecting Fabrin's
		// output is not a behaviour change for anyone already capturing it.
		opts.Logger = logging.New(os.Stderr, opts.LogFormat, opts.LogLevel)
	}

	reg, err := newRegistry(modules, opts.Modules)
	if err != nil {
		return nil, err
	}

	engine := gin.New()

	// Gin trusts all proxies by default, which makes ClientIP() spoofable by any
	// client sending X-Forwarded-For. Nil here means trust none.
	if err := engine.SetTrustedProxies(opts.TrustedProxies); err != nil {
		return nil, fmt.Errorf("fabrin: trusted proxies: %w", err)
	}

	// Order is load-bearing in all three positions. Recovery outermost, so a panic
	// in either of the others still produces a response. RequestID before Logger,
	// because Logger reads the id off the request context — reversed, it silently
	// logs every request without one, and nothing fails.
	engine.Use(gin.Recovery())
	engine.Use(logging.RequestID())
	engine.Use(logging.Logger(opts.Logger))

	checks, err := collectChecks(reg)
	if err != nil {
		return nil, err
	}

	// Mounted before the module loop and without any module opting in: an
	// orchestrator's probes must work against a stock app. A module that declares
	// /healthz itself now panics here, at wiring time, naming the path — which is
	// the right moment to find out, and is Gin's behaviour rather than ours.
	health.Mount(engine, checks)

	app := &App{opts: opts, registry: reg, engine: engine}

	for _, m := range reg.modules {
		// Each module gets a root-level group rather than the engine itself, so it
		// can Use middleware scoped to its own routes without affecting others.
		m.Routes(engine.Group(""))
	}

	opts.Logger.Debug("fabrin: modules mounted",
		"modules", reg.names(),
		"capabilities", reg.capabilities,
		"checks", checks.Len())

	return app, nil
}

// collectChecks builds the readiness registry from the MOUNTED modules that
// implement [Checker].
//
// Mounted, not registered: with a selection in effect, a module this process did
// not mount owns nothing here, and gating this process's traffic on its
// dependency would make one deployment shape fail for a reason it has no stake
// in. That is process slicing applied to readiness.
func collectChecks(reg *registry) (*health.Registry, error) {
	checks := health.NewRegistry()
	for _, m := range reg.modules {
		c, ok := m.(Checker)
		if !ok {
			continue
		}
		// Propagated rather than logged: an unnamed check is a wiring mistake, and
		// New fails on wiring mistakes instead of warning about them. Discovered at
		// 3am from a readiness body that names no culprit, it is much more expensive.
		if err := checks.Register(m.Name(), c.Checks()...); err != nil {
			return nil, fmt.Errorf("fabrin: register health checks for module %q: %w", m.Name(), err)
		}
	}
	return checks, nil
}

// Options returns the options in effect, with defaults applied.
func (a *App) Options() Options { return a.opts }

// Handler returns the HTTP handler serving the mounted modules.
//
// Exposed so tests can drive the app with net/http/httptest without binding a
// port, and so a consumer can mount Fabrin inside an existing server.
func (a *App) Handler() http.Handler { return a.engine }

// Engine returns the underlying Gin engine.
//
// Provided because Gin is public: hiding the engine while aliasing its Context
// would be a half-measure that only blocks legitimate uses. Reconfiguring it
// after New is your responsibility.
func (a *App) Engine() *gin.Engine { return a.engine }

// Modules returns the names of the MOUNTED modules, in registration order. With a
// selection in effect this is a subset of what was registered.
func (a *App) Modules() []string { return a.registry.names() }

// Capabilities reports which optional [Module] interfaces each mounted module
// satisfied, keyed by module name.
//
// This exists because type assertion fails silently. A Start method with a
// mistyped signature does not satisfy [Lifecycle], and nothing anywhere reports
// it — the module simply never starts. Capabilities makes that inspectable, and
// `fabrin routes` will print it.
func (a *App) Capabilities() map[string][]string {
	out := make(map[string][]string, len(a.registry.capabilities))
	for k, v := range a.registry.capabilities {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Addr returns the resolved listen address once the server is listening, or "".
//
// Not the same as Options().Addr: with a port of 0 the kernel assigns one, and
// this is how the caller discovers which.
func (a *App) Addr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

// Start runs every [Lifecycle] module's Start in registration order. If one
// fails, those already started are stopped in reverse order.
//
// [App.Run] calls this; call it directly only when embedding Fabrin's handler in
// a server you own.
func (a *App) Start(ctx context.Context) error { return a.registry.start(ctx) }

// Stop runs every [Lifecycle] module's Stop in REVERSE registration order, so a
// module can rely on the modules it was built after still being alive while it
// shuts down. Every module is stopped even if one fails, and the errors are
// joined.
func (a *App) Stop(ctx context.Context) error { return a.registry.stop(ctx) }

// Run starts the modules, serves HTTP, and shuts down gracefully when ctx is
// cancelled or the process receives SIGINT or SIGTERM.
//
// It returns nil on a clean shutdown — including shutdown caused by cancelling
// ctx. Returning ctx.Err() there would make every intentional stop look like a
// failure to a caller deciding an exit code.
func (a *App) Run(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return ErrAlreadyRunning
	}
	a.running = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.running = false
		a.addr = ""
		a.mu.Unlock()
	}()

	log := a.opts.Logger

	// NotifyContext rather than a signal channel: the second SIGINT reverts to the
	// default handler, so an impatient operator can always force an exit even if
	// shutdown wedges.
	ctx, stopSignals := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	if err := a.Start(ctx); err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           a.engine,
		ReadHeaderTimeout: a.opts.ReadHeaderTimeout,
	}

	// Listening explicitly rather than via ListenAndServe: binding must happen
	// before Run reports readiness, and the resolved address is only knowable from
	// the listener when the requested port is 0.
	ln, err := net.Listen("tcp", a.opts.Addr)
	if err != nil {
		return errors.Join(fmt.Errorf("fabrin: listen %s: %w", a.opts.Addr, err), a.Stop(ctx))
	}

	a.mu.Lock()
	a.addr = ln.Addr().String()
	a.mu.Unlock()

	log.Info("fabrin: serving", "addr", ln.Addr().String(), "modules", a.registry.names())

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		// ErrServerClosed is the expected result of our own Shutdown call.
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		// The server died on its own. Still unwind the modules.
		return errors.Join(err, a.Stop(context.WithoutCancel(ctx)))

	case <-ctx.Done():
		log.Info("fabrin: shutting down", "timeout", a.opts.ShutdownTimeout)
	}

	// ctx is already cancelled, so shutdown needs a fresh deadline of its own —
	// derived from it would expire immediately and drop every in-flight request.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.opts.ShutdownTimeout)
	defer cancel()

	var errs []error
	if err := srv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("fabrin: http shutdown: %w", err))
	}
	if err := <-serveErr; err != nil {
		errs = append(errs, err)
	}
	// Modules stop after the server, so no handler is still using what they own.
	if err := a.Stop(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("fabrin: stop modules: %w", err))
	}

	if err := errors.Join(errs...); err != nil {
		return err
	}
	log.Info("fabrin: stopped")
	return nil
}
