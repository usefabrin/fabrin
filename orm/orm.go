// Package orm holds Fabrin's model metadata: what a table is called, what
// columns it has, and which module declared it.
//
// # Why Fabrin owns this rather than reading the ORM's
//
// The admin, forms, and the migration generator all read metadata from here. If
// they read GORM's instead, the admin would BE GORM-shaped, and swapping the ORM
// would mean rewriting the admin, the forms, and the generator. One layer of
// indirection now buys the ability to be wrong about the ORM later.
//
// # What is not here
//
// A database handle. This package describes models; it opens nothing, holds no
// connection, and does not import database/sql. That is what lets the admin and
// forms read a schema without a database running, and what makes every test in
// here run in microseconds.
//
// The query API is database/sql, or whichever ORM the application chose. Fabrin
// names neither: a module that needs data declares the interface it needs in its
// own package and receives an implementation, exactly as it would for any other
// dependency. See docs/adr/0002-database-sql-is-the-orm-seam.md.
//
// # Metadata is data, not an interface
//
// [Model] is a struct rather than an interface a user's type implements. The
// consumer of this package in F2 is the migration generator, which wants a
// description and nothing else — and a description does not need a zero value of
// the user's type to be constructed in order to answer questions about it.
//
// The cost is that nothing here links a table back to a Go type or provides a
// generic CRUD seam. Admin will need both. Model is already public, so changing
// its representation — including adding fields that break unkeyed literals — is
// a pre-v0 API decision, not something future code may assume is free.
package orm

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Type is a column's type, in Fabrin's own vocabulary.
//
// Deliberately not a driver's type, and not Go's reflect.Kind. A driver's would
// pin the metadata to one database; reflect.Kind cannot tell a timestamp from an
// int64. This is the smallest set the migration generator needs, and it may grow
// — a new constant is additive, where a changed meaning would not be.
type Type string

// The column types. The zero Type is invalid on purpose: a field whose Type was
// forgotten must be an error rather than silently becoming whichever constant
// happens to sort first.
const (
	String Type = "string"
	Int    Type = "int"
	Int64  Type = "int64"
	Float  Type = "float"
	Bool   Type = "bool"
	Time   Type = "time"
	Bytes  Type = "bytes"
)

// Valid reports whether t is one of the declared types.
func (t Type) Valid() bool {
	switch t {
	case String, Int, Int64, Float, Bool, Time, Bytes:
		return true
	}
	return false
}

// Field is one column.
//
// Deliberately minimal: every field is a public representation promise. Adding a
// field can break unkeyed literals even though removing or retyping one is more
// obviously breaking.
type Field struct {
	// Name is the column name, as it appears in SQL.
	Name string

	// Type is the column type. The zero value is invalid.
	Type Type

	// MaxLen bounds a String column. Zero means unbounded. It is an error on any
	// other type — a length on an integer is a misunderstanding that every
	// dialect would either ignore or refuse.
	MaxLen int

	// Nullable, Unique, and Index are provisional metadata. The current migration
	// engine does not consume them; their DDL and composite-constraint semantics
	// must be decided before the metadata API is frozen.
	Nullable   bool
	PrimaryKey bool
	Unique     bool
	Index      bool
}

// Model is one table.
type Model struct {
	// Table is the table name, and the identity a schema conflict is reported
	// against.
	Table string

	// Fields are the columns, in the order the generator will emit them. That
	// order is preserved exactly as declared — see [Registry.Models].
	Fields []Field
}

// Registered is a model together with the module that declared it.
//
// The module is not decoration: a schema conflict has to name who declared each
// side, and registration is the only moment that is known for free.
type Registered struct {
	Module string
	Model  Model
}

// Errors returned by [Registry.Register]. Sentinels so a caller can branch with
// errors.Is rather than matching message text.
var (
	// ErrDuplicateTable means two models claim one table name.
	ErrDuplicateTable = errors.New("orm: duplicate table")

	// ErrInvalidModel means a model could not describe a table — no name, no
	// fields, or no primary key.
	ErrInvalidModel = errors.New("orm: invalid model")

	// ErrInvalidField means a column could not be emitted or diffed.
	ErrInvalidField = errors.New("orm: invalid field")
)

