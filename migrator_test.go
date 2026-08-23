package fabrin_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/migrate"
)

// migrating is a module that declares migrations. It embeds testModule so the
// required half of the interface stays one line.
type migrating struct {
	testModule
	migrations []migrate.M
}

func (m migrating) Migrations() []migrate.M { return m.migrations }

// migrator builds a module named name declaring one migration whose version is
// version. The Up step creates table — a real enough schema change that running
// it against a live database can be asserted.
func migrator(name, version, table string) fabrin.Module {
	return migrating{
		testModule: testModule{name: name, routes: func(fabrin.Router) {}},
		migrations: []migrate.M{
			{
				Version: version,
				Name:    "create " + table,
				Up: func(_ context.Context, h migrate.Handle) error {
					_, err := h.ExecContext(context.Background(),
						"CREATE TABLE "+table+" (id INTEGER PRIMARY KEY)")
					return err
				},
				Down: func(_ context.Context, h migrate.Handle) error {
					_, err := h.ExecContext(context.Background(), "DROP TABLE "+table)
					return err
				},
			},
		},
	}
}

// memoryDB opens an in-process SQLite handle. One connection max: :memory:
// databases live per-connection, so a pool of two would put the migration table
// and the assertions on different databases.
func memoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApp_ReportsMigratorCapability(t *testing.T) {
	t.Parallel()

	app, err := fabrin.New(fabrin.Options{Addr: "127.0.0.1:0"}, migrator("shop", "20260801120000", "orders"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := app.Capabilities()
	if !contains(caps["shop"], "Migrator") {
		t.Errorf("Capabilities()[shop] = %v, want it to include Migrator", caps["shop"])
	}
}

func TestNew_RejectsTwoModulesClaimingOneMigrationVersion(t *testing.T) {
	t.Parallel()

	// Two branches each generating the same timestamped version are both green in
	// isolation and collide only when their modules meet in one binary. The engine
	// would catch this at deploy time against a database — construction is cheaper,
	// and naming both modules is what makes the collision fixable without grepping.
	_, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		migrator("shop", "20260801120000", "orders"),
		migrator("billing", "20260801120000", "invoices"),
	)
	if !errors.Is(err, migrate.ErrDuplicateVersion) {
		t.Fatalf("got %v, want it to wrap migrate.ErrDuplicateVersion", err)
	}
	for _, want := range []string{"20260801120000", "shop", "billing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q, got: %v", want, err)
		}
	}
}

func TestNew_RejectsMigrationsOfMixedWidthsAcrossModules(t *testing.T) {
	t.Parallel()

	// The engine rejects mixed widths within one set, and the collected set spans
	// every mounted module. A module still on 9-digit versions beside one using 14
	// sorts into an order its authors did not write; found here, at wiring time,
	// not at deploy time against a schema each half never saw.
	_, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		migrator("shop", "0001", "orders"),
		migrator("billing", "20260801120000", "invoices"),
	)
	if !errors.Is(err, migrate.ErrInvalidMigration) {
		t.Fatalf("got %v, want it to wrap migrate.ErrInvalidMigration", err)
	}
	if !strings.Contains(err.Error(), "width") {
		t.Errorf("the error should say why — unequal widths, got: %v", err)
	}
}

