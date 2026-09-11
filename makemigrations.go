package fabrin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/usefabrin/fabrin/cli"
	"github.com/usefabrin/fabrin/migratediff"
	"github.com/usefabrin/fabrin/orm"
)

// The on-disk shape makemigrations maintains, one directory per owning module:
//
//	<module>/migrations/
//	  20260802120000_create_orders.go          the migration, hand-editable
//	  20260802120000_create_orders.state.json  the schema that version yields
//	  manifest.json                            ordered list of what exists
//	  all.go                                   generated: var All []migrate.M
//
// The filename prefix IS the version, at fixed width 14 — the discipline the
// engine applies to versions themselves, and what lets a gate read versions off
// disk without compiling anything. The manifest is the source of truth for what
// runs and in what order; a hand-written migration joins it with an entry and
// NO state file, which replay carries forward.

// makemigrationsCommand builds the makemigrations built-in.
func makemigrationsCommand(a *App) cli.Command {
	return cli.Command{
		Name:  "makemigrations",
		Short: "generate migration files for schema changes the modules declared",
		Run: func(ctx context.Context, out io.Writer, _ []string) error {
			return a.runMakemigrations(ctx, out)
		},
	}
}

// runMakemigrations diffs the live schema against the state the recorded chain
// ends in, and writes one migration per changed module.
//
// It touches no database SCHEMA — generating a migration works against a live
// server without altering it. The handle is still required, for one reason: the
// driver's identity chooses the SQL dialect the files are rendered for. A
// migration written for PostgreSQL that later runs against SQLite is worse than
// none.
func (a *App) runMakemigrations(ctx context.Context, out io.Writer) error {
	if a.registry.sliced {
		return fmt.Errorf("fabrin: this process is sliced by FABRIN_MODULES (registered: %v, mounted: %v) — "+
			"migration commands refuse to act on part of the schema, because half-migrating the shared database "+
			"is worse than not migrating. Run without FABRIN_MODULES set",
			a.registry.catalog, a.registry.names())
	}
	if a.opts.DB == nil {
		return fmt.Errorf("fabrin: no database configured for makemigrations — open one in main and set Options.DB (its driver selects the SQL dialect the files are rendered for)")
	}

	dialect, err := dialectFor(a.opts.DB)
	if err != nil {
		return err
	}

	before, manifests, err := a.replayRecordedState()
	if err != nil {
		return err
	}
	after, err := orm.NewSnapshot(a.Models())
	if err != nil {
		return err
	}

	ops := migratediff.Diff(before, after)
	if len(ops) == 0 {
		_, err := fmt.Fprintln(out, "no changes detected in any module's models")
		return err
	}

	// One migration per CHANGED module, versions increasing within the run so
	// two files never claim one version — the engine rejects duplicates, and
	// each file's Down must be undoable on its own.
	byModule := make(map[string][]migratediff.Operation)
	var order []string
	for _, op := range ops {
		table := operationTable(op)
		owner := a.ownerOf(table, before)
		if _, seen := byModule[owner]; !seen {
			order = append(order, owner)
		}
		byModule[owner] = append(byModule[owner], op)
	}
	slices.Sort(order)

	version, err := nextVersion(manifests)
	if err != nil {
		return err
	}
	cumulative := before

	for _, module := range order {
		moduleOps := byModule[module]
		upStmts := make([]string, 0, len(moduleOps))
		downGroups := make([][]string, 0, len(moduleOps))
		describe := make([]string, 0, len(moduleOps))
		for _, op := range moduleOps {
			stmts, err := dialect.Render(op)
			if err != nil {
				return fmt.Errorf("fabrin: module %q's changes cannot be generated for this dialect (%v) — hand-write this migration", module, err)
			}
			inverse, err := invert(op, before)
			if err != nil {
				return fmt.Errorf("fabrin: module %q: %w", module, err)
			}
			down, err := dialect.Render(inverse)
			if errors.Is(err, migratediff.ErrUnsupported) {
				// The dialect states its refusal for live rendering, but a
				// GENERATED rollback cannot just give up: without it, even
				// SQLite's most common change — adding a column — would be
				// ungeneratable. Where a plain statement exists, emit it with
				// the caveat riding in the file; where none does, hand-write.
				raw, ok := rawInverse(op)
				if !ok {
					return fmt.Errorf("fabrin: module %q's changes cannot be reversed for this dialect (%v) — hand-write this migration", module, err)
				}
				down = []string{"-- fabrin: emitted directly; the dialect refuses this operation when\n-- rendered live (older SQLite needs the table-rebuild dance).\n" + raw}
			} else if err != nil {
				return fmt.Errorf("fabrin: module %q's changes cannot be reversed for this dialect (%v) — hand-write this migration", module, err)
			}
			upStmts = append(upStmts, stmts...)
			downGroups = append(downGroups, down)
			describe = append(describe, op.Describe())
		}
		downStmts := make([]string, 0, len(moduleOps))
		for i := len(downGroups) - 1; i >= 0; i-- {
			downStmts = append(downStmts, downGroups[i]...)
		}

		name := migrationSlug(strings.Join(describe, " "))
		base := version + "_" + name
		dir := filepath.Join(module, "migrations")
		varName := "M" + version + "_" + slugIdent(name)

		cumulative, err = stateAfterOperations(cumulative, after, moduleOps)
		if err != nil {
			return fmt.Errorf("fabrin: record cumulative state for module %q: %w", module, err)
		}
		stateBytes, err := encodeSnapshotBytes(cumulative)
		if err != nil {
			return err
		}
		goSrc, err := renderMigrationGo(varName, version, strings.Join(describe, "; "), upStmts, downStmts)
		if err != nil {
			return err
		}

		written := []string{}
		for path, data := range map[string][]byte{
			filepath.Join(dir, base+".go"):         goSrc,
			filepath.Join(dir, base+".state.json"): stateBytes,
			filepath.Join(dir, "manifest.json"):    nil, // written below, after the manifest is updated
		} {
			_ = path
			_ = data
		}
		_ = written

		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("fabrin: create %s: %w", dir, err)
		}
		migPath := filepath.Join(dir, base+".go")
		if err := os.WriteFile(migPath, goSrc, 0o644); err != nil {
			return fmt.Errorf("fabrin: write %s: %w", migPath, err)
		}
		statePath := filepath.Join(dir, base+".state.json")
		if err := os.WriteFile(statePath, stateBytes, 0o644); err != nil {
			return fmt.Errorf("fabrin: write %s: %w", statePath, err)
		}

		manifests[module] = append(manifests[module], manifestEntry{
			Version: version,
			File:    base + ".go",
			Var:     varName,
			State:   base + ".state.json",
		})
		if err := writeManifest(dir, manifests[module]); err != nil {
			return err
		}
		if err := writeAll(dir, manifests[module]); err != nil {
			return err
		}

		if _, err := fmt.Fprintf(out, "wrote %s\n", migPath); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "wrote %s\n", statePath); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "%s: %s\n", version, strings.Join(describe, "; ")); err != nil {
			return err
		}
		version = bumpVersion(version)
	}
	return nil
}

