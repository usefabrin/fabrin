package fabrin_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/orm"
)

// migratingOwner is a module that owns a table AND returns the migrations its
// directory will hold — the shape makemigrations scaffolds for.
type migratingOwner struct {
	testModule
	models []orm.Model
}

func (m migratingOwner) Models() []orm.Model { return m.models }

func owner(name, table string) fabrin.Module {
	return migratingOwner{
		testModule: testModule{name: name, routes: func(fabrin.Router) {}},
		models: []orm.Model{{
			Table:  table,
			Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
		}},
	}
}

func ownerWith(name string, m orm.Model) fabrin.Module {
	return migratingOwner{
		testModule: testModule{name: name, routes: func(fabrin.Router) {}},
		models:     []orm.Model{m},
	}
}

func ordersModelFull() orm.Model {
	return orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, MaxLen: 32},
		},
	}
}

// projectChdir isolates a test in its own directory, because makemigrations
// writes into ./<module>/migrations relative to where the app runs. Deliberately
// not parallel-safe: a working directory is process state.
func projectChdir(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

func migrationFiles(t *testing.T, module string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(module, "migrations"))
	if err != nil {
		t.Fatalf("read migrations dir for %s: %v", module, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func countSuffix(names []string, suffix string, exclude string) int {
	n := 0
	for _, name := range names {
		if name == exclude {
			continue
		}
		if strings.HasSuffix(name, suffix) {
			n++
		}
	}
	return n
}

func TestExecute_MakemigrationsGeneratesFilesForANewTable(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	projectChdir(t)

	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("shop", ordersModelFull()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out strings.Builder
	if err := app.Execute(context.Background(), &out, []string{"makemigrations"}); err != nil {
		t.Fatalf("makemigrations: %v", err)
	}
	for _, want := range []string{"orders", "create table orders"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must mention %q, got: %q", want, out.String())
		}
	}

	dir := filepath.Join("shop", "migrations")
	names := migrationFiles(t, "shop")
	for _, required := range []string{"manifest.json", "all.go"} {
		found := false
		for _, n := range names {
			if n == required {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was not written into %s; files: %v", required, dir, names)
		}
	}
	if got := countSuffix(names, ".go", "all.go"); got != 1 {
		t.Errorf("got %d generated migration files, want 1 (%v)", got, names)
	}
	if got := countSuffix(names, ".state.json", ""); got != 1 {
		t.Errorf("got %d state files, want 1 (%v)", got, names)
	}

	// The generated Go package must compile, not merely parse. An omitted import
	// is valid syntax and still leaves the user with code that fails at their next
	// build — exactly the defect this test exists to catch.
	var genFile string
	for _, n := range names {
		if strings.HasSuffix(n, ".go") && n != "all.go" {
			genFile = n
		}
	}
	src, err := os.ReadFile(filepath.Join(dir, genFile))
	if err != nil {
		t.Fatalf("read %s: %v", genFile, err)
	}
	goMod := fmt.Sprintf(`module example.com/generated

go 1.25

require github.com/usefabrin/fabrin v0.0.0

replace github.com/usefabrin/fabrin => %s
`, filepath.ToSlash(repoRoot))
	if err := os.WriteFile("go.mod", []byte(goMod), 0o644); err != nil {
		t.Fatalf("write generated-project go.mod: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "./shop/migrations")
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated migration package does not compile: %v\n%s\n%s", err, output, src)
	}

	// Filename carries the version, at a fixed width — the same discipline the
	// engine applies to versions themselves, and what the duplicate-version gate
	// will read off disk without compiling anything.
	version := strings.SplitN(genFile, "_", 2)[0]
	if len(version) != 14 {
		t.Errorf("filename-derived version %q is not fixed-width 14", version)
	}

	// The state sidecar is the "before" every later makemigrations diffs
	// against; it must reconstruct exactly the schema just declared.
	stateBytes, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(genFile, ".go")+".state.json"))
	if err != nil {
		t.Fatalf("read state sidecar: %v", err)
	}
	parsed, err := orm.ParseSnapshot(stateBytes, version+".state.json")
	if err != nil {
		t.Fatalf("generated state does not parse: %v", err)
	}
	r := orm.NewRegistry()
	if err := r.Register("shop", ordersModelFull()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	want, err := orm.NewSnapshot(r.Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	gotModels := parsed.Models()
	wantModels := want.Models()
	if len(gotModels) != 1 || gotModels[0].Model.Table != wantModels[0].Model.Table ||
		len(gotModels[0].Model.Fields) != len(wantModels[0].Model.Fields) {
		t.Errorf("recorded state %+v does not match declared schema %+v", gotModels, wantModels)
	}
}

func TestExecute_MakemigrationsTwiceSaysNoChanges(t *testing.T) {
	projectChdir(t)

	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("shop", ordersModelFull()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := app.Execute(ctx, io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("first makemigrations: %v", err)
	}

	before := migrationFiles(t, "shop")
	var out strings.Builder
	if err := app.Execute(ctx, &out, []string{"makemigrations"}); err != nil {
		t.Fatalf("second makemigrations: %v", err)
	}
	if s := strings.ToLower(out.String()); !strings.Contains(s, "no changes") {
		t.Errorf("an unchanged project must say so, got: %q", out.String())
	}
	after := migrationFiles(t, "shop")
	if strings.Join(before, "|") != strings.Join(after, "|") {
		t.Errorf("an unchanged run rewrote files:\nbefore %v\nafter  %v", before, after)
	}
}

func TestExecute_MakemigrationsRefusesUnparsableRecordedState(t *testing.T) {
	projectChdir(t)

	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("shop", ordersModelFull()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := app.Execute(ctx, io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("first makemigrations: %v", err)
	}

	// A corrupted record must never become a silently empty "before" — that is
	// the create-everything-against-a-live-schema disaster. It fails here,
	// naming the file.
	broken := filepath.Join("shop", "migrations")
	entries, _ := os.ReadDir(broken)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".state.json") {
			path := filepath.Join(broken, e.Name())
			if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
				t.Fatalf("corrupt state file: %v", err)
			}
			err := app.Execute(ctx, io.Discard, []string{"makemigrations"})
			if !errors.Is(err, orm.ErrBadState) {
				t.Fatalf("got %v, want it to wrap orm.ErrBadState", err)
			}
			if !strings.Contains(err.Error(), e.Name()) {
				t.Errorf("the error must name the corrupt file, got: %v", err)
			}
			return
		}
	}
	t.Fatal("no state file found to corrupt")
}

func TestExecute_MakemigrationsCarriesHandWrittenStepsForward(t *testing.T) {
	projectChdir(t)

	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("shop", ordersModelFull()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// First run records the two-column schema.
	if err := app.Execute(ctx, io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("first makemigrations: %v", err)
	}

	// Someone hand-writes a migration afterwards: registered in the manifest so
	// it runs, but carrying no state file, because Fabrin cannot fold free-form
	// SQL into schema state. The chain continues from the last known state —
	// refusing would make hand-written and generated migrations unable to mix.
	manifestPath := filepath.Join("shop", "migrations", "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	injected := strings.Replace(string(raw),
		"\n]", `,{"version":"99990101000000","file":"hand_written.go"}
]`, 1)
	if injected == string(raw) {
		t.Fatal("manifest injection did not apply; the fixture drifted from the real format")
	}
	if err := os.WriteFile(manifestPath, []byte(injected), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join("shop", "migrations", "hand_written.go")); err == nil {
		_ = os.Remove(filepath.Join("shop", "migrations", "hand_written.go"))
	}
	if err := os.WriteFile(filepath.Join("shop", "migrations", "hand_written.go"),
		[]byte("// hand-written for the test\npackage migrations\n"), 0o644); err != nil {
		t.Fatalf("write hand-written stub: %v", err)
	}

	// Now the model gains a column. The diff must be ONE added column against
	// the carried-forward state — not a create-everything against an empty one.
	grown := ordersModelFull()
	grown.Fields = append(grown.Fields, orm.Field{Name: "total", Type: orm.TypeFloat, Nullable: true})
	app2, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0", DB: db}, ownerWith("shop", grown))
	if err != nil {
		t.Fatalf("New again: %v", err)
	}
	var out strings.Builder
	if err := app2.Execute(ctx, &out, []string{"makemigrations"}); err != nil {
		t.Fatalf("second makemigrations: %v", err)
	}
	if !strings.Contains(out.String(), "add column orders.total") {
		t.Errorf("expected a single add-column against carried-forward state, got: %q", out.String())
	}
	if strings.Contains(out.String(), "create table orders") {
		t.Errorf("carried-forward state was lost; the diff recreated existing tables: %q", out.String())
	}
}

func TestExecute_MakemigrationsNoInputRefusesPossibleRenameBeforeWriting(t *testing.T) {
	projectChdir(t)

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
	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0", DB: db}, ownerWith("shop", before))
	if err != nil {
		t.Fatalf("New before: %v", err)
	}
	if err := app.Execute(t.Context(), io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("record before: %v", err)
	}
	filesBefore := migrationFiles(t, "shop")

	after := before
	after.Fields[1].Name = "amount"
	app, err = fabrin.New(fabrin.Options{Addr: "127.0.0.1:0", DB: db}, ownerWith("shop", after))
	if err != nil {
		t.Fatalf("New after: %v", err)
	}
	err = app.Execute(t.Context(), io.Discard, []string{"makemigrations", "-no-input"})
	if err == nil {
		t.Fatal("-no-input must refuse a possible rename")
	}
	for _, want := range []string{"orders.total", "orders.amount", "non-interactive"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q: %v", want, err)
		}
	}
	filesAfter := migrationFiles(t, "shop")
	if !slices.Equal(filesAfter, filesBefore) {
		t.Errorf("refused generation wrote files: before %v, after %v", filesBefore, filesAfter)
	}
}

func TestExecute_MakemigrationsGivesEachOwningModuleItsOwnMigration(t *testing.T) {
	projectChdir(t)

	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("billing", orm.Model{
			Table:  "invoices",
			Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
		}),
		ownerWith("shop", ordersModelFull()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out strings.Builder
	if err := app.Execute(context.Background(), &out, []string{"makemigrations"}); err != nil {
		t.Fatalf("makemigrations: %v", err)
	}

	for _, module := range []string{"shop", "billing"} {
		names := migrationFiles(t, module)
		if got := countSuffix(names, ".go", "all.go"); got != 1 {
			t.Errorf("%s: got %d migration files, want 1 (%v)", module, got, names)
		}
	}

	// Two files, two versions — never one shared version, because the engine
	// rejects duplicates and each file's Down must be undoable on its own.
	vShop := versionOf(t, filepath.Join("shop", "migrations"))
	vBilling := versionOf(t, filepath.Join("billing", "migrations"))
	if vShop == vBilling {
		t.Errorf("both modules emitted version %q; simultaneous migrations must not collide", vShop)
	}
}

func TestExecute_MakemigrationsRecordsCumulativeStatePerModuleMigration(t *testing.T) {
	projectChdir(t)

	db := memoryDB(t)
	billingBefore := orm.Model{
		Table:  "invoices",
		Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	}
	shopBefore := orm.Model{
		Table:  "orders",
		Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	}
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("billing", billingBefore),
		ownerWith("shop", shopBefore),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := app.Execute(context.Background(), io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("record initial state: %v", err)
	}

	billingAfter := billingBefore
	billingAfter.Fields = append(billingAfter.Fields, orm.Field{Name: "number", Type: orm.TypeString, Nullable: true})
	shopAfter := shopBefore
	shopAfter.Fields = append(shopAfter.Fields, orm.Field{Name: "reference", Type: orm.TypeString, MaxLen: 32, Nullable: true})
	app, err = fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("billing", billingAfter),
		ownerWith("shop", shopAfter),
	)
	if err != nil {
		t.Fatalf("New changed schema: %v", err)
	}
	if err := app.Execute(context.Background(), io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("record changed state: %v", err)
	}

	billingVersions := generatedVersions(t, filepath.Join("billing", "migrations"))
	shopVersions := generatedVersions(t, filepath.Join("shop", "migrations"))
	billingVersion := billingVersions[len(billingVersions)-1]
	shopVersion := shopVersions[len(shopVersions)-1]
	billingState := generatedState(t, filepath.Join("billing", "migrations"), billingVersion)
	shopState := generatedState(t, filepath.Join("shop", "migrations"), shopVersion)

	if got := snapshotFieldCounts(billingState); len(got) != 2 || got["invoices"] != 2 || got["orders"] != 1 {
		t.Errorf("first sidecar fields = %v, want updated invoices and prior orders", got)
	}
	if got := snapshotFieldCounts(shopState); len(got) != 2 || got["invoices"] != 2 || got["orders"] != 2 {
		t.Errorf("second sidecar fields = %v, want cumulative final schema", got)
	}

	replayed, err := orm.ReplayState([]orm.StateStep{
		{Version: billingVersion, State: &billingState},
		{Version: shopVersion, State: &shopState},
	})
	if err != nil {
		t.Fatalf("ReplayState: %v", err)
	}
	if got := snapshotTables(replayed); !slices.Equal(got, []string{"invoices", "orders"}) {
		t.Errorf("replayed tables = %v, want declared final schema", got)
	}
}

func TestExecute_MakemigrationsRefusesWhenTheProcessIsSliced(t *testing.T) {
	projectChdir(t)

	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db, Modules: []string{"shop"}},
		ownerWith("shop", ordersModelFull()),
		owner("billing", "invoices"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = app.Execute(context.Background(), io.Discard, []string{"makemigrations"})
	if err == nil {
		t.Fatal("a sliced process must refuse to generate migrations")
	}
	if !strings.Contains(err.Error(), "FABRIN_MODULES") {
		t.Errorf("the refusal must name FABRIN_MODULES, got: %v", err)
	}
}

func TestExecute_MakemigrationsRendersIndependentPostgresChangesAndReversesGroups(t *testing.T) {
	projectChdir(t)

	db, err := sql.Open("pgx/v5", "postgres://unused")
	if err != nil {
		t.Fatalf("open pgx handle: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	before := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, Nullable: true},
			{Name: "code", Type: orm.TypeString, Index: true},
		},
	}
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("shop", before),
	)
	if err != nil {
		t.Fatalf("New before: %v", err)
	}
	if err := app.Execute(t.Context(), io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("first makemigrations: %v", err)
	}
	firstVersion := versionOf(t, filepath.Join("shop", "migrations"))

	after := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64},
			{Name: "reference", Type: orm.TypeString, PrimaryKey: true},
			{Name: "code", Type: orm.TypeBytes, Unique: true},
		},
	}
	app, err = fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		ownerWith("shop", after),
	)
	if err != nil {
		t.Fatalf("New after: %v", err)
	}
	if err := app.Execute(t.Context(), io.Discard, []string{"makemigrations"}); err != nil {
		t.Fatalf("second makemigrations: %v", err)
	}

	versions := generatedVersions(t, filepath.Join("shop", "migrations"))
	if len(versions) != 2 {
		t.Fatalf("got generated versions %v, want two", versions)
	}
	secondVersion := versions[1]
	if secondVersion <= firstVersion {
		t.Fatalf("second version %q must follow first %q", secondVersion, firstVersion)
	}
	src := generatedSource(t, filepath.Join("shop", "migrations"), secondVersion)
	up, down, ok := strings.Cut(src, "Down: func")
	if !ok {
		t.Fatalf("generated migration has no Down function:\n%s", src)
	}
	for _, want := range []string{
		"DROP INDEX",
		"DROP CONSTRAINT",
		`ALTER COLUMN \"code\" TYPE BYTEA`,
		`ALTER COLUMN \"reference\" SET NOT NULL`,
		"PRIMARY KEY",
		"UNIQUE",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("generated Up must contain %q:\n%s", want, up)
		}
	}
	if firstDrop, firstAdd := strings.Index(down, "DROP CONSTRAINT"), strings.Index(down, "ADD CONSTRAINT"); firstDrop < 0 || firstAdd < 0 || firstDrop > firstAdd {
		t.Errorf("generated Down must drop new constraints before restoring old ones:\n%s", down)
	}
	if firstDrop, addIndex := strings.Index(down, "DROP CONSTRAINT"), strings.Index(down, "CREATE INDEX"); firstDrop < 0 || addIndex < 0 || firstDrop > addIndex {
		t.Errorf("generated Down must remove new constraints before restoring the old index:\n%s", down)
	}
}

