package cli_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin/cli"
)

// noop is a command that records nothing, for cases where only dispatch matters.
func noop(name string) cli.Command {
	return cli.Command{
		Name:  name,
		Short: name + " does a thing",
		Run:   func(context.Context, []string) error { return nil },
	}
}

func TestDispatch_SelectsByNameAndPassesPositionalArgs(t *testing.T) {
	t.Parallel()

	var got []string
	cmds := []cli.Command{
		noop("serve"),
		{
			Name:  "routes",
			Short: "list routes",
			Run: func(_ context.Context, args []string) error {
				got = args
				return nil
			},
		},
	}

	err := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"routes", "one", "two"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("positional args = %v, want [one two]", got)
	}
}

func TestDispatch_ParsesFlagsIntoTheCommandsOwnSet(t *testing.T) {
	t.Parallel()

	// The flag set belongs to the command, not to the process: two commands may
	// both define -format without colliding, and nothing is registered globally.
	var format string
	var rest []string

	cmds := []cli.Command{{
		Name:  "routes",
		Short: "list routes",
		Flags: func(fs *flag.FlagSet) { fs.StringVar(&format, "format", "text", "output format") },
		Run: func(_ context.Context, args []string) error {
			rest = args
			return nil
		},
	}}

	err := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"routes", "-format", "json", "trailing"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if format != "json" {
		t.Errorf("format = %q, want json", format)
	}
	if len(rest) != 1 || rest[0] != "trailing" {
		t.Errorf("args after flags = %v, want [trailing]", rest)
	}
}

func TestDispatch_RejectsUnknownCommandNamingTheClosestMatch(t *testing.T) {
	t.Parallel()

	// An unknown name is an error, never a silent no-op — the same rule
	// FABRIN_MODULES follows for an unknown module. A typo that exits 0 is the
	// worst outcome: the user believes the command ran.
	cmds := []cli.Command{noop("routes"), noop("serve"), noop("version")}

	err := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"route"})
	if err == nil {
		t.Fatal("unknown command must be an error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "route") {
		t.Errorf("error must name what was typed, got: %s", msg)
	}
	if !strings.Contains(msg, "routes") {
		t.Errorf("error must suggest the closest match, got: %s", msg)
	}
	for _, name := range []string{"serve", "version"} {
		if !strings.Contains(msg, name) {
			t.Errorf("error must list %q among the known commands, got: %s", name, msg)
		}
	}
}

func TestDispatch_NoArgsWritesUsageAndSucceeds(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cmds := []cli.Command{noop("routes"), noop("serve")}

	if err := cli.Dispatch(t.Context(), &out, cmds, nil); err != nil {
		t.Fatalf("no args should print usage and succeed, got: %v", err)
	}

	for _, want := range []string{"routes", "routes does a thing", "serve"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage must mention %q, got:\n%s", want, out.String())
		}
	}
}

func TestDispatch_HelpFlagWritesUsageAndSucceeds(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"-h", "--help", "help"} {
		var out bytes.Buffer
		if err := cli.Dispatch(t.Context(), &out, []cli.Command{noop("routes")}, []string{arg}); err != nil {
			t.Errorf("%s should print usage and succeed, got: %v", arg, err)
		}
		if !strings.Contains(out.String(), "routes") {
			t.Errorf("%s printed no usage, got: %q", arg, out.String())
		}
	}
}

func TestDispatch_PropagatesRunErrorForErrorsIs(t *testing.T) {
	t.Parallel()

	// Callers branch on the error, so it must survive the trip. Flattening it to
	// a message forces string matching, which breaks the first time the wording
	// improves.
	sentinel := errors.New("database unreachable")
	cmds := []cli.Command{{
		Name: "migrate",
		Run:  func(context.Context, []string) error { return sentinel },
	}}

	err := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"migrate"})
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is must find the command's own error, got: %v", err)
	}
}

func TestDispatch_RejectsDuplicateNames(t *testing.T) {
	t.Parallel()

	// Two commands answering to one name means one of them silently never runs.
	// Which one depends on slice order, which is the worst kind of bug: correct
	// on the machine where it was written.
	cmds := []cli.Command{noop("routes"), noop("serve"), noop("routes")}

	err := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"serve"})
	if err == nil {
		t.Fatal("duplicate command names must be an error even when dispatching a third command")
	}
	if !strings.Contains(err.Error(), "routes") {
		t.Errorf("error must name the duplicate, got: %s", err)
	}
}

func TestDispatch_PassesTheContextThrough(t *testing.T) {
	t.Parallel()

	type key struct{}
	ctx := context.WithValue(t.Context(), key{}, "carried")

	var seen any
	cmds := []cli.Command{{
		Name: "serve",
		Run: func(ctx context.Context, _ []string) error {
			seen = ctx.Value(key{})
			return nil
		},
	}}

	if err := cli.Dispatch(ctx, io.Discard, cmds, []string{"serve"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if seen != "carried" {
		t.Errorf("context value = %v, want carried — cancellation must reach the command", seen)
	}
}

func TestDispatch_WritesNothingToStderrOnAFlagParseError(t *testing.T) {
	// Not parallel: it swaps os.Stderr.
	//
	// flag.ContinueOnError stops the FlagSet calling os.Exit, but it still prints
	// the error and the usage to the set's output, which defaults to os.Stderr.
	// A library writing to a stream it does not own is a defect on its own, and
	// here it also duplicates whatever the caller does with the returned error.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	cmds := []cli.Command{{
		Name:  "routes",
		Flags: func(fs *flag.FlagSet) { fs.String("format", "text", "output format") },
		Run:   func(context.Context, []string) error { return nil },
	}}

	dispatchErr := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"routes", "-nonesuch"})

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	os.Stderr = orig

	captured, _ := io.ReadAll(r)
	if len(captured) != 0 {
		t.Errorf("Dispatch wrote to os.Stderr:\n%s", captured)
	}
	if dispatchErr == nil {
		t.Error("an unknown flag must be a returned error")
	}
}

func TestDispatch_CommandWithoutFlagsNeedsNoFlagFunc(t *testing.T) {
	t.Parallel()

	// Flags is optional. Most commands have none, and a nil func must not be a
	// nil dereference at the first invocation.
	ran := false
	cmds := []cli.Command{{
		Name: "version",
		Run:  func(context.Context, []string) error { ran = true; return nil },
	}}

	if err := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"version"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !ran {
		t.Error("command with no Flags never ran")
	}
}

func TestDispatch_RejectsACommandWithNoRun(t *testing.T) {
	t.Parallel()

	// A command that parses and then does nothing is indistinguishable from one
	// that worked. Catch it at dispatch rather than at the call site's confusion.
	cmds := []cli.Command{{Name: "broken"}}

	if err := cli.Dispatch(t.Context(), io.Discard, cmds, []string{"broken"}); err == nil {
		t.Error("a command with no Run must be an error")
	}
}
