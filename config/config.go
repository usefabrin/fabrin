package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Errors returned by [Load] and [Resolve]. Sentinels so callers branch with
// errors.Is rather than matching message text.
var (
	// ErrInvalidValue means a setting was present but could not be parsed.
	ErrInvalidValue = errors.New("config: invalid value")

	// ErrUnknownKey means a FABRIN_-prefixed key is not a recognised setting —
	// almost always a typo, which is why it is an error rather than ignored.
	ErrUnknownKey = errors.New("config: unknown key")

	// ErrBadFile means a settings file could not be read or parsed.
	ErrBadFile = errors.New("config: bad file")

	// ErrNoSources means [Load] or [Resolve] was called with no sources, so it
	// would have read nothing.
	//
	// An error rather than "just the defaults" because the failure is otherwise
	// silent: the process starts, serves, and ignores every FABRIN_ variable, with
	// the only symptom being a setting that mysteriously "does not work". That is
	// the same defect this package already refuses to ship for an unknown key, and
	// the same one the root package refuses for an unknown module name. Say
	// [Defaults] out loud when defaults are genuinely what you want.
	ErrNoSources = errors.New("config: no sources")
)

// keyPrefix scopes which environment keys this package claims. The environment of
// any real process is full of unrelated variables, and a settings file is usually
// shared with other tooling, so unprefixed keys are ignored rather than rejected.
const keyPrefix = "FABRIN_"

// Settings keys, exported so callers and tests refer to them by constant rather
// than by a string literal that a rename would silently orphan.
const (
	KeyAddr              = "FABRIN_ADDR"
	KeyModules           = "FABRIN_MODULES"
	KeyDebug             = "FABRIN_DEBUG"
	KeyLogFormat         = "FABRIN_LOG_FORMAT"
	KeyLogLevel          = "FABRIN_LOG_LEVEL"
	KeyShutdownTimeout   = "FABRIN_SHUTDOWN_TIMEOUT"
	KeyReadHeaderTimeout = "FABRIN_READ_HEADER_TIMEOUT"
	KeyTrustedProxies    = "FABRIN_TRUSTED_PROXIES"
)

// knownKeys is the single list of recognised settings. It drives parsing, the
// unknown-key check, and the near-miss suggestion — so a new setting cannot be
// added to one of those three and forgotten in the others.
var knownKeys = []string{
	KeyAddr, KeyModules, KeyDebug, KeyLogFormat, KeyLogLevel,
	KeyShutdownTimeout, KeyReadHeaderTimeout, KeyTrustedProxies,
}

// Lookup supplies key/value pairs for a layer. All is required rather than a
// per-key Get because detecting an unknown FABRIN_ key means enumerating what was
// supplied, not asking about keys we already know.
type Lookup interface {
	All() map[string]string
}

// MapLookup adapts a map to [Lookup]. Useful for tests and for a caller that has
// already parsed settings from somewhere else.
type MapLookup map[string]string

// All implements [Lookup].
func (m MapLookup) All() map[string]string { return m }

// OSEnv returns a [Lookup] over the process environment.
func OSEnv() Lookup {
	out := MapLookup{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// Standard is the conventional source stack for a main function:
//
//	defaults ← .env file ← environment ← command-line flags
//
//	cfg, err := config.Load(config.Standard()...)
//	if err != nil { log.Fatal(err) }
//
// This is the batteries-included answer, and the one the error from [ErrNoSources]
// points at. Spelled as a helper rather than as the behaviour of an empty [Load]
// so that reading the environment stays something the caller asked for — visible
// at the call site, not a hidden default.
//
// It returns a SLICE rather than one composite source on purpose. Provenance is
// recorded per source, so collapsing three layers into one would report every
// value as coming from "standard" and throw away the ability to answer *which
// layer set this*, which is most of the value of [Resolve].
//
// Two consequences of the flag layer, both intentional:
//
//   - It reads os.Args, so this belongs in main, not in a library or a test. In a
//     test binary os.Args carries go test's own -test.* flags, which the flag
//     layer rejects. Build the stack explicitly there, or use [Defaults].
//   - Append to it freely — config.Standard() is just a slice:
//     append(config.Standard(), config.FromRequiredFile(path))...
func Standard() []Source {
	return []Source{
		FromFile(".env"),
		FromEnv(nil),
		FromFlags(os.Args[1:]),
	}
}

// Defaults is a source that supplies nothing, so [Load] applies only its built-in
// defaults.
//
// It exists so that "defaults only" is something a caller can *say*. Load with no
// sources at all is [ErrNoSources], because an empty argument list is far more
// often an oversight than a decision — and this is how a test asks for a
// deterministic configuration without the machine's environment leaking into it.
func Defaults() Source { return defaultsSource{} }

type defaultsSource struct{}

func (defaultsSource) name() string { return "defaults" }

func (defaultsSource) values() (map[string]string, error) { return map[string]string{}, nil }

// Source is one layer of settings. Layers are applied in the order given to
// [Load], each overriding the previous.
type Source interface {
	// name identifies the layer in provenance output and error messages, so a
	// rejected value says which file or which variable to fix.
	name() string

	// values returns the settings this layer supplies. A key absent from the map is
	// not set by this layer — which is how a flag layer avoids writing its zero
	// values over everything beneath it.
	values() (map[string]string, error)
}

// ── Sources ─────────────────────────────────────────────────────────────────

type envSource struct{ lookup Lookup }

// FromEnv reads settings from lookup, or from the process environment when
// lookup is nil.
func FromEnv(lookup Lookup) Source {
	if lookup == nil {
		lookup = OSEnv()
	}
	return envSource{lookup: lookup}
}

func (s envSource) name() string { return "env" }

func (s envSource) values() (map[string]string, error) {
	out := map[string]string{}
	for k, v := range s.lookup.All() {
		if strings.HasPrefix(k, keyPrefix) {
			out[k] = v
		}
	}
	return out, nil
}

type fileSource struct {
	path     string
	required bool
}

// FromFile reads settings from a dotenv-style file. A missing file is NOT an
// error: the usual deployment has no such file and takes its settings from the
// environment, so requiring it would make the common case the error case.
//
// Use [FromRequiredFile] when the caller named the path explicitly.
func FromFile(path string) Source { return fileSource{path: path} }

// FromRequiredFile is [FromFile] except that a missing file is an error.
//
// For a path the user supplied — a --config flag — silently ignoring its absence
// means running with defaults while believing otherwise.
func FromRequiredFile(path string) Source { return fileSource{path: path, required: true} }

func (s fileSource) name() string { return "file " + s.path }

func (s fileSource) values() (map[string]string, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) && !s.required {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrBadFile, s.path, err)
	}
	defer func() { _ = f.Close() }()

	kv, err := parseDotenv(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBadFile, s.path, err)
	}

	out := map[string]string{}
	for k, v := range kv {
		if strings.HasPrefix(k, keyPrefix) {
			out[k] = v
		}
	}
	return out, nil
}

