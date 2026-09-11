package migratediff

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/usefabrin/fabrin/orm"
)

type multiStatementDialect struct{}

func (multiStatementDialect) Name() string { return "test" }

func (multiStatementDialect) Render(Operation) ([]string, error) {
	return []string{
		`CREATE TABLE "orders" ("id" INTEGER PRIMARY KEY)`,
		`ALTER TABLE "orders" ADD COLUMN "reference" TEXT`,
	}, nil
}

type unknownOperation struct{}

func (unknownOperation) Describe() string { return "unknown test operation" }

func mustSnap(t *testing.T, models ...orm.Model) orm.Snapshot {
	t.Helper()
	r := orm.NewRegistry()
	for i, m := range models {
		if err := r.Register("shop", m); err != nil {
			t.Fatalf("Register model %d: %v", i, err)
		}
	}
	snap, err := orm.NewSnapshot(r.Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return snap
}

func ordersModel() orm.Model {
	return orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, MaxLen: 32},
			{Name: "shipped_at", Type: orm.TypeTime, Nullable: true},
		},
	}
}

func TestSQLite_RenamesAColumnWithoutLosingItsData(t *testing.T) {
	db, err := sql.Open("sqlite", "file:rename-column?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE "orders" ("id" INTEGER PRIMARY KEY, "total" TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO "orders" ("id", "total") VALUES (1, '12.50')`); err != nil {
		t.Fatalf("insert row: %v", err)
	}

	op := RenameColumn{Table: "orders", From: "total", To: "amount"}
	if err := Apply(t.Context(), db, SQLite{}, []Operation{op}); err != nil {
		t.Fatalf("Apply rename: %v", err)
	}

	var amount string
	if err := db.QueryRow(`SELECT "amount" FROM "orders" WHERE "id" = 1`).Scan(&amount); err != nil {
		t.Fatalf("read renamed column: %v", err)
	}
	if amount != "12.50" {
		t.Errorf("renamed value = %q, want 12.50", amount)
	}

	stmts, err := (Postgres{}).Render(op)
	if err != nil {
		t.Fatalf("Postgres.Render: %v", err)
	}
	want := `ALTER TABLE "orders" RENAME COLUMN "total" TO "amount"`
	if len(stmts) != 1 || stmts[0] != want {
		t.Errorf("Postgres rename = %v, want [%s]", stmts, want)
	}
}

// opKinds reduces a diff to "create orders / addcol orders.total / ..." so the
// table-driven tests assert shape without spelling out rendered SQL.
func opKinds(ops []Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		switch o := op.(type) {
		case CreateTable:
			out = append(out, "create "+o.Model.Table)
		case DropTable:
			out = append(out, "drop "+o.Table)
		case AddColumn:
			out = append(out, "addcol "+o.Table+"."+o.Field.Name)
		case DropColumn:
			out = append(out, "dropcol "+o.Table+"."+o.Column)
		case ChangeType:
			out = append(out, "retype "+o.Table+"."+o.Column)
		case ChangeNullability:
			out = append(out, "renull "+o.Table+"."+o.Column)
		case AddUnique:
			out = append(out, "addunique "+o.Table+"."+o.Column)
		case DropUnique:
			out = append(out, "dropunique "+o.Table+"."+o.Column)
		case AddIndex:
			out = append(out, "addindex "+o.Table+"."+o.Column)
		case DropIndex:
			out = append(out, "dropindex "+o.Table+"."+o.Column)
		case AddPrimaryKey:
			out = append(out, "addpk "+o.Table+"."+o.Column)
		case DropPrimaryKey:
			out = append(out, "droppk "+o.Table+"."+o.Column)
		default:
			out = append(out, "?")
		}
	}
	return out
}

func TestDiff_EmitsEveryIndependentChangeOnAField(t *testing.T) {
	t.Parallel()

	before := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, MaxLen: 32, Nullable: true},
		},
	}
	after := before
	after.Fields = append([]orm.Field(nil), before.Fields...)
	after.Fields[1] = orm.Field{
		Name: "reference", Type: orm.TypeBytes, Nullable: false, Unique: true,
	}

	got := opKinds(Diff(mustSnap(t, before), mustSnap(t, after)))
	want := []string{
		"retype orders.reference",
		"renull orders.reference",
		"addunique orders.reference",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("simultaneous diff = %v, want %v", got, want)
	}
}

