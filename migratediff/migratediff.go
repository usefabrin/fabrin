// Package migratediff diffs recorded model state against current model state
// and renders the difference as SQL through a per-database dialect.
//
// This is the seam makemigrations will stand on: the differ produces a
// deterministic list of operations, and a dialect turns operations into the
// statements a real server accepts.
//
// # Why nothing here is exported
//
// This package exports no symbols, on purpose and for now. Every exported
// symbol is a permanent promise, and the right public shape for a differ —
// method granularity, whether the dialect interface is user-facing at all — is
// exactly what a private vertical exists to discover before it freezes. The
// admin CRUD proof set the precedent
// (docs/adr/0005-admin-crud-seam-remains-private.md); this follows it. When
// fabrin makemigrations lands (#59), whatever survived here graduates behind a
// deliberate API decision rather than by accident of being first.
//
// It lives at the repository root rather than under internal/ because the
// boundary rules forbid internal/ from importing sibling Fabrin packages, and
// this package's whole job is reading orm metadata. An unexported root package
// contributes nothing to api/fabrin.txt while staying importable by the
// command that will eventually wrap it.
//
// # What the differ can and cannot see
//
// Nullability is invisible, because the provisional Field flags are withheld
// from recorded state (#79) and nothing consumes them yet. A changed
// nullability therefore produces no operation today; when #79 decides those
// flags, detection arrives with them. Saying so beats emitting NOT NULL on a
// guess.
//
// # Ordering is part of the output
//
// Creates first (tables sorted), then alterations per surviving table
// (columns sorted, drops before retypes before additions), whole-table drops
// last. Fixed once, here, because the generated migration inherits whatever
// wiggle this allows: a generator whose output depends on map iteration
// produces migrations that differ between runs and reviews terribly.
package migratediff

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/usefabrin/fabrin/orm"
)

// errUnsupported means a dialect cannot express an operation at all — SQLite
// altering or dropping a column in place, most notably. Callers get this
// BEFORE anything executes, from render, never halfway through a migration.
var errUnsupported = errors.New("migratediff: unsupported operation")

// dialect renders operations as the SQL one database family accepts.
//
// Two dialects ship from the start — SQLite, which runs hermetically in CI,
// and PostgreSQL, which is what people deploy. Having two from the beginning
// is what stops the interface being accidentally shaped around one.
type dialect interface {
	// name identifies the dialect in errors and generated-file headers.
	name() string

	createTable(m orm.Model) (string, error)
	addColumn(table string, f orm.Field) (string, error)
	dropColumn(table, column string) (string, error)
	changeType(table, column string, to orm.Field) (string, error)
	dropTable(table string) (string, error)
}

// operation is one change between two states, rendered into SQL by a dialect.
// Each operation is exactly one statement; when a future operation needs many
// (a table rebuild), that changes deliberately, not by accident of signature.
type operation interface {
	render(d dialect) (string, error)

	// describe names the change in one line, for errors and for the header of
	// the migration file that carries it.
	describe() string
}

// createTable brings a new table into being, columns in declared order.
type createTable struct {
	model orm.Model
}

// dropTable removes a table and everything in it. Data loss, stated in the SQL.
type dropTable struct {
	table string
}

// addColumn appends one column to an existing table.
type addColumn struct {
	table string
	field orm.Field
}

// dropColumn removes one column and its data. The rendered statement carries
// the warning as a SQL comment, because that is the line a reviewer must not
// skim past.
type dropColumn struct {
	table  string
	column string
}

// changeType moves a column to a new type or length. What the server does with
// the existing values is the server's semantics; the operation states the
// destination, not a promise about conversion.
type changeType struct {
	table  string
	column string
	to     orm.Field
}

