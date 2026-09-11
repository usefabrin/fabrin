package migratediff

import (
	"fmt"
	"strconv"
	"strings"

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
	case ChangeNullability:
		return []string{d.changeNullability(o.Table, o.Column, o.Nullable)}, nil
	case *ChangeNullability:
		if o != nil {
			return []string{d.changeNullability(o.Table, o.Column, o.Nullable)}, nil
		}
	case AddUnique:
		return []string{d.addConstraint("uq", o.Table, o.Column, "UNIQUE")}, nil
	case *AddUnique:
		if o != nil {
			return []string{d.addConstraint("uq", o.Table, o.Column, "UNIQUE")}, nil
		}
	case DropUnique:
		return []string{d.dropConstraint("uq", o.Table, o.Column)}, nil
	case *DropUnique:
		if o != nil {
			return []string{d.dropConstraint("uq", o.Table, o.Column)}, nil
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
	case AddPrimaryKey:
		return []string{d.addConstraint("pk", o.Table, o.Column, "PRIMARY KEY")}, nil
	case *AddPrimaryKey:
		if o != nil {
			return []string{d.addConstraint("pk", o.Table, o.Column, "PRIMARY KEY")}, nil
		}
	case DropPrimaryKey:
		return []string{d.dropPrimaryKey(o.Table, o.Column)}, nil
	case *DropPrimaryKey:
		if o != nil {
			return []string{d.dropPrimaryKey(o.Table, o.Column)}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s cannot render %T", ErrUnsupported, d.Name(), op)
}

func (Postgres) createTable(m orm.Model) ([]string, error) {
	body, err := columnList(postgresType, m)
	if err != nil {
		return nil, err
	}
	stmts := []string{"CREATE TABLE " + quoteIdentifier(m.Table) + " (\n" + body + "\n)"}
	for _, f := range m.Fields {
		if f.Index {
			stmts = append(stmts, Postgres{}.addIndex(m.Table, f.Name))
		}
	}
	return stmts, nil
}

func (Postgres) addColumn(table string, f orm.Field) ([]string, error) {
	definition, err := columnDefinition(postgresType, f)
	if err != nil {
		return nil, err
	}
	stmts := []string{"ALTER TABLE " + quoteIdentifier(table) + " ADD COLUMN " + definition}
	switch {
	case f.PrimaryKey:
		stmts = append(stmts, Postgres{}.addConstraint("pk", table, f.Name, "PRIMARY KEY"))
	case f.Unique:
		stmts = append(stmts, Postgres{}.addConstraint("uq", table, f.Name, "UNIQUE"))
	case f.Index:
		stmts = append(stmts, Postgres{}.addIndex(table, f.Name))
	}
	return stmts, nil
}

// The data-loss warning rides in the SQL itself, where the generated migration
// file will carry it.
func (Postgres) dropColumn(table, column string) (string, error) {
	return "-- fabrin: dropping " + strconv.Quote(table+"."+column) +
		" discards its data\nALTER TABLE " + quoteIdentifier(table) + " DROP COLUMN " + quoteIdentifier(column), nil
}

func (Postgres) renameColumn(table, from, to string) string {
	return "ALTER TABLE " + quoteIdentifier(table) + " RENAME COLUMN " + quoteIdentifier(from) + " TO " + quoteIdentifier(to)
}

func (Postgres) changeType(table, column string, to orm.Field) (string, error) {
	typ, err := postgresType(to)
	if err != nil {
		return "", err
	}
	return "ALTER TABLE " + quoteIdentifier(table) + " ALTER COLUMN " + quoteIdentifier(column) + " TYPE " + typ, nil
}

func (Postgres) changeNullability(table, column string, nullable bool) string {
	action := "SET NOT NULL"
	if nullable {
		action = "DROP NOT NULL"
	}
	return "ALTER TABLE " + quoteIdentifier(table) + " ALTER COLUMN " + quoteIdentifier(column) + " " + action
}

func (Postgres) addConstraint(kind, table, column, constraint string) string {
	name := databaseObjectName(kind, table, column)
	return "ALTER TABLE " + quoteIdentifier(table) + " ADD CONSTRAINT " + quoteIdentifier(name) + " " + constraint + " (" + quoteIdentifier(column) + ")"
}

func (Postgres) dropConstraint(kind, table, column string) string {
	name := databaseObjectName(kind, table, column)
	return "ALTER TABLE " + quoteIdentifier(table) + " DROP CONSTRAINT " + quoteIdentifier(name)
}

// dropPrimaryKey resolves the constraint by table, type, and column instead of
// assuming its generated name. State recorded before ADR 0006 describes the
// old inline primary key accurately, but PostgreSQL assigned that constraint
// its own name. Catalog identity lets the first later key change reverse both
// legacy and newly named schemas without risking an unrelated constraint.
func (Postgres) dropPrimaryKey(table, column string) string {
	tableLiteral := quoteLiteral(table)
	columnLiteral := quoteLiteral(column)
	tag := dollarQuoteTag(table, column)
	return "DO " + tag + "\n" +
		"DECLARE\n" +
		"  constraint_name text;\n" +
		"BEGIN\n" +
		"  SELECT c.conname INTO constraint_name\n" +
		"  FROM pg_constraint AS c\n" +
		"  JOIN pg_attribute AS a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]\n" +
		"  WHERE c.conrelid = to_regclass(format('%I', " + tableLiteral + "))\n" +
		"    AND c.contype = 'p'\n" +
		"    AND array_length(c.conkey, 1) = 1\n" +
		"    AND a.attname = " + columnLiteral + ";\n" +
		"  IF constraint_name IS NULL THEN\n" +
		"    RAISE EXCEPTION 'fabrin: table % has no single-column primary key on %', " + tableLiteral + ", " + columnLiteral + ";\n" +
		"  END IF;\n" +
		"  EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', " + tableLiteral + ", constraint_name);\n" +
		"END;\n" +
		tag
}

func (Postgres) addIndex(table, column string) string {
	name := databaseObjectName("idx", table, column)
	return "CREATE INDEX " + quoteIdentifier(name) + " ON " + quoteIdentifier(table) + " (" + quoteIdentifier(column) + ")"
}

func (Postgres) dropIndex(table, column string) string {
	return "DROP INDEX " + quoteIdentifier(databaseObjectName("idx", table, column))
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func dollarQuoteTag(values ...string) string {
	name := "fabrin"
	for {
		tag := "$" + name + "$"
		collides := false
		for _, value := range values {
			if strings.Contains(value, tag) {
				collides = true
				break
			}
		}
		if !collides {
			return tag
		}
		name += "_"
	}
}

func (Postgres) dropTable(table string) (string, error) {
	return "-- fabrin: dropping " + strconv.Quote(table) + " discards its data\nDROP TABLE " + quoteIdentifier(table), nil
}

// postgresType maps Fabrin's type vocabulary to PostgreSQL's.
//
// Constraint semantics are rendered by the operation handlers above; this
// helper maps only Fabrin's type vocabulary.
func postgresType(f orm.Field) (string, error) {
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
		return "DOUBLE PRECISION", nil
	case orm.TypeBool:
		return "BOOLEAN", nil
	case orm.TypeTime:
		return "TIMESTAMP", nil
	case orm.TypeBytes:
		return "BYTEA", nil
	default:
		return "", fmt.Errorf("%w: PostgreSQL has no rendering for type %q", orm.ErrInvalidField, f.Type)
	}
}
