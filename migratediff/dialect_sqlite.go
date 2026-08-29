package migratediff

import (
	"fmt"
	"strconv"

	"github.com/usefabrin/fabrin/orm"
)

// SQLite renders for SQLite, which is how the migration gate stays
// hermetic: it runs in CI with no server, so every Dialect claim is testable
// against a real database rather than asserted.
type SQLite struct{}

func (SQLite) Name() string { return "SQLite" }

func (d SQLite) Render(op Operation) ([]string, error) {
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

func (SQLite) createTable(m orm.Model) (string, error) {
	body, err := columnList(sqliteType, m)
	if err != nil {
		return "", err
	}
	return "CREATE TABLE " + quoteIdentifier(m.Table) + " (\n" + body + "\n)", nil
}

// Adding is the one alteration SQLite supports in place. This decision slice
// still emits every added column as nullable; ADR 0006's following wiring slice
// adds the stated refusal required for NOT NULL columns without a default.
func (SQLite) addColumn(table string, f orm.Field) (string, error) {
	typ, err := sqliteType(f)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + quoteIdentifier(table) + " ADD COLUMN " + quoteIdentifier(f.Name) + " " + typ, nil
}

// DropColumn refuses. SQLite cannot drop a column in place; the honest options
// are the documented table-rebuild dance or a stated refusal, and this
// iteration states the refusal — from render, before anything executes, never
// halfway through a live migration. The rebuild dance lands when a command
// exists to carry its extra statements; one-statement-per-Operation would have
// to be relaxed deliberately for it.
func (SQLite) dropColumn(_, _ string) (string, error) {
	return "", fmt.Errorf("%w: %s cannot drop a column in place; the table-rebuild dance is not implemented yet",
		ErrUnsupported, SQLite{}.Name())
}

// ChangeType refuses, for the same reason DropColumn does: altering a column's
// type in place is outside what SQLite accepts.
func (SQLite) changeType(_, _ string, _ orm.Field) (string, error) {
	return "", fmt.Errorf("%w: %s cannot alter a column's type in place; the table-rebuild dance is not implemented yet",
		ErrUnsupported, SQLite{}.Name())
}

func (SQLite) dropTable(table string) (string, error) {
	return "-- fabrin: dropping " + strconv.Quote(table) + " discards its data\nDROP TABLE " + quoteIdentifier(table), nil
}

// sqliteType maps Fabrin's vocabulary to SQLite declarations. SQLite stores
// these through type affinity, so VARCHAR(32) holds text of any length — the
// length is recorded for the schema's own documentation, exactly as the
// metadata records it.
func sqliteType(f orm.Field) (string, error) {
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
		return "REAL", nil
	case orm.Bool:
		return "BOOLEAN", nil
	case orm.Time:
		return "TIMESTAMP", nil
	case orm.Bytes:
		return "BLOB", nil
	default:
		return "", fmt.Errorf("%w: SQLite has no rendering for type %q", orm.ErrInvalidField, f.Type)
	}
}
