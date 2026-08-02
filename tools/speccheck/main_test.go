package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validSpec = `behaviours:
  - id: CORE-001
    what: The behavior exists.
    why: It is load-bearing.
    requirement: FR-CORE-1
    status: implemented
    test: sample_test.go::TestExact
`

const validMatrix = `| ID | Behaviour | Test |
|----|-----------|------|
| CORE-001 | Exact behavior | ` + "`sample_test.go::TestExact`" + ` |
`

func TestCheck_AcceptsAStructuralExactMapping(t *testing.T) {
	t.Parallel()

	root := fixture(t, validSpec, validMatrix, "func TestExact(t *testing.T) {}")
	if count, err := check(root); err != nil || count != 1 {
		t.Fatalf("check() = %d, %v; want 1, nil", count, err)
	}
}

func TestCheck_RejectsPreviouslyGreppableFalseEvidence(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		spec       string
		matrix     string
		testSource string
		want       string
	}{
		"invalid status": {
			spec:   strings.Replace(validSpec, "status: implemented", "status: done", 1),
			matrix: validMatrix, testSource: "func TestExact(t *testing.T) {}", want: "invalid status",
		},
		"unknown requirement": {
			spec:   strings.Replace(validSpec, "FR-CORE-1", "FR-CORE-999", 1),
			matrix: validMatrix, testSource: "func TestExact(t *testing.T) {}", want: "unknown requirement",
		},
		"id only in prose": {
			spec:       validSpec,
			matrix:     "CORE-001 is mentioned here, but this is not a matrix row.\n",
			testSource: "func TestExact(t *testing.T) {}", want: "no exact row",
		},
		"test name is only a prefix": {
			spec: validSpec, matrix: validMatrix,
			testSource: "func TestExactLonger(t *testing.T) {}", want: "no exact top-level function",
		},
		"comment is not a test": {
			spec: validSpec, matrix: validMatrix,
			testSource: "// func TestExact(t *testing.T) {}\n", want: "no exact top-level function",
		},
		"wrong test signature": {
			spec: validSpec, matrix: validMatrix,
			testSource: "func TestExact() {}", want: "is not func(*testing.T)",
		},
		"matrix and spec disagree": {
			spec:       validSpec,
			matrix:     strings.Replace(validMatrix, "TestExact", "TestOther", 1),
			testSource: "func TestExact(t *testing.T) {}", want: "does not match spec",
		},
		"duplicate YAML key": {
			spec:   strings.Replace(validSpec, "    status: implemented", "    status: implemented\n    status: planned", 1),
			matrix: validMatrix, testSource: "func TestExact(t *testing.T) {}", want: "mapping key \"status\" already defined",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := fixture(t, tc.spec, tc.matrix, tc.testSource)
			_, err := check(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check() error = %v, want text %q", err, tc.want)
			}
		})
	}
}

func TestCheck_RejectsAProductionFunctionMasqueradingAsATest(t *testing.T) {
	t.Parallel()

	spec := strings.ReplaceAll(validSpec, "sample_test.go", "sample.go")
	matrix := strings.ReplaceAll(validMatrix, "sample_test.go", "sample.go")
	root := fixture(t, spec, matrix, "func TestExact(t *testing.T) {}")
	if err := os.Rename(filepath.Join(root, "sample_test.go"), filepath.Join(root, "sample.go")); err != nil {
		t.Fatal(err)
	}
	_, err := check(root)
	if err == nil || !strings.Contains(err.Error(), "must name a _test.go file") {
		t.Fatalf("check() error = %v, want a _test.go rejection", err)
	}
}

func fixture(t *testing.T, spec, matrix, testSource string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"specs", filepath.Join("docs", "requirements")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join("specs", "system-behavior.yaml"):                  spec,
		filepath.Join("specs", "test-matrix.md"):                        matrix,
		filepath.Join("docs", "requirements", "FABRIN_REQUIREMENTS.md"): "| FR-CORE-1 | requirement | done |\n",
		"sample_test.go": "package sample\nimport \"testing\"\n" + testSource + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
