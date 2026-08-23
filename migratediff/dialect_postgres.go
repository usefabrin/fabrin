package migratediff

import (
	"fmt"

	"github.com/usefabrin/fabrin/orm"
)

// Postgres renders for PostgreSQL. It is the Dialect people deploy,
// which is why it exists alongside SQLite from the first commit: one Dialect
// is how an interface gets shaped around a single server without anyone
// noticing.
type Postgres struct{}

func (Postgres) Name() string { return "PostgreSQL" }

func (Postgres) CreateTable(m orm.Model) (string, error) {
	body, err := columnList(postgresType, m)
	if err != nil {
		return "", err
	}
	return "CREATE TABLE " + m.Table + " (\n" + body + "\n)", nil
}

func (Postgres) AddColumn(table string, f orm.Field) (string, error) {
	typ, err := postgresType(f)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + table + " ADD COLUMN " + f.Name + " " + typ, nil
}

// The data-loss warning rides in the SQL itself, where the generated migration
// file will carry it.
// The data-loss warning rides in the SQL itself, where the generated migration
// file will carry it.
func (Postgres) DropColumn(table, column string) (string, error) {
	return "-- fabrin: dropping " + table + "." + column +
		" discards its data\nALTER TABLE " + table + " DROP COLUMN " + column, nil
}

func (Postgres) ChangeType(table, column string, to orm.Field) (string, error) {
	typ, err := postgresType(to)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + table + " ALTER COLUMN " + column + " TYPE " + typ, nil
}

func (Postgres) ChangeNullability(table, column string, to orm.Field) (string, error) {
	if to.Nullable {
		return "ALTER TABLE " + table + " ALTER COLUMN " + column + " DROP NOT NULL", nil
	}
	return "ALTER TABLE " + table + " ALTER COLUMN " + column + " SET NOT NULL", nil
}

func (Postgres) CreateIndex(table, column string) (string, error) {
	return "CREATE INDEX idx_" + table + "_" + column + " ON " + table + " (" + column + ")", nil
}

func (Postgres) DropIndex(table, column string) (string, error) {
	return "-- fabrin: dropping idx_" + table + "_" + column + "\nDROP INDEX idx_" + table + "_" + column, nil
}

func (Postgres) DropTable(table string) (string, error) {
	return "-- fabrin: dropping " + table + " discards its data\nDROP TABLE " + table, nil
}

// postgresType maps Fabrin's type vocabulary to PostgreSQL's.
//
// Nullability is deliberately absent: the provisional Field flags have no
// agreed semantics yet (#79), so every column this renders is nullable until
// that decision lands. Emitting NOT NULL now would be a guess written into
// users' schema.
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
