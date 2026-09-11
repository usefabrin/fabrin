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
		return d.createTable(o.Model)
	case *CreateTable:
		if o != nil {
			return d.createTable(o.Model)
		}
	case DropTable:
		return oneStatement(d.dropTable(o.Table))
	case *DropTable:
		if o != nil {
			return oneStatement(d.dropTable(o.Table))
		}
	case AddColumn:
		return d.addColumn(o.Table, o.Field)
	case *AddColumn:
		if o != nil {
			return d.addColumn(o.Table, o.Field)
		}
	case DropColumn:
		return oneStatement(d.dropColumn(o.Table, o.Column))
	case *DropColumn:
		if o != nil {
			return oneStatement(d.dropColumn(o.Table, o.Column))
		}
	case RenameColumn:
		return []string{d.renameColumn(o.Table, o.From, o.To)}, nil
	case *RenameColumn:
		if o != nil {
			return []string{d.renameColumn(o.Table, o.From, o.To)}, nil
		}
	case ChangeType:
		return oneStatement(d.changeType(o.Table, o.Column, o.To))
	case *ChangeType:
		if o != nil {
			return oneStatement(d.changeType(o.Table, o.Column, o.To))
		}
	case AddIndex:
		return []string{d.addIndex(o.Table, o.Column)}, nil
	case *AddIndex:
		if o != nil {
			return []string{d.addIndex(o.Table, o.Column)}, nil
		}
	case DropIndex:
		return []string{d.dropIndex(o.Table, o.Column)}, nil
	case *DropIndex:
		if o != nil {
			return []string{d.dropIndex(o.Table, o.Column)}, nil
		}
	case ChangeNullability, AddUnique, DropUnique, AddPrimaryKey, DropPrimaryKey:
		return nil, fmt.Errorf("%w: %s cannot alter table constraints in place; the table-rebuild dance is not implemented yet", ErrUnsupported, d.Name())
	case *ChangeNullability, *AddUnique, *DropUnique, *AddPrimaryKey, *DropPrimaryKey:
		return nil, fmt.Errorf("%w: %s cannot alter table constraints in place; the table-rebuild dance is not implemented yet", ErrUnsupported, d.Name())
	}
	return nil, fmt.Errorf("%w: %s cannot render %T", ErrUnsupported, d.Name(), op)
}

func (SQLite) createTable(m orm.Model) ([]string, error) {
	body, err := columnList(sqliteType, m)
	if err != nil {
		return nil, err
	}
	stmts := []string{"CREATE TABLE " + quoteIdentifier(m.Table) + " (\n" + body + "\n)"}
	for _, f := range m.Fields {
		if f.Index {
			stmts = append(stmts, SQLite{}.addIndex(m.Table, f.Name))
		}
	}
	return stmts, nil
}

// Adding a nullable column is one alteration SQLite supports in place. Fabrin
// has no default-value metadata yet, so a NOT NULL addition is refused rather
// than rendered into a statement that fails as soon as the table has a row.
func (SQLite) addColumn(table string, f orm.Field) ([]string, error) {
	if !f.Nullable {
		return nil, fmt.Errorf("%w: %s cannot ADD COLUMN without a DEFAULT when the column is NOT NULL — make it Nullable or hand-write the migration with its DEFAULT", ErrUnsupported, SQLite{}.Name())
	}
	if f.PrimaryKey || f.Unique {
		return nil, fmt.Errorf("%w: %s cannot ADD COLUMN with a primary-key or UNIQUE constraint; the table-rebuild dance is not implemented yet", ErrUnsupported, SQLite{}.Name())
	}
	definition, err := columnDefinition(sqliteType, f)
	if err != nil {
		return nil, err
	}
	stmts := []string{"ALTER TABLE " + quoteIdentifier(table) + " ADD COLUMN " + definition}
	if f.Index {
		stmts = append(stmts, SQLite{}.addIndex(table, f.Name))
	}
	return stmts, nil
}

// DropColumn refuses. SQLite cannot drop a column in place; the honest options
// are the documented table-rebuild dance or a stated refusal, and this
// iteration states the refusal — from render, before anything executes, never
// halfway through a live migration. The stable Dialect seam can carry the
// rebuild's statement list when that implementation lands.
func (SQLite) dropColumn(_, _ string) (string, error) {
	return "", fmt.Errorf("%w: %s cannot drop a column in place; the table-rebuild dance is not implemented yet",
		ErrUnsupported, SQLite{}.Name())
}

func (SQLite) renameColumn(table, from, to string) string {
	return "ALTER TABLE " + quoteIdentifier(table) + " RENAME COLUMN " + quoteIdentifier(from) + " TO " + quoteIdentifier(to)
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

func (SQLite) addIndex(table, column string) string {
	name := databaseObjectName("idx", table, column)
	return "CREATE INDEX " + quoteIdentifier(name) + " ON " + quoteIdentifier(table) + " (" + quoteIdentifier(column) + ")"
}

func (SQLite) dropIndex(table, column string) string {
	return "DROP INDEX " + quoteIdentifier(databaseObjectName("idx", table, column))
}

// sqliteType maps Fabrin's vocabulary to SQLite declarations. SQLite stores
// these through type affinity, so VARCHAR(32) holds text of any length — the
// length is recorded for the schema's own documentation, exactly as the
// metadata records it.
func sqliteType(f orm.Field) (string, error) {
	switch f.Type {
	case orm.TypeString:
		if f.MaxLen > 0 {
			return fmt.Sprintf("VARCHAR(%d)", f.MaxLen), nil
		}
		return "TEXT", nil
	case orm.TypeInt:
		return "INTEGER", nil
	case orm.TypeInt64:
		return "BIGINT", nil
	case orm.TypeFloat:
		return "REAL", nil
	case orm.TypeBool:
		return "BOOLEAN", nil
	case orm.TypeTime:
		return "TIMESTAMP", nil
	case orm.TypeBytes:
		return "BLOB", nil
	default:
		return "", fmt.Errorf("%w: SQLite has no rendering for type %q", orm.ErrInvalidField, f.Type)
	}
}
