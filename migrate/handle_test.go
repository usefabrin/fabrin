package migrate_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/usefabrin/fabrin/migrate"
)

// The shape of the type users write their migrations against — MIG-010, and
// docs/adr/0003-migrations-take-a-handle-not-a-transaction.md.
//
// Read by reflection rather than asserted by a compile-time `var _ Handle =
// (*sql.Tx)(nil)`, because the property that matters here is a NEGATIVE: the
// method set is four and can never grow. Users will implement Handle with
// recording fakes in their own tests, so a fifth method breaks every one of them
// at once — and the fifth method someone would plausibly add is one *sql.Tx
// already has (Prepare, Query, Exec, Stmt). A compile-time satisfaction check
// keeps compiling when that lands, and so does every test in this package. Only
// reading the method set catches it, which is the same reason
// TestModules_NeverImportEachOther reads the import graph.
//
// api/fabrin.txt records the same set, but a snapshot diff is a thing a human
// reviews; this is a gate that fails with the reason attached.

const migratePkg = "github.com/usefabrin/fabrin/migrate"

func TestM_UpAndDownTakeAHandleRatherThanATransaction(t *testing.T) {
	t.Parallel()

	// *sql.Tx answers "may a migration run outside a transaction?" with no, by
	// construction and silently. CREATE INDEX CONCURRENTLY is the case that
	// forces the question — PostgreSQL refuses it inside a transaction block —
	// and Django ships Migration.atomic = False for exactly it. The engine still
	// passes a transaction today (MIG-001 is unchanged); what changes is that the
	// TYPE no longer promises one, so NonAtomic can land later with no edit to
	// anyone's migration.
	//
	// Up and Down are checked together and required to be identical: one helper
	// shape — func(context.Context, migrate.Handle) error — has to work in both
	// directions, or users' own migration helpers fork.
	up := migrationFunc(t, "Up")
	down := migrationFunc(t, "Down")

	if up != down {
		t.Errorf("M.Up is %s and M.Down is %s — they must be the same type, or a user's "+
			"helper cannot serve both directions", up, down)
	}

	for _, f := range []struct {
		field string
		typ   reflect.Type
	}{{"Up", up}, {"Down", down}} {
		if got, want := f.typ.In(0), reflect.TypeFor[context.Context](); got != want {
			t.Errorf("M.%s's first parameter is %s, want %s", f.field, got, want)
		}

		h := f.typ.In(1)
		if h.Kind() != reflect.Interface {
			t.Errorf("M.%s's second parameter is %s, want an interface — a concrete "+
				"transaction type forecloses non-transactional migrations, and exposes "+
				"Commit and Rollback inside a body whose bookkeeping row commits with it",
				f.field, h)
		}
		if h.Name() != "Handle" || h.PkgPath() != migratePkg {
			t.Errorf("M.%s's second parameter is %s (name %q, package %q), want the named "+
				"interface migrate.Handle — the repository names roles, and users spell "+
				"this type in every migration they write",
				f.field, h, h.Name(), h.PkgPath())
		}
	}
}

func TestHandle_MethodSetIsFrozenAtFourAndSatisfiedUnmodifiedByTxDBAndConn(t *testing.T) {
	t.Parallel()

	// The one genuinely irreversible part of #67. Users implement Handle in their
	// own fakes, so the set is decided once: a fifth method after v0.1 breaks all
	// of them, and a narrower set does not restrict anything — h.(*sql.Tx)
	// compiles either way, so omitting a method buys no safety and only forces an
	// uglier assertion.
	//
	// PrepareContext is in because a data migration backfilling a million rows
	// wants it, and it is the one excluded method a migration cannot synthesise
	// from the others. BeginTx is out because it is absent from *sql.Tx, so its
	// exclusion is forced rather than chosen.
	//
	// Satisfaction by *sql.Tx, *sql.DB and *sql.Conn with NO adapter is what makes
	// the change clean, and it is the property that would be lost first if a
	// signature drifted.
	h := handleType(t)

	want := map[string]reflect.Type{
		"ExecContext":     reflect.TypeFor[func(context.Context, string, ...any) (sql.Result, error)](),
		"QueryContext":    reflect.TypeFor[func(context.Context, string, ...any) (*sql.Rows, error)](),
		"QueryRowContext": reflect.TypeFor[func(context.Context, string, ...any) *sql.Row](),
		"PrepareContext":  reflect.TypeFor[func(context.Context, string) (*sql.Stmt, error)](),
	}

	for i := range h.NumMethod() {
		m := h.Method(i)
		w, frozen := want[m.Name]
		if !frozen {
			t.Errorf("Handle has a fifth method %s %s — the set is frozen at four "+
				"(ADR 0003), because every user fake implementing Handle breaks the day "+
				"it grows", m.Name, m.Type)
			continue
		}
		if m.Type != w {
			t.Errorf("Handle.%s is %s, want %s", m.Name, m.Type, w)
		}
		delete(want, m.Name)
	}
	for name, w := range want {
		t.Errorf("Handle is missing %s %s", name, w)
	}

	// Reported per type rather than in one line: which of the three fails says
	// which method drifted.
	for _, c := range []struct {
		name string
		typ  reflect.Type
	}{
		{"*sql.Tx", reflect.TypeFor[*sql.Tx]()},
		{"*sql.DB", reflect.TypeFor[*sql.DB]()},
		{"*sql.Conn", reflect.TypeFor[*sql.Conn]()},
	} {
		if !c.typ.Implements(h) {
			t.Errorf("%s does not satisfy migrate.Handle — the interface is worth having "+
				"only while the standard library's own handles satisfy it unmodified, with "+
				"no adapter for Fabrin to maintain", c.name)
		}
	}
}

// migrationFunc returns the declared type of M's named func field.
func migrationFunc(t *testing.T, field string) reflect.Type {
	t.Helper()

	f, ok := reflect.TypeFor[migrate.M]().FieldByName(field)
	if !ok {
		t.Fatalf("migrate.M has no %s field", field)
	}
	if f.Type.Kind() != reflect.Func || f.Type.NumIn() != 2 || f.Type.NumOut() != 1 {
		t.Fatalf("M.%s is %s, want func(context.Context, migrate.Handle) error", field, f.Type)
	}
	return f.Type
}

// handleType returns the interface M's bodies are written against.
func handleType(t *testing.T) reflect.Type {
	t.Helper()

	h := migrationFunc(t, "Up").In(1)
	if h.Kind() != reflect.Interface {
		t.Fatalf("M.Up's second parameter is %s, not an interface — migrate.Handle does not exist yet", h)
	}
	return h
}
