package migratediff

import (
	"fmt"

	"github.com/usefabrin/fabrin/orm"
)

// sqliteDialect renders for SQLite, which is how the migration gate stays
// hermetic: it runs in CI with no server, so every dialect claim is testable
// against a real database rather than asserted.
type sqliteDialect struct{}

func (sqliteDialect) name() string { return "SQLite" }

func (sqliteDialect) createTable(m orm.Model) (string, error) {
	body, err := columnList(sqliteType, m)
	if err != nil {
		return "", err
	}
	return "CREATE TABLE " + m.Table + " (\n" + body + "\n)", nil
}

// Adding is the one alteration SQLite supports in place, and only for nullable
// columns — which, until the provisional flags are decided (#79), is every
// column this package emits.
func (sqliteDialect) addColumn(table string, f orm.Field) (string, error) {
	typ, err := sqliteType(f)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + table + " ADD COLUMN " + f.Name + " " + typ, nil
}

// dropColumn refuses. SQLite cannot drop a column in place; the honest options
// are the documented table-rebuild dance or a stated refusal, and this
// iteration states the refusal — from render, before anything executes, never
// halfway through a live migration. The rebuild dance lands when a command
// exists to carry its extra statements; one-statement-per-operation would have
// to be relaxed deliberately for it.
func (sqliteDialect) dropColumn(_, _ string) (string, error) {
	return "", fmt.Errorf("%w: %s cannot drop a column in place; the table-rebuild dance is not implemented yet",
		errUnsupported, sqliteDialect{}.name())
}

// changeType refuses, for the same reason dropColumn does: altering a column's
// type in place is outside what SQLite accepts.
func (sqliteDialect) changeType(_, _ string, to orm.Field) (string, error) {
	return "", fmt.Errorf("%w: %s cannot alter a column's type in place; the table-rebuild dance is not implemented yet",
		errUnsupported, sqliteDialect{}.name())
}

func (sqliteDialect) dropTable(table string) (string, error) {
	return "-- fabrin: dropping " + table + " discards its data\nDROP TABLE " + table, nil
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
