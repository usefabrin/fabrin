package main

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/usefabrin/fabrin"
)

// get drives the app's handler directly, so these tests need no port and can run
// in parallel with everything else.
func get(t *testing.T, app *fabrin.App, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// send is get's counterpart for the requests that carry a body.
func send(t *testing.T, app *fabrin.App, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	return rec
}

// build wires the example exactly as its main function does. Tests that
// construct a *different* app prove nothing about the example a user runs.
func build(t *testing.T, opts fabrin.Options) *fabrin.App {
	t.Helper()
	app, err := newApp(opts)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return app
}

// ── MOD-001 / MOD-002, end to end: process slicing ──────────────────────────

func TestSlicing_MountsOnlyTheSelectedModule(t *testing.T) {
	t.Parallel()

	// The claim on the README's front page: one binary, N deployment shapes.
	app := build(t, fabrin.Options{Modules: []string{"greet"}})

	if rec := get(t, app, "/greet"); rec.Code != http.StatusOK {
		t.Errorf("/greet = %d, want 200: the selected module must be mounted", rec.Code)
	}
	if rec := get(t, app, "/time"); rec.Code != http.StatusNotFound {
		t.Errorf("/time = %d, want 404: an unselected module's routes must not exist", rec.Code)
	}
}

func TestSlicing_MountsEverythingWhenNothingIsSelected(t *testing.T) {
	t.Parallel()

	app := build(t, fabrin.Options{})

	for _, path := range []string{"/greet", "/time"} {
		if rec := get(t, app, path); rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200 with no selection in effect", path, rec.Code)
		}
	}
}

func TestSlicing_RejectsAnUnknownModuleName(t *testing.T) {
	t.Parallel()

	// A process that starts, passes its liveness probe, and serves nothing is the
	// worst available outcome, because nothing about it looks wrong.
	if _, err := newApp(fabrin.Options{Modules: []string{"greet", "nope"}}); err == nil {
		t.Fatal("a selection naming an unregistered module must fail at construction")
	}
}

// ── The greeting actually flows through the port ────────────────────────────

func TestGreet_UsesTheClockItWasGiven(t *testing.T) {
	t.Parallel()

	app := build(t, fabrin.Options{})
	body := get(t, app, "/greet?name=fabrin").Body.String()

	if !strings.Contains(body, "fabrin") {
		t.Errorf("greeting must use the query parameter, got %s", body)
	}
	// The timestamp can only be there if the injected Clock was called, which is
	// the port being satisfied rather than merely declared.
	if !strings.Contains(body, `"at"`) {
		t.Errorf("greeting must carry the time from the injected Clock, got %s", body)
	}
}

// ── The data flows through a port too, and main holds the database ──────────

