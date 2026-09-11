package orm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// A Snapshot is the model schema one migration leaves behind: every table it
// knows about, the module that declared each, and the columns each carries.
//
// makemigrations diffs the live [Registry] against the state the last recorded
// migration left behind. That state cannot come from the database — generating
// a migration has to work on a laptop with nothing running — so each generated
// migration carries the schema it results in, and reconstruction replays those
// records in version order. See [#56].
//
// # What a Snapshot deliberately does not hold
//
// [Field]'s Nullable, Unique, and Index flags have the semantics ADR 0006 gives
// them and travel in versioned state. Unversioned state remains readable using
// the all-nullable, flags-withheld semantics that produced it; unknown marked
// versions fail closed.
//
// # Determinism is the contract
//
// Tables are ordered by name and columns keep their declared order, so encoding
// one schema twice produces identical bytes. Anything unstable here becomes a
// generated migration that differs between runs on an unchanged project — a
// spurious diff nobody can review honestly.
//
// [#56]: https://github.com/usefabrin/fabrin/issues/56
type Snapshot struct {
	models []Registered // sorted by table
}

// Errors returned by the snapshot functions. Sentinels so a caller can branch
// with errors.Is rather than matching message text.
var (
	// ErrBadState means recorded state could not be read: the bytes did not
	// parse, carried keys this version does not know, or described a schema
	// that could not exist. Every ParseSnapshot failure wraps this, alongside
	// the more specific sentinel where one applies.
	ErrBadState = errors.New("orm: bad recorded state")

	// ErrBadSteps means a replay sequence was unusable — versions repeated,
	// went backwards, or a step had none. Replaying it would reconstruct some
	// other project's history and present it as truth.
	ErrBadSteps = errors.New("orm: unusable state steps")
)

// NewSnapshot collects models into the state a migration would leave behind,
// rejecting anything the migration generator could not act on.
//
// Input normally comes straight off [Registry.Models], which has already
// validated and sorted; the checks here exist because the parameter accepts any
// values and replay feeds it whatever migrations recorded. One table claimed
// twice is [ErrDuplicateTable]; a model registration would reject wraps that
// sentinel instead.
func NewSnapshot(models []Registered) (Snapshot, error) {
	out := make([]Registered, 0, len(models))
	for _, reg := range models {
		if err := validate(reg.Model); err != nil {
			return Snapshot{}, err
		}
		if prev, dup := findByTable(out, reg.Model.Table); dup {
			return Snapshot{}, fmt.Errorf("%w: the state claims table %q twice (module %q and %q)",
				ErrDuplicateTable, reg.Model.Table, prev.Module, reg.Module)
		}
		out = append(out, snapshotRegistered(reg))
	}
	slices.SortFunc(out, func(a, b Registered) int {
		switch {
		case a.Model.Table < b.Model.Table:
			return -1
		case a.Model.Table > b.Model.Table:
			return 1
		}
		return 0
	})
	return Snapshot{models: out}, nil
}

// Models returns every model in the snapshot, sorted by table name, as a deep
// copy. The snapshot is read by the differ against the live registry; a caller
// that mutated what it was handed would change what every later comparison
// sees, with nothing in between to blame.
func (s Snapshot) Models() []Registered {
	out := make([]Registered, 0, len(s.models))
	for _, reg := range s.models {
		out = append(out, cloneRegistered(reg))
	}
	return out
}

// EncodeSnapshot renders the state as deterministic, indented JSON ending in a
// newline — a form a reviewer reads as easily as a diff, because seeing what
// the schema became is the point of recording it.
func EncodeSnapshot(s Snapshot) ([]byte, error) {
	wire := wireState{V: stateFormatVersion, Models: make([]wireModel, 0, len(s.models))}
	for _, reg := range s.models {
		wm := wireModel{Module: reg.Module, Table: reg.Model.Table, Fields: make([]wireField, 0, len(reg.Model.Fields))}
		for _, f := range reg.Model.Fields {
			wm.Fields = append(wm.Fields, wireField{
				Name:       f.Name,
				Type:       f.Type,
				MaxLen:     f.MaxLen,
				Nullable:   f.Nullable,
				PrimaryKey: f.PrimaryKey,
				Unique:     f.Unique,
				Index:      f.Index,
			})
		}
		wire.Models = append(wire.Models, wm)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(wire); err != nil {
		return nil, fmt.Errorf("%w: encode: %w", ErrBadState, err)
	}
	return buf.Bytes(), nil
}

// ParseSnapshot reads state back, naming src in every error it returns —
// "unparseable" without a file name sends the reader grepping a directory.
//
// Parsing is stricter than decoding. Unknown keys are REJECTED rather than
// dropped: a newer-format or hand-edited file read silently with its unknown
// information discarded would make the next generated migration diff against
// an impoverished state. And what parsed is revalidated through the same rules
// [Registry.Register] applies, so bytes describing a schema that could not
// exist fail here, with table-and-field context, instead of poisoning every
// later diff.
func ParseSnapshot(data []byte, src string) (Snapshot, error) {
	var wire wireState
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %s: %w", ErrBadState, src, err)
	}
	legacy := wire.V == 0
	if !legacy && wire.V != stateFormatVersion {
		return Snapshot{}, fmt.Errorf("%w: %s: state format version %d is not supported (want %d)", ErrBadState, src, wire.V, stateFormatVersion)
	}

	out := make([]Registered, 0, len(wire.Models))
	for _, wm := range wire.Models {
		m := Model{Table: wm.Table}
		m.Fields = make([]Field, 0, len(wm.Fields))
		for _, wf := range wm.Fields {
			f := Field{
				Name:       wf.Name,
				Type:       wf.Type,
				MaxLen:     wf.MaxLen,
				Nullable:   wf.Nullable,
				PrimaryKey: wf.PrimaryKey,
				Unique:     wf.Unique,
				Index:      wf.Index,
			}
			if legacy && !f.PrimaryKey {
				// Before ADR 0006, generated DDL made every non-primary column
				// nullable and the state codec withheld all three flags. Decode
				// the schema that actually existed rather than applying today's
				// zero-value meaning retroactively.
				f.Nullable = true
			}
			m.Fields = append(m.Fields, f)
		}
		reg := Registered{Module: wm.Module, Model: m}
		if err := validate(reg.Model); err != nil {
			return Snapshot{}, fmt.Errorf("%w: %s: %w", ErrBadState, src, err)
		}
		if _, dup := findByTable(out, reg.Model.Table); dup {
			return Snapshot{}, fmt.Errorf("%w: %s: table %q appears twice in one state",
				ErrBadState, src, reg.Model.Table)
		}
		out = append(out, reg)
	}
	slices.SortFunc(out, func(a, b Registered) int {
		switch {
		case a.Model.Table < b.Model.Table:
			return -1
		case a.Model.Table > b.Model.Table:
			return 1
		}
		return 0
	})
	return Snapshot{models: out}, nil
}

