// Package scaffold generates a runnable Fabrin project.
//
// It is internal deliberately. Every exported symbol in a library is a promise
// to strangers, and a template's shape is the least stable thing in the whole
// repository — the generated project should be free to change with every
// milestone. cmd/fabrin is package main and contributes nothing to
// api/fabrin.txt, so the templates stay out of the public surface entirely.
package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// templates holds the project files. They are .tmpl rather than .go so the Go
// toolchain does not try to compile them as part of this package — and
// gitignore.tmpl rather than .gitignore because go:embed skips dotfiles.
//
//go:embed templates/*.tmpl
var templates embed.FS

// files maps a template to its path in the generated project.
//
// A slice rather than a map so the order is fixed: the failure message from a
// broken template names a predictable file, and a partial write leaves a
// predictable state.
var files = []struct{ tmpl, path string }{
	{"go.mod.tmpl", "go.mod"},
	{"main.go.tmpl", "main.go"},
	{"home.go.tmpl", filepath.Join("home", "home.go")},
	{"home_test.go.tmpl", filepath.Join("home", "home_test.go")},
	{"justfile.tmpl", "justfile"},
	{"gitignore.tmpl", ".gitignore"},
	{"README.md.tmpl", "README.md"},
}

// Project is what to generate and where.
type Project struct {
	// Name is the project's directory name, and the default module path.
	Name string

	// Module is the Go module path. Empty means Name.
	//
	// `go mod init demo` produces a module named demo and builds, so the bare
	// default works without asking. Anyone publishing needs the real path, which
	// is why this exists rather than being derived and left alone.
	Module string

	// Dir is the directory to create. Empty means ./<Name>.
	Dir string
}

// Generate writes the project.
//
// Validation happens before the first write. A half-written project is harder to
// recover from than none, because the user cannot tell which files are theirs.
func (p Project) Generate() error {
	if err := validName(p.Name); err != nil {
		return err
	}

	module := p.Module
	if module == "" {
		module = p.Name
	}
	if err := validModule(module); err != nil {
		return err
	}

	dir := p.Dir
	if dir == "" {
		dir = p.Name
	}
	if err := emptyOrAbsent(dir); err != nil {
		return err
	}

	data := struct{ Name, Module, Go string }{
		Name:   p.Name,
		Module: module,
		Go:     goDirective,
	}

	for _, f := range files {
		out := filepath.Join(dir, f.path)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return fmt.Errorf("fabrin: create %s: %w", filepath.Dir(out), err)
		}

		rendered, err := render(f.tmpl, data)
		if err != nil {
			return err
		}

		// 0o644, not 0o600: these are source files the user will read, edit, and
		// commit, not secrets.
		if err := os.WriteFile(out, rendered, 0o644); err != nil { //nolint:gosec // source files, not secrets
			return fmt.Errorf("fabrin: write %s: %w", out, err)
		}
	}
	return nil
}

func render(name string, data any) ([]byte, error) {
	t, err := template.New(name).
		// A missing field renders as "<no value>" by default, producing a project
		// that compiles into nonsense. Fail instead.
		Option("missingkey=error").
		ParseFS(templates, "templates/"+name)
	if err != nil {
		return nil, fmt.Errorf("fabrin: parse template %s: %w", name, err)
	}

	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("fabrin: render %s: %w", name, err)
	}
	return []byte(b.String()), nil
}

// validName rejects anything that is not usable as both a directory name and the
// tail of a module path.
//
// The separator checks are not only about tidiness: "../escape" as a name would
// write outside the directory the user asked for.
func validName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("fabrin: project name is empty — try `fabrin new myapp`")
	case name == "." || name == "..":
		return fmt.Errorf("fabrin: %q is not a project name", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("fabrin: project name %q contains a path separator — pass a name, and -dir if you want it somewhere else", name)
	case strings.ContainsAny(name, " \t"):
		return fmt.Errorf("fabrin: project name %q contains whitespace, which cannot appear in a Go import path", name)
	}
	return nil
}

func validModule(module string) error {
	switch {
	case strings.ContainsAny(module, " \t"):
		return fmt.Errorf("fabrin: module path %q contains whitespace", module)
	case strings.HasPrefix(module, "/") || strings.HasSuffix(module, "/"):
		return fmt.Errorf("fabrin: module path %q has a leading or trailing slash", module)
	case strings.Contains(module, "//"):
		return fmt.Errorf("fabrin: module path %q has an empty path element", module)
	}
	return nil
}

// emptyOrAbsent allows a directory that does not exist, or one that exists and is
// empty — `mkdir demo && cd demo && fabrin new demo` is a thing people do.
//
// Anything else is refused by name. Overwriting someone's work is not something a
// CLI can offer to undo.
func emptyOrAbsent(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fabrin: read %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("fabrin: %s is not empty (it contains %q) — refusing to overwrite", dir, entries[0].Name())
	}
	return nil
}

// goDirective is what the generated go.mod's `go` line says.
//
// Fabrin's own directive, NOT the toolchain that happens to be running. Emitting
// the running version would pin the new project to whatever the scaffolding
// developer had installed — so a project generated on Go 1.26 stops building for
// a colleague on 1.25, for no reason anyone can see in the diff. Requiring less
// only widens who can build it, and NFR-2 makes the same argument for Fabrin
// itself.
//
// It is a constant because a package cannot go:embed a file above its own
// directory. TestGoDirective_MatchesFabrinsOwnGoMod is what stops it drifting.
const goDirective = "1.25"
