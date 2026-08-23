// Command apicheck records and guards Fabrin's public API surface.
//
// Two modes, driven by scripts/api.sh and wired into `just check`:
//
//	apicheck -mode=snapshot   # print every exported symbol, for api/fabrin.txt
//	apicheck -mode=leak       # fail on an unblessed third-party type in a signature
//
// It is written in Go rather than in bash because both questions need real type
// information: "is this an alias or a definition" and "which package does this
// type in a signature come from" are not answerable by reading source text.
//
// # Why the tools module is separate
//
// This needs golang.org/x/tools/go/packages. Fabrin is a library, so anything in
// its go.mod lands in every consumer's go.sum — for a tool they will never
// invoke. tools/ is its own module for exactly that reason.
package main

import (
	"flag"
	"fmt"
	"go/types"
	"os"
	"path"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// allowlist is the single reviewable record of which third-party packages may
// appear in Fabrin's exported signatures.
//
// Gin is the only entry, and deliberately so. fabrin.Context is a type ALIAS for
// gin.Context, which is what makes every Gin middleware work unmodified — the
// feature users choose Fabrin for. The accepted cost is that Gin's v1 API is part
// of Fabrin's semver contract; that cost is bounded because Gin has been on v1
// since 2015.
//
// Adding a second entry is an architectural decision that needs an ADR, not a
// line edit. Whatever is added here becomes something Fabrin cannot change
// without a major version, forever, and the decision is invisible at every call
// site that benefits from it. See AGENTS.md, hard rule 1.
var allowlist = map[string]bool{
	"github.com/gin-gonic/gin": true,
}

func main() {
	var (
		mode = flag.String("mode", "check", "snapshot | leak")
		dir  = flag.String("dir", "..", "path to the Fabrin module root")
	)
	flag.Parse()

	if err := run(*mode, *dir); err != nil {
		fmt.Fprintln(os.Stderr, "apicheck: "+err.Error())
		os.Exit(1)
	}
}

func run(mode, dir string) error {
	modulePath, err := modulePath(dir)
	if err != nil {
		return err
	}

	names, err := publicPackages(dir)
	if err != nil {
		return err
	}

	patterns := make([]string, 0, len(names))
	for _, n := range names {
		if n == "." {
			patterns = append(patterns, modulePath)
			continue
		}
		patterns = append(patterns, modulePath+"/"+n)
	}

	pkgs, err := load(dir, patterns)
	if err != nil {
		return err
	}

	switch mode {
	case "snapshot":
		return snapshot(os.Stdout, pkgs)
	case "leak":
		return leak(modulePath, pkgs)
	default:
		return fmt.Errorf("unknown mode %q: want snapshot or leak", mode)
	}
}

// publicPackages reads scripts/gates/public-packages.txt.
//
// Deliberately the same manifest the depguard coverage gate uses, rather than a
// second list here. That gate already checks the manifest against the filesystem
// in both directions, so a new public package cannot reach the snapshot without
// also having a recorded boundary decision — and the two cannot drift apart,
// because there is only one of them.
func publicPackages(dir string) ([]string, error) {
	manifest := path.Join(dir, "scripts/gates/public-packages.txt")

	raw, err := os.ReadFile(manifest)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifest, err)
	}

	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no packages", manifest)
	}
	sort.Strings(out)
	return out, nil
}

func modulePath(dir string) (string, error) {
	raw, err := os.ReadFile(path.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("no module line in %s/go.mod", dir)
}

func load(dir string, patterns []string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		// Dir is the Fabrin module root, not this tool's: apicheck runs from
		// tools/, which is a different module.
		Dir:  dir,
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps,
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}

	// packages.Load reports type errors per package rather than returning them.
	// Ignoring them would make apicheck quietly report on a partial surface, which
	// is the failure mode this whole tool exists to prevent.
	var failed []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			failed = append(failed, fmt.Sprintf("%s: %v", p.PkgPath, e))
		}
	}
	if len(failed) > 0 {
		return nil, fmt.Errorf("packages did not type-check:\n  %s", strings.Join(failed, "\n  "))
	}

	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	return pkgs, nil
}

