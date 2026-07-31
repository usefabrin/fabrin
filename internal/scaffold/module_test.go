package scaffold_test

import (
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin/internal/scaffold"
)

// project generates a project and returns its root, so the module tests start
// from the same shape a user would have.
func project(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	if err := (scaffold.Project{Name: "demo", Module: "example.com/demo", Dir: dir}).Generate(); err != nil {
		t.Fatalf("Project.Generate: %v", err)
	}
	return dir
}

func addModule(t *testing.T, dir, name string) {
	t.Helper()
	if err := (scaffold.Module{Name: name, Dir: dir}).Generate(); err != nil {
		t.Fatalf("Module.Generate(%s): %v", name, err)
	}
}

func TestModule_WritesThePackageAndItsTest(t *testing.T) {
	t.Parallel()

	dir := project(t)
	addModule(t, dir, "billing")

	for _, f := range []string{"billing/billing.go", "billing/billing_test.go"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}

	// The route is the module's own, not "/" — two modules both claiming "/" is a
	// Gin panic at construction, which is a hostile first experience.
	src := read(t, dir, "billing/billing.go")
	if !strings.Contains(src, `r.GET("/billing"`) {
		t.Errorf("module should mount its own path:\n%s", src)
	}
	if !strings.Contains(src, `return "billing"`) {
		t.Errorf("Name() must be the module name — it is what FABRIN_MODULES selects:\n%s", src)
	}
}

func TestModule_WiresItselfIntoNewApp(t *testing.T) {
	t.Parallel()

	// A scaffold that leaves the user to find the wiring point has done the easy
	// half: the module compiles, serves nothing, and nothing says why.
	dir := project(t)
	addModule(t, dir, "billing")

	main := read(t, dir, "main.go")
	if !strings.Contains(main, "billing.New(),") {
		t.Errorf("main.go must construct the new module:\n%s", main)
	}
	if !strings.Contains(main, `"example.com/demo/billing"`) {
		t.Errorf("main.go must import the new module:\n%s", main)
	}
	// The module that was already there must survive the edit.
	if !strings.Contains(main, "home.New(),") {
		t.Errorf("the existing module was lost:\n%s", main)
	}
}

func TestModule_EditedMainStillParses(t *testing.T) {
	t.Parallel()

	dir := project(t)
	addModule(t, dir, "billing")
	addModule(t, dir, "inventory")

	path := filepath.Join(dir, "main.go")
	if _, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors); err != nil {
		t.Fatalf("main.go does not parse after two edits: %v\n%s", err, read(t, dir, "main.go"))
	}

	// Each edit must be additive. Losing an earlier module is the failure mode a
	// "did it parse" check alone cannot see.
	main := read(t, dir, "main.go")
	for _, want := range []string{"home.New(),", "billing.New(),", "inventory.New(),"} {
		if !strings.Contains(main, want) {
			t.Errorf("missing %q after two edits:\n%s", want, main)
		}
	}
}

func TestModule_LeavesMainGofmtClean(t *testing.T) {
	t.Parallel()

	// Stronger than asserting the imports are sorted, and it catches the same bug:
	// gofmt sorts within an import group, so inserting in the wrong place is not a
	// compile error — it is a file `gofmt -l` flags on the user's next commit,
	// blaming their edit rather than ours.
	//
	// Names chosen to land on both sides of the module that is already there:
	// billing sorts before home, inventory after.
	dir := project(t)
	addModule(t, dir, "inventory")
	addModule(t, dir, "billing")

	src := []byte(read(t, dir, "main.go"))
	formatted, err := format.Source(src)
	if err != nil {
		t.Fatalf("main.go does not even parse: %v\n%s", err, src)
	}
	if string(formatted) != string(src) {
		t.Errorf("editing a gofmt-clean main.go left it unformatted.\n--- got ---\n%s\n--- gofmt wants ---\n%s",
			src, formatted)
	}
}

