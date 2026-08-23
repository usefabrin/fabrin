package migratediff

import (
	"fmt"

	"github.com/usefabrin/fabrin/orm"
)

// SQLite renders for SQLite, which is how the migration gate stays
// hermetic: it runs in CI with no server, so every Dialect claim is testable
// against a real database rather than asserted.
type SQLite struct{}

func (SQLite) Name() string { return "SQLite" }

func (SQLite) CreateTable(m orm.Model) (string, error) {
	body, err := columnList(sqliteType, m)
	if err != nil {
		return "", err
	}
	return "CREATE TABLE " + m.Table + " (\n" + body + "\n)", nil
}

// Adding is the one alteration SQLite supports in place, and only for nullable
// columns — which, until the provisional flags are decided (#79), is every
// column this package emits.
// Adding a NOT NULL column requires a DEFAULT clause in SQLite, and the
// metadata has no default concept yet — so the stated refusal covers this case
// too rather than emitting SQL that fails halfway through a live migration.
func (SQLite) AddColumn(table string, f orm.Field) (string, error) {
	if !f.Nullable && !f.PrimaryKey {
		return "", fmt.Errorf("%w: %s cannot ADD COLUMN without a DEFAULT when the column is NOT NULL — give the column Nullable or hand-write the migration with its DEFAULT", ErrUnsupported, SQLite{}.Name())
	}
	typ, err := sqliteType(f)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + table + " ADD COLUMN " + f.Name + " " + typ, nil
}

// DropColumn refuses. SQLite cannot drop a column in place; the honest options
// are the documented table-rebuild dance or a stated refusal, and this
// iteration states the refusal — from render, before anything executes, never
// halfway through a live migration. The rebuild dance lands when a command
// exists to carry its extra statements; one-statement-per-Operation would have
// to be relaxed deliberately for it.
func (SQLite) DropColumn(_, _ string) (string, error) {
	return "", fmt.Errorf("%w: %s cannot drop a column in place; the table-rebuild dance is not implemented yet",
		ErrUnsupported, SQLite{}.Name())
}

// ChangeType refuses, for the same reason DropColumn does: altering a column's
// type in place is outside what SQLite accepts.
func (SQLite) ChangeType(_, _ string, to orm.Field) (string, error) {
	return "", fmt.Errorf("%w: %s cannot alter a column's type in place; the table-rebuild dance is not implemented yet",
		ErrUnsupported, SQLite{}.Name())
}

func (SQLite) ChangeNullability(_, _ string, _ orm.Field) (string, error) {
	return "", fmt.Errorf("%w: %s cannot alter a column's nullability in place; the table-rebuild dance is not implemented yet",
		ErrUnsupported, SQLite{}.Name())
}

func (SQLite) CreateIndex(table, column string) (string, error) {
	return "CREATE INDEX idx_" + table + "_" + column + " ON " + table + " (" + column + ")", nil
}

func (SQLite) DropIndex(table, column string) (string, error) {
	return "-- fabrin: dropping idx_" + table + "_" + column + "\nDROP INDEX idx_" + table + "_" + column, nil
}

func (SQLite) DropTable(table string) (string, error) {
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