// ── snapshot ────────────────────────────────────────────────────────────────

func snapshot(out *os.File, pkgs []*packages.Package) error {
	fmt.Fprintln(out, "# Fabrin public API surface. GENERATED — regenerate with `just api`.")
	fmt.Fprintln(out, "#")
	fmt.Fprintln(out, "# One line per exported symbol. Every line is a promise to strangers: adding")
	fmt.Fprintln(out, "# one is cheap, removing one breaks their build. A diff here is the review.")
	fmt.Fprintln(out, "#")
	fmt.Fprintln(out, "# Type aliases are recorded UNEXPANDED, so a Gin patch bump does not churn this")
	fmt.Fprintln(out, "# file. A gate that cries wolf gets ignored, and an ignored gate is worse than")
	fmt.Fprintln(out, "# none. See AGENTS.md, hard rule 2.")

	for _, p := range pkgs {
		var lines []string
		scope := p.Types.Scope()

		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			lines = append(lines, describe(p.PkgPath, obj)...)
		}

		sort.Strings(lines)
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintln(out)
		for _, l := range lines {
			fmt.Fprintf(out, "pkg %s, %s\n", p.PkgPath, l)
		}
	}
	return nil
}

// describe renders one package-scope object as one or more snapshot lines.
func describe(pkgPath string, obj types.Object) []string {
	q := qualifier(pkgPath)

	switch o := obj.(type) {
	case *types.Const:
		// The value, not just the type: a default IS part of the promise.
		// `DefaultAddr` changing from ":8080" is a change users feel, so it should
		// show up in this diff rather than hide behind an unchanged type.
		return []string{fmt.Sprintf("const %s %s = %s", o.Name(), types.TypeString(o.Type(), q), o.Val())}

	case *types.Var:
		return []string{fmt.Sprintf("var %s %s", o.Name(), types.TypeString(o.Type(), q))}

	case *types.Func:
		sig := strings.TrimPrefix(types.TypeString(o.Type(), q), "func")
		return []string{fmt.Sprintf("func %s%s", o.Name(), sig)}

	case *types.TypeName:
		return describeType(o, q)
	}
	return nil
}

func describeType(o *types.TypeName, q types.Qualifier) []string {
	// An ALIAS is recorded as the one line it is, and never expanded.
	//
	// This is the rule the whole snapshot format hangs on. fabrin.Context is an
	// alias for gin.Context; expanding it would paste forty-odd Gin methods into
	// this file, and every one of them would churn on a Gin patch release that
	// changed nothing about Fabrin. api-check would go red on a dependency bump
	// with no Fabrin change, which is precisely how a gate teaches people to
	// ignore it.
	if o.IsAlias() {
		// Unalias to reach the RIGHT-HAND SIDE. Since Go 1.22's alias
		// materialisation, o.Type() is a *types.Alias whose own name is the alias —
		// printing it directly yields the tautology "type Context = Context", which
		// records nothing and would hide the day the alias started pointing
		// somewhere else. That target is the entire promise being made here.
		return []string{fmt.Sprintf("type %s = %s", o.Name(), types.TypeString(types.Unalias(o.Type()), q))}
	}

	named, ok := o.Type().(*types.Named)
	if !ok {
		return []string{fmt.Sprintf("type %s %s", o.Name(), types.TypeString(o.Type(), q))}
	}

	var lines []string

	switch u := named.Underlying().(type) {
	case *types.Struct:
		lines = append(lines, fmt.Sprintf("type %s struct", o.Name()))
		for i := range u.NumFields() {
			f := u.Field(i)
			if !f.Exported() {
				continue
			}
			// Exported fields are part of the promise — Options is a struct of
			// them by design, and its fields may be added, never removed or
			// retyped. Unexported ones are implementation and stay out, so an
			// internal rename does not show up as an API change.
			lines = append(lines, fmt.Sprintf("type %s struct, %s %s",
				o.Name(), f.Name(), types.TypeString(f.Type(), q)))
		}

	case *types.Interface:
		lines = append(lines, fmt.Sprintf("type %s interface", o.Name()))
		for i := range u.NumMethods() {
			m := u.Method(i)
			if !m.Exported() {
				continue
			}
			sig := strings.TrimPrefix(types.TypeString(m.Type(), q), "func")
			lines = append(lines, fmt.Sprintf("type %s interface, %s%s", o.Name(), m.Name(), sig))
		}

	default:
		lines = append(lines, fmt.Sprintf("type %s %s", o.Name(), types.TypeString(u, q)))
	}

	// DECLARED methods only, not the full method set. A promoted method from an
	// embedded third-party type would reintroduce exactly the churn the alias rule
	// above avoids; an embedded exported field is already recorded as a field, so
	// the embedding itself is still visible in the diff.
	for i := range named.NumMethods() {
		m := named.Method(i)
		if !m.Exported() {
			continue
		}
		recv := types.TypeString(m.Signature().Recv().Type(), q)
		sig := strings.TrimPrefix(types.TypeString(m.Type(), q), "func")
		lines = append(lines, fmt.Sprintf("method (%s) %s%s", recv, m.Name(), sig))
	}

	sort.Strings(lines)
	return lines
}

