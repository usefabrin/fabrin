// Command hello is the smallest Fabrin app that demonstrates the two claims F0
// makes — ports rather than imports, and process slicing — plus the one F2 adds:
// a module reaching its data through a port it declares, with this file as the
// only one that names SQL.
//
// Run it:
//
//	go run ./examples/hello                      # every module
//	FABRIN_MODULES=greet go run ./examples/hello # only greet builds and mounts; /time is a 404
//
// Then:
//
//	curl localhost:8080/greet?name=you
//	curl localhost:8080/time
//	curl -X POST localhost:8080/orders \
//	  -H 'Content-Type: application/json' -d '{"item":"widget"}'
//	curl localhost:8080/orders/1   # the order that POST created
//	curl localhost:8080/healthz    # liveness, consults nothing
//	curl localhost:8080/readyz     # readiness, aggregates module checks
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/config"
	"github.com/usefabrin/fabrin/examples/hello/clock"
	"github.com/usefabrin/fabrin/examples/hello/greet"
	"github.com/usefabrin/fabrin/examples/hello/orders"
	"github.com/usefabrin/fabrin/migrate"

	// The driver is main's business and nobody else's. orders never learns that
	// SQL exists, and fabrin/migrate takes a *sql.DB without importing a driver
	// of its own — see ADR 0002.
	_ "modernc.org/sqlite"
)

func main() {
	// The conventional stack, later layers winning: defaults ← .env ← env ← flags.
	// Load with no sources at all is an error rather than a silent no-op.
	cfg, err := config.Load(config.Standard()...)
	if err != nil {
		log.Fatal(err)
	}

	app, err := newApp(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}

	// With no arguments this serves — the mounted modules plus /healthz and
	// /readyz, every request logged with an id, and a graceful shutdown on SIGINT
	// or SIGTERM. With arguments it runs the named command.
	//
	// Execute rather than Run because Go compiles: no separate tool can introspect
	// an application it did not build, so `routes` has to be answered by the
	// binary that already has the modules linked in.
	if err := app.Execute(context.Background(), os.Stdout, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// newApp is where the wiring lives, separate from main so the tests exercise the
// same construction the binary does. A test that builds a different app proves
// nothing about the example anyone actually runs.
//
// This function is also the whole of the "ports, not imports" story. It is the
// ONLY place that knows both modules exist: greet asks for a greet.Clock, the
// clock module happens to satisfy it, and neither package names the other.
// Swapping in a remote clock is a change here and nowhere else.
func newApp(ctx context.Context, opts fabrin.Options) (*fabrin.App, error) {
	return newAppWithDB(ctx, opts, openDB)
}

type dbOpener func(context.Context) (*sql.DB, error)

func newAppWithDB(ctx context.Context, opts fabrin.Options, open dbOpener) (*fabrin.App, error) {
	// Both selected factories can need the same clock: greet as a typed port, and
	// clock as the HTTP module. This memoized constructor is ordinary explicit Go
	// wiring, not a service locator. A greet-only process constructs the dependency
	// it actually needs without mounting the clock module's routes or checks.
	var clk *clock.Module
	clockFor := func() *clock.Module {
		if clk == nil {
			clk = clock.New()
		}
		return clk
	}

	return fabrin.NewFromFactories(ctx, opts,
		fabrin.LazyModule("greet", func(context.Context) (fabrin.Module, error) {
			return greet.New(clockFor()), nil // the port, satisfied in-process
		}),
		fabrin.LazyModule("clock", func(context.Context) (fabrin.Module, error) {
			return clockFor(), nil
		}),
		fabrin.LazyModule("orders", func(context.Context) (fabrin.Module, error) {
			store := &sqlStore{open: open}
			return &ordersRuntime{Module: orders.New(store), store: store}, nil
		}),
	)
}

// openDB is the only place in this program that names a database. It opens
// SQLite in memory and brings the schema up to date before anything serves.
//
// In memory so the example needs no server and no file, and one connection
// because every :memory: connection gets its own empty database — a pool would
// hand the second request a schema the first never created.
//
// A real deployment runs migrations as a separate step, from a process that
// mounts no routes: that is why fabrin/migrate takes a *sql.DB and imports no
// transport. Doing it inline here keeps `go run ./examples/hello` a single
// command.
func openDB(ctx context.Context) (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := migrate.Run(ctx, db, migrations()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// migrations is what `fabrin makemigrations` will generate once it exists. Until
// then it is written by hand, which is the same thing the generator will emit:
// migrations are Go, not a DSL, so there is nothing here a generator could not
// have produced and nothing a human cannot read.
//
// Note the handle. Up and Down take a migrate.Handle rather than a *sql.Tx, so
// the same body would work if a future migration opted out of its transaction —
// see ADR 0003. Down is required, and a migration that genuinely cannot be
// undone says so by returning an error rather than by being omitted.
func migrations() []migrate.M {
	return []migrate.M{{
		Version: "20260802090000",
		Name:    "create orders",
		Up: func(ctx context.Context, h migrate.Handle) error {
			_, err := h.ExecContext(ctx, `CREATE TABLE orders (
				id   INTEGER PRIMARY KEY AUTOINCREMENT,
				item TEXT NOT NULL
			)`)
			return err
		},
		Down: func(ctx context.Context, h migrate.Handle) error {
			_, err := h.ExecContext(ctx, `DROP TABLE orders`)
			return err
		},
	}}
}

// sqlStore satisfies orders.Store. It lives here rather than in the orders
// package on purpose: the module must not know that SQL exists, and this file is
// the one place allowed to.
type sqlStore struct {
	open dbOpener
	db   *sql.DB
}

// ordersRuntime keeps resource ownership beside the module while preserving the
// optional interfaces embedded orders.Module contributes. Construction is cheap;
// Lifecycle.Start is the only place that opens the database.
type ordersRuntime struct {
	*orders.Module
	store *sqlStore
}

func (m *ordersRuntime) Start(ctx context.Context) error {
	db, err := m.store.open(ctx)
	if err != nil {
		return err
	}
	m.store.db = db
	return nil
}

func (m *ordersRuntime) Stop(context.Context) error {
	if m.store.db == nil {
		return nil
	}
	err := m.store.db.Close()
	m.store.db = nil
	return err
}

func (s *sqlStore) Create(ctx context.Context, o *orders.Order) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO orders (item) VALUES ($1)`, o.Item)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	o.ID = id
	return nil
}

func (s *sqlStore) Find(ctx context.Context, id int64) (*orders.Order, error) {
	var o orders.Order
	err := s.db.QueryRowContext(ctx, `SELECT id, item FROM orders WHERE id = $1`, id).
		Scan(&o.ID, &o.Item)
	if errors.Is(err, sql.ErrNoRows) {
		// Translated at the boundary, so the module can answer 404 without
		// importing database/sql to recognise the error.
		return nil, orders.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}
