package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/usefabrin/fabrin/migrate"
)

// newDB opens an in-memory SQLite database for one test.
//
// MaxOpenConns(1) is load-bearing, not tuning. Every ":memory:" connection gets
// its OWN empty database, so a pool of two means a migration applied on one
// connection is invisible on the next — which looks exactly like a bug in the
// engine and is not one.
func newDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// createTable is the body of a migration that adds one table.
func createTable(name string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE `+name+` (id INTEGER PRIMARY KEY)`)
		return err
	}
}

func dropTable(name string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE `+name)
		return err
	}
}

// m builds a migration that creates and drops one table.
func m(version, name, table string) migrate.M {
	return migrate.M{
		Version: version,
		Name:    name,
		Up:      createTable(table),
		Down:    dropTable(table),
	}
}

// tableExists reports whether name is a table in the database.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()

	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=$1`, name).Scan(&n)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return n > 0
}

// versions returns the versions in the applied-state table, in order.
func versions(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(context.Background(),
		`SELECT version FROM fabrin_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read applied state: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func names(ms []migrate.M) []string {
	out := make([]string, 0, len(ms))
	for _, mig := range ms {
		out = append(out, mig.Version)
	}
	return out
}

// ── The two that dictate the structure ──────────────────────────────────────

func TestRun_LeavesNothingBehindWhenAMigrationFails(t *testing.T) {
	t.Parallel()

	// The whole promise of the engine. The failure has to be INSIDE the
	// transaction, and the applied-state row has to be written in the SAME
	// transaction as the migration body — otherwise a half-applied migration is
	// recorded as done, and the next run skips the half that never happened.
	//
	// Asserted on both halves: the DDL that ran before the error must be gone,
	// AND the version must not be recorded.
	boom := errors.New("no")
	db := newDB(t)

	_, err := migrate.Run(t.Context(), db, []migrate.M{
		m("001", "first", "alpha"),
		{
			Version: "002",
			Name:    "explodes",
			Up: func(ctx context.Context, tx *sql.Tx) error {
				// DDL first, so there is something to roll back.
				if _, err := tx.ExecContext(ctx, `CREATE TABLE beta (id INTEGER PRIMARY KEY)`); err != nil {
					return err
				}
				return boom
			},
			Down: dropTable("beta"),
		},
	})
	if err == nil {
		t.Fatal("a failing migration must return an error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("the cause must survive: got %v", err)
	}

	// The migration before it committed and stays.
	if !tableExists(t, db, "alpha") {
		t.Error("a migration that already committed must not be rolled back")
	}
	if got := versions(t, db); len(got) != 1 || got[0] != "001" {
		t.Errorf("applied state = %v, want only 001", got)
	}

	// The failing one left nothing at all.
	if tableExists(t, db, "beta") {
		t.Error("DDL from the failed migration survived — it was not in the transaction")
	}
}

func TestRun_RejectsAMigrationOrderedBeforeOneAlreadyApplied(t *testing.T) {
	t.Parallel()

	// This is the shape of a branch merged out of order: someone's 002 lands
	// after 003 is already in production. Applying it silently would run it
	// against a schema it was never written against, and the diff it assumed is
	// not the diff it gets.
	db := newDB(t)

	if _, err := migrate.Run(t.Context(), db, []migrate.M{
		m("001", "first", "alpha"),
		m("003", "third", "gamma"),
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 004 is perfectly fine and pending. It must NOT be applied either: the whole
	// set is checked before anything runs, so an operator retries once rather than
	// discovering a partially-advanced schema.
	_, err := migrate.Run(t.Context(), db, []migrate.M{
		m("001", "first", "alpha"),
		m("002", "sneaked in", "beta"),
		m("003", "third", "gamma"),
		m("004", "innocent bystander", "delta"),
	})
	if err == nil {
		t.Fatal("a migration ordered before an applied one must be rejected")
	}
	if !errors.Is(err, migrate.ErrOutOfOrder) {
		t.Errorf("callers branch with errors.Is, got %v", err)
	}
	// Both versions, or the reader cannot tell what conflicts with what.
	for _, want := range []string{"002", "003"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q, got: %v", want, err)
		}
	}

	// Rejected BEFORE applying anything: nothing ran, including the valid one.
	if tableExists(t, db, "beta") {
		t.Error("the out-of-order migration was applied despite the error")
	}
	if tableExists(t, db, "delta") {
		t.Error("a valid pending migration ran anyway — the set is checked before anything is applied")
	}
	if got := versions(t, db); strings.Join(got, ",") != "001,003" {
		t.Errorf("applied state = %v, want it untouched at 001,003", got)
	}
}

// ── Applying ────────────────────────────────────────────────────────────────

func TestRun_AppliesPendingMigrationsInVersionOrder(t *testing.T) {
	t.Parallel()

	// Ordered by version, never by slice order — a caller that globs a directory
	// gets whatever the filesystem returns, and that is not a total order.
	db := newDB(t)

	applied, err := migrate.Run(t.Context(), db, []migrate.M{
		m("003", "third", "gamma"),
		m("001", "first", "alpha"),
		m("002", "second", "beta"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := names(applied); strings.Join(got, ",") != "001,002,003" {
		t.Errorf("applied %v, want them in version order", got)
	}
	for _, table := range []string{"alpha", "beta", "gamma"} {
		if !tableExists(t, db, table) {
			t.Errorf("migration for %s did not run", table)
		}
	}
}

func TestRun_IsIdempotent(t *testing.T) {
	t.Parallel()

	// Already-applied migrations are skipped, not re-run. Re-running the CREATE
	// TABLE above would fail, so this also proves they were genuinely skipped
	// rather than run and forgiven.
	db := newDB(t)
	ms := []migrate.M{m("001", "first", "alpha"), m("002", "second", "beta")}

	if _, err := migrate.Run(t.Context(), db, ms); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	applied, err := migrate.Run(t.Context(), db, ms)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("second Run applied %v, want nothing", names(applied))
	}
	if got := versions(t, db); len(got) != 2 {
		t.Errorf("applied state = %v, want the original two", got)
	}
}

func TestRun_RecordsVersionNameAndWhenItWasApplied(t *testing.T) {
	t.Parallel()

	// "When" is the column people actually reach for during an incident: which
	// deploy introduced this, and did the migration land before or after.
	db := newDB(t)
	before := time.Now().UTC().Add(-time.Second)

	if _, err := migrate.Run(t.Context(), db, []migrate.M{m("001", "add orders", "alpha")}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		version, name string
		appliedAt     time.Time
	)
	err := db.QueryRowContext(t.Context(),
		`SELECT version, name, applied_at FROM fabrin_migrations`).Scan(&version, &name, &appliedAt)
	if err != nil {
		t.Fatalf("read applied state: %v", err)
	}

	if version != "001" || name != "add orders" {
		t.Errorf("recorded (%q, %q), want (001, add orders)", version, name)
	}
	if appliedAt.Before(before) || appliedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("applied_at = %v, want roughly now", appliedAt)
	}
}

// ── Rejected before anything runs ───────────────────────────────────────────

func TestRun_RejectsAnUnusableMigrationSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ms   []migrate.M
		want error
		why  string
	}{
		{
			name: "duplicate version",
			ms: []migrate.M{
				m("001", "first", "alpha"),
				m("001", "also first", "beta"),
			},
			want: migrate.ErrDuplicateVersion,
			why:  "two branches in flight is a matter of when, not if",
		},
		{
			name: "no version",
			ms:   []migrate.M{m("", "nameless", "alpha")},
			want: migrate.ErrInvalidMigration,
			why:  "there is nothing to order it by and nothing to record",
		},
		{
			name: "no up step",
			ms:   []migrate.M{{Version: "001", Name: "empty", Down: dropTable("alpha")}},
			want: migrate.ErrInvalidMigration,
			why:  "a migration that does nothing is a file someone forgot to finish",
		},
		{
			name: "no down step",
			ms:   []migrate.M{{Version: "001", Name: "one way", Up: createTable("alpha")}},
			want: migrate.ErrInvalidMigration,
			why:  "a migration you cannot reverse is a deploy you cannot roll back",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newDB(t)
			_, err := migrate.Run(t.Context(), db, tc.ms)
			if err == nil {
				t.Fatalf("must be rejected — %s", tc.why)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want it to wrap %v", err, tc.want)
			}
			// Validation happens before anything is applied.
			if tableExists(t, db, "alpha") {
				t.Error("a rejected set must not have applied anything")
			}
		})
	}
}