// replayRecordedState loads every module's manifest, orders the entries into one
// chain, and reconstructs the schema the chain ends in.
//
// A manifest entry WITH a state file contributes its recorded snapshot; an
// entry WITHOUT one is hand-written, and carries the last known state forward —
// Fabrin cannot fold free-form SQL into schema state, and refusing would make
// hand-written and generated migrations unable to mix. An entry whose state file
// is listed but unreadable fails loudly naming the file: a silently empty
// "before" is how makemigrations ends up proposing to create-everything against
// a live schema.
func (a *App) replayRecordedState() (orm.Snapshot, map[string][]manifestEntry, error) {
	type step struct {
		version string
		src     string
	}
	var steps []step
	manifests := make(map[string][]manifestEntry)
	seen := make(map[string]string)

	for _, module := range a.registry.names() {
		dir := filepath.Join(module, "migrations")
		entries, err := loadManifest(dir)
		if err != nil {
			return orm.Snapshot{}, nil, err
		}
		manifests[module] = entries
		for _, e := range entries {
			if prev, dup := seen[e.Version]; dup {
				return orm.Snapshot{}, nil, fmt.Errorf("fabrin: migration version %q appears in both %s and %s", e.Version, prev, dir)
			}
			seen[e.Version] = dir
			steps = append(steps, step{version: e.Version, src: filepath.Join(dir, e.File)})
		}
	}
	slices.SortFunc(steps, func(x, y step) int {
		switch {
		case x.version < y.version:
			return -1
		case x.version > y.version:
			return 1
		}
		return 0
	})

	chain := make([]orm.StateStep, 0, len(steps))
	for _, s := range steps {
		ms := orm.StateStep{Version: s.version}
		// Find the state file listed for this entry, wherever its module lives.
		var statePath string
		for _, module := range a.registry.names() {
			for _, e := range manifests[module] {
				if e.Version == s.version && e.State != "" {
					statePath = filepath.Join(module, "migrations", e.State)
				}
			}
		}
		if statePath == "" {
			chain = append(chain, ms) // hand-written: carry forward
			continue
		}
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return orm.Snapshot{}, nil, fmt.Errorf("fabrin: read recorded state for %s: %w", s.src, err)
		}
		snap, err := orm.ParseSnapshot(raw, statePath)
		if err != nil {
			return orm.Snapshot{}, nil, err
		}
		ms.State = &snap
		chain = append(chain, ms)
	}

	replayed, err := orm.ReplayState(chain)
	if err != nil {
		return orm.Snapshot{}, nil, fmt.Errorf("fabrin: reconstruct recorded state: %w", err)
	}
	return replayed, manifests, nil
}

