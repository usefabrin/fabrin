package fabrin

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/usefabrin/fabrin/migratediff"
	"github.com/usefabrin/fabrin/orm"
)

func TestNextVersionAdvancesPastARecordedVersionFromTheCurrentSecond(t *testing.T) {
	current := time.Now().UTC().Format("20060102150405")
	got, err := nextVersion(map[string][]manifestEntry{
		"shop": {{Version: current}},
	})
	if err != nil {
		t.Fatalf("nextVersion: %v", err)
	}
	if got <= current {
		t.Fatalf("nextVersion = %q, want a version after %q", got, current)
	}
}

func TestResolveRenames_ConfirmedPairBecomesOneRename(t *testing.T) {
	before, after := renameSnapshots(t)
	ops := migratediff.Diff(before, after)
	var out strings.Builder

	resolved, err := resolveRenames(ops, before, after, strings.NewReader("y\n"), &out, true)
	if err != nil {
		t.Fatalf("resolveRenames: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved operations = %#v, want one rename", resolved)
	}
	rename, ok := resolved[0].(migratediff.RenameColumn)
	if !ok || rename.Table != "orders" || rename.From != "total" || rename.To != "amount" {
		t.Errorf("resolved operation = %#v, want orders.total -> orders.amount", resolved[0])
	}
	if !strings.Contains(out.String(), "orders.total -> orders.amount") {
		t.Errorf("prompt does not name the candidate: %q", out.String())
	}
}

func TestResolveRenames_DeclinedPairRemainsDropAndAdd(t *testing.T) {
	before, after := renameSnapshots(t)
	ops := migratediff.Diff(before, after)

	resolved, err := resolveRenames(ops, before, after, strings.NewReader("n\n"), &strings.Builder{}, true)
	if err != nil {
		t.Fatalf("resolveRenames: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved operations = %#v, want drop and add", resolved)
	}
	if _, ok := resolved[0].(migratediff.DropColumn); !ok {
		t.Errorf("first operation = %T, want DropColumn", resolved[0])
	}
	if _, ok := resolved[1].(migratediff.AddColumn); !ok {
		t.Errorf("second operation = %T, want AddColumn", resolved[1])
	}
}

func TestResolveRenames_NonInteractiveRunRefusesPossibleDataLoss(t *testing.T) {
	before, after := renameSnapshots(t)
	ops := migratediff.Diff(before, after)

	_, err := resolveRenames(ops, before, after, strings.NewReader("y\n"), &strings.Builder{}, false)
	if err == nil {
		t.Fatal("non-interactive rename candidate must fail instead of dropping data")
	}
	for _, want := range []string{"orders.total", "orders.amount", "non-interactive"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q: %v", want, err)
		}
	}
}

func TestResolveRenames_ConfirmedConstrainedColumnRequiresHandWrittenMigration(t *testing.T) {
	before, err := orm.NewSnapshot([]orm.Registered{{Module: "shop", Model: orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "code", Type: orm.TypeString, Unique: true},
		},
	}}})
	if err != nil {
		t.Fatalf("before snapshot: %v", err)
	}
	after, err := orm.NewSnapshot([]orm.Registered{{Module: "shop", Model: orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "slug", Type: orm.TypeString, Unique: true},
		},
	}}})
	if err != nil {
		t.Fatalf("after snapshot: %v", err)
	}

	_, err = resolveRenames(migratediff.Diff(before, after), before, after, strings.NewReader("yes\n"), &strings.Builder{}, true)
	if err == nil {
		t.Fatal("a constrained rename must not leave its generated object name stale")
	}
	for _, want := range []string{"orders.code", "orders.slug", "hand-write", "constraint"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q: %v", want, err)
		}
	}
}