// diff compares two recorded states and returns the operations that turn
// before into after, in the order they should run.
//
// Both snapshots normally come from [orm.Snapshot.Models]; they are read-only
// inputs here. Fields are compared BY NAME — a pure reorder of the declared
// field list produces nothing, because DDL cannot reorder columns without
// rebuilding the table and destroying data over a cosmetic diff.
func diff(before, after orm.Snapshot) []operation {
	beforeByTable := index(before.Models())
	afterModels := after.Models()
	afterByTable := index(afterModels)

	var ops []operation

	// New tables, sorted.
	for _, reg := range afterModels {
		if _, exists := beforeByTable[reg.Model.Table]; !exists {
			ops = append(ops, createTable{model: reg.Model})
		}
	}

	// Alterations to surviving tables: table-sorted, column-sorted within a
	// table, drops before retypes before additions so data loss clusters
	// early in review.
	for _, reg := range afterModels {
		was, exists := beforeByTable[reg.Model.Table]
		if !exists {
			continue
		}
		type ranked struct {
			column string
			rank   int
			op     operation
		}
		var alters []ranked
		add := func(column string, rank int, op operation) {
			alters = append(alters, ranked{column: column, rank: rank, op: op})
		}
		beforeFields := indexFields(was.Model.Fields)
		for _, f := range reg.Model.Fields {
			prev, exists := beforeFields[f.Name]
			switch {
			case !exists:
				add(f.Name, 2, addColumn{table: reg.Model.Table, field: f})
			case prev.Type != f.Type || prev.MaxLen != f.MaxLen:
				add(f.Name, 1, changeType{table: reg.Model.Table, column: f.Name, to: f})
			}
		}
		afterFields := indexFields(reg.Model.Fields)
		for _, f := range was.Model.Fields {
			if _, exists := afterFields[f.Name]; !exists {
				add(f.Name, 0, dropColumn{table: reg.Model.Table, column: f.Name})
			}
		}
		slices.SortFunc(alters, func(a, b ranked) int {
			switch {
			case a.column != b.column:
				return strings.Compare(a.column, b.column)
			case a.rank < b.rank:
				return -1
			case a.rank > b.rank:
				return 1
			}
			return 0
		})
		for _, a := range alters {
			ops = append(ops, a.op)
		}
	}

	// Whole-table drops, sorted, last.
	for _, reg := range before.Models() {
		if _, exists := afterByTable[reg.Model.Table]; !exists {
			ops = append(ops, dropTable{table: reg.Model.Table})
		}
	}

	return ops
}

// apply renders every operation through d and executes it, in order, stopping
// at the first failure. A convenience for tests and for whatever command
// eventually runs generated migrations; anything transactional around it is
// the caller's business, exactly as it is for migrate.Run.
func apply(ctx context.Context, db *sql.DB, d dialect, ops []operation) error {
	for _, op := range ops {
		stmt, err := op.render(d)
		if err != nil {
			return fmt.Errorf("%s: %w", op.describe(), err)
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", op.describe(), err)
		}
	}
	return nil
}

// render implementations. Each operation knows how to hand itself to a
// dialect; the dialect owns the SQL, the operation owns the intent. Adding a
// dialect means implementing the dialect interface once — not touching every
// operation.
func (o createTable) render(d dialect) (string, error) { return d.createTable(o.model) }

func (o dropTable) render(d dialect) (string, error) { return d.dropTable(o.table) }

func (o addColumn) render(d dialect) (string, error) { return d.addColumn(o.table, o.field) }

func (o dropColumn) render(d dialect) (string, error) { return d.dropColumn(o.table, o.column) }

func (o changeType) render(d dialect) (string, error) {
	return d.changeType(o.table, o.column, o.to)
}

// Describe implementations. One line each: what changed, named precisely
// enough that an error or a generated-file header carries its own context.
func (o createTable) describe() string { return "create table " + o.model.Table }

func (o dropTable) describe() string { return "drop table " + o.table }

func (o addColumn) describe() string {
	return fmt.Sprintf("add column %s.%s", o.table, o.field.Name)
}

func (o dropColumn) describe() string {
	return fmt.Sprintf("drop column %s.%s", o.table, o.column)
}

func (o changeType) describe() string {
	return fmt.Sprintf("change type of %s.%s", o.table, o.column)
}

func index(models []orm.Registered) map[string]orm.Registered {
	out := make(map[string]orm.Registered, len(models))
	for _, reg := range models {
		out[reg.Model.Table] = reg
	}
	return out
}

func indexFields(fields []orm.Field) map[string]orm.Field {
	out := make(map[string]orm.Field, len(fields))
	for _, f := range fields {
		out[f.Name] = f
	}
	return out
}

// columnList renders a CREATE TABLE body — two-space indented columns in the
// model's declared order, primary key inline. Shared by both dialects because
// layout is not a dialect decision: identical structure across databases is
// what keeps generated files diffable when a project changes driver.
func columnList(typeOf func(orm.Field) (string, error), m orm.Model) (string, error) {
	lines := make([]string, 0, len(m.Fields))
	for _, f := range m.Fields {
		typ, err := typeOf(f)
		if err != nil {
			return "", fmt.Errorf("table %s, field %s: %w", m.Table, f.Name, err)
		}
		line := "  " + f.Name + " " + typ
		if f.PrimaryKey {
			line += " PRIMARY KEY"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, ",\n"), nil
}
