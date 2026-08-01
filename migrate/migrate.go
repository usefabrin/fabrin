// Package migrate applies ordered, reversible schema changes to a database.
//
// # The handle belongs to the caller
//
// Every entry point takes a *sql.DB. Fabrin opens nothing, ships no driver, and
// names no ORM: the application chose its database and its driver, and the
// migration engine is a consumer of that choice rather than a second place to
// make it. See docs/adr/0002-database-sql-is-the-orm-seam.md.
//
// This is also what lets migrations run from a process that mounts no HTTP
// routes at all. Nothing here imports Gin, an App, or the router.
//
// # A down step is required
//
// [M.Down] is not optional. A migration you cannot reverse is a deploy you cannot
// roll back, and the moment you need it is the worst possible moment to discover
// nobody wrote it. Where a change genuinely cannot be undone — a dropped column
// whose data is gone — Down says so and returns an error. That is a decision
// recorded in the file, which is a different thing from an omission.
//
// # Each migration is one transaction
//
// A migration's body and the row recording it are written in the SAME
// transaction, so a failure halfway leaves both the schema and the applied-state
// table exactly where they started. Recording the version outside the
// transaction is the classic form of this bug: the migration half-runs, the row
// says it finished, and the next run skips the half that never happened.
//
// This relies on the database rolling back DDL. SQLite and PostgreSQL do. MySQL
// and MariaDB do NOT — DDL there commits implicitly, so a failure can leave a
// partially-migrated schema that this package cannot undo for you. Fabrin does
// not paper over that; it is a property of the server, and the honest thing is to
// say so rather than to promise atomicity nobody can deliver.
//
// # Portability of the bookkeeping
//
// The statements this package issues use $1-style placeholders, which PostgreSQL
// requires and SQLite accepts, so the engine needs no dialect code of its own.
// Drivers that accept only ? — MySQL's, most notably — need the dialect work
// tracked in the F2 roadmap.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Table is the name of the applied-state table this package creates and owns.
//
// Exported because it is a table in the user's database and will appear in their
// schema dumps, their backups, and any migration tool they later evaluate.
// Something they will see is something they should be able to name.
const Table = "fabrin_migrations"

// M is one migration.
//
// Version orders migrations and identifies them in the applied-state table. It is
// a string rather than an int so that a timestamp — the format that stops two
// branches colliding — is the natural choice. Ordering is a plain lexicographic
// comparison, so a fixed-width format is required: 20260801120000 sorts
// correctly, 9 sorts after 10.
type M struct {
	// Version is unique and orders this migration against the others.
	Version string

	// Name is for humans reading the applied-state table during an incident.
	Name string

	// Up applies the change. It runs inside a transaction that also records the
	// version, so returning an error undoes both.
	Up func(ctx context.Context, tx *sql.Tx) error

	// Down reverses it, and is required — see the package documentation. If the
	// change cannot be reversed, return an error saying so.
	Down func(ctx context.Context, tx *sql.Tx) error
}

// Errors returned by [Run] and [Rollback]. Sentinels so a caller can branch with
// errors.Is rather than matching message text.
var (
	// ErrDuplicateVersion means two migrations claim one version.
	ErrDuplicateVersion = errors.New("migrate: duplicate version")

	// ErrInvalidMigration means a migration could not be applied or recorded —
	// no version, no Up, or no Down.
	ErrInvalidMigration = errors.New("migrate: invalid migration")

	// ErrOutOfOrder means an unapplied migration sorts before one already
	// applied, which is the shape of a branch merged out of order.
	ErrOutOfOrder = errors.New("migrate: out-of-order migration")

	// ErrMissingMigration means the database records a version that is not in the
	// set handed to [Rollback], so there is no Down to run.
	ErrMissingMigration = errors.New("migrate: no migration for applied version")
)

// Run applies every migration in ms that has not been applied yet, in version
// order, and returns those it applied.
//
// The whole set is validated and checked against the applied state BEFORE
// anything runs, so a set that will be rejected is rejected while the database is
// still untouched. An empty result means the database was already up to date;
// that is the normal case on a redeploy and is not an error.
func Run(ctx context.Context, db *sql.DB, ms []M) ([]M, error) {
	sorted, err := prepare(ctx, db, ms)
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	// Reject the out-of-order case before applying anything. Checked against the
	// HIGHEST applied version rather than pairwise, because that is the only one
	// a pending migration can be "behind": everything below it has already run.
	var highest string
	for _, v := range applied {
		if v > highest {
			highest = v
		}
	}
	pending := make([]M, 0, len(sorted))
	for _, m := range sorted {
		if slices.Contains(applied, m.Version) {
			continue
		}
		if m.Version < highest {
			return nil, fmt.Errorf("%w: %q (%s) sorts before %q, which is already applied — "+
				"this is a branch merged out of order, and applying it now would run it "+
				"against a schema it was never written against",
				ErrOutOfOrder, m.Version, m.Name, highest)
		}
		pending = append(pending, m)
	}

	for i, m := range pending {
		if err := apply(ctx, db, m); err != nil {
			// The migrations before this one committed and stay applied. Naming
			// what did run matters: "migration failed" alone leaves the operator
			// guessing at the database's actual state.
			return pending[:i], err
		}
	}
	return pending, nil
}