// qualifier prints every type with its FULL package path, except types from the
// package being described.
//
// Full paths because "gin.Context" and "othermod/gin.Context" are different
// promises that a short name renders identically — and this file is the record of
// which one was made.
func qualifier(pkgPath string) types.Qualifier {
	return func(p *types.Package) string {
		if p.Path() == pkgPath {
			return ""
		}
		return p.Path()
	}
}

// ── leak ────────────────────────────────────────────────────────────────────

func leak(modulePath string, pkgs []*packages.Package) error {
	var findings []string
	for _, p := range pkgs {
		findings = append(findings, leakInScope(p.PkgPath, p.Types.Scope(), modulePath)...)
	}

	if len(findings) == 0 {
		fmt.Println("✓ api: no unblessed third-party type in any exported signature.")
		return nil
	}

	sort.Strings(findings)
	findings = dedupe(findings)

	var b strings.Builder
	b.WriteString("unblessed third-party type in an exported signature:\n")
	for _, f := range findings {
		b.WriteString("    " + f + "\n")
	}
	b.WriteString("\n")
	b.WriteString("    An unblessed type in the public API makes that dependency's version part\n")
	b.WriteString("    of Fabrin's semver contract, permanently and by accident.\n\n")
	b.WriteString("    Fix it by not exposing the type: accept an interface you declare, or\n")
	b.WriteString("    return your own type. Adding to apicheck's allowlist is an architectural\n")
	b.WriteString("    decision that needs an ADR. See AGENTS.md, hard rule 1.")
	return fmt.Errorf("%s", b.String())
}

// leakInScope reports every unblessed third-party type reachable from an
// exported object in scope.
//
// Split out from [leak] so it can be tested against a hand-built types.Package:
// this traversal is where the subtle bugs live — the ones a passing `just check`
// on a clean tree cannot reveal, because a clean tree has nothing to find.
func leakInScope(pkgPath string, scope *types.Scope, modulePath string) []string {
	var findings []string

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}

		for _, bad := range offenders(obj.Type(), modulePath, map[types.Type]bool{}) {
			findings = append(findings, fmt.Sprintf("%s.%s exposes %s", pkgPath, name, bad))
		}

		// Methods carry signatures of their own and are just as public.
		//
		// Assert on the OBJECT before its type: a constant like `StatusUp Status`
		// also has a *types.Named type, so testing the type first and casting the
		// object second panics on every typed constant.
		tn, isTypeName := obj.(*types.TypeName)
		if !isTypeName || tn.IsAlias() {
			continue
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			continue
		}
		for i := range named.NumMethods() {
			m := named.Method(i)
			if !m.Exported() {
				continue
			}
			for _, bad := range offenders(m.Type(), modulePath, map[types.Type]bool{}) {
				findings = append(findings,
					fmt.Sprintf("%s.%s.%s exposes %s", pkgPath, name, m.Name(), bad))
			}
		}
	}
	return findings
}