func TestExecute_MigrateAppliesPendingMigrationsAndSaysSo(t *testing.T) {
	t.Parallel()

	// The command runs against the database main handed over — Fabrin opened
	// nothing, per ADR 0002 — and its output names what ran, because a migrate
	// job's whole value is knowing what state the schema is in afterwards.
	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		migrator("shop", "20260801120000", "orders"),
		migrator("billing", "20260802120000", "invoices"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out strings.Builder
	if err := app.Execute(context.Background(), &out, []string{"migrate"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, want := range []string{"20260801120000", "20260802120000", "create orders"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must mention %q, got: %q", want, out.String())
		}
	}

	// The migration actually ran: its table exists.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n); err != nil {
		t.Fatalf("the applied migration's table is missing: %v", err)
	}
}

func TestExecute_MigrateTwiceIsIdempotentAndSaysSo(t *testing.T) {
	t.Parallel()

	// An empty result means already up to date, which is the normal case on a
	// redeploy — the command says so rather than printing nothing, because silence
	// after a job reads as failure.
	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		migrator("shop", "20260801120000", "orders"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Execute(ctx, io.Discard, []string{"migrate"}); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	var out strings.Builder
	if err := app.Execute(ctx, &out, []string{"migrate"}); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if s := strings.ToLower(out.String()); !strings.Contains(s, "up to date") && !strings.Contains(s, "no migrations") {
		t.Errorf("an already-current migrate must say so, got: %q", out.String())
	}
}

func TestExecute_MigrateToRollsBackToAnExclusiveTarget(t *testing.T) {
	t.Parallel()

	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		migrator("shop", "20260801120000", "orders"),
		migrator("billing", "20260802120000", "invoices"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := app.Execute(ctx, io.Discard, []string{"migrate"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var out strings.Builder
	if err := app.Execute(ctx, &out, []string{"migrate", "-to", "20260801120000"}); err != nil {
		t.Fatalf("migrate -to: %v", err)
	}
	if !strings.Contains(strings.ToLower(out.String()), "roll") {
		t.Errorf("a backward move must say which direction it is going, got: %q", out.String())
	}

	// Exclusive target: 20260801120000 stays applied, the later one is undone.
	var invoices int
	err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'invoices'`).Scan(&invoices)
	if err != nil || invoices != 0 {
		t.Errorf("invoices table survived rollback (count=%d, err=%v); want dropped", invoices, err)
	}
	var orders int
	err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'orders'`).Scan(&orders)
	if err != nil || orders != 1 {
		t.Errorf("orders table did not survive rollback (count=%d, err=%v)", orders, err)
	}
}

func TestExecute_MigrateToMovesForwardToATarget(t *testing.T) {
	t.Parallel()

	// -to is not only rollback: with two pending migrations, -to the first applies
	// exactly one and stops. A deploy that must land halfway between versions is a
	// normal operation, not an exotic one.
	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db},
		migrator("shop", "20260801120000", "orders"),
		migrator("billing", "20260802120000", "invoices"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out strings.Builder
	if err := app.Execute(context.Background(), &out, []string{"migrate", "-to", "20260801120000"}); err != nil {
		t.Fatalf("migrate -to: %v", err)
	}
	if !strings.Contains(strings.ToLower(out.String()), "forward") {
		t.Errorf("a forward move must say which direction it is going, got: %q", out.String())
	}
	if strings.Contains(out.String(), "create invoices") {
		t.Errorf("-to applied past its target: %q", out.String())
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n); err != nil {
		t.Fatalf("the target migration did not apply: %v", err)
	}
}

func TestExecute_MigrateRefusesWhenTheProcessIsSliced(t *testing.T) {
	t.Parallel()

	// A sliced process holds a subset of the schema, and running its migrations
	// against the shared database would half-migrate it — or worse, makemigrations
	// would diff the subset and propose dropping every table whose module was
	// selected out. Refusing names the cause and the fix instead.
	db := memoryDB(t)
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0", DB: db, Modules: []string{"shop"}},
		migrator("shop", "20260801120000", "orders"),
		migrator("billing", "20260802120000", "invoices"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = app.Execute(context.Background(), io.Discard, []string{"migrate"})
	if err == nil {
		t.Fatal("a sliced process must refuse to migrate")
	}
	for _, want := range []string{"FABRIN_MODULES", "shop", "billing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q, got: %v", want, err)
		}
	}
}

func TestExecute_MigrateWithoutADatabaseNamesTheFix(t *testing.T) {
	t.Parallel()

	// Fabrin opens nothing (ADR 0002), so the command has no handle unless main
	// passed one. Failing with the exact fix beats a nil-pointer panic inside a
	// deploy job.
	app, err := fabrin.New(
		fabrin.Options{Addr: "127.0.0.1:0"},
		migrator("shop", "20260801120000", "orders"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = app.Execute(context.Background(), io.Discard, []string{"migrate"})
	if err == nil {
		t.Fatal("migrating with no configured database must fail")
	}
	if !strings.Contains(err.Error(), "DB") {
		t.Errorf("the error should point at Options.DB, got: %v", err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