// ── Rolling back ────────────────────────────────────────────────────────────

func TestRollback_UndoesInReverseOrder(t *testing.T) {
	t.Parallel()

	// Reverse order, because a later migration may depend on an earlier one.
	// Undoing 001 before 003 would drop the table 003's own Down still needs.
	db := newDB(t)
	ms := []migrate.M{
		m("001", "first", "alpha"),
		m("002", "second", "beta"),
		m("003", "third", "gamma"),
	}
	if _, err := migrate.Run(t.Context(), db, ms); err != nil {
		t.Fatalf("Run: %v", err)
	}

	undone, err := migrate.Rollback(t.Context(), db, ms, "")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if got := names(undone); strings.Join(got, ",") != "003,002,001" {
		t.Errorf("undone %v, want newest first", got)
	}
	for _, table := range []string{"alpha", "beta", "gamma"} {
		if tableExists(t, db, table) {
			t.Errorf("%s survived a full rollback", table)
		}
	}
	if got := versions(t, db); len(got) != 0 {
		t.Errorf("applied state = %v, want empty", got)
	}
}

func TestRollback_StopsAtTheTargetVersion(t *testing.T) {
	t.Parallel()

	// toVersion is EXCLUSIVE: it is the state you want to be left in, so the
	// migration named stays applied. "Roll back to 001" leaving 001 undone would
	// be an off-by-one nobody notices until production.
	db := newDB(t)
	ms := []migrate.M{
		m("001", "first", "alpha"),
		m("002", "second", "beta"),
		m("003", "third", "gamma"),
	}
	if _, err := migrate.Run(t.Context(), db, ms); err != nil {
		t.Fatalf("Run: %v", err)
	}

	undone, err := migrate.Rollback(t.Context(), db, ms, "001")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if got := names(undone); strings.Join(got, ",") != "003,002" {
		t.Errorf("undone %v, want 003 then 002", got)
	}
	if !tableExists(t, db, "alpha") {
		t.Error("the target version must stay applied")
	}
	if got := versions(t, db); len(got) != 1 || got[0] != "001" {
		t.Errorf("applied state = %v, want only 001", got)
	}
}

