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
			// NOT NULL now that ADR 0006 decided it — and asserted below.
			{Name: "reference", Type: orm.String, MaxLen: 32},
			{Name: "shipped_at", Type: orm.Time, Nullable: true},
		},
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
		case AddIndex:
			out = append(out, "addidx "+o.Table+"."+o.Column)
		case DropIndex:
			out = append(out, "dropidx "+o.Table+"."+o.Column)
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

			got := Diff(before, after)
			if strings.Join(opKinds(got), ",") != strings.Join(tc.want, ",") {
				t.Errorf("diff = %v, want %v", opKinds(got), tc.want)
			}
		})
	}
}

func TestDiff_DetectsNullabilityAndIndexChanges(t *testing.T) {
	t.Parallel()

	// ADR 0006 gave the flags semantics, so changes in them became schema
	// changes. Nullability flips are their own operation - PostgreSQL renders
	// them separately from type changes - and an index flag toggles a named,
	// deterministic index rather than riding along with anything.
	base := func(f orm.Field) orm.Model {
		return orm.Model{
			Table:  "orders",
			Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}, f},
		}
	}

	tests := []struct {
		name   string
		before orm.Field
		after  orm.Field
		want   []string
	}{
		{
			name:   "column becomes nullable",
			before: orm.Field{Name: "reference", Type: orm.String},
			after:  orm.Field{Name: "reference", Type: orm.String, Nullable: true},
			want:   []string{"renull orders.reference"},
		},
		{
			name:   "column becomes NOT NULL",
			before: orm.Field{Name: "reference", Type: orm.String, Nullable: true},
			after:  orm.Field{Name: "reference", Type: orm.String},
			want:   []string{"renull orders.reference"},
		},
		{
			name:   "index added",
			before: orm.Field{Name: "reference", Type: orm.String},
			after:  orm.Field{Name: "reference", Type: orm.String, Index: true},
			want:   []string{"addidx orders.reference"},
		},
		{
			name:   "index dropped",
			before: orm.Field{Name: "reference", Type: orm.String, Index: true},
			after:  orm.Field{Name: "reference", Type: orm.String},
			want:   []string{"dropidx orders.reference"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Diff(mustSnap(t, base(tc.before)), mustSnap(t, base(tc.after)))
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
		{Name: "shipped_at", Type: orm.Time, Nullable: true},
		{Name: "id", Type: orm.Int64, PrimaryKey: true},
		{Name: "reference", Type: orm.String, MaxLen: 32},
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

func TestSQLite_RefusesColumnDropAndRetypeWithStatedErrors(t *testing.T) {
	t.Parallel()

	// SQLite cannot alter or drop a column in place; the honest options are the
	// table-rebuild dance or a stated refusal. This iteration refuses, naming
	// what it will not do — emitting SQL that fails halfway through a live
	// migration is the worst place to discover a Dialect limit.
	dropErr := func() error {
		_, err := DropColumn{Table: "orders", Column: "reference"}.Render(SQLite{})
		return err
	}()
	if !errors.Is(dropErr, ErrUnsupported) {
		t.Errorf("DropColumn: got %v, want ErrUnsupported", dropErr)
	}
	retypeErr := func() error {
		_, err := ChangeType{
			Table:  "orders",
			Column: "reference",
			To:     orm.Field{Name: "reference", Type: orm.Int},
		}.Render(SQLite{})
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

func TestSQLite_RefusesToAddANotNullColumnWithoutADefault(t *testing.T) {
	t.Parallel()

	// ALTER TABLE ADD COLUMN ... NOT NULL requires a DEFAULT clause in SQLite,
	// and the metadata has no default concept yet - so the stated refusal
	// covers this case too, before anything executes.
	err := func() error {
		_, err := AddColumn{
			Table: "orders",
			Field: orm.Field{Name: "total", Type: orm.Float},
		}.Render(SQLite{})
		return err
	}()
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "DEFAULT") {
		t.Errorf("the refusal should say why (DEFAULT required), got: %v", err)
	}
}

func TestPostgres_RendersNullabilityAndIndexes(t *testing.T) {
	t.Parallel()

	// String-level assertions on PostgreSQL rendering: nullability moves are
	// their own statement, separate from type changes, and indexes carry the
	// deterministic ADR 0006 name.
	setNotNull, err := ChangeNullability{
		Table:  "orders",
		Column: "reference",
		To:     orm.Field{Name: "reference", Type: orm.String},
	}.Render(Postgres{})
	if err != nil {
		t.Fatalf("SET NOT NULL: %v", err)
	}
	if !strings.Contains(setNotNull, "SET NOT NULL") {
		t.Errorf("want SET NOT NULL, got %q", setNotNull)
	}
	dropNotNull, err := ChangeNullability{
		Table:  "orders",
		Column: "reference",
		To:     orm.Field{Name: "reference", Type: orm.String, Nullable: true},
	}.Render(Postgres{})
	if err != nil {
		t.Fatalf("DROP NOT NULL: %v", err)
	}
	if !strings.Contains(dropNotNull, "DROP NOT NULL") {
		t.Errorf("want DROP NOT NULL, got %q", dropNotNull)
	}
	createIdx, err := AddIndex{Table: "orders", Column: "reference"}.Render(Postgres{})
	if err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}
	for _, want := range []string{"CREATE INDEX idx_orders_reference", "ON orders (reference)"} {
		if !strings.Contains(createIdx, want) {
			t.Errorf("index DDL must contain %q, got %q", want, createIdx)
		}
	}

	// Inline constraints ride CREATE TABLE: NOT NULL unless Nullable opts out,
	// UNIQUE when asked, and a primary key never says NOT NULL twice.
	r := orm.NewRegistry()
	must := func(err error) {
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	must(r.Register("shop", orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.Int64, PrimaryKey: true},
			{Name: "reference", Type: orm.String, MaxLen: 32, Unique: true},
			{Name: "shipped_at", Type: orm.Time, Nullable: true},
		},
	}))
	snap, err := orm.NewSnapshot(r.Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	ddl, err := CreateTable{Model: snap.Models()[0].Model}.Render(Postgres{})
	if err != nil {
		t.Fatalf("CreateTable render: %v", err)
	}
	for _, want := range []string{"reference VARCHAR(32) NOT NULL UNIQUE", "shipped_at TIMESTAMP", "id BIGINT PRIMARY KEY"} {
		if !strings.Contains(ddl, want) {
			t.Errorf("table DDL must contain %q, got:\n%s", want, ddl)
		}
	}
	if strings.Count(ddl, "NOT NULL") != 1 {
		t.Errorf("primary key must not say NOT NULL twice, got:\n%s", ddl)
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
		m.Fields = append(m.Fields, orm.Field{Name: "total", Type: orm.Float})
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
	stmt, err := DropColumn{Table: "orders", Column: "reference"}.Render(Postgres{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(stmt, "--") || !strings.Contains(stmt, "data") || !strings.Contains(stmt, "reference") {
		t.Errorf("the drop statement must carry its data-loss warning naming the column, got: %q", stmt)
	}
}
