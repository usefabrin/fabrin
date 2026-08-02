package scaffold_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin/internal/scaffold"
)

func generate(t *testing.T, p scaffold.Project) string {
	t.Helper()
	dir := t.TempDir()
	p.Dir = filepath.Join(dir, p.Name)
	if err := p.Generate(); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return p.Dir
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestGenerate_WritesEveryFileTheProjectNeeds(t *testing.T) {
	t.Parallel()

	dir := generate(t, scaffold.Project{Name: "demo"})

	// A scaffold that omits one of these is a scaffold whose output does not run,
	// which is worse than no scaffold: the user debugs a framework they have not
	// learned yet.
	want := []string{
		"go.mod",
		"main.go",
		"home/home.go",
		"home/home_test.go",
		"justfile",
		".gitignore",
		"README.md",
	}
	for _, f := range want {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

func TestGenerate_EveryGoFileItWritesParses(t *testing.T) {
	t.Parallel()

	// Templates are text until something compiles them. A stray brace in a
	// template is invisible to every test that only checks a file exists, and it
	// surfaces as a compile error in the user's brand-new project.
	dir := generate(t, scaffold.Project{Name: "demo"})

	var checked int
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if _, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors); perr != nil {
			t.Errorf("generated %s does not parse: %v", filepath.Base(path), perr)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Guards against passing because it found nothing to parse.
	if checked < 3 {
		t.Fatalf("expected at least three generated .go files, parsed %d", checked)
	}
}

func TestGenerate_ModulePathDefaultsToTheNameAndIsOverridable(t *testing.T) {
	t.Parallel()

	// `go mod init demo` produces a module named demo and builds fine, so the
	// default has to work without asking. Anyone publishing needs the real path,
	// so it has to be overridable without editing go.mod by hand.
	bare := generate(t, scaffold.Project{Name: "demo"})
	if got := read(t, bare, "go.mod"); !strings.Contains(got, "module demo\n") {
		t.Errorf("default module path:\n%s", got)
	}

	owned := generate(t, scaffold.Project{Name: "demo", Module: "github.com/me/demo"})
	gomod := read(t, owned, "go.mod")
	if !strings.Contains(gomod, "module github.com/me/demo\n") {
		t.Errorf("-module was not honoured:\n%s", gomod)
	}
	// The module's own package is imported by main, so the path has to follow.
	if main := read(t, owned, "main.go"); !strings.Contains(main, `"github.com/me/demo/home"`) {
		t.Errorf("main.go must import the module under the chosen path:\n%s", main)
	}
}

func TestGenerate_MainHandsArgumentsToExecute(t *testing.T) {
	t.Parallel()

	// The whole reason `routes` can answer anything: the generated binary is the
	// one with the modules linked in. A main that only calls Run leaves the user
	// with a CLI that does not exist.
	main := read(t, generate(t, scaffold.Project{Name: "demo"}), "main.go")

	for _, want := range []string{"app.Execute(", "os.Args[1:]", "config.Load(config.Standard()...)"} {
		if !strings.Contains(main, want) {
			t.Errorf("generated main.go missing %q:\n%s", want, main)
		}
	}
	if strings.Contains(main, "config.MustLoad") {
		t.Errorf("generated main must report bad flags as an ordinary startup error, not panic:\n%s", main)
	}
}

func TestGenerate_RefusesANonEmptyDirectory(t *testing.T) {
	t.Parallel()

	// Overwriting someone's work is not recoverable from a CLI. Refuse, and name
	// what is in the way so the message is actionable.
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := scaffold.Project{Name: "demo", Dir: dir}.Generate()
	if err == nil {
		t.Fatal("generating into a non-empty directory must fail")
	}
	if !strings.Contains(err.Error(), "notes.txt") {
		t.Errorf("the error must name what is in the way, got: %v", err)
	}
}

func TestGenerate_RefusesAParentDirectoryThatDoesNotExist(t *testing.T) {
	t.Parallel()

	// `-dir` is most often a typo of a path that exists (`-dir ~/projcts`), and
	// os.MkdirAll would happily materialise the whole chain. Nothing fails, so
	// nobody looks — the user just has a project somewhere they did not mean.
	//
	// The project directory itself must still be creatable; it is the PARENT the
	// user is naming rather than asking for.
	root := t.TempDir()
	missing := filepath.Join(root, "typo", "nested", "demo")

	err := scaffold.Project{Name: "demo", Dir: missing}.Generate()
	if err == nil {
		t.Fatal("a parent directory that does not exist must be an error")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Errorf("the error must name the missing directory, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "typo")); !os.IsNotExist(statErr) {
		t.Error("nothing should have been created under a rejected parent")
	}
}

func TestGenerate_CreatesTheProjectDirectoryItself(t *testing.T) {
	t.Parallel()

	// The other half of the rule above: `fabrin new demo` must create demo/, and
	// a relative Dir has "." as its parent, which always exists.
	root := t.TempDir()
	if err := (scaffold.Project{Name: "demo", Dir: filepath.Join(root, "demo")}).Generate(); err != nil {
		t.Errorf("the project directory itself must be creatable: %v", err)
	}
}

func TestGenerate_AcceptsAnEmptyExistingDirectory(t *testing.T) {
	t.Parallel()

	// `mkdir demo && cd demo && fabrin new demo` is a thing people do.
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := (scaffold.Project{Name: "demo", Dir: dir}).Generate(); err != nil {
		t.Errorf("an empty existing directory must be usable: %v", err)
	}
}

func TestGenerate_RejectsUnusableNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		why  string
	}{
		{"", "empty"},
		{".", "the current directory"},
		{"..", "the parent directory"},
		{"a/b", "a path rather than a name"},
		{"../escape", "an attempt to write outside the target"},
		{"my project", "a space, which breaks the import path"},
	}

	for _, tc := range tests {
		err := scaffold.Project{Name: tc.name, Dir: filepath.Join(t.TempDir(), "x")}.Generate()
		if err == nil {
			t.Errorf("name %q (%s) must be rejected", tc.name, tc.why)
		}
	}
}

func TestGenerate_RejectsAnUnusableModulePath(t *testing.T) {
	t.Parallel()

	for _, mod := range []string{"has space", "/leading", "trailing/"} {
		err := scaffold.Project{
			Name:   "demo",
			Module: mod,
			Dir:    filepath.Join(t.TempDir(), "demo"),
		}.Generate()
		if err == nil {
			t.Errorf("module path %q must be rejected before it reaches go.mod", mod)
		}
	}
}

func TestGoDirective_MatchesFabrinsOwnGoMod(t *testing.T) {
	t.Parallel()

	// The generated go.mod's `go` line is a constant here, because a package
	// cannot go:embed a file above its own directory. This is what stops it
	// drifting — and drift in this direction is quiet: a project pinned higher
	// than Fabrin needs stops building for a colleague on the older toolchain,
	// and nothing in the diff says why.
	b, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read Fabrin's go.mod: %v", err)
	}

	var fabrins string
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			fabrins = strings.TrimSpace(rest)
			break
		}
	}
	if fabrins == "" {
		t.Fatal("no go directive in Fabrin's go.mod")
	}

	// Fabrin declares a patch version ("1.25.0"); a generated go.mod wants the
	// major.minor pair.
	parts := strings.SplitN(fabrins, ".", 3)
	want := parts[0] + "." + parts[1]

	got := read(t, generate(t, scaffold.Project{Name: "demo"}), "go.mod")
	if !strings.Contains(got, "go "+want+"\n") {
		t.Errorf("generated go.mod should say `go %s`, matching Fabrin's own %q:\n%s", want, fabrins, got)
	}
}

func TestGenerate_WritesNothingWhenTheNameIsRejected(t *testing.T) {
	t.Parallel()

	// Validation before the first write. A half-written project is harder to
	// recover from than none, because the user cannot tell which files are theirs.
	root := t.TempDir()
	dir := filepath.Join(root, "demo")

	if err := (scaffold.Project{Name: "a/b", Dir: dir}).Generate(); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a rejected name must leave no directory behind, got err=%v", err)
	}
}