// StateStep is one migration's contribution to reconstructed state: the
// version that orders it and the schema it resulted in.
//
// State may be nil — hand-written migrations carry no recorded state, because
// Fabrin has no operation vocabulary to fold. Carrying the last known state
// forward through them is the stated rule, chosen over refusing the chain so
// generated and hand-written migrations can coexist.
type StateStep struct {
	// Version orders this step against the others. It must be non-empty, and
	// the sequence must ascend strictly — the same fixed-width discipline the
	// migration engine applies to the versions themselves.
	Version string

	// State is the full schema after this migration applied. Nil carries the
	// previous state forward unchanged.
	State *Snapshot
}

// ReplayState folds steps in version order and returns the state the chain
// leaves behind — the "before" the next makemigrations diffs against.
//
// Steps must ascend strictly by version, because they arrive ordered off disk
// and a chain that repeats or reverses is a corrupted directory presenting
// itself as history. Reconstruction touches no database: the whole point of
// recorded state is generating a migration with nothing running.
func ReplayState(steps []StateStep) (Snapshot, error) {
	var cur []Registered
	prev := ""
	for _, step := range steps {
		switch {
		case step.Version == "":
			return Snapshot{}, fmt.Errorf("%w: a step with no version has nothing to order it by", ErrBadSteps)
		case prev != "" && step.Version == prev:
			return Snapshot{}, fmt.Errorf("%w: version %q appears twice", ErrBadSteps, step.Version)
		case prev != "" && step.Version < prev:
			return Snapshot{}, fmt.Errorf("%w: %q sorts before %q, which came earlier — steps must ascend by version", ErrBadSteps, step.Version, prev)
		}
		prev = step.Version

		if step.State == nil {
			continue
		}
		cur = make([]Registered, 0, len(step.State.models))
		for _, reg := range step.State.models {
			cur = append(cur, cloneRegistered(reg))
		}
	}
	return Snapshot{models: cur}, nil
}

// snapshotRegistered copies exactly the metadata the state format records.
// Building it field-by-field prevents a future Field addition from silently
// entering migration history merely because the public struct grew.
func snapshotRegistered(reg Registered) Registered {
	fields := make([]Field, len(reg.Model.Fields))
	for i, f := range reg.Model.Fields {
		fields[i] = Field{
			// Go names describe generated source rather than database state. A
			// historical snapshot reconstructs the SQL schema and deliberately
			// leaves them empty.
			Name:       f.Name,
			Type:       f.Type,
			MaxLen:     f.MaxLen,
			Nullable:   f.Nullable,
			PrimaryKey: f.PrimaryKey,
			Unique:     f.Unique,
			Index:      f.Index,
		}
	}
	return Registered{Module: reg.Module, Model: Model{Table: reg.Model.Table, Fields: fields}}
}

func cloneRegistered(reg Registered) Registered {
	return Registered{Module: reg.Module, Model: clone(reg.Model)}
}

func findByTable(models []Registered, table string) (Registered, bool) {
	for _, reg := range models {
		if reg.Model.Table == table {
			return reg, true
		}
	}
	return Registered{}, false
}

// The wire representation. Its field list is the format: only metadata with
// agreed semantics travels, and adding a key here is a deliberate format
// change, not something struct growth grants for free.
type wireState struct {
	V      int         `json:"v,omitempty"`
	Models []wireModel `json:"models"`
}

const stateFormatVersion = 1

type wireModel struct {
	Module string      `json:"module"`
	Table  string      `json:"table"`
	Fields []wireField `json:"fields"`
}

type wireField struct {
	Name       string `json:"name"`
	Type       Type   `json:"type"`
	MaxLen     int    `json:"max_len,omitempty"`
	Nullable   bool   `json:"nullable,omitempty"`
	PrimaryKey bool   `json:"primary_key,omitempty"`
	Unique     bool   `json:"unique,omitempty"`
	Index      bool   `json:"index,omitempty"`
}
