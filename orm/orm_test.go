package orm_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin/orm"
)

func order() orm.Model {
	return orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.Int64, PrimaryKey: true},
			{Name: "reference", Type: orm.String, MaxLen: 32, Unique: true},
			{Name: "shipped_at", Type: orm.Time, Nullable: true},
		},
	}
}

func TestRegistry_CollectsModelsWithTheModuleThatDeclaredThem(t *testing.T) {
	t.Parallel()

	// The owning module is not decoration: a schema conflict has to name who
	// declared each side, and the admin will group by it. Recording it at
	// registration is the only moment it is known for free.
	r := orm.NewRegistry()
	if err := r.Register("shop", order()); err != nil {
		t.Fatalf("Register: %v", err)
	}

	models := r.Models()
	if len(models) != 1 {
		t.Fatalf("Models() = %d entries, want 1", len(models))
	}
	if models[0].Module != "shop" {
		t.Errorf("module = %q, want shop", models[0].Module)
	}
	if models[0].Model.Table != "orders" {
		t.Errorf("table = %q, want orders", models[0].Model.Table)
	}
}

func TestRegistry_RejectsTwoModelsClaimingOneTable(t *testing.T) {
	t.Parallel()

	// Two modules writing one table is either a mistake or a coupling nobody
	// declared. Either way the migration generator would emit a diff against a
	// schema that is the union of two intentions, and the error must name both
	// sides — "duplicate table" alone means grepping every module.
	r := orm.NewRegistry()
	if err := r.Register("shop", order()); err != nil {
		t.Fatalf("Register: %v", err)
	}

	err := r.Register("billing", orm.Model{
		Table:  "orders",
		Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}},
	})
	if err == nil {
		t.Fatal("a second model claiming one table must be rejected")
	}
	if !errors.Is(err, orm.ErrDuplicateTable) {
		t.Errorf("callers branch with errors.Is, got %v", err)
	}
	for _, want := range []string{"orders", "shop", "billing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q, got: %v", want, err)
		}
	}
}

func TestRegistry_RejectsAModelWithNothingToMigrate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model orm.Model
		want  error
		why   string
	}{
		{
			name:  "no table name",
			model: orm.Model{Fields: []orm.Field{{Name: "id", Type: orm.Int64}}},
			want:  orm.ErrInvalidModel,
			why:   "DDL has nowhere to point",
		},
		{
			name:  "no fields",
			model: orm.Model{Table: "empty"},
			want:  orm.ErrInvalidModel,
			why:   "a table with no columns is not a table",
		},
		{
			name:  "field with no name",
			model: orm.Model{Table: "orders", Fields: []orm.Field{{Type: orm.Int64}}},
			want:  orm.ErrInvalidField,
			why:   "an anonymous column cannot be emitted or diffed",
		},
		{
			name:  "field with no type",
			model: orm.Model{Table: "orders", Fields: []orm.Field{{Name: "id"}}},
			want:  orm.ErrInvalidField,
			why:   "the zero Type is not a type, and would silently pick one",
		},
		{
			name: "two fields with one name",
			model: orm.Model{Table: "orders", Fields: []orm.Field{
				{Name: "id", Type: orm.Int64},
				{Name: "id", Type: orm.String},
			}},
			want: orm.ErrInvalidField,
			why:  "the second would silently shadow the first in every diff",
		},
		{
			name: "negative MaxLen on a string",
			model: orm.Model{Table: "orders", Fields: []orm.Field{
				{Name: "reference", Type: orm.String, MaxLen: -1},
			}},
			want: orm.ErrInvalidField,
			why:  "a negative length is not a bound — no dialect would accept it",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// At registration, not at DDL time: the generator runs against a real
			// database, and a mystery there is a mystery in the worst place.
			err := orm.NewRegistry().Register("shop", tc.model)
			if err == nil {
				t.Fatalf("must be rejected — %s", tc.why)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want it to wrap %v", err, tc.want)
			}
		})
	}
}