// ownerOf names the module that declared table, preferring the AFTER schema (a
// new table's owner exists only there) and falling back to BEFORE (a dropped
// table's owner survives only there).
func (a *App) ownerOf(table string, before orm.Snapshot) string {
	for _, reg := range a.Models() {
		if reg.Model.Table == table {
			return reg.Module
		}
	}
	for _, reg := range before.Models() {
		if reg.Model.Table == table {
			return reg.Module
		}
	}
	return "(unknown)"
}

func operationTable(op migratediff.Operation) string {
	switch o := op.(type) {
	case migratediff.CreateTable:
		return o.Model.Table
	case migratediff.DropTable:
		return o.Table
	case migratediff.AddColumn:
		return o.Table
	case migratediff.DropColumn:
		return o.Table
	case migratediff.ChangeType:
		return o.Table
	case migratediff.ChangeNullability:
		return o.Table
	case migratediff.AddUnique:
		return o.Table
	case migratediff.DropUnique:
		return o.Table
	case migratediff.AddIndex:
		return o.Table
	case migratediff.DropIndex:
		return o.Table
	case migratediff.AddPrimaryKey:
		return o.Table
	case migratediff.DropPrimaryKey:
		return o.Table
	default:
		return ""
	}
}

// stateAfterOperations advances recorded state by exactly one generated
// migration. Each sidecar describes the schema that its own version leaves
// behind; recording the run's final state beside every module would let an
// earlier version claim changes that have not run yet.
func stateAfterOperations(current, final orm.Snapshot, ops []migratediff.Operation) (orm.Snapshot, error) {
	models := make(map[string]orm.Registered)
	for _, reg := range current.Models() {
		models[reg.Model.Table] = reg
	}
	finalModels := make(map[string]orm.Registered)
	for _, reg := range final.Models() {
		finalModels[reg.Model.Table] = reg
	}

	for _, op := range ops {
		table := operationTable(op)
		if reg, exists := finalModels[table]; exists {
			models[table] = reg
		} else {
			delete(models, table)
		}
	}

	next := make([]orm.Registered, 0, len(models))
	for _, reg := range models {
		next = append(next, reg)
	}
	return orm.NewSnapshot(next)
}

