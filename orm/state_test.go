package orm_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin/orm"
)

// shopRegistry builds a registry with two modules and three tables, registered
// in an order that is deliberately not sorted by table.
func shopRegistry() *orm.Registry {
	r := orm.NewRegistry()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(r.Register("shop", order()))
	must(r.Register("shop", orm.Model{
		Table:  "shipments",
		Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	}))
	must(r.Register("billing", orm.Model{
		Table: "invoices",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "total", Type: orm.TypeFloat},
		},
	}))
	return r
}

func TestSnapshot_RoundTripsThroughEncodeAndParse(t *testing.T) {
	t.Parallel()

	// makemigrations diffs the live registry against the state reconstructed
	// from recorded migrations. Every step of state to bytes and back must
	// preserve the schema exactly, or the next generated migration diffs
	// against something the user never wrote.
	snap, err := orm.NewSnapshot(shopRegistry().Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	encoded, err := orm.EncodeSnapshot(snap)
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}

	parsed, err := orm.ParseSnapshot(encoded, "20260801120000_add_orders.state.json")
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}

	got, want := parsed.Models(), snap.Models()
	if len(got) != len(want) {
		t.Fatalf("parsed %d tables, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Module != want[i].Module || got[i].Model.Table != want[i].Model.Table {
			t.Errorf("entry %d = {%s %s}, want {%s %s}", i,
				got[i].Module, got[i].Model.Table, want[i].Module, want[i].Model.Table)
			continue
		}
		if len(got[i].Model.Fields) != len(want[i].Model.Fields) {
			t.Errorf("table %s: %d fields, want %d",
				want[i].Model.Table, len(got[i].Model.Fields), len(want[i].Model.Fields))
			continue
		}
		for j, f := range want[i].Model.Fields {
			g := got[i].Model.Fields[j]
			if g != f {
				t.Errorf("table %s field %d = %+v, want %+v", want[i].Model.Table, j, g, f)
			}
		}
	}
}

func TestSnapshot_EncodeIsDeterministic(t *testing.T) {
	t.Parallel()

	// Same inputs, same bytes. A generated migration that differs between two
	// runs on an unchanged project is a spurious diff nobody can review
	// honestly. Table order comes from the schema alone, never from
	// registration order or map iteration.
	first, err := orm.NewSnapshot(shopRegistry().Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	second, err := orm.NewSnapshot(shopRegistry().Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	a, err := orm.EncodeSnapshot(first)
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}
	b, err := orm.EncodeSnapshot(second)
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}
	if string(a) != string(b) {
		t.Error("two encodings of one schema differ; the output is not deterministic")
	}

	// Construction order must not reach the bytes either: the same models
	// handed over reversed still encode identically.
	models := shopRegistry().Models()
	for i, j := 0, len(models)-1; i < j; i, j = i+1, j-1 {
		models[i], models[j] = models[j], models[i]
	}
	reversed, err := orm.NewSnapshot(models)
	if err != nil {
		t.Fatalf("NewSnapshot(reversed): %v", err)
	}
	c, err := orm.EncodeSnapshot(reversed)
	if err != nil {
		t.Fatalf("EncodeSnapshot(reversed): %v", err)
	}
	if string(a) != string(c) {
		t.Error("construction order changed the encoded bytes")
	}
}