// Rollback undoes applied migrations newer than toVersion, newest first, and
// returns those it undid.
//
// toVersion is EXCLUSIVE — it names the state to be left in, so the migration
// with that version stays applied. An empty toVersion undoes everything.
//
// Reverse order, because a later migration may depend on an earlier one; undoing
// 001 before 003 would remove what 003's own Down still needs.
//
// The WHOLE of ms is validated, not only the migrations being undone, so a
// malformed migration outside the rollback range still fails the call. That is
// deliberate: ms is the set of migrations the caller believes it has, and
// half-validating it would mean the same input is accepted by Rollback and
// rejected by [Run].
func Rollback(ctx context.Context, db *sql.DB, ms []M, toVersion string) ([]M, error) {
	sorted, err := prepare(ctx, db, ms)
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	byVersion := make(map[string]M, len(sorted))
	for _, m := range sorted {
		byVersion[m.Version] = m
	}

	// Newest first.
	targets := make([]string, 0, len(applied))
	for _, v := range applied {
		if v > toVersion {
			targets = append(targets, v)
		}
	}
	slices.Sort(targets)
	slices.Reverse(targets)

	// Resolved up front, so a version with no Down in hand fails before anything
	// is undone. Skipping it silently would report success while leaving the
	// schema ahead of where the caller asked to be.
	undo := make([]M, 0, len(targets))
	for _, v := range targets {
		m, ok := byVersion[v]
		if !ok {
			return nil, fmt.Errorf("%w: %q is recorded in %s but was not passed to Rollback, "+
				"so there is no Down to run", ErrMissingMigration, v, Table)
		}
		undo = append(undo, m)
	}

	for i, m := range undo {
		if err := revert(ctx, db, m); err != nil {
			return undo[:i], err
		}
	}
	return undo, nil
}

// prepare validates the set and ensures the applied-state table exists. It
// returns the migrations sorted by version.
//
// Validation first, so an unusable set is rejected before the engine touches the
// database at all.
func prepare(ctx context.Context, db *sql.DB, ms []M) ([]M, error) {
	seen := make(map[string]M, len(ms))
	for _, m := range ms {
		switch {
		case m.Version == "":
			return nil, fmt.Errorf("%w: a migration with no version has nothing to order it by and nothing to record", ErrInvalidMigration)
		case m.Up == nil:
			return nil, fmt.Errorf("%w: %q (%s) has no Up step", ErrInvalidMigration, m.Version, m.Name)
		case m.Down == nil:
			return nil, fmt.Errorf("%w: %q (%s) has no Down step — a migration you cannot reverse is a deploy you cannot roll back. If it genuinely cannot be undone, say so from Down", ErrInvalidMigration, m.Version, m.Name)
		}
		if prev, dup := seen[m.Version]; dup {
			return nil, fmt.Errorf("%w: %q claimed by %q and %q", ErrDuplicateVersion, m.Version, prev.Name, m.Name)
		}
		seen[m.Version] = m
	}

	sorted := slices.Clone(ms)
	slices.SortFunc(sorted, func(a, b M) int {
		switch {
		case a.Version < b.Version:
			return -1
		case a.Version > b.Version:
			return 1
		}
		return 0
	})

	if err := ensureTable(ctx, db); err != nil {
		return nil, err
	}
	return sorted, nil
}

// ensureTable creates the applied-state table if it is not already there.
//
// The types are the intersection that means the same thing everywhere: TEXT and
// TIMESTAMP. No SERIAL, no AUTOINCREMENT, no dialect-specific defaults — this one
// statement has to work on every database Fabrin supports, and the version is the
// natural primary key anyway.
func ensureTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+Table+` (
		version    TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMP NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("migrate: create %s: %w", Table, err)
	}
	return nil
}

// appliedVersions reads the versions already recorded.
func appliedVersions(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM `+Table+` ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", Table, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", Table, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", Table, err)
	}
	return out, nil
}

// apply runs one migration's Up and records it, in a single transaction.
func apply(ctx context.Context, db *sql.DB, m M) error {
	return inTx(ctx, db, m, func(tx *sql.Tx) error {
		if err := m.Up(ctx, tx); err != nil {
			return err
		}
		// Same transaction as the body above. This is the line the package
		// documentation is about: recorded outside it, a half-applied migration
		// would be marked done and skipped forever after.
		_, err := tx.ExecContext(ctx,
			`INSERT INTO `+Table+` (version, name, applied_at) VALUES ($1, $2, $3)`,
			m.Version, m.Name, time.Now().UTC())
		return err
	})
}

// revert runs one migration's Down and removes its record, in a single
// transaction.
func revert(ctx context.Context, db *sql.DB, m M) error {
	return inTx(ctx, db, m, func(tx *sql.Tx) error {
		if err := m.Down(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM `+Table+` WHERE version = $1`, m.Version)
		return err
	})
}

// inTx runs fn in a transaction, rolling back on any error and naming the
// migration in whatever it returns.
func inTx(ctx context.Context, db *sql.DB, m M, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: %q (%s): begin: %w", m.Version, m.Name, err)
	}

	if err := fn(tx); err != nil {
		// The rollback error is deliberately not returned in its place: the
		// original failure is what the operator needs, and replacing it with
		// "rollback failed" loses the only useful information.
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return fmt.Errorf("migrate: %q (%s): %w (rollback also failed: %v)", m.Version, m.Name, err, rbErr)
		}
		return fmt.Errorf("migrate: %q (%s): %w", m.Version, m.Name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: %q (%s): commit: %w", m.Version, m.Name, err)
	}
	return nil
}