// invert builds the schema-level inverse of an operation: creates become drops,
// drops become creates FROM THE RECORDED BEFORE — restoring structure rather
// than data, exactly Django's autodetector trade. Retypes revert to the
// previously recorded type.
func invert(op migratediff.Operation, before orm.Snapshot) (migratediff.Operation, error) {
	switch o := op.(type) {
	case migratediff.CreateTable:
		return migratediff.DropTable{Table: o.Model.Table}, nil
	case migratediff.DropTable:
		for _, reg := range before.Models() {
			if reg.Model.Table == o.Table {
				return migratediff.CreateTable{Model: reg.Model}, nil
			}
		}
		return nil, fmt.Errorf("cannot reverse dropping %s: no recorded schema for it", o.Table)
	case migratediff.AddColumn:
		return migratediff.DropColumn{Table: o.Table, Column: o.Field.Name}, nil
	case migratediff.DropColumn:
		for _, reg := range before.Models() {
			if reg.Model.Table != o.Table {
				continue
			}
			for _, f := range reg.Model.Fields {
				if f.Name == o.Column {
					return migratediff.AddColumn{Table: o.Table, Field: f}, nil
				}
			}
		}
		return nil, fmt.Errorf("cannot reverse dropping %s.%s: no recorded field", o.Table, o.Column)
	case migratediff.ChangeType:
		for _, reg := range before.Models() {
			if reg.Model.Table != o.Table {
				continue
			}
			for _, f := range reg.Model.Fields {
				if f.Name == o.Column {
					return migratediff.ChangeType{Table: o.Table, Column: o.Column, To: f}, nil
				}
			}
		}
		return nil, fmt.Errorf("cannot reverse changing %s.%s: no recorded field", o.Table, o.Column)
	case migratediff.ChangeNullability:
		for _, reg := range before.Models() {
			if reg.Model.Table != o.Table {
				continue
			}
			for _, f := range reg.Model.Fields {
				if f.Name == o.Column {
					return migratediff.ChangeNullability{Table: o.Table, Column: o.Column, Nullable: f.Nullable}, nil
				}
			}
		}
		return nil, fmt.Errorf("cannot reverse changing %s.%s: no recorded field", o.Table, o.Column)
	case migratediff.AddUnique:
		return migratediff.DropUnique(o), nil
	case migratediff.DropUnique:
		return migratediff.AddUnique(o), nil
	case migratediff.AddIndex:
		return migratediff.DropIndex(o), nil
	case migratediff.DropIndex:
		return migratediff.AddIndex(o), nil
	case migratediff.AddPrimaryKey:
		return migratediff.DropPrimaryKey(o), nil
	case migratediff.DropPrimaryKey:
		return migratediff.AddPrimaryKey(o), nil
	default:
		return nil, fmt.Errorf("cannot reverse an unknown operation")
	}
}

// rawInverse is the fallback for generated Downs when a dialect refuses to
// render its own inverse. Only operations with an unambiguous plain statement
// qualify; anything needing the table-rebuild dance stays hand-written.
func rawInverse(op migratediff.Operation) (string, bool) {
	switch o := op.(type) {
	case migratediff.AddColumn:
		return "ALTER TABLE " + quoteSQLIdentifier(o.Table) + " DROP COLUMN " + quoteSQLIdentifier(o.Field.Name) + ";", true
	default:
		return "", false
	}
}

func quoteSQLIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// dialectFor maps the configured driver to its Dialect.
//
// database/sql deliberately exposes no driver NAME — driver.Driver is one Open
// method — so this reads the dynamic type's package path and displayed name.
// That is a heuristic and said to be one: it matches the drivers Fabrin knows
// (the pure-Go SQLite driver and pgx's generically named stdlib adapter), and
// anything else is an error naming the detected identity rather than a guess
// rendered into files users commit. A driver that wants generated migrations
// can add itself here deliberately.
func dialectFor(db *sql.DB) (migratediff.Dialect, error) {
	driverType := reflect.TypeOf(db.Driver())
	detected := driverType.String()
	for driverType.Kind() == reflect.Pointer {
		driverType = driverType.Elem()
	}
	detected = driverType.PkgPath() + "." + detected
	switch {
	case strings.Contains(detected, "sqlite"):
		return migratediff.SQLite{}, nil
	case strings.Contains(detected, "pgx"), strings.Contains(detected, "postgres"):
		return migratediff.Postgres{}, nil
	default:
		return nil, fmt.Errorf("fabrin: no SQL dialect known for driver %s (supported: sqlite, pgx)", detected)
	}
}

