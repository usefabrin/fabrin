// Package cli is Fabrin's command surface: a command type modules can
// contribute, and a dispatcher that runs them.
//
// # Why this package knows nothing about App
//
// [github.com/usefabrin/fabrin.Commander] returns []Command, so the root package
// imports this one. This package can therefore never import the root package —
// the build would cycle — which means it cannot take an *App and cannot serve
// anything itself. Assembling Fabrin's built-in commands with a module's own is
// the root package's job, because root is the only layer that sees both.
//
// The constraint is a feature. A CLI that needs an HTTP stack constructed before
// it can print its own help is a CLI nobody can test.
//
// # Why stdlib flag
//
// Command is Fabrin's own struct rather than a third-party command type. Whatever
// appears here becomes something Fabrin cannot change without a major version,
// and apicheck's allowlist holds exactly one entry for that reason — see
// AGENTS.md hard rule 1 and docs/adr/0001-gin-as-a-type-alias.md.
//
// *flag.FlagSet is deliberately concrete rather than an interface Fabrin defines.
// Wrapping the standard library for its own sake is the "narrow interface"
// alternative that ADR 0001 rejected: it would make Fabrin cheap to change and
// user code expensive to write. The accepted cost is that a command's flag
// definitions are coupled to flag's API.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// Command is one subcommand.
//
// Flags is optional; most commands have none. Run is not: a command that parses
// its arguments and then does nothing is indistinguishable, from the outside,
// from one that worked.
type Command struct {
	// Name is how the command is invoked. It must be unique within one dispatch.
	Name string

	// Short is one line, shown in the command list.
	Short string

	// Flags registers this command's flags on a set of its own. Two commands may
	// both define -format without colliding, and nothing is registered globally.
	Flags func(*flag.FlagSet)

	// Run receives the writer Dispatch was given and the positional arguments
	// left after flag parsing.
	//
	// out rather than os.Stdout for the same reason Dispatch takes one: a command
	// that prints is untestable without capturing it, and a module's command is
	// the least appropriate place in the whole framework to be reaching for a
	// process-global stream. Django hands its management commands self.stdout for
	// exactly this reason.
	//
	// The context carries cancellation: a command that blocks — serving, draining
	// a queue — must return when it is done.
	Run func(ctx context.Context, out io.Writer, args []string) error
}

// Dispatch runs the command named by args[0], or writes usage to out.
//
// Usage goes to out rather than os.Stdout because a library must not write to a
// stream it does not own. It is also what makes the usage output testable
// without swapping globals.
//
// With no arguments, or with a help request, Dispatch writes usage and returns
// nil: asking for help is not a failure. Note that a Fabrin application overrides
// the no-argument case — for an app binary, no arguments means *serve*.
func Dispatch(ctx context.Context, out io.Writer, cmds []Command, args []string) error {
	// Before anything else, and regardless of which command was asked for. Two
	// commands answering to one name means one of them silently never runs, and
	// which one depends on slice order — a bug that is correct on the machine
	// where it was written.
	if err := checkDuplicates(cmds); err != nil {
		return err
	}

	if len(args) == 0 || isHelp(args[0]) {
		return usage(out, cmds)
	}

	name := args[0]
	cmd, ok := find(cmds, name)
	if !ok {
		return unknownCommand(name, cmds)
	}
	if cmd.Run == nil {
		return fmt.Errorf("fabrin: command %q has no Run function", name)
	}

	fs := flag.NewFlagSet(name, flag.ContinueOnError)

	// ContinueOnError stops the set calling os.Exit. It does NOT stop it printing
	// the error and its usage — the output defaults to os.Stderr, which this
	// package does not own, and which would duplicate whatever the caller does
	// with the error returned below.
	fs.SetOutput(io.Discard)

	if cmd.Flags != nil {
		cmd.Flags(fs)
	}

	positional, err := parseInterspersed(fs, args[1:])
	if err != nil {
		// `cmd -h` asks this command for its flags. Same reasoning as above: a
		// help request is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return commandUsage(out, cmd, fs)
		}
		return fmt.Errorf("fabrin: %s: %w", name, err)
	}

	return cmd.Run(ctx, out, positional)
}