func TestDiff_DetectsIndexAndPrimaryKeyChanges(t *testing.T) {
	t.Parallel()

	before := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, Index: true},
		},
	}
	after := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64},
			{Name: "reference", Type: orm.TypeString, PrimaryKey: true},
		},
	}

	got := opKinds(Diff(mustSnap(t, before), mustSnap(t, after)))
	want := []string{
		"dropindex orders.reference",
		"droppk orders.id",
		"addpk orders.reference",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("constraint diff = %v, want %v", got, want)
	}
}

func TestGeneratedObjectNamesAreBoundedAndCollisionResistant(t *testing.T) {
	t.Parallel()

	a := databaseObjectName("idx", "a_b", "c")
	b := databaseObjectName("idx", "a", "b_c")
	if a == b {
		t.Fatalf("ambiguous identifiers produced one name: %q", a)
	}
	if again := databaseObjectName("idx", "a_b", "c"); again != a {
		t.Errorf("name changed between calls: %q then %q", a, again)
	}
	long := databaseObjectName("uq", strings.Repeat("table", 30), strings.Repeat("column", 30))
	if len(long) > 63 {
		t.Errorf("name is %d bytes, exceeds PostgreSQL's 63-byte limit: %q", len(long), long)
	}
	if !strings.HasPrefix(long, "uq_") {
		t.Errorf("name lost its object kind: %q", long)
	}
}

func TestDiff_DetectsEachShapeOfChange(t *testing.T) {
	t.Parallel()

	base := func() orm.Model {
		return orm.Model{
			Table: "orders",
			Fields: []orm.Field{
				{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
				{Name: "reference", Type: orm.TypeString, MaxLen: 32},
			},
		}
	}

	tests := []struct {
		name   string
		before []orm.Model
		after  []orm.Model
		want   []string
	}{
		{
			name:   "no change",
			before: []orm.Model{base()},
			after:  []orm.Model{base()},
			want:   nil,
		},
		{
			name:   "new table",
			before: []orm.Model{base()},
			after: []orm.Model{
				base(),
				{Table: "shipments", Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}},
			},
			want: []string{"create shipments"},
		},
		{
			name: "dropped table",
			before: []orm.Model{
				base(),
				{Table: "shipments", Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}},
			},
			after: []orm.Model{base()},
			want:  []string{"drop shipments"},
		},
		{
			name:   "added field",
			before: []orm.Model{base()},
			after: func() []orm.Model {
				m := base()
				m.Fields = append(m.Fields, orm.Field{Name: "total", Type: orm.TypeFloat})
				return []orm.Model{m}
			}(),
			want: []string{"addcol orders.total"},
		},
		{
			name:   "dropped field",
			before: []orm.Model{base()},
			after: func() []orm.Model {
				m := base()
				m.Fields = m.Fields[:1]
				return []orm.Model{m}
			}(),
			want: []string{"dropcol orders.reference"},
		},
		{
			name:   "changed type",
			before: []orm.Model{base()},
			after: func() []orm.Model {
				m := base()
				m.Fields[1].Type = orm.TypeInt
				m.Fields[1].MaxLen = 0
				return []orm.Model{m}
			}(),
			want: []string{"retype orders.reference"},
		},
		{
			name:   "changed max length",
			before: []orm.Model{base()},
			after: func() []orm.Model {
				m := base()
				m.Fields[1].MaxLen = 64
				return []orm.Model{m}
			}(),
			want: []string{"retype orders.reference"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before := mustSnap(t, tc.before...)
			after := mustSnap(t, tc.after...)

			got := Diff(before, after)
			if strings.Join(opKinds(got), ",") != strings.Join(tc.want, ",") {
				t.Errorf("diff = %v, want %v", opKinds(got), tc.want)
			}
		})
	}
}