func nextVersion(manifests map[string][]manifestEntry) (string, error) {
	highest := ""
	for _, entries := range manifests {
		for _, e := range entries {
			if e.Version > highest {
				highest = e.Version
			}
		}
	}
	candidate := time.Now().UTC().Format("20060102150405")
	if highest >= candidate {
		// A recorded version is in this clock second or later — either another
		// generated migration in the same run, a hand-written placeholder like
		// 9999..., or skew between machines. Jump just past it rather than
		// colliding or inching toward it one second at a time.
		t, err := time.Parse("20060102150405", highest)
		if err != nil {
			return "", fmt.Errorf("fabrin: recorded version %q is not a fixed-width YYYYMMDDHHMMSS timestamp", highest)
		}
		candidate = t.Add(time.Second).Format("20060102150405")
	}
	if candidate <= highest {
		return "", fmt.Errorf("fabrin: cannot generate a version above %q", highest)
	}
	return candidate, nil
}

func bumpVersion(v string) string {
	t, err := time.Parse("20060102150405", v)
	if err != nil {
		return v
	}
	return t.Add(time.Second).Format("20060102150405")
}

// migrationSlug lowercases a description into a filename-safe form.
func migrationSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	return strings.Trim(out, "_")
}

// slugIdent squeezes a slug into a legal, readable Go identifier fragment.
func slugIdent(slug string) string {
	parts := strings.Split(slug, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// renderMigrationGo produces the migration file's source. Statements are quoted
// with %q so arbitrary SQL survives round trip through Go source unharmed.
func renderMigrationGo(varName, version, name string, up, down []string) ([]byte, error) {
	q := func(stmts []string) string {
		lines := make([]string, 0, len(stmts))
		for _, s := range stmts {
			lines = append(lines, "\t\t\t"+fmt.Sprintf("%q,", s))
		}
		return strings.Join(lines, "\n")
	}
	src := fmt.Sprintf(`// Code generated by fabrin makemigrations. Editing Up or Down by hand is
// expected; editing the recorded state is not — rerun makemigrations instead,
// or the next diff will be taken against a state that never existed.
package migrations

import (
	"context"

	"github.com/usefabrin/fabrin/migrate"
)

var %[1]s = migrate.M{
	Version: %[2]q,
	Name:    %[3]q,
	Up: func(ctx context.Context, h migrate.Handle) error {
		for _, stmt := range []string{
%[4]s
		} {
			if _, err := h.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	},
	Down: func(ctx context.Context, h migrate.Handle) error {
		for _, stmt := range []string{
%[5]s
		} {
			if _, err := h.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	},
}
`, varName, version, name, q(up), q(down))
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("fabrin: format generated migration %s: %w", version, err)
	}
	return formatted, nil
}

func encodeSnapshotBytes(s orm.Snapshot) ([]byte, error) {
	data, err := orm.EncodeSnapshot(s)
	if err != nil {
		return nil, fmt.Errorf("fabrin: record model state: %w", err)
	}
	return data, nil
}

// manifestEntry is one line of a module's manifest.json.
type manifestEntry struct {
	Version string `json:"version"`
	File    string `json:"file"`
	Var     string `json:"var"`
	State   string `json:"state,omitempty"`
}

func loadManifest(dir string) ([]manifestEntry, error) {
	path := filepath.Join(dir, "manifest.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fabrin: read %s: %w", path, err)
	}
	var entries []manifestEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("fabrin: parse %s: %w", path, err)
	}
	return entries, nil
}

func writeManifest(dir string, entries []manifestEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("fabrin: write %s: %w", path, err)
	}
	return nil
}

// writeAll regenerates the module's all.go from its manifest — the explicit
// list a module's Migrator returns. Generated from data, not scanned from
// source, so regeneration cannot silently lose a hand-written migration that
// is registered in the manifest.
func writeAll(dir string, entries []manifestEntry) error {
	refs := make([]string, 0, len(entries))
	for _, e := range entries {
		refs = append(refs, "\t"+e.Var+",")
	}
	src := fmt.Sprintf(`// Code generated by fabrin makemigrations. DO NOT EDIT.
package migrations

import "github.com/usefabrin/fabrin/migrate"

// All is every migration this module declares, in version order. A module's
// Migrator returns this slice.
var All = []migrate.M{
%s
}
`, strings.Join(refs, "\n"))
	path := filepath.Join(dir, "all.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return fmt.Errorf("fabrin: write %s: %w", path, err)
	}
	return nil
}