// Registry is the collected schema of an application.
//
// Not safe for concurrent use, and it does not need to be: fabrin.New builds it
// once from the mounted modules that implement Modeler, and everything afterwards
// reads it. A mutex would buy nothing against that pattern and would imply the
// schema can change while an application runs, which it cannot — which is also
// why fabrin.App hands out the models rather than this type.
type Registry struct {
	byTable map[string]Registered
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byTable: map[string]Registered{}}
}

// Register adds a module's models, rejecting anything the migration generator
// could not act on.
//
// Everything here fails at registration rather than at DDL time. The generator
// runs against a real database, and a mystery there is a mystery in the most
// expensive available place.
func (r *Registry) Register(module string, models ...Model) error {
	for _, m := range models {
		if err := validate(m); err != nil {
			return err
		}
		if prev, dup := r.byTable[m.Table]; dup {
			// Both sides, because "duplicate table" alone means grepping every
			// module for the name.
			return fmt.Errorf("%w: %q declared by module %q and module %q",
				ErrDuplicateTable, m.Table, prev.Module, module)
		}
		r.byTable[m.Table] = Registered{Module: module, Model: clone(m)}
	}
	return nil
}

// Models returns every registered model, sorted by table name.
//
// Sorted rather than in registration order: registration follows module order,
// which follows FABRIN_MODULES and the argument order in main — neither of which
// should reach a generated migration. Sorting makes the generator's output a
// function of the schema alone.
//
// The result is a deep copy. This registry is read by the migration generator,
// the admin, and forms; a caller that mutated what it was handed would change
// the schema every later reader sees, with nothing in between to blame.
func (r *Registry) Models() []Registered {
	out := make([]Registered, 0, len(r.byTable))
	for _, reg := range r.byTable {
		out = append(out, Registered{Module: reg.Module, Model: clone(reg.Model)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model.Table < out[j].Model.Table })
	return out
}

func clone(m Model) Model {
	return Model{Table: m.Table, Fields: slices.Clone(m.Fields)}
}

func validate(m Model) error {
	if strings.TrimSpace(m.Table) == "" {
		return fmt.Errorf("%w: a model with no table name has nowhere for DDL to point", ErrInvalidModel)
	}
	if len(m.Fields) == 0 {
		return fmt.Errorf("%w: table %q declares no fields", ErrInvalidModel, m.Table)
	}

	seen := make(map[string]bool, len(m.Fields))
	primaries := 0

	for i, f := range m.Fields {
		switch {
		case strings.TrimSpace(f.Name) == "":
			return fmt.Errorf("%w: table %q, field %d has no name — it could be neither emitted nor diffed", ErrInvalidField, m.Table, i)
		case !f.Type.Valid():
			return fmt.Errorf("%w: table %q, field %q has type %q, which is not one of Fabrin's column types", ErrInvalidField, m.Table, f.Name, f.Type)
		case seen[f.Name]:
			return fmt.Errorf("%w: table %q declares field %q twice — the second would shadow the first in every diff", ErrInvalidField, m.Table, f.Name)
		case f.MaxLen != 0 && f.Type != String:
			return fmt.Errorf("%w: table %q, field %q is %s with MaxLen %d — a length applies only to %s", ErrInvalidField, m.Table, f.Name, f.Type, f.MaxLen, String)
		case f.MaxLen < 0:
			return fmt.Errorf("%w: table %q, field %q has a negative MaxLen", ErrInvalidField, m.Table, f.Name)
		}

		seen[f.Name] = true
		if f.PrimaryKey {
			primaries++
		}
	}

	// Without one, an update or delete generated later cannot address a single
	// row, and the migration engine has no stable way to talk about the table's
	// contents. Composite keys are not supported yet, and saying so is better
	// than accepting two and emitting DDL for one.
	if primaries == 0 {
		return fmt.Errorf("%w: table %q has no primary key", ErrInvalidModel, m.Table)
	}
	if primaries > 1 {
		return fmt.Errorf("%w: table %q declares %d primary keys — composite keys are not supported yet", ErrInvalidModel, m.Table, primaries)
	}
	return nil
}
