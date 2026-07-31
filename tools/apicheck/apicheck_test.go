package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

const (
	testModule = "github.com/usefabrin/fabrin"
	vendorPkg  = "example.com/vendor"
)

// check type-checks src as package path, resolving imports from deps.
//
// Hand-built packages rather than a testdata module: this exercises the type
// traversal, which is the part with the sharp edges, and it needs no network, no
// go.sum, and no second module to keep in sync.
func check(t *testing.T, path, src string, deps ...*types.Package) *types.Package {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path+"/x.go", src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	byPath := map[string]*types.Package{}
	for _, d := range deps {
		byPath[d.Path()] = d
	}

	conf := types.Config{
		Importer: importerFunc(func(p string) (*types.Package, error) {
			if pkg, ok := byPath[p]; ok {
				return pkg, nil
			}
			return nil, fmt.Errorf("unexpected import %q", p)
		}),
	}

	pkg, err := conf.Check(path, fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatalf("type-check %s: %v", path, err)
	}
	return pkg
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

// vendorPackage is a stand-in for any unblessed third-party dependency.
//
// T carries a method because the alias test below asserts a NEGATIVE — that the
// snapshot does not expand an alias into its target's method set. Without a
// method on the target there is nothing for that assertion to fail on, and it
// would pass for the wrong reason.
func vendorPackage(t *testing.T) *types.Package {
	t.Helper()
	return check(t, vendorPkg, `package vendor

type T struct{ Field int }
type Opt struct{ N int }

func (T) Reticulate() int { return 0 }
`)
}

func TestLeak_FindsUnblessedTypesInEveryPosition(t *testing.T) {
	t.Parallel()

	dep := vendorPackage(t)
	pkg := check(t, testModule, `package fabrin

import "example.com/vendor"

// Each of these puts a third-party type into the semver contract.
func InResult() vendor.T          { return vendor.T{} }
func InParam(v vendor.T)          {}
func InVariadic(v ...vendor.Opt)  {}

type InField struct{ V vendor.T }

// Two containers deep — the walk has to keep going, not stop at the map.
type InNested struct{ Deep map[string][]*vendor.T }

type WithMethod struct{}

func (WithMethod) Query() vendor.T { return vendor.T{} }

var InVar vendor.T
`, dep)

	got := leakInScope(testModule, pkg.Scope(), testModule)

	// Every one of these is a distinct way for a dependency to reach the public
	// API. A checker that catches only the obvious first case gives false
	// confidence, which is worse than no checker.
	want := []string{
		"InResult exposes example.com/vendor.T",
		"InParam exposes example.com/vendor.T",
		"InVariadic exposes example.com/vendor.Opt",
		"InField exposes example.com/vendor.T",
		"InNested exposes example.com/vendor.T",
		"WithMethod.Query exposes example.com/vendor.T",
		"InVar exposes example.com/vendor.T",
	}

	joined := strings.Join(got, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missed a leak vector: %s\ngot:\n%s", w, joined)
		}
	}
}

func TestLeak_AllowsBlessedStdlibAndOwnTypes(t *testing.T) {
	t.Parallel()

	// A checker that fires on legitimate code gets disabled, so the negative case
	// matters as much as the positive one.
	pkg := check(t, testModule, `package fabrin

type Mine struct{ N int }

func OwnType() Mine        { return Mine{} }
func Builtin() (string, error) { return "", nil }

type Iface interface{ Do() Mine }
`)

	if got := leakInScope(testModule, pkg.Scope(), testModule); len(got) != 0 {
		t.Errorf("own and builtin types must not be reported, got %v", got)
	}
}

func TestLeak_DoesNotPanicOnATypedConstant(t *testing.T) {
	t.Parallel()

	// Regression guard. A constant like `StatusUp Status = "up"` has a *types.Named
	// type, so code that tested the TYPE for Named and then cast the OBJECT to
	// *types.TypeName panicked on every typed constant in the tree. It never fired
	// in snapshot mode, so `just api` looked fine and only the leak gate crashed.
	pkg := check(t, testModule, `package fabrin

type Status string

const StatusUp Status = "up"
`)

	got := leakInScope(testModule, pkg.Scope(), testModule)
	if len(got) != 0 {
		t.Errorf("no leak expected, got %v", got)
	}
}

func TestLeak_DoesNotDescendIntoForeignTypes(t *testing.T) {
	t.Parallel()

	// Naming a foreign type is the whole commitment; what it is made of internally
	// is not Fabrin's surface. Reporting its fields separately would be noise, and
	// noise is how a gate gets ignored.
	dep := vendorPackage(t)
	pkg := check(t, testModule, `package fabrin

import "example.com/vendor"

func One() vendor.T { return vendor.T{} }
`, dep)

	got := leakInScope(testModule, pkg.Scope(), testModule)
	if len(got) != 1 {
		t.Errorf("a foreign type should be reported exactly once, got %v", got)
	}
}

