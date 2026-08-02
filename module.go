package fabrin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/usefabrin/fabrin/cli"
	"github.com/usefabrin/fabrin/health"
	"github.com/usefabrin/fabrin/orm"
)

// Module is Fabrin's answer to Django's INSTALLED_APPS: a self-contained slice of
// an application that owns its own routes and, optionally, its own models,
// migrations, commands, and health checks.
//
// The required interface is two methods. Everything else a module can contribute
// is an OPTIONAL interface, discovered by type assertion at registration — so a
// module that owns no resources never has to write an empty Start.
//
// # Modules never import each other
//
// A module that needs something another module owns declares the interface it
// needs in its own package, and the wiring in main passes in whatever satisfies
// it:
//
//	// inside the blog module — it does not know who provides this
//	type Clock interface{ Now() time.Time }
//
//	func New(clock Clock) *Blog { return &Blog{clock: clock} }
//
// That interface is the seam along which the module can later move into its own
// process. A direct import welds the two together permanently, and the weld is
// invisible until someone tries to split them. Fabrin therefore provides no
// service locator or global registry for fetching one module from another.
type Module interface {
	// Name identifies the module. It must be unique and non-empty: it is how
	// FABRIN_MODULES selects the module and how a failing health check names it.
	Name() string

	// Routes mounts the module's HTTP routes.
	Routes(r Router)
}

// ModuleFactory is an immutable, named module construction plan.
//
// Its fields are private so the representation can evolve without breaking
// callers. Create values with [LazyModule] and pass them to
// [NewFromFactories]. The zero value is invalid.
type ModuleFactory struct {
	name  string
	build func(context.Context) (Module, error)
}

// LazyModule declares how to build one named module after process selection.
//
// build runs exactly once when name is selected, and not at all when it is not.
// It receives the construction context and must return a module whose [Module.Name]
// matches name. Dependencies stay explicit and compile-time checked by capturing
// typed values or constructors in build; Fabrin provides no service locator.
//
// Long-lived I/O belongs in [Lifecycle.Start], not build. That keeps an error
// from a later factory or from [New] from leaking a resource that no App owns.
// Validation is deferred to [NewFromFactories] so all wiring failures follow the
// same error-returning construction path.
func LazyModule(name string, build func(context.Context) (Module, error)) ModuleFactory {
	return ModuleFactory{name: name, build: build}
}

// Checker is an optional [Module] interface for a module that has a dependency
// whose availability decides whether this process should receive traffic.
//
// The checks contribute to /readyz, never to /healthz. See [health] for why the
// two must not be conflated: readiness answers "should I get traffic", and
// liveness answers "would restarting help".
type Checker interface {
	Checks() []health.Check
}

// Commander is an optional [Module] interface for a module contributing
// subcommands to the application's own binary. It is Fabrin's answer to Django's
// management commands.
//
// The commands are collected from the MOUNTED modules only, and a name that
// collides with a built-in or with another module's command is an error at
// construction — see [App.Execute].
type Commander interface {
	Commands() []cli.Command
}

// Modeler is an optional [Module] interface for a module declaring the tables it
// owns. It is Fabrin's answer to Django's models being discovered from
// INSTALLED_APPS.
//
// Registration is explicit: Fabrin never scans packages for models. Django gets
// away with discovery because a Python import has side effects at runtime; the Go
// equivalent is a blank import whose absence is silent, and a model that quietly
// fails to register is a table the migration generator proposes to DROP.
//
// The models are collected from the MOUNTED modules only, and two modules
// claiming one table name is an error at construction — see [App.Models].
type Modeler interface {
	Models() []orm.Model
}

