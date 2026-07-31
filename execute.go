package fabrin

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/usefabrin/fabrin/cli"
)

// Route is one mounted route and the module that mounted it.
//
// Module is empty for a route the framework mounted itself — /healthz and
// /readyz, which are there without any module opting in.
type Route struct {
	Method string
	Path   string
	Module string
}

// routeKey identifies a route the way Gin's own router does: a method and a
// path together, since the same path under two methods is two routes.
func routeKey(method, path string) string { return method + " " + path }

// Routes reports every route this process mounts, sorted by path then method.
//
// Sorted, because Gin builds its own listing by walking one radix tree per HTTP
// method: the order is neither registration order nor alphabetical, and it can
// change when an unrelated route is added. Unstable output makes the `routes`
// command useless for the thing people reach for it — diffing two deployments
// against each other.
//
// Only what THIS process mounts. Under a selection, a module that was not
// mounted registered nothing and appears nowhere, which is the honest answer:
// listing the binary's full catalogue would be actively misleading in exactly
// the deployment shape process slicing exists to serve.
func (a *App) Routes() []Route {
	infos := a.engine.Routes()

	out := make([]Route, 0, len(infos))
	for _, ri := range infos {
		out = append(out, Route{
			Method: ri.Method,
			Path:   ri.Path,
			Module: a.routeOwner[routeKey(ri.Method, ri.Path)],
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// Execute runs the command named by args, or serves when args is empty.
//
// This is what a Fabrin application's main hands its arguments to. Go compiles,
// so a separate tool cannot introspect an application it did not build — the
// binary that already has the modules linked in is the only one that can answer
// "which module owns this URL", and this is where it answers.
//
// out is a parameter rather than os.Stdout so that a caller embedding Fabrin's
// commands in a larger tool can capture them, and so that the output is testable
// without swapping globals.
//
// # No arguments means serve
//
// Deliberately, and it must stay that way. Anything else silently changes what
// `./myapp` does the moment its main switches from Run to Execute: a container
// that used to serve would print usage and exit 0, which every orchestrator
// reads as a successful run.
//
// # A leading flag is a setting, not a command
//
// config.Standard() parses os.Args for -addr, -log-level and the rest, and Go's
// flag package stops at the first positional argument. So `./myapp -addr :9000`
// arrives here as ["-addr", ":9000"] with the value already applied, and
// dispatching it would report `unknown command "-addr"` for an invocation that
// worked before this method existed. It serves, which is what it did before.
//
// The two layers do not fight, because a subcommand is a positional: config's
// parser stops at `routes` and never sees the flags after it.
func (a *App) Execute(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return a.Run(ctx)
	}
	return cli.Dispatch(ctx, out, a.commands(), args)
}

// commands is what Dispatch selects from: the built-ins plus whatever the
// mounted modules contributed, collected once at construction.
func (a *App) commands() []cli.Command {
	return append(a.builtins(), a.moduleCommands...)
}

// builtins is the set Fabrin owns. It is also the list collectCommands reads to
// find reserved names — deliberately, rather than a second slice of strings that
// could drift from what is actually dispatchable.
//
// It takes no writer: each command receives one from Dispatch, so there is
// nothing to capture here and no chance of a command printing somewhere other
// than where the caller asked.
func (a *App) builtins() []cli.Command {
	return []cli.Command{
		{
			Name:  "serve",
			Short: "serve HTTP until the context is cancelled or a signal arrives",
			Run:   func(ctx context.Context, _ io.Writer, _ []string) error { return a.Run(ctx) },
		},
		{
			Name:  "routes",
			Short: "list mounted routes with the module that owns each",
			Run:   func(_ context.Context, out io.Writer, _ []string) error { return a.printRoutes(out) },
		},
		{
			Name:  "version",
			Short: "print the Fabrin version this binary was built against",
			Run:   func(_ context.Context, out io.Writer, _ []string) error { return printVersion(out) },
		},
	}
}

// collectCommands gathers every mounted module's commands and rejects a name
// collision at construction.
//
// MOUNTED modules, from the registry rather than from New's arguments. A module
// this process did not mount registered nothing, and offering its command would
// advertise work this process cannot do — the same process-slicing rule
// collectChecks follows for readiness.
//
// A collision is a wiring mistake whose only cheap moment of discovery is now.
// Found at dispatch instead, a shadowed `routes` is a command that suddenly does
// something else, and which of the two wins depends on slice order — correct on
// the machine where it was written.
func (a *App) collectCommands() error {
	// Reserved names come from builtins() itself, not from a second list. One
	// source, so the check cannot drift from what is actually dispatchable.
	owner := make(map[string]string)
	for _, c := range a.builtins() {
		owner[c.Name] = "" // empty means Fabrin owns it
	}

	for _, m := range a.registry.modules {
		cmd, ok := m.(Commander)
		if !ok {
			continue
		}

		for _, c := range cmd.Commands() {
			if strings.TrimSpace(c.Name) == "" {
				// Unreachable rather than harmless: Dispatch selects by name, so a
				// nameless command can never be invoked, and its absence reads as a
				// bug in Fabrin rather than in the module.
				return fmt.Errorf("fabrin: module %q declares a command with an empty name", m.Name())
			}

			if prev, taken := owner[c.Name]; taken {
				if prev == "" {
					return fmt.Errorf("fabrin: module %q declares command %q, which is a Fabrin built-in — rename the module's command", m.Name(), c.Name)
				}
				return fmt.Errorf("fabrin: modules %q and %q both declare command %q", prev, m.Name(), c.Name)
			}

			owner[c.Name] = m.Name()
			a.moduleCommands = append(a.moduleCommands, c)
		}
	}
	return nil
}

func (a *App) printRoutes(out io.Writer) error {
	routes := a.Routes()

	// Column widths from the data, not a guess: a path long enough to overflow a
	// fixed column turns the listing into ragged noise precisely when it is
	// longest and most needed.
	methodWidth, pathWidth := 0, 0
	for _, r := range routes {
		methodWidth = max(methodWidth, len(r.Method))
		pathWidth = max(pathWidth, len(r.Path))
	}

	for _, r := range routes {
		owner := r.Module
		if owner == "" {
			owner = "(framework)"
		}
		if _, err := fmt.Fprintf(out, "%-*s  %-*s  %s\n",
			methodWidth, r.Method, pathWidth, r.Path, owner); err != nil {
			return err
		}
	}
	return nil
}

// printVersion reports the Fabrin version from build info rather than from a
// constant.
//
// A constant is wrong from the first commit after someone forgets to bump it,
// and nothing fails when it is. Build info is filled in by the toolchain, so it
// cannot drift from what was actually linked.
func printVersion(out io.Writer) error {
	_, err := fmt.Fprintf(out, "fabrin %s\n", fabrinVersion())
	return err
}

func fabrinVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	// Building Fabrin itself — its tests, its examples — puts it in Main. Building
	// an application on top of it puts it in Deps.
	if info.Main.Path == modulePath && info.Main.Version != "" {
		return info.Main.Version
	}
	for _, d := range info.Deps {
		if d.Path != modulePath {
			continue
		}
		// A `replace` directive means the linked code is not the version the
		// module graph names, and saying so is the whole point of reporting a
		// version at all.
		if d.Replace != nil {
			return d.Replace.Version + " (replaced)"
		}
		return d.Version
	}
	return "unknown"
}

const modulePath = "github.com/usefabrin/fabrin"