func TestRollback_LeavesNothingBehindWhenADownStepFails(t *testing.T) {
	t.Parallel()

	// Same transactional promise as Run, in the other direction: a Down that
	// fails halfway must not leave the version deleted from the applied table.
	boom := errors.New("no")
	db := newDB(t)
	ms := []migrate.M{
		m("001", "first", "alpha"),
		{
			Version: "002",
			Name:    "wont go quietly",
			Up:      createTable("beta"),
			Down: func(ctx context.Context, tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, `DROP TABLE beta`); err != nil {
					return err
				}
				return boom
			},
		},
	}
	if _, err := migrate.Run(t.Context(), db, ms); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := migrate.Rollback(t.Context(), db, ms, ""); err == nil {
		t.Fatal("a failing Down must return an error")
	}

	if !tableExists(t, db, "beta") {
		t.Error("the failed Down's DROP was not rolled back")
	}
	if got := versions(t, db); len(got) != 2 {
		t.Errorf("applied state = %v, want both still recorded", got)
	}
}

func TestRollback_ReportsAnAppliedVersionItCannotUndo(t *testing.T) {
	t.Parallel()

	// A version in the database with no migration in hand cannot be rolled back —
	// its Down does not exist. Silently skipping it would report success while
	// leaving the schema ahead of where the caller asked for.
	db := newDB(t)
	if _, err := migrate.Run(t.Context(), db, []migrate.M{m("001", "first", "alpha")}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	_, err := migrate.Rollback(t.Context(), db, nil, "")
	if err == nil {
		t.Fatal("rolling back a version with no Down in hand must fail")
	}
	if !strings.Contains(err.Error(), "001") {
		t.Errorf("the error must name the version, got: %v", err)
	}
	if !tableExists(t, db, "alpha") {
		t.Error("nothing should have been undone")
	}
}
