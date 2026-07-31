package fabrin_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/cli"
)

// commanding is a module that contributes subcommands. It embeds testModule so
// the required half of the interface stays one line.
type commanding struct {
	testModule
	cmds []cli.Command
}

func (c commanding) Commands() []cli.Command { return c.cmds }

// commander builds a module named name whose single command is cmdName.
func commander(name, cmdName string, run func(context.Context, io.Writer, []string) error) fabrin.Module {
	return commanding{
		testModule: testModule{name: name, routes: func(fabrin.Router) {}},
		cmds: []cli.Command{{
			Name:  cmdName,
			Short: cmdName + " belongs to " + name,
			Run:   run,
		}},
	}
}

func TestApp_ExecuteRunsAModulesOwnCommand(t *testing.T) {
	t.Parallel()

	// Django's management commands, and the reason fabrin/cli exists as its own
	// package: a module contributes a subcommand to the app's binary without
	// knowing anything about argument parsing.
	ran := false
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		commander("billing", "reconcile", func(context.Context, io.Writer, []string) error {
			ran = true
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := app.Execute(t.Context(), &bytes.Buffer{}, []string{"reconcile"}); err != nil {
		t.Fatalf("Execute reconcile: %v", err)
	}
	if !ran {
		t.Error("the module's command never ran")
	}
}

func TestApp_ExecutePassesArgumentsAndErrorsThroughAModuleCommand(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("nothing to reconcile")
	var got []string

	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		commander("billing", "reconcile", func(_ context.Context, _ io.Writer, args []string) error {
			got = args
			return sentinel
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = app.Execute(t.Context(), &bytes.Buffer{}, []string{"reconcile", "--", "2026-01"})
	if !errors.Is(err, sentinel) {
		t.Errorf("a module command's error must reach the caller for errors.Is, got %v", err)
	}
	if !slices.Contains(got, "2026-01") {
		t.Errorf("positional arguments did not reach the command: %v", got)
	}
}

func TestApp_CapabilitiesReportsCommander(t *testing.T) {
	t.Parallel()

	// A mistyped method name means the interface is silently not satisfied and the
	// module simply never contributes its commands. Capabilities is the one place
	// that answers "what did this module actually contribute".
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		commander("billing", "reconcile", func(context.Context, io.Writer, []string) error { return nil }),
		route("plain", "/plain"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	caps := app.Capabilities()
	if !slices.Contains(caps["billing"], "Commander") {
		t.Errorf("billing capabilities = %v, want Commander among them", caps["billing"])
	}
	if slices.Contains(caps["plain"], "Commander") {
		t.Errorf("a module with no Commands() must not report Commander: %v", caps["plain"])
	}
}

func TestApp_ExecuteListsModuleCommandsInUsage(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		commander("billing", "reconcile", func(context.Context, io.Writer, []string) error { return nil }),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out bytes.Buffer
	if err := app.Execute(t.Context(), &out, []string{"help"}); err != nil {
		t.Fatalf("Execute help: %v", err)
	}
	for _, want := range []string{"reconcile", "routes", "serve", "version"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage missing %q:\n%s", want, out.String())
		}
	}
}

// ── Collection follows process slicing ──────────────────────────────────────

func TestNew_CollectsCommandsFromMountedModulesOnly(t *testing.T) {
	t.Parallel()

	// A module this process did not mount contributes no commands, for the same
	// reason it contributes no readiness checks: it registered nothing, and
	// offering its command would advertise work this process cannot do.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", Modules: []string{"blog"}},
		route("blog", "/posts"),
		commander("billing", "reconcile", func(context.Context, io.Writer, []string) error {
			t.Error("an unmounted module's command must never run")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = app.Execute(t.Context(), &bytes.Buffer{}, []string{"reconcile"})
	if err == nil {
		t.Fatal("an unmounted module's command must not be dispatchable")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("expected an unknown-command error, got: %v", err)
	}
}

// ── Collisions are wiring-time errors ───────────────────────────────────────

func TestNew_RejectsAModuleCommandThatShadowsABuiltIn(t *testing.T) {
	t.Parallel()

	// Discovered at dispatch, a shadowed built-in is a `routes` that suddenly does
	// something else — and which of the two wins depends on slice order. Wiring
	// time is the only cheap moment.
	for _, name := range []string{"routes", "serve", "version"} {
		_, err := fabrin.New(
			fabrin.Options{Addr: "127.0.0.1:0"},
			commander("billing", name, func(context.Context, io.Writer, []string) error { return nil }),
		)
		if err == nil {
			t.Errorf("a module declaring the built-in %q must fail at construction", name)
			continue
		}
		if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "billing") {
			t.Errorf("the error must name the command and the module, got: %v", err)
		}
	}
}

func TestNew_RejectsTwoModulesDeclaringOneCommand(t *testing.T) {
	t.Parallel()

	_, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		commander("billing", "sync", func(context.Context, io.Writer, []string) error { return nil }),
		commander("inventory", "sync", func(context.Context, io.Writer, []string) error { return nil }),
	)
	if err == nil {
		t.Fatal("two modules claiming one command name must fail at construction")
	}
	for _, want := range []string{"billing", "inventory", "sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name both modules and the command, missing %q: %v", want, err)
		}
	}
}

func TestNew_RejectsACommandWithNoName(t *testing.T) {
	t.Parallel()

	// Unreachable rather than harmless: Dispatch selects by name, so a nameless
	// command can never be invoked and its absence looks like a bug in Fabrin.
	_, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		commander("billing", "", func(context.Context, io.Writer, []string) error { return nil }),
	)
	if err == nil {
		t.Fatal("a command with an empty name must fail at construction")
	}
	if !strings.Contains(err.Error(), "billing") {
		t.Errorf("the error must name the module, got: %v", err)
	}
}