func TestSnapshot_KeepsFieldOrderAsDeclared(t *testing.T) {
	t.Parallel()

	// Column layout is the author's intent, the same rule Registry.Models
	// follows. Sorting fields by name would produce diffs on projects nobody
	// changed.
	snap, err := orm.NewSnapshot(shopRegistry().Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	for _, reg := range snap.Models() {
		if reg.Model.Table != "orders" {
			continue
		}
		names := make([]string, 0, 3)
		for _, f := range reg.Model.Fields {
			names = append(names, f.Name)
		}
		if strings.Join(names, ",") != "id,reference,shipped_at" {
			t.Errorf("field order = %v, want the declared order", names)
		}
		return
	}
	t.Fatal("orders not found in snapshot")
}

func TestSnapshot_EncodesConstraintFlagsInVersionedState(t *testing.T) {
	t.Parallel()

	// ADR 0006 decided Nullable, Unique and Index. Schemas differing only in
	// those flags must now encode differently, and the flags must survive a
	// round trip so the next diff sees the schema the migration actually made.
	withFlags := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, MaxLen: 32, Unique: true},
			{Name: "shipped_at", Type: orm.TypeTime, Nullable: true, Index: true},
		},
	}
	withoutFlags := orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, MaxLen: 32},
			{Name: "shipped_at", Type: orm.TypeTime},
		},
	}

	encode := func(m orm.Model) (string, orm.Snapshot) {
		t.Helper()
		r := orm.NewRegistry()
		if err := r.Register("shop", m); err != nil {
			t.Fatalf("Register: %v", err)
		}
		snap, err := orm.NewSnapshot(r.Models())
		if err != nil {
			t.Fatalf("NewSnapshot: %v", err)
		}
		b, err := orm.EncodeSnapshot(snap)
		if err != nil {
			t.Fatalf("EncodeSnapshot: %v", err)
		}
		return string(b), snap
	}

	withEncoded, withSnapshot := encode(withFlags)
	withoutEncoded, _ := encode(withoutFlags)
	if withEncoded == withoutEncoded {
		t.Error("constraint flags did not change the encoded bytes")
	}
	if !strings.Contains(withEncoded, `"v": 1`) {
		t.Errorf("encoded state has no version marker:\n%s", withEncoded)
	}

	parsed, err := orm.ParseSnapshot([]byte(withEncoded), "flags.state.json")
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}
	got := parsed.Models()[0].Model.Fields
	want := withSnapshot.Models()[0].Model.Fields
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseSnapshot_DecodesLegacyConstraintSemantics(t *testing.T) {
	t.Parallel()

	// Unversioned state was written while the generator emitted every non-PK
	// column nullable and withheld all three flags. Decode that actual schema so
	// the first post-ADR diff can propose the intended NOT NULL transition.
	const legacy = `{"models":[{"module":"shop","table":"orders","fields":[
		{"name":"id","type":"int64","primary_key":true},
		{"name":"reference","type":"string","max_len":32}]}]}`

	snap, err := orm.ParseSnapshot([]byte(legacy), "legacy.state.json")
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}
	fields := snap.Models()[0].Model.Fields
	if fields[0].Nullable {
		t.Error("legacy primary key decoded nullable")
	}
	if !fields[1].Nullable {
		t.Error("legacy non-primary column decoded NOT NULL; old migrations emitted it nullable")
	}
	if fields[1].Unique || fields[1].Index {
		t.Errorf("legacy withheld flags decoded as set: %+v", fields[1])
	}
}

