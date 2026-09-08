// Package schema generates typed PostgreSQL access from explicit Go declarations.
// Generation is offline: it neither opens a database nor applies migrations.
// The API is a developer preview pending the review described in ADR 0007.
package schema

import (
	"fmt"
	"go/format"
	"go/token"
	"regexp"
	"slices"
	"strings"

	"github.com/usefabrin/fabrin/migratediff"
	"github.com/usefabrin/fabrin/orm"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

// Field is an immutable column declaration. Construct one with a type function.
type Field struct {
	name, kind        string
	primary, nullable bool
	maxLen            int
}

// String declares a PostgreSQL text column.
func String(name string) Field { return Field{name: name, kind: "String"} }

// Int64 declares a PostgreSQL bigint column.
func Int64(name string) Field { return Field{name: name, kind: "Int64"} }

// Bool declares a PostgreSQL boolean column.
func Bool(name string) Field { return Field{name: name, kind: "Bool"} }

// Time declares a PostgreSQL timestamp without time zone, matching orm.Time.
func Time(name string) Field { return Field{name: name, kind: "Time"} }

// PrimaryKey identifies the single, caller-supplied key. It cannot be nullable.
func (f Field) PrimaryKey() Field { f.primary = true; return f }

// Nullable opts a field out of NOT NULL; generated values use sql.Null* types.
func (f Field) Nullable() Field { f.nullable = true; return f }

// MaxLen bounds a string in PostgreSQL characters. Zero means unbounded.
func (f Field) MaxLen(n int) Field { f.maxLen = n; return f }

// Model describes one generated Go record and its database table.
type Model struct {
	name, table string
	fields      []Field
}

// New validates a model. Names use exported Go identifiers and lowercase SQL
// snake_case respectively. PostgreSQL identifiers are limited to 63 ASCII bytes.
func New(name, table string, fields ...Field) (Model, error) {
	m := Model{name: name, table: table, fields: slices.Clone(fields)}
	if err := m.validate(); err != nil {
		return Model{}, err
	}
	return m, nil
}

func (m Model) validate() error {
	if !token.IsIdentifier(m.name) || !token.IsExported(m.name) || !identifier.MatchString(m.table) || len(m.table) > 63 || len(m.fields) == 0 {
		return fmt.Errorf("schema: invalid model %q / table %q", m.name, m.table)
	}
	names := map[string]bool{}
	keys := 0
	for _, f := range m.fields {
		if !identifier.MatchString(f.name) || len(f.name) > 63 || names[goName(f.name)] {
			return fmt.Errorf("schema: invalid or duplicate field %q", f.name)
		}
		names[goName(f.name)] = true
		switch f.kind {
		case "String", "Int64", "Bool", "Time":
		default:
			return fmt.Errorf("schema: field %q has no supported type", f.name)
		}
		if f.maxLen < 0 || (f.maxLen != 0 && f.kind != "String") {
			return fmt.Errorf("schema: invalid length on %q", f.name)
		}
		if f.primary {
			keys++
			if f.nullable {
				return fmt.Errorf("schema: nullable primary key %q", f.name)
			}
		}
	}
	if keys != 1 {
		return fmt.Errorf("schema: %q needs exactly one primary key", m.name)
	}
	return nil
}

func goName(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "id" {
			parts[i] = "ID"
		} else {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
func quote(s string) string { return `"` + s + `"` }
func (f Field) goType() string {
	if f.nullable {
		return "sql.Null" + f.kind
	}
	switch f.kind {
	case "String":
		return "string"
	case "Int64":
		return "int64"
	case "Bool":
		return "bool"
	default:
		return "time.Time"
	}
}
func (m Model) metadata() orm.Model {
	fields := make([]orm.Field, 0, len(m.fields))
	for _, f := range m.fields {
		fields = append(fields, orm.Field{Name: f.name, Type: orm.Type(strings.ToLower(f.kind)), MaxLen: f.maxLen, PrimaryKey: f.primary, Nullable: f.nullable})
	}
	return orm.Model{Table: m.table, Fields: fields}
}

// Generate returns formatted source for all models in one package. It rejects
// naming collisions before returning any output. Write the result to a .go file
// in a package dedicated to generated records; it cannot inspect other files.
func Generate(pkg string, models ...Model) ([]byte, error) {
	reserved := map[string]bool{"DB": true, "context": true, "sql": true, "orm": true, "time": true, "fmt": true}
	if !token.IsIdentifier(pkg) || pkg == "_" || reserved[pkg] || len(models) == 0 {
		return nil, fmt.Errorf("schema: invalid package %q or empty model list", pkg)
	}
	tables := map[string]bool{}
	for _, m := range models {
		if err := m.validate(); err != nil {
			return nil, err
		}
		if tables[m.table] {
			return nil, fmt.Errorf("schema: duplicate table %q", m.table)
		}
		tables[m.table] = true
		for _, symbol := range []string{m.name, m.name + "Store", "New" + m.name + "Store", m.name + "Model", m.name + "CreateTableSQL"} {
			if reserved[symbol] {
				return nil, fmt.Errorf("schema: generated name collision %q", symbol)
			}
			reserved[symbol] = true
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by Fabrin schema. DO NOT EDIT.\npackage %s\n\nimport (\n\"context\"\n\"database/sql\"\n\"fmt\"\n\"github.com/usefabrin/fabrin/orm\"\n", pkg)
	needsTime := false
	for _, m := range models {
		for _, f := range m.fields {
			needsTime = needsTime || f.kind == "Time" && !f.nullable
		}
	}
	if needsTime {
		b.WriteString("\"time\"\n")
	}
	b.WriteString(")\n\n")
	b.WriteString("// DB is satisfied by *sql.DB and *sql.Tx. The caller owns its lifecycle.\ntype DB interface {\nExecContext(context.Context,string,...any)(sql.Result,error)\nQueryRowContext(context.Context,string,...any)*sql.Row\n}\n\n")
	b.WriteString("func validateDB(db DB) error {\ninvalid := db == nil\nswitch v := db.(type) {\ncase *sql.DB: invalid = v == nil\ncase *sql.Tx: invalid = v == nil\ncase *sql.Conn: invalid = v == nil\n}\nif invalid { return fmt.Errorf(\"database handle must not be nil\") }; return nil\n}\n")
	for _, m := range models {
		if err := m.generate(&b); err != nil {
			return nil, err
		}
	}
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("schema: format generated code: %w", err)
	}
	return out, nil
}

func (m Model) generate(b *strings.Builder) error {
	ddl, err := (migratediff.Postgres{}).Render(migratediff.CreateTable{Model: m.metadata()})
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "// %s is a row in %s.\ntype %s struct {\n", m.name, m.table, m.name)
	cols, args, scans, params := []string{}, []string{}, []string{}, []string{}
	var key Field
	for i, f := range m.fields {
		fmt.Fprintf(b, "%s %s `json:%q`\n", goName(f.name), f.goType(), f.name)
		cols = append(cols, quote(f.name))
		args = append(args, "record."+goName(f.name))
		scans = append(scans, "&record."+goName(f.name))
		params = append(params, fmt.Sprintf("$%d", i+1))
		if f.primary {
			key = f
		}
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "// %sCreateTableSQL creates the initial table; execute only in a migration.\nconst %sCreateTableSQL = %q\n\n", m.name, m.name, strings.Join(ddl, ";\n"))
	fmt.Fprintf(b, "// %sModel returns fresh migration metadata.\nfunc %sModel() orm.Model { return orm.Model{Table:%q,Fields:[]orm.Field{\n", m.name, m.name, m.table)
	for _, f := range m.fields {
		fmt.Fprintf(b, "{Name:%q,Type:orm.%s,MaxLen:%d,PrimaryKey:%t,Nullable:%t},\n", f.name, f.kind, f.maxLen, f.primary, f.nullable)
	}
	b.WriteString("}}}\n\n")
	fmt.Fprintf(b, "// %sStore provides typed access; it does not authorize callers.\ntype %sStore struct { db DB }\n", m.name, m.name)
	fmt.Fprintf(b, "// New%sStore validates and binds caller-owned database access.\nfunc New%sStore(db DB) (%sStore,error) { if err:=validateDB(db);err!=nil {return %sStore{},err}; return %sStore{db:db},nil }\n", m.name, m.name, m.name, m.name, m.name)
	fmt.Fprintf(b, "// Create inserts a record with a caller-supplied primary key.\nfunc (s %sStore) Create(ctx context.Context, record %s) error {\n_,err:=s.db.ExecContext(ctx,%q,%s)\nif err!=nil {return fmt.Errorf(%q,err)}\nreturn nil\n}\n", m.name, m.name, "INSERT INTO "+quote(m.table)+" ("+strings.Join(cols, ", ")+") VALUES ("+strings.Join(params, ", ")+")", strings.Join(args, ","), "create "+m.table+": %w")
	fmt.Fprintf(b, "// Get finds a record by key; errors.Is(err, sql.ErrNoRows) identifies absence.\nfunc (s %sStore) Get(ctx context.Context, id %s) (%s,error) {\nvar record %s\nerr:=s.db.QueryRowContext(ctx,%q,id).Scan(%s)\nif err!=nil {return %s{},fmt.Errorf(%q,err)}\nreturn record,nil\n}\n\n", m.name, key.goType(), m.name, m.name, "SELECT "+strings.Join(cols, ", ")+" FROM "+quote(m.table)+" WHERE "+quote(key.name)+" = $1", strings.Join(scans, ","), m.name, "get "+m.table+": %w")
	return nil
}
