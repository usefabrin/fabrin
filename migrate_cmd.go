package fabrin

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"slices"

	"github.com/usefabrin/fabrin/cli"
	"github.com/usefabrin/fabrin/migrate"
)

// toFlag is the migrate command's target-version flag.
const toFlag = "to"

// migrateCommand builds the migrate built-in. It is a function rather than a
// literal in [App.builtins] so the parsed -to value lives in a binding created
// per call: Flags and Run are two closures over it, Dispatch parses before Run
// fires, and one invocation's value can never leak into another — including
// into a parallel Execute of the same App.
func migrateCommand(a *App) cli.Command {
	var to string
	return cli.Command{
		Name:  "migrate",
		Short: "apply the modules' schema migrations, or roll back to a version with -to",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&to, toFlag, "", "target version: apply pending migrations up to it, or roll back to it")
		},
		Run: func(ctx context.Context, out io.Writer, _ []string) error {
			return a.runMigrate(ctx, out, to)
		},
	}
}

// runMigrate applies pending migrations, or — with -to — moves the schema to
// that version, forward or backward depending on where the database stands.
//
// The direction is decided by READING the applied-state table before anything
// runs, and stated in the output before acting: a migration job that does not
// say which way it is going is a deploy incident waiting for its moment. The
// engine still owns every mutation; this reads only.
//
// Refusals, both before the database is touched:
//
//   - A sliced process refuses outright. Its modules hold a SUBSET of the
//     schema, and migrating from a subset would half-migrate the shared
//     database — while makemigrations run the same way would propose dropping
//     every table whose module was selected out. FABRIN_MODULES is route
//     selection, never schema selection.
//   - No configured database refuses with an error naming [Options.DB], because
//     Fabrin opens nothing (ADR 0002): main opened the handle, main hands it
//     over, and only main can fix its absence.
func (a *App) runMigrate(ctx context.Context, out io.Writer, target string) error {
	if a.registry.sliced {
		return fmt.Errorf("fabrin: this process is sliced by FABRIN_MODULES (registered: %v, mounted: %v) — "+
			"migration commands refuse to act on part of the schema, because half-migrating the shared database "+
			"is worse than not migrating. Run without FABRIN_MODULES set",
			a.registry.catalog, a.registry.names())
	}
	if len(a.migrations) == 0 {
		if _, err := fmt.Fprintln(out, "no migrations declared; nothing to do"); err != nil {
			return err
		}
		return nil
	}
	if a.opts.DB == nil {
		return fmt.Errorf("fabrin: no database configured for migrate — open one in main and set Options.DB")
	}
	if target != "" && !slices.ContainsFunc(a.migrations, func(m migrate.M) bool { return m.Version == target }) {
		return fmt.Errorf("fabrin: -to %q matches no declared migration version", target)
	}

	if err := migrate.Ensure(ctx, a.opts.DB); err != nil {
		return err
	}
	applied, err := readApplied(ctx, a.opts.DB)
	if err != nil {
		return err
	}
	isApplied := func(v string) bool { return slices.Contains(applied, v) }

	switch {
	case target == "":
		if _, err := fmt.Fprintf(out, "migrating forward (%d declared)\n", len(a.migrations)); err != nil {
			return err
		}
		done, err := migrate.Run(ctx, a.opts.DB, a.migrations)
		if rerr := report(out, done); err == nil {
			return rerr
		}
		return err
	case !isApplied(target):
		// Forward to the target: everything up to and including it that has not
		// applied yet. Filtering here keeps the engine untouched — Run already
		// validates the whole subset, orders it, and stops cleanly at the end of
		// what it was handed.
		if _, err := fmt.Fprintf(out, "migrating forward to %s\n", target); err != nil {
			return err
		}
		subset := make([]migrate.M, 0, len(a.migrations))
		for _, m := range a.migrations {
			if m.Version <= target && !isApplied(m.Version) {
				subset = append(subset, m)
			}
		}
		slices.SortFunc(subset, func(x, y migrate.M) int {
			switch {
			case x.Version < y.Version:
				return -1
			case x.Version > y.Version:
				return 1
			}
			return 0
		})
		done, err := migrate.Run(ctx, a.opts.DB, subset)
		if rerr := report(out, done); err == nil {
			return rerr
		}
		return err
	default:
		if _, err := fmt.Fprintf(out, "rolling back to %s (exclusive)\n", target); err != nil {
			return err
		}
		done, err := migrate.Rollback(ctx, a.opts.DB, a.migrations, target)
		if rerr := report(out, done); err == nil {
			return rerr
		}
		return err
	}
}

// report prints what ran, or the already-current message when nothing did. An
// empty result after a job must SAY itself: silence reads as failure.
func report(out io.Writer, done []migrate.M) error {
	if len(done) == 0 {
		_, err := fmt.Fprintln(out, "up to date; no migrations applied")
		return err
	}
	for _, m := range done {
		if _, err := fmt.Fprintf(out, "applied %s %s\n", m.Version, m.Name); err != nil {
			return err
		}
	}
	return nil
}

// readApplied lists the versions recorded in the applied-state table. The
// command reads state only to choose a direction; every write goes through the
// engine, which owns the transactional guarantees around those writes.
func readApplied(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM `+migrate.Table+` ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("fabrin: read applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("fabrin: read applied migrations: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