func TestParseSnapshot_RejectsUnknownStateVersion(t *testing.T) {
	t.Parallel()

	const src = "future.state.json"
	_, err := orm.ParseSnapshot([]byte(`{"v":2,"models":[]}`), src)
	if !errors.Is(err, orm.ErrBadState) {
		t.Fatalf("got %v, want ErrBadState", err)
	}
	for _, want := range []string{src, "version", "2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}
}

func TestParseSnapshot_ErrorsNameTheirSource(t *testing.T) {
	t.Parallel()

	// A state record nobody can parse must be an error naming the file it came
	// from, never a silently empty "before" — which would make the next
	// makemigrations emit a create-everything migration against a live schema.
	const src = "20260801120000_add_orders.state.json"

	tests := []struct {
		name string
		data string
	}{
		{"truncated", `{"models":[{"module":"shop","table":"orders"`},
		{"not json at all", "hello, world"},
		{"wrong top level", `[1,2,3]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := orm.ParseSnapshot([]byte(tc.data), src)
			if err == nil {
				t.Fatal("unparseable state must be rejected")
			}
			if !errors.Is(err, orm.ErrBadState) {
				t.Errorf("callers branch with errors.Is, got %v", err)
			}
			if !strings.Contains(err.Error(), src) {
				t.Errorf("the error must name the source file, got: %v", err)
			}
		})
	}
}

func TestParseSnapshot_RejectsKeysItDoesNotKnow(t *testing.T) {
	t.Parallel()

	// encoding/json ignores unknown fields by default. That is exactly wrong
	// here: a hand-edited or newer-format file carrying a key this version does
	// not understand would be silently read with that information DROPPED, and
	// the next generated migration would diff against an impoverished state.
	// Failing loud is the fail-closed answer.
	data := `{"v":1,"models":[{"module":"shop","table":"orders","fields":[
		{"name":"id","type":"int64","primary_key":true,"collate":"nocase"}]}]}`

	_, err := orm.ParseSnapshot([]byte(data), "edited.state.json")
	if err == nil {
		t.Fatal("an unknown key must be rejected, not dropped")
	}
	if !errors.Is(err, orm.ErrBadState) {
		t.Errorf("callers branch with errors.Is, got %v", err)
	}
	if !strings.Contains(err.Error(), "edited.state.json") {
		t.Errorf("the error must name the source file, got: %v", err)
	}
}

func TestParseSnapshot_RevalidatesWhatItReads(t *testing.T) {
	t.Parallel()

	// Bytes from disk are untrusted in the exact sense registration input is:
	// a corrupted or hand-mangled file must fail with table-and-field context,
	// not poison every later diff with a schema that cannot exist.
	tests := []struct {
		name string
		data string
		want error
	}{
		{
			name: "unknown type",
			data: `{"models":[{"module":"shop","table":"orders","fields":[
				{"name":"id","type":"money","primary_key":true}]}]}`,
			want: orm.ErrInvalidField,
		},
		{
			name: "two primary keys",
			data: `{"models":[{"module":"shop","table":"orders","fields":[
				{"name":"id","type":"int64","primary_key":true},
				{"name":"reference","type":"string","primary_key":true}]}]}`,
			want: orm.ErrInvalidModel,
		},
		{
			name: "duplicate column",
			data: `{"models":[{"module":"shop","table":"orders","fields":[
				{"name":"id","type":"int64","primary_key":true},
				{"name":"id","type":"string"}]}]}`,
			want: orm.ErrInvalidField,
		},
		{
			name: "no fields",
			data: `{"models":[{"module":"shop","table":"orders","fields":[]}]}`,
			want: orm.ErrInvalidModel,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := orm.ParseSnapshot([]byte(tc.data), "mangled.state.json")
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want it to wrap %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), "mangled.state.json") {
				t.Errorf("the error must name the source file, got: %v", err)
			}
		})
	}
}

func TestNewSnapshot_RejectsDuplicateTables(t *testing.T) {
	t.Parallel()

	// Registry output can never contain one table twice, but NewSnapshot takes
	// arbitrary values and replay feeds it whatever was recorded. The duplicate
	// check belongs where the collection is built, not two layers later in the
	// differ.
	models := shopRegistry().Models()
	dup := append(models, models[0])

	_, err := orm.NewSnapshot(dup)
	if !errors.Is(err, orm.ErrDuplicateTable) {
		t.Errorf("got %v, want it to wrap %v", err, orm.ErrDuplicateTable)
	}
	if !strings.Contains(err.Error(), models[0].Model.Table) {
		t.Errorf("the error must name the table, got: %v", err)
	}
}

func TestReplayState_IsDeterministicAndCarriesHandWrittenStepsForward(t *testing.T) {
	t.Parallel()

	// Hand-written migrations carry no recorded state — there are no operations
	// for Fabrin to fold. Carrying the last known state forward is the stated
	// rule, and reconstruction from the same files must be byte-for-byte stable,
	// because the differ's output inherits whatever wiggle lives here.
	first := mustSnapshot(t, "shop", orm.Model{
		Table:  "orders",
		Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	})
	second := mustSnapshot(t, "shop", orm.Model{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.TypeInt64, PrimaryKey: true},
			{Name: "reference", Type: orm.TypeString, MaxLen: 32},
		},
	})

	steps := []orm.StateStep{
		{Version: "20260801120000", State: &first},
		{Version: "20260802120000"}, // hand-written: no recorded state
		{Version: "20260803120000", State: &second},
	}

	got, err := orm.ReplayState(steps)
	if err != nil {
		t.Fatalf("ReplayState: %v", err)
	}
	if tables := len(got.Models()); tables != 1 {
		t.Fatalf("replayed %d tables, want 1 (the last known state carried forward)", tables)
	}
	if fields := len(got.Models()[0].Model.Fields); fields != 2 {
		t.Errorf("carried %d fields through, want 2", fields)
	}

	again, err := orm.ReplayState(steps)
	if err != nil {
		t.Fatalf("ReplayState again: %v", err)
	}
	a, _ := orm.EncodeSnapshot(got)
	b, _ := orm.EncodeSnapshot(again)
	if string(a) != string(b) {
		t.Error("two replays of one chain differ; reconstruction is not deterministic")
	}
}

func TestReplayState_StartsEmptyWhenTheFirstStepHasNoState(t *testing.T) {
	t.Parallel()

	// An empty schema is a valid start: the first generated migration creates
	// everything. A zero Snapshot means exactly that, so nothing about the
	// empty case needs special-casing at the call site.
	got, err := orm.ReplayState([]orm.StateStep{{Version: "20260801120000"}})
	if err != nil {
		t.Fatalf("ReplayState: %v", err)
	}
	if n := len(got.Models()); n != 0 {
		t.Errorf("empty start produced %d tables, want 0", n)
	}
}

func TestReplayState_RejectsBrokenSequences(t *testing.T) {
	t.Parallel()

	// Steps arrive ordered off disk. A chain whose versions repeat or go
	// backwards is a corrupted directory or a merge gone wrong; replaying it
	// would reconstruct some OTHER project's history and present it as truth.
	state := mustSnapshot(t, "shop", orm.Model{
		Table:  "orders",
		Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	})

	tests := []struct {
		name  string
		steps []orm.StateStep
	}{
		{
			name: "duplicate version",
			steps: []orm.StateStep{
				{Version: "20260801120000"},
				{Version: "20260801120000", State: &state},
			},
		},
		{
			name: "descending versions",
			steps: []orm.StateStep{
				{Version: "20260803120000"},
				{Version: "20260802120000", State: &state},
			},
		},
		{
			name: "missing version",
			steps: []orm.StateStep{
				{Version: "", State: &state},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := orm.ReplayState(tc.steps)
			if !errors.Is(err, orm.ErrBadSteps) {
				t.Errorf("got %v, want it to wrap %v", err, orm.ErrBadSteps)
			}
		})
	}
}

func TestReplayState_DoesNotAliasCallerSteps(t *testing.T) {
	t.Parallel()

	// The reconstructed state is handed to the differ, which compares it
	// against the live registry. If replay kept a reference into the caller's
	// steps, mutating one would silently change what the other sees.
	state := mustSnapshot(t, "shop", orm.Model{
		Table:  "orders",
		Fields: []orm.Field{{Name: "id", Type: orm.TypeInt64, PrimaryKey: true}},
	})

	got, err := orm.ReplayState([]orm.StateStep{
		{Version: "20260801120000", State: &state},
	})
	if err != nil {
		t.Fatalf("ReplayState: %v", err)
	}

	models := got.Models()
	models[0].Model.Table = "vandalised"
	models[0].Model.Fields[0].Name = "also vandalised"
	state.Models()[0].Model.Table = "vandalised through the step"

	if again := got.Models(); again[0].Model.Table != "orders" {
		t.Errorf("the snapshot was mutated through Models(): %q", again[0].Model.Table)
	}
}

func mustSnapshot(t *testing.T, module string, m orm.Model) orm.Snapshot {
	t.Helper()
	r := orm.NewRegistry()
	if err := r.Register(module, m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	snap, err := orm.NewSnapshot(r.Models())
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return snap
}