func TestDiff_EmitsNothingWhenStatesAgree(t *testing.T) {
	t.Parallel()

	// makemigrations on an unchanged project must say "no changes", not write a
	// file full of nothing. An empty diff is the normal case on a redeploy.
	snap := mustSnap(t, ordersModel(), orm.Model{
		Table:  "invoices",
		Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	})

	if got := Diff(snap, snap); len(got) != 0 {
		t.Errorf("a state diffed against itself produced %d ops: %v", len(got), opKinds(got))
	}
}

func TestDiff_IsDeterministicAndOrdersItsOps(t *testing.T) {
	t.Parallel()

	// Same inputs, same ops in the same order. The order is chosen once —
	// creates sorted by table, then per-table alterations sorted by column,
	// drops last — rather than left to map iteration, because the generated
	// migration inherits whatever wiggle lives here.
	before := mustSnap(t,
		orm.Model{Table: "orders", Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}},
		orm.Model{Table: "customers", Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}},
		orm.Model{Table: "archive", Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}},
	)
	after := mustSnap(t,
		orm.Model{Table: "customers", Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "email", Type: orm.TypeString, MaxLen: 254},
		}},
		orm.Model{Table: "orders", Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "total", Type: orm.TypeFloat},
			{Name: "memo", Type: orm.TypeString},
		}},
		orm.Model{Table: "invoices", Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}},
	)

	first := opKinds(Diff(before, after))
	second := opKinds(Diff(before, after))
	if strings.Join(first, "|") != strings.Join(second, "|") {
		t.Errorf("two diffs of one pair differ:\n%v\n%v", first, second)
	}

	// Creates first (sorted), then alterations per surviving table (columns
	// alphabetical), whole-table drops last — a dropped table is data loss and
	// reads better at the end of the story than interleaved with additions.
	want := []string{
		"create invoices",
		"addcol customers.email",
		"addcol orders.memo",
		"addcol orders.total",
		"drop archive",
	}
	if strings.Join(first, "|") != strings.Join(want, "|") {
		t.Errorf("op order = %v, want %v", first, want)
	}
}

func TestDiff_TreatsAReorderedFieldListAsNoChange(t *testing.T) {
	t.Parallel()

	// DDL has no way to reorder columns without rebuilding the table, and the
	// registry preserves declared order as layout intent. A pure reorder is
	// therefore not a schema change the differ can express, and inventing a
	// drop-and-re-add for it would destroy data over a cosmetic diff.
	a := mustSnap(t, ordersModel())
	reordered := ordersModel()
	reordered.Fields = []orm.Field{
		{Name: "shipped_at", Type: orm.TypeTime, Nullable: true},
		{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
		{Name: "reference", Type: orm.TypeString, MaxLen: 32},
	}
	b := mustSnap(t, reordered)

	if got := Diff(a, b); len(got) != 0 {
		t.Errorf("a pure field reorder produced %d ops: %v", len(got), opKinds(got))
	}
}

func TestSQLite_CreatesTablesThatAcceptRows(t *testing.T) {
	t.Parallel()

	// Emitted DDL is tested against a real database, not eyeballed: a Dialect
	// that renders plausible SQL nobody's server accepts is worse than none.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("no sqlite driver available: %v", err)
	}
	defer func() { _ = db.Close() }()

	snap := mustSnap(t, ordersModel())
	ops := Diff(mustSnap(t), snap)
	if err := Apply(context.Background(), db, SQLite{}, ops); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO orders (id, reference, shipped_at) VALUES (1, 'A-1', NULL)`); err != nil {
		t.Errorf("the created table refused a row: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n); err != nil || n != 1 {
		t.Errorf("row count = %d, err %v; want 1, nil", n, err)
	}
}

func TestApply_PreflightsEveryOperationBeforeMutating(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("no sqlite driver available: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	err = Apply(context.Background(), db, SQLite{}, []Operation{
		CreateTable{Model: ordersModel()},
		DropColumn{Table: "orders", Column: "reference"},
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Apply error = %v, want ErrUnsupported", err)
	}

	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'orders'`).Scan(&tables); err != nil {
		t.Fatalf("inspect schema: %v", err)
	}
	if tables != 0 {
		t.Errorf("orders table exists after failed preflight; want database untouched")
	}
}