func TestRegistry_RejectsAModelWithNoPrimaryKey(t *testing.T) {
	t.Parallel()

	// Not style. Without one, an update or delete generated later cannot address
	// a single row, and the migration engine has no stable way to talk about the
	// table's contents.
	err := orm.NewRegistry().Register("shop", orm.Model{
		Table:  "orders",
		Fields: []orm.Field{{Name: "reference", Type: orm.String}},
	})
	if err == nil {
		t.Fatal("a model with no primary key must be rejected")
	}
	if !strings.Contains(err.Error(), "orders") {
		t.Errorf("the error must name the table, got: %v", err)
	}
}

func TestRegistry_KeepsFieldOrderAsDeclared(t *testing.T) {
	t.Parallel()

	// The generator emits columns from this order. Anything unstable — a map, a
	// sort by name — produces a spurious diff on a project nobody changed, which
	// is how a generator earns a reputation for lying.
	r := orm.NewRegistry()
	if err := r.Register("shop", order()); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got := make([]string, 0, 3)
	for _, f := range r.Models()[0].Model.Fields {
		got = append(got, f.Name)
	}
	if strings.Join(got, ",") != "id,reference,shipped_at" {
		t.Errorf("field order = %v, want the declared order", got)
	}
}

func TestRegistry_ModelsIsOrderedByTableRatherThanRegistration(t *testing.T) {
	t.Parallel()

	// Registration order follows module registration, which follows FABRIN_MODULES
	// and the argument order in main — neither of which should reach a generated
	// migration. Sorting by table makes the output a function of the schema alone.
	r := orm.NewRegistry()
	for _, table := range []string{"shipments", "orders", "customers"} {
		m := orm.Model{Table: table, Fields: []orm.Field{{Name: "id", Type: orm.Int64, PrimaryKey: true}}}
		if err := r.Register("shop", m); err != nil {
			t.Fatalf("Register(%s): %v", table, err)
		}
	}

	got := make([]string, 0, 3)
	for _, m := range r.Models() {
		got = append(got, m.Model.Table)
	}
	if strings.Join(got, ",") != "customers,orders,shipments" {
		t.Errorf("Models() = %v, want them sorted by table", got)
	}
}

func TestRegistry_ModelsReturnsACopy(t *testing.T) {
	t.Parallel()

	// The registry is read by the migration generator, the admin, and forms. A
	// caller that mutates what it was handed would change the schema every later
	// reader sees, with nothing in between to blame.
	r := orm.NewRegistry()
	if err := r.Register("shop", order()); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got := r.Models()
	got[0].Model.Table = "vandalised"
	got[0].Model.Fields[0].Name = "also vandalised"

	again := r.Models()
	if again[0].Model.Table != "orders" {
		t.Errorf("the registry's table was mutated through Models(): %q", again[0].Model.Table)
	}
	if again[0].Model.Fields[0].Name != "id" {
		t.Errorf("the registry's fields were mutated through Models(): %q", again[0].Model.Fields[0].Name)
	}
}

func TestRegistry_RejectsMaxLenOnSomethingThatIsNotAString(t *testing.T) {
	t.Parallel()

	// A length on an integer is a misunderstanding, and every dialect would either
	// ignore it or refuse. Saying so here is cheaper than either.
	err := orm.NewRegistry().Register("shop", orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.Int64, PrimaryKey: true, MaxLen: 11},
		},
	})
	if err == nil {
		t.Fatal("MaxLen on a non-string field must be rejected")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Errorf("the error must name the field, got: %v", err)
	}
}

func TestField_TypesAreFabrinsOwn(t *testing.T) {
	t.Parallel()

	// The whole reason the registry exists: the admin, forms, and the migration
	// generator read FABRIN metadata, so swapping the ORM does not rewrite them.
	// A driver's type here would undo that, and would need an apicheck allowlist
	// entry besides. See docs/adr/0002-database-sql-is-the-orm-seam.md.
	for _, ty := range []orm.Type{
		orm.String, orm.Int, orm.Int64, orm.Float, orm.Bool, orm.Time, orm.Bytes,
	} {
		if ty == "" {
			t.Error("a declared type must not be the zero value, which Register rejects")
		}
	}

	// The zero value has to stay invalid, or a forgotten Type silently becomes
	// whichever constant happens to be first.
	var zero orm.Type
	if zero.Valid() {
		t.Error("the zero Type must not be valid")
	}
}