// offenders walks t and returns the package-qualified names of every type from a
// package that is neither Fabrin's, nor the standard library, nor allowlisted.
func offenders(t types.Type, modulePath string, seen map[types.Type]bool) []string {
	if t == nil || seen[t] {
		return nil
	}
	seen[t] = true

	var out []string
	add := func(more []string) { out = append(out, more...) }

	switch t := t.(type) {
	case *types.Named:
		ours := false
		if obj := t.Obj(); obj != nil && obj.Pkg() != nil {
			p := obj.Pkg().Path()
			ours = p == modulePath || strings.HasPrefix(p, modulePath+"/")
			if !permitted(p, modulePath) {
				out = append(out, p+"."+obj.Name())
			}
		}
		// Type arguments are part of the signature: Foo[gorm.DB] exposes gorm.
		if args := t.TypeArgs(); args != nil {
			for i := range args.Len() {
				add(offenders(args.At(i), modulePath, seen))
			}
		}
		// Descend into OUR OWN named types, because their exported fields and
		// methods are our surface too. `type Options struct { DB *gorm.DB }` leaks
		// just as surely as a function returning one, and is likelier — a field is
		// added without anyone rereading the signature rules.
		//
		// Not into a FOREIGN named type: what a dependency's type is made of
		// internally is not something Fabrin exposes. Naming the type is the whole
		// commitment, and it has already been recorded above.
		if ours {
			add(offenders(t.Underlying(), modulePath, seen))
		}

	case *types.Alias:
		add(offenders(types.Unalias(t), modulePath, seen))

	case *types.Pointer:
		add(offenders(t.Elem(), modulePath, seen))
	case *types.Slice:
		add(offenders(t.Elem(), modulePath, seen))
	case *types.Array:
		add(offenders(t.Elem(), modulePath, seen))
	case *types.Chan:
		add(offenders(t.Elem(), modulePath, seen))
	case *types.Map:
		add(offenders(t.Key(), modulePath, seen))
		add(offenders(t.Elem(), modulePath, seen))

	case *types.Signature:
		add(offenders(t.Params(), modulePath, seen))
		add(offenders(t.Results(), modulePath, seen))

	case *types.Tuple:
		for i := range t.Len() {
			add(offenders(t.At(i).Type(), modulePath, seen))
		}

	case *types.Struct:
		for i := range t.NumFields() {
			if f := t.Field(i); f.Exported() {
				add(offenders(f.Type(), modulePath, seen))
			}
		}

	case *types.Interface:
		for i := range t.NumMethods() {
			if m := t.Method(i); m.Exported() {
				add(offenders(m.Type(), modulePath, seen))
			}
		}
	}
	return out
}

// permitted reports whether a package path may appear in an exported signature.
func permitted(pkgPath, modulePath string) bool {
	switch {
	case pkgPath == modulePath, strings.HasPrefix(pkgPath, modulePath+"/"):
		return true // our own
	case allowlist[pkgPath], allowlisted(pkgPath):
		return true
	case stdlib(pkgPath):
		return true
	}
	return false
}

// allowlisted covers subpackages of an allowlisted module, so blessing gin also
// blesses gin/binding rather than requiring an entry per subpackage.
func allowlisted(pkgPath string) bool {
	for allowed := range allowlist {
		if strings.HasPrefix(pkgPath, allowed+"/") {
			return true
		}
	}
	return false
}

// stdlib reports whether a package path is in the standard library.
//
// The test is that its first path segment has no dot: every module path outside
// the standard library begins with a domain. This is the same rule the go command
// itself uses to tell an import path from a module path.
func stdlib(pkgPath string) bool {
	first, _, _ := strings.Cut(pkgPath, "/")
	return !strings.Contains(first, ".")
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	var prev string
	for i, s := range sorted {
		if i == 0 || s != prev {
			out = append(out, s)
		}
		prev = s
	}
	return out
}
