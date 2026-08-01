package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestTidy_ReportsThatTheProjectSurvivedTheFailure(t *testing.T) {
	// The success path needs the network and belongs to scripts/check-scaffold.sh,
	// which runs the generator with -skip-tidy for exactly that reason. What has no
	// coverage anywhere else is this branch: the project is already on disk and
	// correct, and only resolution failed.
	//
	// Deleting the user's brand-new project over a network blip would be worse
	// than leaving it, so the message has to say both what happened and what to do
	// — and prose no test reads is prose that rots.
	t.Setenv("GO", "fabrin-no-such-go-binary")

	var out bytes.Buffer
	err := tidy(context.Background(), &out, t.TempDir())
	if err == nil {
		t.Fatal("a go binary that does not exist must be an error")
	}

	msg := err.Error()
	for _, want := range []string{"the project was created", "-skip-tidy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error must contain %q, got: %s", want, msg)
		}
	}
	if !strings.Contains(msg, t.TempDir()[:4]) && !strings.Contains(msg, "/") {
		t.Errorf("the error must name the directory, got: %s", msg)
	}

	// The progress line is written before the command runs, so the caller can see
	// which step is taking seconds rather than watching an apparently hung tool.
	if !strings.Contains(out.String(), "go mod tidy") {
		t.Errorf("tidy must announce itself, got: %q", out.String())
	}
}

func TestCommands_AreTheOnesThatNeedNoCompilation(t *testing.T) {
	t.Parallel()

	// There is deliberately no `fabrin routes` and no `fabrin serve`: this binary
	// cannot introspect an application it did not build. Those belong to the app's
	// own binary, via fabrin.App.Execute. A drive-by addition here would look
	// harmless and would need `go run` under the hood on every invocation.
	got := map[string]bool{}
	for _, c := range commands() {
		got[c.Name] = true
		if c.Run == nil {
			t.Errorf("command %q has no Run", c.Name)
		}
		if c.Short == "" {
			t.Errorf("command %q has no Short, so it is invisible in usage", c.Name)
		}
	}

	for _, want := range []string{"new", "startapp", "version"} {
		if !got[want] {
			t.Errorf("missing command %q", want)
		}
	}
	for _, unwanted := range []string{"routes", "serve"} {
		if got[unwanted] {
			t.Errorf("%q must not be a command of the global binary — it needs the app's modules linked in", unwanted)
		}
	}
	if len(got) != 3 {
		t.Errorf("commands() has %d entries, want exactly new/startapp/version: %v", len(got), got)
	}
}
