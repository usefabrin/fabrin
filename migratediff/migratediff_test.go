package migratediff

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/usefabrin/fabrin/orm"
)

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
			{Name: "id", Type: orm.Int64, PrimaryKey: true},
			{Name: "reference", Type: orm.String, MaxLen: 32},
			{Name: "shipped_at", Type: orm.Time},
		},
	}
}

// opKinds reduces a diff to "create orders / addcol orders.total / ..." so the
// table-driven tests assert shape without spelling out rendered SQL.
func opKinds(ops []operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		switch o := op.(type) {
		case createTable:
			out = append(out, "create "+o.model.Table)
		case dropTable:
			out = append(out, "drop "+o.table)
		case addColumn:
			out = append(out, "addcol "+o.table+"."+o.field.Name)
		case dropColumn:
			out = append(out, "dropcol "+o.table+"."+o.column)
		case changeType:
			out = append(out, "retype "+o.table+"."+o.column)
		default:
			out = append(out, "?")
		}
	}
	return out
}

func TestDiff_DetectsEachShapeOfChange(t *testing.T) {
	t.Parallel()

	base := func() orm.Model {
		return orm.Model{
			Table: "orders",
			Fields: []orm.Field{
				{Name: "id", Type: orm.Int64, PrimaryKey: true},
				{Name: "reference", Type: orm.String, MaxLen: 32},
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
				{Table: "shipments", Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}}},
			},
			want: []string{"create shipments"},
		},
		{
			name: "dropped table",
			before: []orm.Model{
				base(),
				{Table: "shipments", Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}}},
			},
			after: []orm.Model{base()},
			want:  []string{"drop shipments"},
		},
		{
			name:   "added field",
			before: []orm.Model{base()},
			after: func() []orm.Model {
				m := base()
				m.Fields = append(m.Fields, orm.Field{Name: "total", Type: orm.Float})
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
				m.Fields[1].Type = orm.Int
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

			got := diff(before, after)
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
		Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}},
	})

	if got := diff(snap, snap); len(got) != 0 {
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
		orm.Model{Table: "orders", Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}}},
		orm.Model{Table: "customers", Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}}},
		orm.Model{Table: "archive", Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}}},
	)
	after := mustSnap(t,
		orm.Model{Table: "customers", Fields: []orm.Field{
			{Name: "id", Type: orm.Int64, PrimaryKey: true},
			{Name: "email", Type: orm.String, MaxLen: 254},
		}},
		orm.Model{Table: "orders", Fields: []orm.Field{
			{Name: "id", Type: orm.Int64, PrimaryKey: true},
			{Name: "total", Type: orm.Float},
			{Name: "memo", Type: orm.String},
		}},
		orm.Model{Table: "invoices", Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}}},
	)

	first := opKinds(diff(before, after))
	second := opKinds(diff(before, after))
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
		{Name: "shipped_at", Type: orm.Time},
		{Name: "id", Type: orm.Int64, PrimaryKey: true},
		{Name: "reference", Type: orm.String, MaxLen: 32},
	}
	b := mustSnap(t, reordered)

	if got := diff(a, b); len(got) != 0 {
		t.Errorf("a pure field reorder produced %d ops: %v", len(got), opKinds(got))
	}
}

func TestSQLite_CreatesTablesThatAcceptRows(t *testing.T) {
	t.Parallel()

	// Emitted DDL is tested against a real database, not eyeballed: a dialect
	// that renders plausible SQL nobody's server accepts is worse than none.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("no sqlite driver available: %v", err)
	}
	defer func() { _ = db.Close() }()

	snap := mustSnap(t, ordersModel())
	ops := diff(mustSnap(t), snap)
	if err := apply(context.Background(), db, sqliteDialect{}, ops); err != nil {
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

func TestSQLite_RefusesColumnDropAndRetypeWithStatedErrors(t *testing.T) {
	t.Parallel()

	// SQLite cannot alter or drop a column in place; the honest options are the
	// table-rebuild dance or a stated refusal. This iteration refuses, naming
	// what it will not do — emitting SQL that fails halfway through a live
	// migration is the worst place to discover a dialect limit.
	dropErr := func() error {
		_, err := dropColumn{table: "orders", column: "reference"}.render(sqliteDialect{})
		return err
	}()
	if !errors.Is(dropErr, errUnsupported) {
		t.Errorf("dropColumn: got %v, want errUnsupported", dropErr)
	}
	retypeErr := func() error {
		_, err := changeType{
			table:  "orders",
			column: "reference",
			to:     orm.Field{Name: "reference", Type: orm.Int},
		}.render(sqliteDialect{})
		return err
	}()
	if !errors.Is(retypeErr, errUnsupported) {
		t.Errorf("changeType: got %v, want errUnsupported", retypeErr)
	}
	for _, err := range []error{dropErr, retypeErr} {
		if !strings.Contains(err.Error(), "QLite") {
			t.Errorf("the refusal should name the dialect, got: %v", err)
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
	if err := apply(context.Background(), db, postgresDialect{}, diff(before, after)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Add a column — the cheap alteration a server round trip can prove without
	// a rebuild dance — then confirm the server actually has it.
	added := mustSnap(t, func() orm.Model {
		m := ordersModel()
		m.Fields = append(m.Fields, orm.Field{Name: "total", Type: orm.Float})
		return m
	}())
	if err := apply(context.Background(), db, postgresDialect{}, diff(after, added)); err != nil {
		t.Fatalf("add column: %v", err)
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
	stmt, err := dropColumn{table: "orders", column: "reference"}.render(postgresDialect{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(stmt, "--") || !strings.Contains(stmt, "data") || !strings.Contains(stmt, "reference") {
		t.Errorf("the drop statement must carry its data-loss warning naming the column, got: %q", stmt)
	}
}