type flagSource struct{ args []string }

// FromFlags reads settings from command-line arguments, e.g. os.Args[1:].
//
// Only flags actually PASSED contribute a value. Help requests contribute no
// setting and are left for [github.com/usefabrin/fabrin.App.Execute] to render.
// A layer that wrote its zero values over everything beneath it would make
// FromFlags erase the environment merely by being present, which is the most
// likely bug in a precedence chain.
func FromFlags(args []string) Source { return flagSource{args: args} }

func (s flagSource) name() string { return "flag" }

func (s flagSource) values() (map[string]string, error) {
	fs := flag.NewFlagSet("fabrin", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the returned error is the diagnostic

	byFlag := map[string]string{
		"addr": KeyAddr, "debug": KeyDebug, "log-format": KeyLogFormat,
		"log-level": KeyLogLevel, "modules": KeyModules,
		"read-header-timeout": KeyReadHeaderTimeout,
		"shutdown-timeout":    KeyShutdownTimeout, "trusted-proxies": KeyTrustedProxies,
	}
	names := make([]string, 0, len(byFlag))
	for name := range byFlag {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if byFlag[name] == KeyDebug {
			fs.Bool(name, false, "enable development conveniences")
			continue
		}
		fs.String(name, "", "see "+byFlag[name])
	}

	parseErr := fs.Parse(s.args)
	if parseErr != nil {
		// Help belongs to the application command surface. Treating it as a
		// configuration failure makes the conventional MustLoad call panic before
		// App.Execute has a chance to print usage.
		if !errors.Is(parseErr, flag.ErrHelp) {
			return nil, fmt.Errorf("config: parse flags: %w", parseErr)
		}
	}

	out := map[string]string{}
	// Visit walks only the flags that were actually set.
	fs.Visit(func(f *flag.Flag) { out[byFlag[f.Name]] = f.Value.String() })
	return out, nil
}

// ── Resolution ──────────────────────────────────────────────────────────────

// Resolved is the outcome of applying every layer: the winning value for each
// key, and which layer set it.
type Resolved struct {
	values  map[string]string
	sources map[string]string
}

// Get returns the resolved value for key, and whether any layer set it.
func (r *Resolved) Get(key string) (string, bool) {
	v, ok := r.values[key]
	return v, ok
}

// SourceOf returns the name of the layer that set key — "default" when no layer
// did, and "" when key is not a recognised setting.
//
// This is the answer to "why is this value what it is", which is the question a
// misconfigured deploy actually poses.
func (r *Resolved) SourceOf(key string) string {
	if s, ok := r.sources[key]; ok {
		return s
	}
	if slices.Contains(knownKeys, key) {
		return "default"
	}
	return ""
}

// String renders every known setting with its value and origin, sorted by key.
// This is what someone pastes into a bug report.
func (r *Resolved) String() string {
	keys := append([]string(nil), knownKeys...)
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		v, set := r.values[k]
		if !set {
			v = "(default)"
		}
		fmt.Fprintf(&b, "%-28s %-24s [%s]\n", k, v, r.SourceOf(k))
	}
	return b.String()
}

