package migratediff

import (
	"fmt"
	"strconv"

	"github.com/usefabrin/fabrin/orm"
)

// Postgres renders for PostgreSQL. It is the Dialect people deploy,
// which is why it exists alongside SQLite from the first commit: one Dialect
// is how an interface gets shaped around a single server without anyone
// noticing.
type Postgres struct{}

func (Postgres) Name() string { return "PostgreSQL" }

func (d Postgres) Render(op Operation) ([]string, error) {
	switch o := op.(type) {
	case CreateTable:
		return oneStatement(d.createTable(o.Model))
	case *CreateTable:
		if o != nil {
			return oneStatement(d.createTable(o.Model))
		}
	case DropTable:
		return oneStatement(d.dropTable(o.Table))
	case *DropTable:
		if o != nil {
			return oneStatement(d.dropTable(o.Table))
		}
	case AddColumn:
		return oneStatement(d.addColumn(o.Table, o.Field))
	case *AddColumn:
		if o != nil {
			return oneStatement(d.addColumn(o.Table, o.Field))
		}
	case DropColumn:
		return oneStatement(d.dropColumn(o.Table, o.Column))
	case *DropColumn:
		if o != nil {
			return oneStatement(d.dropColumn(o.Table, o.Column))
		}
	case ChangeType:
		return oneStatement(d.changeType(o.Table, o.Column, o.To))
	case *ChangeType:
		if o != nil {
			return oneStatement(d.changeType(o.Table, o.Column, o.To))
		}
	}
	return nil, fmt.Errorf("%w: %s cannot render %T", ErrUnsupported, d.Name(), op)
}

func (Postgres) createTable(m orm.Model) (string, error) {
	body, err := columnList(postgresType, m)
	if err != nil {
		return "", err
	}
	return "CREATE TABLE " + quoteIdentifier(m.Table) + " (\n" + body + "\n)", nil
}

func (Postgres) addColumn(table string, f orm.Field) (string, error) {
	typ, err := postgresType(f)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + quoteIdentifier(table) + " ADD COLUMN " + quoteIdentifier(f.Name) + " " + typ, nil
}

// The data-loss warning rides in the SQL itself, where the generated migration
// file will carry it.
// The data-loss warning rides in the SQL itself, where the generated migration
// file will carry it.
func (Postgres) dropColumn(table, column string) (string, error) {
	return "-- fabrin: dropping " + strconv.Quote(table+"."+column) +
		" discards its data\nALTER TABLE " + quoteIdentifier(table) + " DROP COLUMN " + quoteIdentifier(column), nil
}

func (Postgres) changeType(table, column string, to orm.Field) (string, error) {
	typ, err := postgresType(to)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + quoteIdentifier(table) + " ALTER COLUMN " + quoteIdentifier(column) + " TYPE " + typ, nil
}

func (Postgres) dropTable(table string) (string, error) {
	return "-- fabrin: dropping " + strconv.Quote(table) + " discards its data\nDROP TABLE " + quoteIdentifier(table), nil
}

// postgresType maps Fabrin's type vocabulary to PostgreSQL's.
//
// Nullability is deliberately absent in this decision slice. ADR 0006 has
// decided the semantics, but state, differ, and both dialects switch together
// in the following slice so no half-wired migration can be generated.
func postgresType(f orm.Field) (string, error) {
	switch f.Type {
	case orm.String:
		if f.MaxLen > 0 {
			return fmt.Sprintf("VARCHAR(%d)", f.MaxLen), nil
		}
		return "TEXT", nil
	case orm.Int:
		return "INTEGER", nil
	case orm.Int64:
		return "BIGINT", nil
	case orm.Float:
		return "DOUBLE PRECISION", nil
	case orm.Bool:
		return "BOOLEAN", nil
	case orm.Time:
		return "TIMESTAMP", nil
	case orm.Bytes:
		return "BYTEA", nil
	default:
		return "", fmt.Errorf("%w: PostgreSQL has no rendering for type %q", orm.ErrInvalidField, f.Type)
	}
}