// Lifecycle is an optional [Module] interface for a module owning a resource that
// must be opened before serving and closed after.
//
// Start runs in registration order; Stop runs in REVERSE registration order, so a
// module can rely on the modules it was built after still being alive while it
// shuts down.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Errors returned by [New], [NewFromFactories], and [App.Run]. They are
// sentinels so callers can branch on them with errors.Is rather than matching
// message text — a caller matching on a string is a caller broken by editing
// that string.
var (
	// ErrDuplicateModule means two modules claim the same name, which would make
	// one module's routes silently shadow the other's.
	ErrDuplicateModule = errors.New("fabrin: duplicate module name")

	// ErrUnknownModule means a module selection named something not registered.
	ErrUnknownModule = errors.New("fabrin: unknown module in selection")

	// ErrNoModules means no modules or module factories were passed to an App
	// constructor, so the app would serve nothing.
	ErrNoModules = errors.New("fabrin: no modules registered")

	// ErrAlreadyRunning means Run was called on an app that is already serving.
	ErrAlreadyRunning = errors.New("fabrin: app is already running")
)

// registry holds the mounted modules in registration order. Order is
// load-bearing: Start walks it forwards and Stop walks it backwards.
type registry struct {
	modules []Module

	// capabilities records which optional interfaces each module satisfied, keyed
	// by module name. Type assertion is silent when it fails — a Start method with
	// a mistyped signature means the module simply never starts, with no error
	// anywhere — so what matched has to be inspectable.
	capabilities map[string][]string
}

// newRegistry validates the module set, applies the selection, and records
// capabilities.
//
// selection is the value of Options.Modules: nil or empty means mount everything.
func newRegistry(modules []Module, selection []string) (*registry, error) {
	if len(modules) == 0 {
		return nil, ErrNoModules
	}

	byName := make(map[string]Module, len(modules))
	order := make([]string, 0, len(modules))

	for i, m := range modules {
		name := m.Name()
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("fabrin: module at position %d has an empty name: it could not be selected by FABRIN_MODULES or named in a health check", i)
		}
		if _, dup := byName[name]; dup {
			return nil, fmt.Errorf("%w: %q registered twice — one module's routes would silently shadow the other's", ErrDuplicateModule, name)
		}
		byName[name] = m
		order = append(order, name)
	}

	wanted, err := resolveSelection(order, selection)
	if err != nil {
		return nil, err
	}

	r := &registry{
		modules:      make([]Module, 0, len(wanted)),
		capabilities: make(map[string][]string, len(wanted)),
	}
	for _, name := range wanted {
		m := byName[name]
		r.modules = append(r.modules, m)
		r.capabilities[name] = capabilitiesOf(m)
	}
	return r, nil
}

// buildSelectedModules validates the complete factory catalogue before it
// invokes anything, then builds the selected set in caller-supplied registration
// order. Validation first is load-bearing: a typo must not partially open a
// deployment shape before startup fails.
func buildSelectedModules(ctx context.Context, factories []ModuleFactory, selection []string) ([]Module, error) {
	if len(factories) == 0 {
		return nil, ErrNoModules
	}

	byName := make(map[string]ModuleFactory, len(factories))
	order := make([]string, 0, len(factories))
	for i, factory := range factories {
		name := factory.name
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("fabrin: module factory at position %d has an empty name: it could not be selected by FABRIN_MODULES", i)
		}
		if factory.build == nil {
			return nil, fmt.Errorf("fabrin: module factory %q has no build function", name)
		}
		if _, dup := byName[name]; dup {
			return nil, fmt.Errorf("%w: factory %q registered twice", ErrDuplicateModule, name)
		}
		byName[name] = factory
		order = append(order, name)
	}

	wanted, err := resolveSelection(order, selection)
	if err != nil {
		return nil, err
	}

	modules := make([]Module, 0, len(wanted))
	for _, name := range wanted {
		module, err := byName[name].build(ctx)
		if err != nil {
			return nil, fmt.Errorf("fabrin: build module %q: %w", name, err)
		}
		if module == nil {
			return nil, fmt.Errorf("fabrin: module factory %q returned nil", name)
		}
		if got := module.Name(); got != name {
			return nil, fmt.Errorf("fabrin: module factory %q returned module named %q", name, got)
		}
		modules = append(modules, module)
	}
	return modules, nil
}