// ── snapshot rendering ──────────────────────────────────────────────────────

// described renders every exported object in pkg the way `snapshot` does.
//
// It mirrors snapshot's loop rather than calling it, because snapshot takes
// []*packages.Package — a loader type — while the interesting logic is all in
// describe, which takes a plain types.Object.
func described(t *testing.T, pkg *types.Package) []string {
	t.Helper()

	var out []string
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if obj := scope.Lookup(name); obj.Exported() {
			out = append(out, describe(pkg.Path(), obj)...)
		}
	}
	sort.Strings(out)
	return out
}

func TestDescribe_RecordsAliasesUnexpanded(t *testing.T) {
	t.Parallel()

	// The rule the whole snapshot format hangs on. Two failures are possible and
	// both are caught by the exact comparison below:
	//
	//   - Expanding the alias into its target's method set, which would make a Gin
	//     patch release churn api/fabrin.txt with no Fabrin change. `Reticulate`
	//     exists on vendor.T purely so that failure has something to produce.
	//   - Printing "type Ctx = Ctx". Since Go 1.22's alias materialisation the
	//     object's own type is a *types.Alias named after the alias, so the naive
	//     rendering is a tautology that records nothing — and would hide the day
	//     the alias started pointing somewhere else.
	dep := vendorPackage(t)
	pkg := check(t, testModule, `package fabrin

import "example.com/vendor"

type Ctx = vendor.T
`, dep)

	got := strings.Join(described(t, pkg), "\n")
	want := "type Ctx = example.com/vendor.T"

	if got != want {
		t.Errorf("alias rendering:\n got: %s\nwant: %s", got, want)
	}
}

func TestDescribe_RecordsEveryExportedKindAndNothingUnexported(t *testing.T) {
	t.Parallel()

	// The snapshot is only worth diffing if it is faithful: a symbol the renderer
	// drops can be removed from the API without api-check saying a word. So this
	// asserts the WHOLE output, not that particular lines appear — a missing line
	// is exactly the failure mode, and a Contains-style check cannot see one.
	pkg := check(t, testModule, `package fabrin

type Status string

const StatusUp Status = "up"

// Untyped, so the recorded type is "untyped string" — the value is the promise.
const DefaultAddr = ":8080"

var ErrBoom error

func Handle(path string, n int) error { return nil }

type Options struct {
	Addr   string
	hidden int
}

func (o Options) WithAddr(a string) Options { return o }
func (o *Options) unexported()              {}

type Runner interface {
	Run() error
	private()
}

type unexported struct{}
`)

	want := []string{
		`const DefaultAddr untyped string = ":8080"`,
		`const StatusUp Status = "up"`,
		`func Handle(path string, n int) error`,
		`method (Options) WithAddr(a string) Options`,
		`type Options struct`,
		`type Options struct, Addr string`,
		`type Runner interface`,
		`type Runner interface, Run() error`,
		`type Status string`,
		`var ErrBoom error`,
	}

	got := described(t, pkg)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("snapshot rendering:\n got:\n%s\n\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// ── The allowlist is the reviewable record ──────────────────────────────────

func TestAllowlist_HoldsOnlyGin(t *testing.T) {
	t.Parallel()

	// Not a style assertion. Every entry here is something Fabrin cannot change
	// without a major version, forever, and the cost is invisible at the call
	// sites that benefit. AGENTS.md hard rule 1 requires an ADR to add one — this
	// test makes a silent addition fail, so the ADR conversation actually happens.
	if len(allowlist) != 1 || !allowlist["github.com/gin-gonic/gin"] {
		t.Fatalf("allowlist must contain exactly github.com/gin-gonic/gin, got %v\n"+
			"Adding an entry needs an ADR, not a line edit — see AGENTS.md hard rule 1.", allowlist)
	}
}

func TestPermitted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
		why  string
	}{
		{"github.com/usefabrin/fabrin", true, "the module itself"},
		{"github.com/usefabrin/fabrin/health", true, "our own subpackage"},
		{"context", true, "stdlib"},
		{"net/http", true, "stdlib with a slash"},
		{"log/slog", true, "stdlib with a slash"},
		{"github.com/gin-gonic/gin", true, "allowlisted"},
		{"github.com/gin-gonic/gin/binding", true, "subpackage of an allowlisted module"},
		{"gorm.io/gorm", false, "unblessed"},
		{"github.com/usefabrin/fabrinx", false, "prefix of our path but a different module"},
		{"github.com/gin-gonic/ginx", false, "prefix of the allowlist entry but a different module"},
	}

	for _, tc := range tests {
		if got := permitted(tc.path, testModule); got != tc.want {
			t.Errorf("permitted(%q) = %v, want %v — %s", tc.path, got, tc.want, tc.why)
		}
	}
}
