package fabrin_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/orm"
)

// modelling is a module that declares models. It embeds testModule so the
// required half of the interface stays one line.
type modelling struct {
	testModule
	models []orm.Model
}

func (m modelling) Models() []orm.Model { return m.models }

// modeler builds a module named name declaring one table, with the minimum a
// model needs to be valid: a named, typed primary key.
func modeler(name, table string) fabrin.Module {
	return modelling{
		testModule: testModule{name: name, routes: func(fabrin.Router) {}},
		models: []orm.Model{{
			Table:  table,
			Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}},
		}},
	}
}

func TestApp_ModelsCollectsEachModulesTablesWithItsName(t *testing.T) {
	t.Parallel()

	// Fabrin never scans packages for models. Registration is explicit — a module
	// hands them over. Django gets away with discovery because Python imports have
	// side effects at runtime; the Go equivalent is a blank import whose absence is
	// silent, which is the failure TestModules_NeverImportEachOther exists to catch.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		modeler("shop", "orders"),
		modeler("billing", "invoices"),
		route("plain", "/plain"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := app.Models()
	if len(got) != 2 {
		t.Fatalf("Models() = %d entries, want 2", len(got))
	}

	// Sorted by table, so the migration generator's input does not depend on the
	// argument order in main.
	if got[0].Model.Table != "invoices" || got[1].Model.Table != "orders" {
		t.Errorf("Models() = %v, want them sorted by table", tables(got))
	}
	byTable := map[string]string{}
	for _, r := range got {
		byTable[r.Model.Table] = r.Module
	}
	if byTable["orders"] != "shop" || byTable["invoices"] != "billing" {
		t.Errorf("each table must carry the module that declared it, got %v", byTable)
	}
}

func TestApp_ReportsModelerAmongAModulesCapabilities(t *testing.T) {
	t.Parallel()

	// A mistyped method name otherwise fails silently — the module simply never
	// contributes its models, and nothing says so.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		modeler("shop", "orders"),
		route("plain", "/plain"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	caps := app.Capabilities()
	if !slices.Contains(caps["shop"], "Modeler") {
		t.Errorf("shop capabilities = %v, want Modeler among them", caps["shop"])
	}
	if slices.Contains(caps["plain"], "Modeler") {
		t.Errorf("a module with no Models() must not report Modeler: %v", caps["plain"])
	}
}

func TestApp_ModelsIsEmptyWhenNoModuleDeclaresAny(t *testing.T) {
	t.Parallel()

	// A module contributing no models pays nothing beyond the type switch already
	// there, and an app with none is not an error — F0 and F1 apps have none.
	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, route("plain", "/plain"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := app.Models(); len(got) != 0 {
		t.Errorf("Models() = %v, want none", tables(got))
	}
}

// ── Collection follows process slicing ──────────────────────────────────────

func TestNew_CollectsModelsFromMountedModulesOnly(t *testing.T) {
	t.Parallel()

	// The same rule collectChecks and collectCommands follow. A migrate-only
	// process that mounts one module must not be handed another's models — it
	// would generate DDL for tables this deployment shape has no stake in.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", Modules: []string{"shop"}},
		modeler("shop", "orders"),
		modeler("billing", "invoices"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := app.Models()
	if len(got) != 1 || got[0].Model.Table != "orders" {
		t.Fatalf("Models() = %v, want only the mounted module's table", tables(got))
	}
}

// ── A schema conflict is a wiring-time error ────────────────────────────────

func TestNew_RejectsTwoModulesDeclaringOneTable(t *testing.T) {
	t.Parallel()

	// Propagated rather than logged: two modules writing one table is either a
	// mistake or a coupling nobody declared, and New fails on wiring mistakes
	// instead of warning about them. Discovered when the generator emits a diff
	// against the union of two intentions, it is much more expensive.
	_, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		modeler("shop", "orders"),
		modeler("billing", "orders"),
	)
	if err == nil {
		t.Fatal("two modules claiming one table must fail at construction")
	}

	// The wrap must not break errors.Is, or a caller has nothing to branch on but
	// message text.
	if !errors.Is(err, orm.ErrDuplicateTable) {
		t.Errorf("callers branch with errors.Is, got %v", err)
	}
	for _, want := range []string{"orders", "shop", "billing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q, got: %v", want, err)
		}
	}
}

func TestApp_ModelsReturnsACopy(t *testing.T) {
	t.Parallel()

	// App.Models returns the schema; it does not hand out the registry. A caller
	// that mutated what it was given would change what the migration generator,
	// the admin, and forms all see afterwards, with nothing in between to blame.
	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, modeler("shop", "orders"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := app.Models()
	got[0].Model.Table = "vandalised"
	got[0].Model.Fields[0].Name = "also vandalised"

	again := app.Models()
	if again[0].Model.Table != "orders" {
		t.Errorf("the schema was mutated through Models(): %q", again[0].Model.Table)
	}
	if again[0].Model.Fields[0].Name != "id" {
		t.Errorf("the fields were mutated through Models(): %q", again[0].Model.Fields[0].Name)
	}
}

func tables(rs []orm.Registered) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Model.Table)
	}
	return out
}
