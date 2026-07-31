package scaffold

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Module is one Fabrin module added to an existing project.
//
// Generate writes the package AND wires it into main.go. A scaffold that leaves
// the user to find the wiring point has done the easy half: the module compiles,
// serves nothing, and nothing says why.
type Module struct {
	// Name is the package name, and the value the module's Name() returns.
	Name string

	// Dir is the project root. Empty means the working directory.
	Dir string
}

// moduleFiles are the same templates `fabrin new` uses for its first module.
// One pair, so a fix to the shape a module should have reaches both commands.
var moduleFiles = []struct{ tmpl, path string }{
	{"module.go.tmpl", "%[1]s.go"},
	{"module_test.go.tmpl", "%[1]s_test.go"},
}

// Generate writes the module package and wires it into newApp.
func (m Module) Generate() error {
	if err := validPackageName(m.Name); err != nil {
		return err
	}

	dir := m.Dir
	if dir == "" {
		dir = "."
	}

	mainPath := filepath.Join(dir, "main.go")
	modulePath, err := projectModulePath(dir)
	if err != nil {
		return err
	}

	pkgDir := filepath.Join(dir, m.Name)
	if _, err := os.Stat(pkgDir); err == nil {
		return fmt.Errorf("fabrin: %s already exists — module %q is already part of this project", pkgDir, m.Name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("fabrin: %s: %w", pkgDir, err)
	}

	// The edit first, in memory. Writing the package and then failing to wire it
	// leaves a module that exists and serves nothing, which is the state hardest
	// to diagnose — everything looks present.
	wired, err := wire(mainPath, modulePath, m.Name)
	if err != nil {
		return err
	}

	if err := m.writePackage(pkgDir, modulePath); err != nil {
		return err
	}

	if err := os.WriteFile(mainPath, wired, 0o644); err != nil { //nolint:gosec // source file, not a secret
		return fmt.Errorf("fabrin: write %s: %w", mainPath, err)
	}
	return nil
}

func (m Module) writePackage(pkgDir, modulePath string) error {
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return fmt.Errorf("fabrin: create %s: %w", pkgDir, err)
	}

	data := moduleData(m.Name, modulePath, filepath.Base(modulePath), "/"+m.Name)

	for _, f := range moduleFiles {
		rendered, err := render(f.tmpl, data)
		if err != nil {
			return err
		}
		out := filepath.Join(pkgDir, fmt.Sprintf(f.path, m.Name))
		if err := os.WriteFile(out, rendered, 0o644); err != nil { //nolint:gosec // source file, not a secret
			return fmt.Errorf("fabrin: write %s: %w", out, err)
		}
	}
	return nil
}

// moduleData is the template input shared by `new` and `startapp`.
func moduleData(pkg, modulePath, app, route string) map[string]string {
	return map[string]string{
		"Package": pkg,
		"Module":  modulePath,
		"App":     app,
		"Route":   route,
		"Title":   strings.ToUpper(pkg[:1]) + pkg[1:],
	}
}

// projectModulePath finds the project's module path, and confirms this is a
// Fabrin project rather than any directory with a go.mod.
func projectModulePath(dir string) (string, error) {
	gomod := filepath.Join(dir, "go.mod")
	raw, err := os.ReadFile(gomod)
	if err != nil {
		return "", fmt.Errorf("fabrin: %s not found — run this inside a Fabrin project (it needs go.mod and a main.go wiring modules in newApp)", gomod)
	}

	var modulePath string
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			modulePath = strings.TrimSpace(rest)
			break
		}
	}
	if modulePath == "" {
		return "", fmt.Errorf("fabrin: no module line in %s", gomod)
	}

	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		return "", fmt.Errorf("fabrin: %s not found — run this inside a Fabrin project (it needs go.mod and a main.go wiring modules in newApp)", filepath.Join(dir, "main.go"))
	}
	return modulePath, nil
}

// wire returns main.go with the module imported and constructed.
//
// # Why the AST locates but does not print
//
// A regex is fragile against a user who reformatted, added a comment, or wired a
// port — all of which the generated file's own comments encourage. Parsing finds
// the real fabrin.New call rather than a string that looks like one.
//
// But re-printing the file with go/format would reformat code the user owns and
// can move their comments, turning "add a module" into a diff nobody wants to
// review. So the AST is used only to find WHERE, and the edit is a byte splice:
// the correctness of a parser with the diff of a one-line insert.
//
// (golang.org/x/tools/go/ast/astutil would make the import trivial. It is a
// dev-only dependency, and NFR-3 keeps those out of the framework's go.mod, so
// the import edit is hand-rolled.)
func wire(mainPath, modulePath, name string) ([]byte, error) {
	src, err := os.ReadFile(mainPath)
	if err != nil {
		return nil, fmt.Errorf("fabrin: read %s: %w", mainPath, err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainPath, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("fabrin: %s does not parse, so it cannot be edited safely: %w", mainPath, err)
	}

	call := findNewCall(file)
	if call == nil {
		return nil, fmt.Errorf("fabrin: no fabrin.New(...) call found in %s — wire %s.New() in yourself", mainPath, name)
	}

	importPath := modulePath + "/" + name

	// Descending offsets, so the earlier splice does not move the later one.
	argEdit := insertArgument(fset, src, call, name)
	impEdit, err := insertImport(fset, src, file, importPath)
	if err != nil {
		return nil, err
	}

	out := splice(src, argEdit)
	out = splice(out, impEdit)

	// Verify the edit rather than assume it. A parse failure here is this code's
	// fault, not the user's, and the caller must not write the result.
	edited, err := parser.ParseFile(token.NewFileSet(), mainPath, out, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("fabrin: wiring %s produced a %s that does not parse — this is a bug, please report it: %w", name, mainPath, err)
	}
	if findNewCall(edited) == nil {
		return nil, fmt.Errorf("fabrin: wiring %s lost the fabrin.New call in %s — this is a bug, please report it", name, mainPath)
	}
	return out, nil
}

// edit is one insertion, as a byte offset and the text to put there.
type edit struct {
	at   int
	text string
}

func splice(src []byte, e edit) []byte {
	out := make([]byte, 0, len(src)+len(e.text))
	out = append(out, src[:e.at]...)
	out = append(out, e.text...)
	return append(out, src[e.at:]...)
}

// findNewCall returns the fabrin.New(...) call, wherever in the file it is.
//
// Not "inside newApp": a user who renamed that function, or inlined it back into
// main, still deserves the wiring to work. The call itself is the thing that
// matters, and there is exactly one in a Fabrin main.
func findNewCall(file *ast.File) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "New" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fabrin" {
			found = call
			return false
		}
		return true
	})
	return found
}