func versionOf(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".go") && name != "all.go" {
			return strings.SplitN(name, "_", 2)[0]
		}
	}
	t.Fatalf("no generated migration file in %s", dir)
	return ""
}

func generatedVersions(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var versions []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".go") && name != "all.go" {
			versions = append(versions, strings.SplitN(name, "_", 2)[0])
		}
	}
	return versions
}

func generatedSource(t *testing.T, dir, version string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, version+"_") && strings.HasSuffix(name, ".go") {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read generated migration %s: %v", name, err)
			}
			return string(raw)
		}
	}
	t.Fatalf("no generated migration for version %s in %s", version, dir)
	return ""
}

func generatedState(t *testing.T, dir, version string) orm.Snapshot {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, version+"_") && strings.HasSuffix(name, ".state.json") {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read generated state %s: %v", name, err)
			}
			state, err := orm.ParseSnapshot(raw, name)
			if err != nil {
				t.Fatalf("parse generated state %s: %v", name, err)
			}
			return state
		}
	}
	t.Fatalf("no generated state for version %s in %s", version, dir)
	return orm.Snapshot{}
}

func snapshotTables(state orm.Snapshot) []string {
	models := state.Models()
	tables := make([]string, 0, len(models))
	for _, reg := range models {
		tables = append(tables, reg.Model.Table)
	}
	return tables
}

func snapshotFieldCounts(state orm.Snapshot) map[string]int {
	counts := make(map[string]int)
	for _, reg := range state.Models() {
		counts[reg.Model.Table] = len(reg.Model.Fields)
	}
	return counts
}