// parseInterspersed parses flags that appear before, between, or after the
// positional arguments, and returns the positionals in order.
//
// Go's flag package stops at the first non-flag argument, and that is right for
// a program's own arguments — `go run x.go -v` must hand `-v` to the program
// rather than to `go`. It is wrong for a subcommand, where the name has already
// been consumed and everything left belongs to that command. Without this,
// `fabrin new demo -module example.com/demo` silently treats the module path as
// a second project name, which is the sort of paper cut that makes a CLI feel
// hostile.
//
// Everything after a bare "--" is positional by definition and is never
// re-parsed: `mycmd -- -weird` must pass `-weird` through, not fail on it.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var terminated []string
	if i := slices.Index(args, "--"); i >= 0 {
		terminated = args[i+1:]
		args = args[:i]
	}

	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		// One positional, then keep parsing: the next element may be a flag again.
		positional = append(positional, rest[0])
		args = rest[1:]
	}

	return append(positional, terminated...), nil
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func find(cmds []Command, name string) (Command, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

func checkDuplicates(cmds []Command) error {
	seen := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		if seen[c.Name] {
			return fmt.Errorf("fabrin: duplicate command %q", c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

// unknownCommand names what was typed, suggests the nearest command, and lists
// the rest.
//
// Never a silent no-op: the same rule FABRIN_MODULES follows for an unknown
// module name. A typo that exits 0 is the worst outcome available, because the
// user believes the command ran.
func unknownCommand(name string, cmds []Command) error {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "fabrin: unknown command %q", name)
	if near, ok := closest(name, names); ok {
		fmt.Fprintf(&b, "\n\n    Did you mean %q?", near)
	}
	fmt.Fprintf(&b, "\n\n    Available commands: %s", strings.Join(names, ", "))
	return errors.New(b.String())
}

// closest returns the nearest candidate by edit distance, when one is near
// enough to be worth suggesting.
//
// The threshold matters more than the algorithm. A suggestion that is wrong more
// often than right trains people to ignore the line, so this only fires for a
// name that is plausibly a typo of the input rather than merely the least
// dissimilar of a short list.
func closest(name string, candidates []string) (string, bool) {
	best, bestDist := "", -1
	for _, c := range candidates {
		d := distance(name, c)
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}

	limit := max((len(name)+2)/3, 1)
	if bestDist < 0 || bestDist > limit {
		return "", false
	}
	return best, true
}

// distance is Levenshtein, over bytes.
//
// Bytes rather than runes: command names are ASCII by convention, and this only
// ranks suggestions — a multi-byte name would produce a slightly different
// ordering, never a wrong answer.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func usage(out io.Writer, cmds []Command) error {
	sorted := make([]Command, len(cmds))
	copy(sorted, cmds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	width := 0
	for _, c := range sorted {
		width = max(width, len(c.Name))
	}

	var b strings.Builder
	b.WriteString("Usage:\n    <command> [flags] [arguments]\n\nCommands:\n")
	for _, c := range sorted {
		fmt.Fprintf(&b, "    %-*s  %s\n", width, c.Name, c.Short)
	}
	b.WriteString("\nRun a command with -h to see its flags.\n")

	_, err := io.WriteString(out, b.String())
	return err
}

func commandUsage(out io.Writer, cmd Command, fs *flag.FlagSet) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage:\n    %s [flags] [arguments]\n", cmd.Name)
	if cmd.Short != "" {
		fmt.Fprintf(&b, "\n%s\n", cmd.Short)
	}

	// PrintDefaults writes to the set's output, which Dispatch pointed at
	// io.Discard so that flag could not reach os.Stderr. Point it here for the
	// length of this call, which is the one place the caller asked for it.
	fs.SetOutput(&b)
	var flagged bool
	fs.VisitAll(func(*flag.Flag) { flagged = true })
	if flagged {
		b.WriteString("\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.SetOutput(io.Discard)

	_, err := io.WriteString(out, b.String())
	return err
}