// insertArgument places `<name>.New()` as the last argument of call.
func insertArgument(fset *token.FileSet, src []byte, call *ast.CallExpr, name string) edit {
	lparen := fset.Position(call.Lparen)
	rparen := fset.Position(call.Rparen)

	// Single line: `fabrin.New(opts, home.New())` — a comma and a space, before
	// the closing paren. The multi-line branch's trailing comma would be a syntax
	// error here.
	if lparen.Line == rparen.Line {
		return edit{at: rparen.Offset, text: ", " + name + ".New()"}
	}

	// Multi-line. Match the indentation of the closing paren's line plus one tab,
	// rather than assuming two: the call may sit inside a closure, a switch, or
	// whatever the user has since wrapped it in.
	indent := lineIndent(src, rparen.Offset) + "\t"
	return edit{at: startOfLine(src, rparen.Offset), text: indent + name + ".New(),\n"}
}

// insertImport places importPath in the project's own import group, in sorted
// position, or opens a new group at the end.
func insertImport(fset *token.FileSet, src []byte, file *ast.File, importPath string) (edit, error) {
	var decl *ast.GenDecl
	for _, d := range file.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			decl = g
			break
		}
	}
	if decl == nil {
		return edit{}, fmt.Errorf("fabrin: no import block in the generated main.go")
	}

	// The project's own imports are the ones sharing this module path's prefix.
	// They are the last group in a file gofmt has seen, and the right home for a
	// sibling package.
	prefix := importPath[:strings.LastIndex(importPath, "/")+1]

	// gofmt sorts within a group, so the insertion point is between the last
	// sibling that sorts before this path and the first that sorts after. Getting
	// this wrong is not a compile error — it is a file that `gofmt -l` flags on
	// the user's next commit, blaming their edit rather than ours.
	var before, after *ast.ImportSpec
	for _, spec := range decl.Specs {
		imp, ok := spec.(*ast.ImportSpec)
		if !ok {
			continue
		}
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		if path < importPath {
			before = imp
		} else if after == nil {
			after = imp
		}
	}

	if before != nil {
		end := fset.Position(before.End()).Offset
		return edit{at: endOfLine(src, end), text: "\n" + lineIndent(src, end) + `"` + importPath + `"`}, nil
	}
	if after != nil {
		start := fset.Position(after.Pos()).Offset
		indent := lineIndent(src, start)
		return edit{at: startOfLine(src, start), text: indent + `"` + importPath + `"` + "\n"}, nil
	}

	// No sibling imports yet: open a group of their own before the closing paren,
	// which is what gofmt would keep.
	rparen := fset.Position(decl.Rparen)
	return edit{at: startOfLine(src, rparen.Offset), text: "\n\t\"" + importPath + "\"\n"}, nil
}

func startOfLine(src []byte, offset int) int {
	i := strings.LastIndexByte(string(src[:offset]), '\n')
	return i + 1
}

func endOfLine(src []byte, offset int) int {
	if i := strings.IndexByte(string(src[offset:]), '\n'); i >= 0 {
		return offset + i
	}
	return len(src)
}

func lineIndent(src []byte, offset int) string {
	line := src[startOfLine(src, offset):]
	return string(line[:len(line)-len(strings.TrimLeft(string(line), " \t"))])
}

// goKeywords are rejected as package names because `package select` does not
// compile, and the resulting error names the generated file rather than the
// argument the user typed.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

func validPackageName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("fabrin: module name is empty — try `fabrin startapp billing`")
	case goKeywords[name]:
		return fmt.Errorf("fabrin: %q is a Go keyword and cannot be a package name", name)
	}

	for i, r := range name {
		switch {
		case unicode.IsLetter(r) || r == '_':
		case unicode.IsDigit(r) && i > 0:
		default:
			return fmt.Errorf("fabrin: module name %q is not a valid Go package name — letters, digits and underscore only, not starting with a digit", name)
		}
	}
	return nil
}
