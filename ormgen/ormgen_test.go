package ormgen_test

import (
	"bytes"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin/orm"
	"github.com/usefabrin/fabrin/ormgen"
)

func TestGenerate_ProducesDeterministicCompilingPostgreSQLCreateAndGet(t *testing.T) {
	t.Parallel()

	models := []orm.Model{{
		GoName: "Order",
		Table:  "order",
		Fields: []orm.Field{
			{GoName: "ID", Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{GoName: "Reference", Name: "reference", Type: orm.TypeString, MaxLen: 32},
			{GoName: "Quantity", Name: "quantity", Type: orm.TypeInt},
			{GoName: "Total", Name: "total", Type: orm.TypeFloat},
			{GoName: "Active", Name: "active", Type: orm.TypeBool},
			{GoName: "Payload", Name: "payload", Type: orm.TypeBytes, Nullable: true},
			{GoName: "Note", Name: "note", Type: orm.TypeString, Nullable: true},
			{GoName: "ShippedAt", Name: "shipped_at", Type: orm.TypeTime, Nullable: true},
		},
	}}

	first, err := ormgen.Generate("orderdb", models)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := ormgen.Generate("orderdb", models)
	if err != nil {
		t.Fatalf("Generate again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the same declaration generated different bytes")
	}
	fixture, err := os.ReadFile("../internal/ormgentest/orderdb/orderdb_gen.go")
	if err != nil {
		t.Fatalf("read generated integration fixture: %v", err)
	}
	if !bytes.Equal(first, fixture) {
		t.Fatal("the compiled integration fixture is stale; regenerate orderdb_gen.go")
	}

	source := string(first)
	for _, want := range []string{
		"type Order struct",
		"const createOrder = " + strconv.Quote(`INSERT INTO "order" ("id", "reference", "quantity", "total", "active", "payload", "note", "shipped_at") VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`),
		"const getOrder = " + strconv.Quote(`SELECT "id", "reference", "quantity", "total", "active", "payload", "note", "shipped_at" FROM "order" WHERE "id" = $1`),
		"func (q *Queries) CreateOrder(ctx context.Context, row *Order) error",
		"func (q *Queries) GetOrder(ctx context.Context, id int64) (*Order, error)",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated source does not contain %q:\n%s", want, source)
		}
	}
	for _, want := range []string{
		`\bID\s+int64\b`, `\bQuantity\s+int\b`, `\bTotal\s+float64\b`,
		`\bActive\s+bool\b`, `\bPayload\s+\[\]byte\b`, `\bNote\s+\*string\b`,
		`\bShippedAt\s+\*time\.Time\b`,
	} {
		if !regexp.MustCompile(want).MatchString(source) {
			t.Errorf("generated source does not match %q:\n%s", want, source)
		}
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "orderdb_gen.go", first, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, source)
	}
	if _, err := (&types.Config{Importer: importer.Default()}).Check("orderdb", fset, []*ast.File{file}, nil); err != nil {
		t.Fatalf("generated source does not compile: %v\n%s", err, source)
	}
}

func TestGenerate_RejectsInvalidOrCollidingNamesBeforeOutput(t *testing.T) {
	t.Parallel()

	valid := orm.Model{
		GoName: "Order",
		Table:  "orders",
		Fields: []orm.Field{{GoName: "ID", Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	}
	tests := []struct {
		name        string
		packageName string
		models      []orm.Model
		want        string
	}{
		{name: "bad package", packageName: "order-db", models: []orm.Model{valid}, want: "order-db"},
		{name: "no models", packageName: "orderdb", want: "no models"},
		{name: "missing model Go name", packageName: "orderdb", models: []orm.Model{{Table: valid.Table, Fields: valid.Fields}}, want: "model Go name"},
		{name: "generated type collision", packageName: "orderdb", models: []orm.Model{{GoName: "Queries", Table: valid.Table, Fields: valid.Fields}}, want: "collides"},
		{name: "generated constructor collision", packageName: "orderdb", models: []orm.Model{{GoName: "New", Table: valid.Table, Fields: valid.Fields}}, want: "collides"},
		{name: "duplicate model Go name", packageName: "orderdb", models: []orm.Model{valid, {GoName: "Order", Table: "archived_orders", Fields: valid.Fields}}, want: "appears twice"},
		{name: "duplicate SQL table", packageName: "orderdb", models: []orm.Model{valid, {GoName: "ArchivedOrder", Table: valid.Table, Fields: valid.Fields}}, want: "duplicate table"},
		{name: "bad table", packageName: "orderdb", models: []orm.Model{{GoName: valid.GoName, Table: "order-items", Fields: valid.Fields}}, want: "order-items"},
		{name: "missing field Go name", packageName: "orderdb", models: []orm.Model{{GoName: valid.GoName, Table: valid.Table, Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}}}, want: "field Go name"},
		{name: "duplicate field Go name", packageName: "orderdb", models: []orm.Model{{GoName: valid.GoName, Table: valid.Table, Fields: []orm.Field{{GoName: "ID", Name: "id", Type: orm.TypeInt64, PrimaryKey: true}, {GoName: "ID", Name: "legacy_id", Type: orm.TypeInt64}}}}, want: "appears twice"},
		{name: "duplicate SQL column", packageName: "orderdb", models: []orm.Model{{GoName: valid.GoName, Table: valid.Table, Fields: []orm.Field{{GoName: "ID", Name: "id", Type: orm.TypeInt64, PrimaryKey: true}, {GoName: "LegacyID", Name: "id", Type: orm.TypeInt64}}}}, want: "twice"},
		{name: "bad column", packageName: "orderdb", models: []orm.Model{{GoName: valid.GoName, Table: valid.Table, Fields: []orm.Field{{GoName: "ID", Name: "Order ID", Type: orm.TypeInt64, PrimaryKey: true}}}}, want: "Order ID"},
		{name: "composite primary key", packageName: "orderdb", models: []orm.Model{{GoName: valid.GoName, Table: valid.Table, Fields: []orm.Field{{GoName: "TenantID", Name: "tenant_id", Type: orm.TypeInt64, PrimaryKey: true}, {GoName: "ID", Name: "id", Type: orm.TypeInt64, PrimaryKey: true}}}}, want: "2 primary keys"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ormgen.Generate(tt.packageName, tt.models)
			if err == nil {
				t.Fatalf("Generate returned %d bytes, want an error", len(got))
			}
			if !errors.Is(err, ormgen.ErrInvalidSchema) {
				t.Errorf("error = %v, want ErrInvalidSchema", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