// resolveSelection returns the module names to mount, in registration order.
//
// Registration order is preserved rather than the order given in the selection:
// Stop runs in reverse registration order, and honouring the selection's order
// instead would make shutdown order depend on how someone spelled an environment
// variable.
func resolveSelection(registered []string, selection []string) ([]string, error) {
	// Callers can pass a trailing empty element from splitting an empty env var.
	clean := make([]string, 0, len(selection))
	for _, s := range selection {
		if s = strings.TrimSpace(s); s != "" {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return registered, nil
	}

	inRegistered := make(map[string]bool, len(registered))
	for _, n := range registered {
		inRegistered[n] = true
	}

	var unknown []string
	want := make(map[string]bool, len(clean))
	for _, s := range clean {
		if !inRegistered[s] {
			unknown = append(unknown, s)
			continue
		}
		want[s] = true
	}

	// Failing here rather than mounting the subset that did match: a process that
	// starts, passes its liveness probe, and serves nothing is the worst available
	// outcome, because nothing about it looks wrong.
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%w: %s not registered (registered: %s)",
			ErrUnknownModule,
			strings.Join(quoteAll(unknown), ", "),
			strings.Join(quoteAll(registered), ", "))
	}

	out := make([]string, 0, len(want))
	for _, n := range registered {
		if want[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// capabilitiesOf reports which optional Module interfaces m satisfies.
//
// Reserved for later milestones: Migrator (F2), Subscriber (F6). Each is added
// here as its interface lands, so Capabilities stays the one place that answers
// "what did this module actually contribute".
func capabilitiesOf(m Module) []string {
	var caps []string
	if _, ok := m.(Checker); ok {
		caps = append(caps, "Checker")
	}
	if _, ok := m.(Lifecycle); ok {
		caps = append(caps, "Lifecycle")
	}
	if _, ok := m.(Commander); ok {
		caps = append(caps, "Commander")
	}
	if _, ok := m.(Modeler); ok {
		caps = append(caps, "Modeler")
	}
	sort.Strings(caps)
	return caps
}

// names returns the mounted module names in registration order.
func (r *registry) names() []string {
	out := make([]string, len(r.modules))
	for i, m := range r.modules {
		out[i] = m.Name()
	}
	return out
}

// start runs every Lifecycle module's Start in registration order. If one fails,
// the modules already started are stopped in reverse order before returning —
// leaving them running would leak whatever they own with nothing left holding a
// reference to close it.
func (r *registry) start(ctx context.Context) error {
	var started []Lifecycle

	for _, m := range r.modules {
		lc, ok := m.(Lifecycle)
		if !ok {
			continue
		}
		if err := lc.Start(ctx); err != nil {
			// Joined rather than discarded: if unwinding also fails, the caller is
			// looking at two problems and needs to see both. Reporting only the
			// original hides a resource that is now stuck open.
			return errors.Join(
				fmt.Errorf("fabrin: start module %q: %w", m.Name(), err),
				stopAll(ctx, started),
			)
		}
		started = append(started, lc)
	}
	return nil
}

// stop runs every Lifecycle module's Stop in reverse registration order.
//
// Every module is stopped even if an earlier one fails, and the errors are
// joined: abandoning the loop on the first failure would leak every resource
// after it, which is precisely when cleanup matters most.
func (r *registry) stop(ctx context.Context) error {
	lcs := make([]Lifecycle, 0, len(r.modules))
	for _, m := range r.modules {
		if lc, ok := m.(Lifecycle); ok {
			lcs = append(lcs, lc)
		}
	}
	return stopAll(ctx, lcs)
}

// stopAll stops lcs in reverse order, joining every error.
func stopAll(ctx context.Context, lcs []Lifecycle) error {
	var errs []error
	for i := len(lcs) - 1; i >= 0; i-- {
		if err := lcs[i].Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