// Resolve applies sources in order and reports the winning value and origin for
// each key. It fails on an unrecognised FABRIN_ key.
func Resolve(sources ...Source) (*Resolved, error) {
	// Checked here rather than in Load so that both public entry points get the
	// same answer from one implementation.
	if len(sources) == 0 {
		return nil, fmt.Errorf("%w: nothing would be read, so every FABRIN_ setting would be silently ignored. "+
			"Pass config.Standard()... for the conventional stack (.env, environment, flags), "+
			"or config.Defaults() if defaults really are what you want", ErrNoSources)
	}

	known := make(map[string]bool, len(knownKeys))
	for _, k := range knownKeys {
		known[k] = true
	}

	res := &Resolved{
		values:  map[string]string{},
		sources: map[string]string{},
	}

	for _, src := range sources {
		vals, err := src.values()
		if err != nil {
			return nil, err
		}

		// Sorted so an error naming "the first unknown key" is deterministic rather
		// than dependent on map iteration order.
		keys := make([]string, 0, len(vals))
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if !known[k] {
				// Rejected rather than ignored: a silently dropped typo is how a setting
				// "does not work" with no diagnostic anywhere.
				return nil, fmt.Errorf("%w: %s (in %s)%s", ErrUnknownKey, k, src.name(), suggest(k))
			}
			res.values[k] = vals[k]
			res.sources[k] = src.name()
		}
	}
	return res, nil
}

// suggest offers the closest known key, so a typo's fix is in the error rather
// than in the documentation.
func suggest(key string) string {
	best, bestScore := "", 0
	for _, k := range knownKeys {
		if n := commonPrefixLen(k, key); n > bestScore {
			best, bestScore = k, n
		}
	}
	// A prefix shorter than "FABRIN_" plus a character is not a near miss.
	if best == "" || bestScore <= len(keyPrefix) {
		return ""
	}
	return ", did you mean " + best + "?"
}

func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// ── Loading ─────────────────────────────────────────────────────────────────

// Load resolves sources into [Options] with defaults applied.
//
// The conventional production call is [Standard]:
//
//	cfg, err := config.Load(config.Standard()...)
//
// which is the same as spelling the layers out, and you should when you want
// something other than the convention:
//
//	cfg, err := config.Load(
//	    config.FromFile(".env"),
//	    config.FromEnv(nil),
//	    config.FromFlags(os.Args[1:]),
//	)
//
// With NO sources it fails with [ErrNoSources] rather than returning the
// defaults. Reading nothing is a legitimate thing to want and an illegitimate
// thing to want by accident, so it has to be said out loud — [Defaults].
//
// Every parse failure is reported at load, naming the key and the layer. Failing
// at first use instead means failing in production after a green deploy.
func Load(sources ...Source) (Options, error) {
	res, err := Resolve(sources...)
	if err != nil {
		return Options{}, err
	}

	var o Options
	get := func(key string) (string, bool) { return res.Get(key) }
	fail := func(key, val, want string) error {
		return fmt.Errorf("%w: %s=%q in %s: want %s",
			ErrInvalidValue, key, val, res.SourceOf(key), want)
	}

	if v, ok := get(KeyAddr); ok {
		o.Addr = v
	}
	if v, ok := get(KeyModules); ok {
		o.Modules = splitList(v)
	}
	if v, ok := get(KeyTrustedProxies); ok {
		o.TrustedProxies = splitList(v)
	}

	if v, ok := get(KeyDebug); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Options{}, fail(KeyDebug, v, "a boolean (true, false, 1, 0)")
		}
		o.Debug = b
	}

	for _, spec := range []struct {
		key  string
		dest *time.Duration
	}{
		{KeyShutdownTimeout, &o.ShutdownTimeout},
		{KeyReadHeaderTimeout, &o.ReadHeaderTimeout},
	} {
		v, ok := get(spec.key)
		if !ok {
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return Options{}, fail(spec.key, v, `a duration such as "15s" or "1m30s"`)
		}
		if d < 0 {
			return Options{}, fail(spec.key, v, "a non-negative duration")
		}
		*spec.dest = d
	}

	if v, ok := get(KeyLogFormat); ok {
		if v != "json" && v != "text" {
			return Options{}, fail(KeyLogFormat, v, `"json" or "text"`)
		}
		o.LogFormat = v
	}
	if v, ok := get(KeyLogLevel); ok {
		switch v {
		case "debug", "info", "warn", "error":
			o.LogLevel = v
		default:
			return Options{}, fail(KeyLogLevel, v, `"debug", "info", "warn", or "error"`)
		}
	}

	return o.WithDefaults(), nil
}

// MustLoad is [Load] but panics on error.
//
// For a main function where a bad configuration means the program cannot start
// anyway, so an error return would only be turned into the same exit. The name is
// the warning; do not use it in library code.
func MustLoad(sources ...Source) Options {
	o, err := Load(sources...)
	if err != nil {
		panic(err)
	}
	return o
}

// splitList parses a comma-separated list, dropping empty entries.
//
// A trailing comma or an empty value is what a shell produces from an unset
// variable or a generated compose file. Turning that into an entry of "" would
// produce a module named "", which then fails selection with a baffling error
// about a module nobody wrote.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