func TestModule_KeepsTheUsersFormattingAndComments(t *testing.T) {
	t.Parallel()

	// The AST locates; the edit is a splice. Re-printing the whole file would
	// reformat code the user owns and can move their comments, which turns "add a
	// module" into a diff nobody wants to review.
	dir := project(t)

	path := filepath.Join(dir, "main.go")
	before := read(t, dir, "main.go")
	marked := strings.Replace(before,
		"func newApp(", "// A comment the user added, which must survive.\nfunc newApp(", 1)
	if err := os.WriteFile(path, []byte(marked), 0o600); err != nil {
		t.Fatal(err)
	}

	addModule(t, dir, "billing")

	after := read(t, dir, "main.go")
	if !strings.Contains(after, "// A comment the user added, which must survive.") {
		t.Errorf("the user's comment was lost:\n%s", after)
	}
	// Every line the user had should still be there, in order, with exactly the
	// new ones added.
	for _, line := range strings.Split(marked, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(after, line) {
			t.Errorf("line rewritten by the edit: %q", line)
		}
	}
}

func TestModule_WiresIntoASingleLineNewCall(t *testing.T) {
	t.Parallel()

	// Nothing stops a user collapsing the call, and a splice that assumes the
	// generated multi-line shape would produce `fabrin.New(opts, home.New()
	// billing.New())` — which does not parse, and which the parse check would
	// then blame on them.
	dir := project(t)
	path := filepath.Join(dir, "main.go")

	src := read(t, dir, "main.go")
	collapsed := strings.Replace(src,
		"return fabrin.New(opts,\n\t\thome.New(),\n\t)",
		"return fabrin.New(opts, home.New())", 1)
	if collapsed == src {
		t.Fatal("the generated call shape changed; update this test's replacement")
	}
	if err := os.WriteFile(path, []byte(collapsed), 0o600); err != nil {
		t.Fatal(err)
	}

	addModule(t, dir, "billing")

	if _, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors); err != nil {
		t.Fatalf("single-line call edited into something that does not parse: %v\n%s", err, read(t, dir, "main.go"))
	}
	main := read(t, dir, "main.go")
	for _, want := range []string{"home.New()", "billing.New()"} {
		if !strings.Contains(main, want) {
			t.Errorf("missing %q:\n%s", want, main)
		}
	}
}

func TestModule_RefusesToOverwriteAnExistingModule(t *testing.T) {
	t.Parallel()

	dir := project(t)
	addModule(t, dir, "billing")

	err := scaffold.Module{Name: "billing", Dir: dir}.Generate()
	if err == nil {
		t.Fatal("an existing module must not be overwritten")
	}
	if !strings.Contains(err.Error(), "billing") {
		t.Errorf("the error must name the module, got: %v", err)
	}

	// And main.go must not have gained a second wiring line for it.
	if n := strings.Count(read(t, dir, "main.go"), "billing.New(),"); n != 1 {
		t.Errorf("billing wired %d times, want exactly 1", n)
	}
}

func TestModule_RefusesOutsideAFabrinProject(t *testing.T) {
	t.Parallel()

	// Say what was looked for. "not a Fabrin project" with no detail leaves the
	// user guessing whether they are in the wrong directory or hit a bug.
	err := scaffold.Module{Name: "billing", Dir: t.TempDir()}.Generate()
	if err == nil {
		t.Fatal("running outside a Fabrin project must fail")
	}
	for _, want := range []string{"go.mod", "main.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name what it looked for (%q), got: %v", want, err)
		}
	}
}

func TestModule_RejectsNamesThatAreNotGoPackageNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		why  string
	}{
		{"", "empty"},
		{"go-live", "a hyphen, which no Go package name may contain"},
		{"2fa", "a leading digit"},
		{"select", "a Go keyword"},
		{"My Module", "whitespace"},
		{"orders/v2", "a path separator"},
	}

	for _, tc := range tests {
		dir := project(t)
		if err := (scaffold.Module{Name: tc.name, Dir: dir}).Generate(); err == nil {
			t.Errorf("module name %q (%s) must be rejected", tc.name, tc.why)
		}
	}
}

func TestModule_LeavesMainUntouchedWhenTheNameIsRejected(t *testing.T) {
	t.Parallel()

	dir := project(t)
	before := read(t, dir, "main.go")

	if err := (scaffold.Module{Name: "go-live", Dir: dir}).Generate(); err == nil {
		t.Fatal("expected rejection")
	}
	if after := read(t, dir, "main.go"); after != before {
		t.Error("a rejected name must not touch main.go")
	}
	if _, err := os.Stat(filepath.Join(dir, "go-live")); !os.IsNotExist(err) {
		t.Error("a rejected name must not create a package directory")
	}
}