func TestOrders_RoundTripsAnOrderThroughTheStoreMainWiredIn(t *testing.T) {
	t.Parallel()

	// The same module the orders package tests against a map, running here
	// against whatever main opened — SQLite, so this needs no server. Two
	// requests rather than one: the second can only answer if the first reached
	// storage that outlives a request, which an echo cannot fake.
	app := build(t, fabrin.Options{})

	rec := send(t, app, http.MethodPost, "/orders", `{"item":"widget"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /orders = %d, want 201, body %s", rec.Code, rec.Body.String())
	}

	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("POST /orders returned %s, which is not the created order: %v", rec.Body.String(), err)
	}
	if created.ID == 0 {
		t.Fatalf("POST /orders must return the id the store assigned, got %s", rec.Body.String())
	}

	rec = get(t, app, "/orders/"+strconv.FormatInt(created.ID, 10))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /orders/%d = %d, want 200, body %s", created.ID, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "widget") {
		t.Errorf("GET /orders/%d returned %s, want the order the first request wrote", created.ID, rec.Body.String())
	}
}

func TestOrders_DeclaresTheTableItOwnsSoAGeneratorHasSomethingToDiff(t *testing.T) {
	t.Parallel()

	app := build(t, fabrin.Options{})

	// Deliberately not asserting column names: what makemigrations needs is a
	// model attributed to the module that declared it, and pinning the schema
	// here would make every later column an edit to this test rather than to the
	// module. orm.Register already rejects a table with no primary key, so
	// checking for one would be testing orm.
	var owned int
	for _, reg := range app.Models() {
		if reg.Module != "orders" {
			continue
		}
		owned++
		if len(reg.Model.Fields) == 0 {
			t.Errorf("table %q declares no columns", reg.Model.Table)
		}
	}
	if owned == 0 {
		t.Fatalf("no model is attributed to module %q, so makemigrations would have nothing to diff; the app declares %v",
			"orders", app.Models())
	}
}

// ── FR-CLI-3/4, end to end: the commands the binary answers ─────────────────

func TestCommand_GreetUsesTheSamePortTheHandlerDoes(t *testing.T) {
	t.Parallel()

	// The point of the module command: it IS the module, not a second
	// implementation living nearby. It reaches the clock through the port greet
	// declares, so a swapped Clock changes the command and the handler together.
	app := build(t, fabrin.Options{})

	var out bytes.Buffer
	if err := app.Execute(t.Context(), &out, []string{"greet", "you"}); err != nil {
		t.Fatalf("greet: %v", err)
	}
	if !strings.Contains(out.String(), "hello, you") {
		t.Errorf("greet printed %q, want a greeting for \"you\"", out.String())
	}
}

func TestCommand_RoutesNamesTheOwningModule(t *testing.T) {
	t.Parallel()

	app := build(t, fabrin.Options{})

	var out bytes.Buffer
	if err := app.Execute(t.Context(), &out, []string{"routes"}); err != nil {
		t.Fatalf("routes: %v", err)
	}

	// The README prints this listing. A test that only checked "some output"
	// would let the README go stale without anything failing.
	// The exact spacing is the assertion, not an accident of it: printRoutes sizes
	// the path column from the rows present, so these strings only match while
	// examples/hello/README.md's listing is still what the binary prints. A new
	// route reflows the table and turns this red, which is the point.
	for _, want := range []string{"/greet       greet", "/time        clock", "(framework)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("routes output missing %q:\n%s", want, out.String())
		}
	}
}

func TestCommand_SlicingDropsAnUnmountedModulesCommand(t *testing.T) {
	t.Parallel()

	// Process slicing applies to commands exactly as it does to routes and
	// readiness checks: this process cannot greet, so it must not offer to.
	app := build(t, fabrin.Options{Modules: []string{"clock"}})

	var out bytes.Buffer
	err := app.Execute(t.Context(), &out, []string{"greet"})
	if err == nil {
		t.Fatal("an unmounted module's command must not be dispatchable")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("want an unknown-command error, got: %v", err)
	}
}

// ── MOD-003: ports, not imports ─────────────────────────────────────────────

func TestModules_NeverImportEachOther(t *testing.T) {
	t.Parallel()

	// Hard rule 3, checked rather than asserted in prose. A module that needs
	// something another module owns declares the interface in its OWN package and
	// lets main pass in whatever satisfies it — that interface is the seam along
	// which the module can later move to its own process. A direct import welds
	// the two together permanently, and the weld is invisible until someone tries
	// to split them.
	//
	// Reading the import graph is the only way to prove a negative like this: no
	// behavioural test can distinguish "greet does not import clock" from "greet
	// imports clock but happens not to call it today".
	const modulePrefix = "github.com/usefabrin/fabrin/examples/hello/"

	modules, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read example dir: %v", err)
	}

	var checked int
	for _, entry := range modules {
		if !entry.IsDir() {
			continue
		}
		self := modulePrefix + entry.Name()

		for _, imp := range importsOf(t, entry.Name()) {
			if !strings.HasPrefix(imp, modulePrefix) || imp == self {
				continue
			}
			t.Errorf("module %q imports module %q — declare the interface you need in your own package instead",
				entry.Name(), strings.TrimPrefix(imp, modulePrefix))
		}
		checked++
	}

	// Guards against the test passing because it found nothing to look at — a
	// rename of the directory layout would otherwise make this silently vacuous.
	if checked < 2 {
		t.Fatalf("expected at least two module packages to check, found %d", checked)
	}
}

func TestOrders_ImportsNoDatabaseHandleNorAnythingOutsideFabrin(t *testing.T) {
	t.Parallel()

	// ADR 0002 read off the import graph. The orders module gets its data through
	// an interface it declares, and main is the only file that knows what
	// satisfies it — so the module names no ORM, no driver, and not database/sql
	// either. No behavioural test can prove that: a module that imports a handle
	// and does not use it today passes every request-level assertion, and the
	// weld only becomes visible when someone tries to run this module against
	// something else.
	const (
		fabrinMod = "github.com/usefabrin/fabrin"
		dir       = "orders"
	)

	// The floor, and it counts NON-TEST files on purpose. The module's own
	// _test.go imports fabrin and the standard library, so a floor that asked
	// only "did we read any Go at all" would be satisfied by the test file
	// sitting next to a module that does not exist yet — this test would then
	// pass by finding nothing, which is the one way an import-graph test fails
	// silently.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var sources int
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			sources++
		}
	}
	if sources == 0 {
		t.Fatalf("%s holds no non-test .go file, so there is no module here to read an import graph from", dir)
	}

	// Test files are read as well, which is stricter than the `!**/*_test.go`
	// exclusion migrate's depguard rule carries — and deliberately so here. A
	// module whose own tests need a driver is a module nobody can exercise
	// without a database, whatever its shipped imports say.
	for _, imp := range importsOf(t, dir) {
		switch {
		case imp == "database/sql" || strings.HasPrefix(imp, "database/sql/"):
			t.Errorf("orders imports %q — the handle belongs to main, and this module declares the interface it needs instead (ADR 0002)", imp)

		case imp == fabrinMod || strings.HasPrefix(imp, fabrinMod+"/"):
			// Fabrin itself, fabrin/orm included: metadata describes tables and
			// holds no database handle.

		case strings.Contains(strings.SplitN(imp, "/", 2)[0], "."):
			// An allowlist, not a list of ORMs to deny. A deny list of prefixes
			// fails open the moment someone reaches for one nobody wrote a rule
			// against, and "no ORM" is a claim about every ORM there will ever be.
			// A dot in the first path element is what makes a path a module path
			// rather than the standard library.
			t.Errorf("orders imports %q, which is neither the standard library nor Fabrin — whichever data layer this application chose, main is the only place allowed to name it", imp)
		}
	}
}

// importsOf returns every import path in dir's .go files.
func importsOf(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var out []string
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, e.Name())

		// ImportsOnly: the declarations are irrelevant and parsing them is just a
		// way for this test to break on unrelated code.
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("bad import path in %s: %v", path, err)
			}
			out = append(out, p)
		}
	}
	return out
}

// ── The probes a deployment needs, without any module asking ────────────────

func TestProbes_AnswerOnAStockApp(t *testing.T) {
	t.Parallel()

	app := build(t, fabrin.Options{})

	if rec := get(t, app, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", rec.Code)
	}
	// scripts/smoke-examples.sh polls /healthz, so this failing means the smoke
	// gate fails too — with a much less specific message.

	rec := get(t, app, "/readyz")
	if rec.Code != http.StatusOK {
		t.Errorf("/readyz = %d, want 200, body %s", rec.Code, rec.Body.String())
	}
	// The clock module contributes a Check, so readiness is aggregating a real
	// one here rather than reporting an empty list.
	if !strings.Contains(rec.Body.String(), `"module":"clock"`) {
		t.Errorf("/readyz must report the clock module's check, got %s", rec.Body.String())
	}
}

func TestProbes_LivenessIgnoresModuleChecks(t *testing.T) {
	t.Parallel()

	// Only the selected module is mounted, so no check is registered at all —
	// liveness must be indifferent either way.
	app := build(t, fabrin.Options{Modules: []string{"greet"}})

	if rec := get(t, app, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200 regardless of which modules are mounted", rec.Code)
	}
}