func TestApply_ExecutesPreflightedOperationsInOrder(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("no sqlite driver available: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	err = Apply(context.Background(), db, SQLite{}, []Operation{
		CreateTable{Model: ordersModel()},
		AddColumn{Table: "orders", Field: orm.Field{Name: "total", Type: orm.TypeFloat, Nullable: true}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO orders (id, reference, total) VALUES (1, 'A-1', 12.5)`); err != nil {
		t.Errorf("preflighted operations did not execute in order: %v", err)
	}
}

func TestDialect_HasOneStableRenderMethodForAllOperations(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf((*Dialect)(nil)).Elem()
	want := []string{"Name", "Render"}
	if typ.NumMethod() != len(want) {
		t.Fatalf("Dialect has %d methods, want %d stable methods", typ.NumMethod(), len(want))
	}
	for i, name := range want {
		if got := typ.Method(i).Name; got != name {
			t.Errorf("Dialect method %d = %s, want %s", i, got, name)
		}
	}
}

func TestApply_ExecutesEveryStatementReturnedForOneOperation(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("no sqlite driver available: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	if err := Apply(context.Background(), db, multiStatementDialect{}, []Operation{CreateTable{Model: ordersModel()}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO "orders" ("id", "reference") VALUES (1, 'A-1')`); err != nil {
		t.Errorf("second statement was not executed: %v", err)
	}
}

func TestDialects_QuoteIdentifiersInsteadOfTreatingThemAsSQL(t *testing.T) {
	t.Parallel()

	model := orm.Model{
		Table: "order items",
		Fields: []orm.Field{
			{Name: `select"value`, Type: orm.TypeInt64, PrimaryKey: true},
		},
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("no sqlite driver available: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := Apply(context.Background(), db, SQLite{}, []Operation{CreateTable{Model: model}}); err != nil {
		t.Fatalf("Apply quoted model: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO "order items" ("select""value") VALUES (1)`); err != nil {
		t.Errorf("quoted identifiers were not created literally: %v", err)
	}

	stmts, err := Postgres{}.Render(DropColumn{Table: model.Table, Column: model.Fields[0].Name})
	if err != nil {
		t.Fatalf("Postgres Render: %v", err)
	}
	if len(stmts) != 1 || !strings.Contains(stmts[0], `ALTER TABLE "order items" DROP COLUMN "select""value"`) {
		t.Errorf("PostgreSQL identifiers are not quoted safely: %q", stmts)
	}
	stmts, err = Postgres{}.Render(RenameColumn{Table: model.Table, From: model.Fields[0].Name, To: `new"value`})
	if err != nil {
		t.Fatalf("Postgres rename Render: %v", err)
	}
	if len(stmts) != 1 || !strings.Contains(stmts[0], `ALTER TABLE "order items" RENAME COLUMN "select""value" TO "new""value"`) {
		t.Errorf("PostgreSQL rename identifiers are not quoted safely: %q", stmts)
	}
}

func TestBuiltInDialects_RejectUnknownOperations(t *testing.T) {
	t.Parallel()

	_, err := Postgres{}.Render(unknownOperation{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}

func TestPostgres_RendersCompleteConstraintsForNewTablesAndColumns(t *testing.T) {
	t.Parallel()

	model := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, MaxLen: 32, Unique: true},
			{Name: "account_id", Type: orm.TypeInt64, Index: true},
			{Name: "note", Type: orm.TypeString, Nullable: true},
		},
	}
	stmts, err := Postgres{}.Render(CreateTable{Model: model})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		`"id" BIGINT NOT NULL`,
		`CONSTRAINT "` + databaseObjectName("pk", "orders", "id") + `" PRIMARY KEY ("id")`,
		`"reference" VARCHAR(32) NOT NULL`,
		`CONSTRAINT "` + databaseObjectName("uq", "orders", "reference") + `" UNIQUE ("reference")`,
		`CREATE INDEX "` + databaseObjectName("idx", "orders", "account_id") + `"`,
		`"note" TEXT`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered table lacks %q:\n%s", want, joined)
		}
	}

	added := orm.Field{Name: "external_id", Type: orm.TypeString, Unique: true}
	stmts, err = Postgres{}.Render(AddColumn{Table: "orders", Field: added})
	if err != nil {
		t.Fatalf("AddColumn: %v", err)
	}
	joined = strings.Join(stmts, "\n")
	for _, want := range []string{
		`ADD COLUMN "external_id" TEXT NOT NULL`,
		`ADD CONSTRAINT "` + databaseObjectName("uq", "orders", "external_id") + `" UNIQUE ("external_id")`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered column lacks %q:\n%s", want, joined)
		}
	}
}

