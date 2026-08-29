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
// # Constraint vocabulary
//
// ADR 0006's nullability, primary-key, unique, and plain-index semantics are
// first-class operations. They are compared independently from type and length,
// so changing several properties of one field cannot mask another change.
//
// # Ordering is part of the output
//
// Creates first (tables sorted), then alterations per surviving table with
// dependent objects dropped before columns or types change and constraints
// restored afterwards, then whole-table drops last. Fixed once, here, because
// the generated migration inherits whatever wiggle this allows: a generator
// whose output depends on map iteration produces migrations that differ between
// runs and reviews terribly.
package migratediff

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	// Name identifies the Dialect in errors and generated-file headers.
	Name() string

	// Render translates one schema intent into the ordered SQL statements it
	// requires. One method is deliberate: adding an Operation must not break
	// every third-party Dialect implementation at compile time.
	Render(Operation) ([]string, error)
}

// Operation is one change between two states. A Dialect owns its SQL rendering;
// one operation may require several ordered statements.
type Operation interface {
	// Describe names the change in one line, for errors and for the header of
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

// ChangeNullability moves one column between NOT NULL and nullable.
type ChangeNullability struct {
	Table    string
	Column   string
	Nullable bool
}

// AddUnique adds the named single-column UNIQUE constraint ADR 0006 defines.
type AddUnique struct {
	Table  string
	Column string
}

// DropUnique removes that UNIQUE constraint.
type DropUnique struct {
	Table  string
	Column string
}

// AddIndex creates the named plain single-column index ADR 0006 defines.
type AddIndex struct {
	Table  string
	Column string
}

// DropIndex removes that plain index.
type DropIndex struct {
	Table  string
	Column string
}

// AddPrimaryKey adds a named single-column primary-key constraint.
type AddPrimaryKey struct {
	Table  string
	Column string
}

// DropPrimaryKey removes that primary-key constraint.
type DropPrimaryKey struct {
	Table  string
	Column string
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

	// Alterations to surviving tables: table-sorted, then dependency rank, then
	// column. Constraints and indexes drop before the columns they depend on;
	// type/nullability changes happen in the middle; additions land last.
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
			if !exists {
				add(f.Name, 6, AddColumn{Table: reg.Model.Table, Field: f})
				continue
			}
			if prev.Index && !f.Index {
				add(f.Name, 0, DropIndex{Table: reg.Model.Table, Column: f.Name})
			}
			if prev.Unique && !f.Unique {
				add(f.Name, 1, DropUnique{Table: reg.Model.Table, Column: f.Name})
			}
			if prev.PrimaryKey && !f.PrimaryKey {
				add(f.Name, 2, DropPrimaryKey{Table: reg.Model.Table, Column: f.Name})
			}
			if prev.Type != f.Type || prev.MaxLen != f.MaxLen {
				add(f.Name, 4, ChangeType{Table: reg.Model.Table, Column: f.Name, To: f})
			}
			if prev.Nullable != f.Nullable {
				add(f.Name, 5, ChangeNullability{Table: reg.Model.Table, Column: f.Name, Nullable: f.Nullable})
			}
			if !prev.PrimaryKey && f.PrimaryKey {
				add(f.Name, 7, AddPrimaryKey{Table: reg.Model.Table, Column: f.Name})
			}
			if !prev.Unique && f.Unique {
				add(f.Name, 8, AddUnique{Table: reg.Model.Table, Column: f.Name})
			}
			if !prev.Index && f.Index {
				add(f.Name, 9, AddIndex{Table: reg.Model.Table, Column: f.Name})
			}
		}
		afterFields := indexFields(reg.Model.Fields)
		for _, f := range was.Model.Fields {
			if _, exists := afterFields[f.Name]; !exists {
				add(f.Name, 3, DropColumn{Table: reg.Model.Table, Column: f.Name})
			}
		}
		slices.SortFunc(alters, func(a, b ranked) int {
			switch {
			case a.rank < b.rank:
				return -1
			case a.rank > b.rank:
				return 1
			case a.column != b.column:
				return strings.Compare(a.column, b.column)
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
		op    Operation
		stmts []string
	}

	rendered := make([]renderedOperation, 0, len(ops))
	for _, op := range ops {
		stmts, err := d.Render(op)
		if err != nil {
			return fmt.Errorf("%s: %w", op.Describe(), err)
		}
		if len(stmts) == 0 {
			return fmt.Errorf("%s: %w: %s rendered no statements", op.Describe(), ErrUnsupported, d.Name())
		}
		for _, stmt := range stmts {
			if strings.TrimSpace(stmt) == "" {
				return fmt.Errorf("%s: %w: %s rendered an empty statement", op.Describe(), ErrUnsupported, d.Name())
			}
		}
		rendered = append(rendered, renderedOperation{op: op, stmts: stmts})
	}

	for _, item := range rendered {
		for _, stmt := range item.stmts {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", item.op.Describe(), err)
			}
		}
	}
	return nil
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

func (o ChangeNullability) Describe() string {
	return fmt.Sprintf("change nullability of %s.%s", o.Table, o.Column)
}

func (o AddUnique) Describe() string { return fmt.Sprintf("add unique %s.%s", o.Table, o.Column) }

func (o DropUnique) Describe() string {
	return fmt.Sprintf("drop unique %s.%s", o.Table, o.Column)
}

func (o AddIndex) Describe() string { return fmt.Sprintf("add index %s.%s", o.Table, o.Column) }

func (o DropIndex) Describe() string {
	return fmt.Sprintf("drop index %s.%s", o.Table, o.Column)
}

func (o AddPrimaryKey) Describe() string {
	return fmt.Sprintf("add primary key %s.%s", o.Table, o.Column)
}

func (o DropPrimaryKey) Describe() string {
	return fmt.Sprintf("drop primary key %s.%s", o.Table, o.Column)
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
// model's declared order followed by named primary-key and UNIQUE constraints.
// Shared by both dialects because layout is not a Dialect decision: identical
// structure across databases is what keeps generated files diffable when a
// project changes driver.
func columnList(typeOf func(orm.Field) (string, error), m orm.Model) (string, error) {
	lines := make([]string, 0, len(m.Fields)*2)
	for _, f := range m.Fields {
		line, err := columnDefinition(typeOf, f)
		if err != nil {
			return "", fmt.Errorf("table %s, field %s: %w", m.Table, f.Name, err)
		}
		lines = append(lines, "  "+line)
	}
	for _, f := range m.Fields {
		switch {
		case f.PrimaryKey:
			lines = append(lines, "  CONSTRAINT "+quoteIdentifier(databaseObjectName("pk", m.Table, f.Name))+" PRIMARY KEY ("+quoteIdentifier(f.Name)+")")
		case f.Unique:
			lines = append(lines, "  CONSTRAINT "+quoteIdentifier(databaseObjectName("uq", m.Table, f.Name))+" UNIQUE ("+quoteIdentifier(f.Name)+")")
		}
	}
	return strings.Join(lines, ",\n"), nil
}

func columnDefinition(typeOf func(orm.Field) (string, error), f orm.Field) (string, error) {
	typ, err := typeOf(f)
	if err != nil {
		return "", err
	}
	line := quoteIdentifier(f.Name) + " " + typ
	if !f.Nullable {
		line += " NOT NULL"
	}
	return line, nil
}

// quoteIdentifier quotes one SQL identifier using the ANSI form accepted by
// both PostgreSQL and SQLite. Metadata names are identifiers, never fragments;
// doubling an embedded quote preserves the literal name without granting it SQL
// syntax.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func oneStatement(stmt string, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	return []string{stmt}, nil
}

const maxDatabaseObjectNameBytes = 63

// databaseObjectName produces the stable object names ADR 0006 specifies. The
// digest disambiguates underscore joins and long readable prefixes; the result
// stays below PostgreSQL's silent 63-byte truncation boundary.
func databaseObjectName(kind, table, column string) string {
	payload := strconv.Itoa(len(kind)) + ":" + kind +
		strconv.Itoa(len(table)) + ":" + table +
		strconv.Itoa(len(column)) + ":" + column
	sum := sha256.Sum256([]byte(payload))
	digest := fmt.Sprintf("%x", sum[:8])
	readable := readableIdentifier(table + "_" + column)
	reserved := len(kind) + len(digest) + 2
	if max := maxDatabaseObjectNameBytes - reserved; len(readable) > max {
		readable = strings.Trim(readable[:max], "_")
	}
	if readable == "" {
		readable = "object"
	}
	return kind + "_" + readable + "_" + digest
}

func readableIdentifier(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			underscore = false
		case !underscore:
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
