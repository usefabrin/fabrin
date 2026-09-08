package schema_test

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin/schema"
)

func note(t *testing.T) schema.Model {
	t.Helper()
	m, err := schema.New("Note", "notes", schema.Int64("id").PrimaryKey(), schema.String("body").MaxLen(200), schema.String("description").Nullable(), schema.Bool("published"), schema.Time("created_at"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGenerate_DeterministicTypedStores(t *testing.T) {
	m := note(t)
	a, err := schema.Generate("records", m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := schema.Generate("records", m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("generation is not deterministic")
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "records.go", a, parser.AllErrors); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type Note struct", "sql.NullString", "NewNoteStore", "Create(ctx context.Context", "Get(ctx context.Context", "$1", "NoteModel", "time.Time"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestGenerate_RejectsInvalidNames(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		fields      []schema.Field
	}{
		{"note", "notes", []schema.Field{schema.Int64("id").PrimaryKey()}},
		{"Note", "notes; DROP TABLE users", []schema.Field{schema.Int64("id").PrimaryKey()}},
		{"Note", "notes", []schema.Field{schema.Int64("id")}},
		{"Note", "notes", []schema.Field{schema.Int64("id").PrimaryKey().Nullable()}},
		{"Note", "notes", []schema.Field{schema.Int64("id").PrimaryKey(), schema.String("id")}},
		{"Note", "notes", []schema.Field{schema.Int64("id").PrimaryKey(), schema.String("bad-name")}},
		{"Note", "notes", []schema.Field{schema.Int64("id").PrimaryKey(), schema.Int64("size").MaxLen(10)}},
	} {
		t.Run(tc.name+tc.table, func(t *testing.T) {
			if _, err := schema.New(tc.name, tc.table, tc.fields...); err == nil {
				t.Fatal("accepted invalid schema")
			}
		})
	}
	m := note(t)
	for _, pkg := range []string{"", "package", "sql", "bad-name", "_"} {
		if out, err := schema.Generate(pkg, m); err == nil || len(out) > 0 {
			t.Errorf("accepted package %q", pkg)
		}
	}
	if _, err := schema.Generate("records", m, m); err == nil {
		t.Fatal("accepted duplicate model")
	}
	collision, err := schema.New("NoteStore", "other", schema.Int64("id").PrimaryKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schema.Generate("records", m, collision); err == nil {
		t.Fatal("accepted generated symbol collision")
	}
	if _, err := schema.Generate("records", schema.Model{}); err == nil {
		t.Fatal("accepted zero model")
	}
}

func TestGenerate_CompilesAndUsesPostgres(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source, err := schema.Generate("records", note(t))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/generated_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	mod := fmt.Sprintf("module generatedpreview\n\ngo 1.25.0\n\nrequire github.com/usefabrin/fabrin v0.0.0\nreplace github.com/usefabrin/fabrin => %s\n", filepath.ToSlash(root))
	for name, contents := range map[string][]byte{"go.mod": []byte(mod), "records.go": source, "records_test.go": fixture} {
		if err := os.WriteFile(filepath.Join(dir, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "-v", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated project: %v\n%s", err, out)
	}
	t.Log(string(out))
}