func TestSQLite_RendersInitialConstraintsAndRefusesUnsupportedAdditions(t *testing.T) {
	t.Parallel()

	model := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, Unique: true},
			{Name: "note", Type: orm.TypeString, Nullable: true, Index: true},
		},
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("no sqlite driver available: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := Apply(context.Background(), db, SQLite{}, []Operation{CreateTable{Model: model}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO "orders" ("id", "reference") VALUES (1, 'A-1')`); err != nil {
		t.Fatalf("insert initial row: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO "orders" ("id", "reference") VALUES (2, 'A-1')`); err == nil {
		t.Error("named UNIQUE constraint was not enforced")
	}
	indexName := databaseObjectName("idx", "orders", "note")
	var indexes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, indexName).Scan(&indexes); err != nil {
		t.Fatalf("inspect initial index: %v", err)
	}
	if indexes != 1 {
		t.Errorf("initial plain index count = %d, want 1", indexes)
	}

	added := AddColumn{
		Table: "orders",
		Field: orm.Field{Name: "external_id", Type: orm.TypeString, Nullable: true, Index: true},
	}
	if err := Apply(context.Background(), db, SQLite{}, []Operation{added}); err != nil {
		t.Fatalf("add nullable indexed column: %v", err)
	}
	indexName = databaseObjectName("idx", "orders", "external_id")
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, indexName).Scan(&indexes); err != nil {
		t.Fatalf("inspect added index: %v", err)
	}
	if indexes != 1 {
		t.Errorf("added-column index count = %d, want 1", indexes)
	}

	tests := []AddColumn{
		{Table: "orders", Field: orm.Field{Name: "required", Type: orm.TypeString}},
		{Table: "orders", Field: orm.Field{Name: "code", Type: orm.TypeString, Nullable: true, Unique: true}},
	}
	for _, op := range tests {
		_, err := SQLite{}.Render(op)
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("AddColumn(%+v) error = %v, want ErrUnsupported", op.Field, err)
		}
	}
}

func TestPostgres_RendersConstraintTransitionsWithStableNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   Operation
		want string
	}{
		{ChangeNullability{Table: "orders", Column: "reference", Nullable: false}, `SET NOT NULL`},
		{ChangeNullability{Table: "orders", Column: "reference", Nullable: true}, `DROP NOT NULL`},
		{AddUnique{Table: "orders", Column: "reference"}, `ADD CONSTRAINT "` + databaseObjectName("uq", "orders", "reference") + `"`},
		{DropUnique{Table: "orders", Column: "reference"}, `DROP CONSTRAINT "` + databaseObjectName("uq", "orders", "reference") + `"`},
		{AddIndex{Table: "orders", Column: "reference"}, `CREATE INDEX "` + databaseObjectName("idx", "orders", "reference") + `"`},
		{DropIndex{Table: "orders", Column: "reference"}, `DROP INDEX "` + databaseObjectName("idx", "orders", "reference") + `"`},
		{AddPrimaryKey{Table: "orders", Column: "reference"}, `ADD CONSTRAINT "` + databaseObjectName("pk", "orders", "reference") + `"`},
		{DropPrimaryKey{Table: "orders", Column: "id"}, `FROM pg_constraint`},
	}
	for _, tc := range tests {
		stmts, err := Postgres{}.Render(tc.op)
		if err != nil {
			t.Fatalf("%s: %v", tc.op.Describe(), err)
		}
		if !strings.Contains(strings.Join(stmts, "\n"), tc.want) {
			t.Errorf("%s lacks %q: %q", tc.op.Describe(), tc.want, stmts)
		}
	}

	stmts, err := Postgres{}.Render(DropPrimaryKey{Table: `order's$fabrin$`, Column: `owner's id`})
	if err != nil {
		t.Fatalf("drop primary key by catalog identity: %v", err)
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{`c.contype = 'p'`, `'order''s$fabrin$'`, `'owner''s id'`, `DROP CONSTRAINT %I`} {
		if !strings.Contains(joined, want) {
			t.Errorf("catalog-based primary-key drop lacks %q: %s", want, joined)
		}
	}
	if strings.HasPrefix(joined, "DO $fabrin$\n") {
		t.Errorf("DO block delimiter must not occur in metadata embedded in its body: %s", joined)
	}
}