func TestResolveRenames_AsksAmbiguousCandidatesOneAtATime(t *testing.T) {
	before, err := orm.NewSnapshot([]orm.Registered{{Module: "shop", Model: orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "subtotal", Type: orm.TypeFloat},
			{Name: "tax", Type: orm.TypeFloat},
		},
	}}})
	if err != nil {
		t.Fatalf("before snapshot: %v", err)
	}
	after, err := orm.NewSnapshot([]orm.Registered{{Module: "shop", Model: orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "amount", Type: orm.TypeFloat},
			{Name: "vat", Type: orm.TypeFloat},
		},
	}}})
	if err != nil {
		t.Fatalf("after snapshot: %v", err)
	}

	var out strings.Builder
	resolved, err := resolveRenames(
		migratediff.Diff(before, after), before, after,
		strings.NewReader("no\nno\nno\nno\n"), &out, true,
	)
	if err != nil {
		t.Fatalf("resolveRenames: %v", err)
	}
	if got := strings.Count(out.String(), "Did you rename "); got != 4 {
		t.Errorf("prompt count = %d, want four individual candidate questions: %q", got, out.String())
	}
	if len(resolved) != 4 {
		t.Errorf("declining every candidate yielded %d operations, want two drops and two adds", len(resolved))
	}
}

func TestRunMakemigrations_ConfirmedRenameGeneratesReversibleSQL(t *testing.T) {
	raw := generateRenameMigration(t, "yes\n")
	for _, want := range []string{
		`RENAME COLUMN \"total\" TO \"amount\"`,
		`RENAME COLUMN \"amount\" TO \"total\"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("generated migration must contain %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "DROP COLUMN") {
		t.Errorf("confirmed rename must not generate a drop:\n%s", raw)
	}
}

func TestRunMakemigrations_DeclinedRenameGeneratesDropAndAdd(t *testing.T) {
	raw := generateRenameMigration(t, "no\n")
	for _, want := range []string{
		`DROP COLUMN \"total\"`,
		`ADD COLUMN \"amount\"`,
		`dropping \"orders.total\" discards its data`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("declined rename migration must contain %q:\n%s", want, raw)
		}
	}
}

func generateRenameMigration(t *testing.T, answer string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	db, err := sql.Open("pgx/v5", "postgres://unused")
	if err != nil {
		t.Fatalf("open pgx handle: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	before := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "total", Type: orm.TypeFloat},
		},
	}
	app, err := New(Options{Addr: "127.0.0.1:0", DB: db}, renameModule{model: before})
	if err != nil {
		t.Fatalf("New before: %v", err)
	}
	if err := app.runMakemigrations(context.Background(), strings.NewReader(""), io.Discard, false); err != nil {
		t.Fatalf("record before: %v", err)
	}

	after := before
	after.Fields[1].Name = "amount"
	app, err = New(Options{Addr: "127.0.0.1:0", DB: db}, renameModule{model: after})
	if err != nil {
		t.Fatalf("New after: %v", err)
	}
	var out strings.Builder
	if err := app.runMakemigrations(context.Background(), strings.NewReader(answer), &out, true); err != nil {
		t.Fatalf("generate rename: %v", err)
	}

	versions := internalGeneratedVersions(t, filepath.Join("shop", "migrations"))
	if len(versions) != 2 {
		t.Fatalf("generated versions = %v, want two", versions)
	}
	return internalGeneratedSource(t, filepath.Join("shop", "migrations"), versions[1])
}

type renameModule struct {
	model orm.Model
}

func (renameModule) Name() string          { return "shop" }
func (renameModule) Routes(Router)         {}
func (m renameModule) Models() []orm.Model { return []orm.Model{m.model} }

func internalGeneratedVersions(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var versions []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && name != "all.go" {
			versions = append(versions, strings.SplitN(name, "_", 2)[0])
		}
	}
	return versions
}

func internalGeneratedSource(t *testing.T, dir, version string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, version+"_") && strings.HasSuffix(name, ".go") {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			return string(raw)
		}
	}
	t.Fatalf("no generated migration for version %s", version)
	return ""
}

func renameSnapshots(t *testing.T) (orm.Snapshot, orm.Snapshot) {
	t.Helper()
	before, err := orm.NewSnapshot([]orm.Registered{{Module: "shop", Model: orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "total", Type: orm.TypeFloat},
		},
	}}})
	if err != nil {
		t.Fatalf("before snapshot: %v", err)
	}
	after, err := orm.NewSnapshot([]orm.Registered{{Module: "shop", Model: orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "amount", Type: orm.TypeFloat},
		},
	}}})
	if err != nil {
		t.Fatalf("after snapshot: %v", err)
	}
	return before, after
}
