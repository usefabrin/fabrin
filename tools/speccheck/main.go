// Command speccheck validates Fabrin's behaviour spec against requirements,
// executable tests, and the human-readable test matrix.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	behaviourID   = regexp.MustCompile(`^[A-Z]+-[0-9]{3}$`)
	requirementID = regexp.MustCompile(`^(?:FR-[A-Z]+|NFR|INV)-[0-9]+$`)
)

type document struct {
	Behaviours []behaviour `yaml:"behaviours"`
}

type behaviour struct {
	ID          string `yaml:"id"`
	What        string `yaml:"what"`
	Why         string `yaml:"why"`
	Requirement string `yaml:"requirement"`
	Status      string `yaml:"status"`
	Test        string `yaml:"test"`
}

type matrixRow struct {
	test string
	line int
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	count, err := check(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("✓ specs: %d behaviour(s), structurally valid and traceable to requirements and tests.\n", count)
}

func check(root string) (int, error) {
	specPath := filepath.Join(root, "specs", "system-behavior.yaml")
	matrixPath := filepath.Join(root, "specs", "test-matrix.md")
	requirementsPath := filepath.Join(root, "docs", "requirements", "FABRIN_REQUIREMENTS.md")

	doc, err := decodeSpec(specPath)
	if err != nil {
		return 0, err
	}
	matrix, err := readMatrix(matrixPath)
	if err != nil {
		return 0, err
	}
	requirements, err := readRequirements(requirementsPath)
	if err != nil {
		return 0, err
	}

	seen := make(map[string]bool, len(doc.Behaviours))
	var errs []error
	for i, b := range doc.Behaviours {
		where := fmt.Sprintf("specs: behaviour %d", i+1)
		if !behaviourID.MatchString(b.ID) {
			errs = append(errs, fmt.Errorf("%s has invalid id %q", where, b.ID))
			continue
		}
		if seen[b.ID] {
			errs = append(errs, fmt.Errorf("specs: duplicate behaviour id %s", b.ID))
			continue
		}
		seen[b.ID] = true

		for field, value := range map[string]string{"what": b.What, "why": b.Why, "requirement": b.Requirement, "status": b.Status} {
			if strings.TrimSpace(value) == "" {
				errs = append(errs, fmt.Errorf("specs: %s has empty %s", b.ID, field))
			}
		}
		if !requirementID.MatchString(b.Requirement) || !requirements[b.Requirement] {
			errs = append(errs, fmt.Errorf("specs: %s cites unknown requirement %q", b.ID, b.Requirement))
		}
		if b.Status != "planned" && b.Status != "implemented" {
			errs = append(errs, fmt.Errorf("specs: %s has invalid status %q (want planned or implemented)", b.ID, b.Status))
		}

		row, ok := matrix[b.ID]
		if !ok {
			errs = append(errs, fmt.Errorf("specs: %s has no exact row in specs/test-matrix.md", b.ID))
			continue
		}
		wantTest := b.Test
		if b.Status == "planned" {
			wantTest = "_planned_"
			if b.Test != "" {
				errs = append(errs, fmt.Errorf("specs: planned behaviour %s must not name a test", b.ID))
			}
		} else if b.Test == "" {
			errs = append(errs, fmt.Errorf("specs: implemented behaviour %s names no test", b.ID))
		}
		if row.test != wantTest {
			errs = append(errs, fmt.Errorf("specs: %s matrix test %q does not match spec %q", b.ID, row.test, wantTest))
		}
		if b.Status == "implemented" && b.Test != "" {
			if err := checkTest(root, b.Test); err != nil {
				errs = append(errs, fmt.Errorf("specs: %s: %w", b.ID, err))
			}
		}
	}

	for id := range matrix {
		if !seen[id] {
			errs = append(errs, fmt.Errorf("specs: matrix has row for %s, which is absent from the spec", id))
		}
	}
	if len(doc.Behaviours) == 0 {
		errs = append(errs, errors.New("specs: no behaviours declared"))
	}
	return len(doc.Behaviours), errors.Join(errs...)
}

func decodeSpec(path string) (document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return document{}, fmt.Errorf("specs: read %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return document{}, fmt.Errorf("specs: decode %s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return document{}, fmt.Errorf("specs: %s contains more than one YAML document", path)
		}
		return document{}, fmt.Errorf("specs: decode trailing YAML in %s: %w", path, err)
	}
	return doc, nil
}

func readMatrix(path string) (map[string]matrixRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("specs: read %s: %w", path, err)
	}
	rows := map[string]matrixRow{}
	var errs []error
	for i, line := range strings.Split(string(data), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}
		id := strings.TrimSpace(cells[1])
		if !behaviourID.MatchString(id) {
			continue
		}
		if prev, ok := rows[id]; ok {
			errs = append(errs, fmt.Errorf("specs: duplicate matrix row %s on lines %d and %d", id, prev.line, i+1))
			continue
		}
		rawTest := strings.TrimSpace(cells[3])
		test := rawTest
		if strings.HasPrefix(rawTest, "`") {
			rest := strings.TrimPrefix(rawTest, "`")
			end := strings.Index(rest, "`")
			if end < 0 {
				errs = append(errs, fmt.Errorf("specs: matrix row %s line %d has an unterminated test reference", id, i+1))
				continue
			}
			test = rest[:end]
		}
		rows[id] = matrixRow{test: test, line: i + 1}
	}
	return rows, errors.Join(errs...)
}

func readRequirements(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("specs: read %s: %w", path, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		id := strings.TrimSpace(cells[1])
		if requirementID.MatchString(id) {
			out[id] = true
		}
	}
	return out, nil
}

func checkTest(root, ref string) error {
	path, fn, hasFunction := strings.Cut(ref, "::")
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid test path %q", path)
	}
	full := filepath.Join(root, path)
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("test file %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("test path %q is a directory", path)
	}
	if !hasFunction {
		return nil
	}
	if !strings.HasSuffix(path, "_test.go") {
		return fmt.Errorf("test function reference %q must name a _test.go file", ref)
	}
	if fn == "" || strings.Contains(fn, "::") {
		return fmt.Errorf("invalid test function in %q", ref)
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), full, nil, 0)
	if err != nil {
		return fmt.Errorf("parse test file %q: %w", path, err)
	}
	testingPackages := map[string]bool{}
	for _, imp := range parsed.Imports {
		if imp.Path.Value != `"testing"` {
			continue
		}
		name := "testing"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		testingPackages[name] = true
	}
	for _, decl := range parsed.Decls {
		f, ok := decl.(*ast.FuncDecl)
		if !ok || f.Recv != nil || f.Name.Name != fn {
			continue
		}
		if validTestSignature(f, testingPackages) {
			return nil
		}
		return fmt.Errorf("top-level function %s in %q is not func(*testing.T)", fn, path)
	}
	return fmt.Errorf("test file %q has no exact top-level function %s", path, fn)
}

func validTestSignature(f *ast.FuncDecl, testingPackages map[string]bool) bool {
	if !strings.HasPrefix(f.Name.Name, "Test") || f.Type.Params == nil || len(f.Type.Params.List) != 1 {
		return false
	}
	if f.Type.Results != nil && len(f.Type.Results.List) != 0 {
		return false
	}
	ptr, ok := f.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := ptr.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && testingPackages[pkg.Name]
}