func TestSQLite_RefusesColumnDropAndRetypeWithStatedErrors(t *testing.T) {
	t.Parallel()

	// SQLite cannot alter or drop a column in place; the honest options are the
	// table-rebuild dance or a stated refusal. This iteration refuses, naming
	// what it will not do — emitting SQL that fails halfway through a live
	// migration is the worst place to discover a Dialect limit.
	dropErr := func() error {
		_, err := SQLite{}.Render(DropColumn{Table: "orders", Column: "reference"})
		return err
	}()
	if !errors.Is(dropErr, ErrUnsupported) {
		t.Errorf("DropColumn: got %v, want ErrUnsupported", dropErr)
	}
	retypeErr := func() error {
		_, err := SQLite{}.Render(ChangeType{
			Table:  "orders",
			Column: "reference",
			To:     orm.Field{Name: "reference", Type: orm.TypeInt},
		})
		return err
	}()
	if !errors.Is(retypeErr, ErrUnsupported) {
		t.Errorf("ChangeType: got %v, want ErrUnsupported", retypeErr)
	}
	for _, err := range []error{dropErr, retypeErr} {
		if !strings.Contains(err.Error(), "QLite") {
			t.Errorf("the refusal should name the Dialect, got: %v", err)
		}
	}
}

func TestPostgres_RendersDDLThatALiveServerAccepts(t *testing.T) {
	t.Parallel()

	// Skipped with a notice when no server is configured — never silently
	// passing, and never making the hermetic gate depend on a running Postgres.
	dsn := os.Getenv("FABRIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FABRIN_TEST_PG_DSN is not set; skipping live PostgreSQL checks")
	}

	db, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Skipf("PostgreSQL DSN unusable: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("PostgreSQL unreachable: %v", err)
	}

	before := mustSnap(t)
	after := mustSnap(t, ordersModel())
	if err := Apply(context.Background(), db, Postgres{}, Diff(before, after)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Add a column — the cheap alteration a server round trip can prove without
	// a rebuild dance — then confirm the server actually has it.
	added := mustSnap(t, func() orm.Model {
		m := ordersModel()
		m.Fields = append(m.Fields, orm.Field{Name: "total", Type: orm.TypeFloat})
		return m
	}())
	if err := Apply(context.Background(), db, Postgres{}, Diff(after, added)); err != nil {
		t.Fatalf("add Column: %v", err)
	}

	var dataType string
	err = db.QueryRowContext(context.Background(),
		`SELECT data_type FROM information_schema.columns WHERE table_name = 'orders' AND column_name = 'total'`).
		Scan(&dataType)
	if err != nil {
		t.Fatalf("column total missing after add: %v", err)
	}
}

func TestDataLossIsStatedInTheEmittedSQL(t *testing.T) {
	t.Parallel()

	// Dropping a column destroys whatever it held, and that is exactly the one
	// line a reviewer must not skim past. It rides in the emitted SQL itself,
	// where the migration file carries it, rather than in a log nobody diffs.
	stmts, err := Postgres{}.Render(DropColumn{Table: "orders", Column: "reference"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(stmts) != 1 || !strings.Contains(stmts[0], "--") || !strings.Contains(stmts[0], "data") || !strings.Contains(stmts[0], "reference") {
		t.Errorf("the drop statement must carry its data-loss warning naming the column, got: %q", stmts)
	}
}
