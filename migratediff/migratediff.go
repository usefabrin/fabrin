// Package migratediff diffs recorded model state against current model state
// and renders the difference as SQL through a per-database Dialect.
//
// This is the seam makemigrations will stand on: the differ produces a
// deterministic list of operations, and a Dialect turns operations into the
// statements a real server accepts.
//
// # Public seam
//
// The differ began as a private proof and graduated when makemigrations became
// its first real consumer. Operations describe schema intent; Dialect owns the
// SQL representation; Apply preflights all rendering before it executes the
// first statement. The exported shapes are intentionally small because each one
// is part of Fabrin's compatibility promise.
//
// It lives at the repository root rather than under internal/ because the
// boundary rules forbid internal/ from importing sibling Fabrin packages, and
// this package's whole job is reading orm metadata.
//
// # What the differ can and cannot see
//
// Nullability is temporarily invisible because the newly decided Field flags
// remain withheld from recorded state until ADR 0006's versioned codec and
// operation wiring land together. A changed nullability therefore produces no
// Operation in this decision slice.
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

// ErrUnsupported means a Dialect cannot express an Operation at all — SQLite
// altering or dropping a column in place, most notably. Callers get this
// BEFORE anything executes, from render, never halfway through a migration.
var ErrUnsupported = errors.New("migratediff: unsupported Operation")

// Dialect renders operations as the SQL one database family accepts.
//
// Two dialects ship from the start — SQLite, which runs hermetically in CI,
// and PostgreSQL, which is what people deploy. Having two from the beginning
// is what stops the interface being accidentally shaped around one.
type Dialect interface {
	// name identifies the Dialect in errors and generated-file headers.
	Name() string

	CreateTable(m orm.Model) (string, error)
	AddColumn(table string, f orm.Field) (string, error)
	DropColumn(table, column string) (string, error)
	ChangeType(table, column string, to orm.Field) (string, error)
	DropTable(table string) (string, error)
}

// Operation is one change between two states, rendered into SQL by a Dialect.
// Each Operation is exactly one statement; when a future Operation needs many
// (a table rebuild), that changes deliberately, not by accident of signature.
type Operation interface {
	Render(d Dialect) (string, error)

	// describe names the change in one line, for errors and for the header of
	// the migration file that carries it.
	Describe() string
}

// CreateTable brings a new table into being, columns in declared order.
type CreateTable struct {
	Model orm.Model
}

// DropTable removes a table and everything in it. Data loss, stated in the SQL.
type DropTable struct {
	Table string
}

// AddColumn appends one column to an existing table.
type AddColumn struct {
	Table string
	Field orm.Field
}

// DropColumn removes one column and its data. The rendered statement carries
// the warning as a SQL comment, because that is the line a reviewer must not
// skim past.
type DropColumn struct {
	Table  string
	Column string
}

// ChangeType moves a column to a new type or length. What the server does with
// the existing values is the server's semantics; the Operation states the
// destination, not a promise about conversion.
type ChangeType struct {
	Table  string
	Column string
	To     orm.Field
}

// diff compares two recorded states and returns the operations that turn
// before into after, in the order they should run.
//
// Both snapshots normally come from [orm.Snapshot.Models]; they are read-only
// inputs here. Fields are compared BY NAME — a pure reorder of the declared
// field list produces nothing, because DDL cannot reorder columns without
// rebuilding the table and destroying data over a cosmetic diff.
func Diff(before, after orm.Snapshot) []Operation {
	beforeByTable := index(before.Models())
	afterModels := after.Models()
	afterByTable := index(afterModels)

	var ops []Operation

	// New tables, sorted.
	for _, reg := range afterModels {
		if _, exists := beforeByTable[reg.Model.Table]; !exists {
			ops = append(ops, CreateTable{Model: reg.Model})
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
			op     Operation
		}
		var alters []ranked
		add := func(column string, rank int, op Operation) {
			alters = append(alters, ranked{column: column, rank: rank, op: op})
		}
		beforeFields := indexFields(was.Model.Fields)
		for _, f := range reg.Model.Fields {
			prev, exists := beforeFields[f.Name]
			switch {
			case !exists:
				add(f.Name, 2, AddColumn{Table: reg.Model.Table, Field: f})
			case prev.Type != f.Type || prev.MaxLen != f.MaxLen:
				add(f.Name, 1, ChangeType{Table: reg.Model.Table, Column: f.Name, To: f})
			}
		}
		afterFields := indexFields(reg.Model.Fields)
		for _, f := range was.Model.Fields {
			if _, exists := afterFields[f.Name]; !exists {
				add(f.Name, 0, DropColumn{Table: reg.Model.Table, Column: f.Name})
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
			ops = append(ops, DropTable{Table: reg.Model.Table})
		}
	}

	return ops
}

// apply renders every Operation through d and executes it, in order, stopping
// at the first failure. A convenience for tests and for whatever command
// eventually runs generated migrations; anything transactional around it is
// the caller's business, exactly as it is for migrate.Run.
func Apply(ctx context.Context, db *sql.DB, d Dialect, ops []Operation) error {
	type renderedOperation struct {
		op   Operation
		stmt string
	}

	rendered := make([]renderedOperation, 0, len(ops))
	for _, op := range ops {
		stmt, err := op.Render(d)
		if err != nil {
			return fmt.Errorf("%s: %w", op.Describe(), err)
		}
		rendered = append(rendered, renderedOperation{op: op, stmt: stmt})
	}

	for _, item := range rendered {
		if _, err := db.ExecContext(ctx, item.stmt); err != nil {
			return fmt.Errorf("%s: %w", item.op.Describe(), err)
		}
	}
	return nil
}

// render implementations. Each Operation knows how to hand itself to a
// Dialect; the Dialect owns the SQL, the Operation owns the intent. Adding a
// Dialect means implementing the Dialect interface once — not touching every
// Operation.
func (o CreateTable) Render(d Dialect) (string, error) { return d.CreateTable(o.Model) }

func (o DropTable) Render(d Dialect) (string, error) { return d.DropTable(o.Table) }

func (o AddColumn) Render(d Dialect) (string, error) { return d.AddColumn(o.Table, o.Field) }

func (o DropColumn) Render(d Dialect) (string, error) { return d.DropColumn(o.Table, o.Column) }

func (o ChangeType) Render(d Dialect) (string, error) {
	return d.ChangeType(o.Table, o.Column, o.To)
}

// Describe implementations. One line each: what changed, named precisely
// enough that an error or a generated-file header carries its own context.
func (o CreateTable) Describe() string { return "create table " + o.Model.Table }

func (o DropTable) Describe() string { return "drop table " + o.Table }

func (o AddColumn) Describe() string {
	return fmt.Sprintf("add column %s.%s", o.Table, o.Field.Name)
}

func (o DropColumn) Describe() string {
	return fmt.Sprintf("drop column %s.%s", o.Table, o.Column)
}

func (o ChangeType) Describe() string {
	return fmt.Sprintf("change type of %s.%s", o.Table, o.Column)
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
// layout is not a Dialect decision: identical structure across databases is
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
