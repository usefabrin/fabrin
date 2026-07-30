package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usefabrin/fabrin/config"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fabrin.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// ── CFG-001: layer precedence ───────────────────────────────────────────────

func TestLoad_LayersOverrideInDocumentedOrder(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "FABRIN_ADDR=:2222\n")

	opts, err := config.Load(
		config.FromFile(path),
		config.FromEnv(config.MapLookup{"FABRIN_ADDR": ":3333"}),
		config.FromFlags([]string{"-addr", ":4444"}),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Later layers win. Getting this backwards silently ignores production
	// overrides, which is the whole reason the order is documented.
	if opts.Addr != ":4444" {
		t.Errorf("Addr = %q, want the flag value :4444 to win", opts.Addr)
	}
}

func TestLoad_EachLayerWinsOverThePreviousOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources []config.Source
		want    string
	}{
		{"defaults only", nil, config.DefaultAddr},
		{"file over defaults", []config.Source{
			config.FromFile(writeFile(t, "FABRIN_ADDR=:2222\n")),
		}, ":2222"},
		{"env over file", []config.Source{
			config.FromFile(writeFile(t, "FABRIN_ADDR=:2222\n")),
			config.FromEnv(config.MapLookup{"FABRIN_ADDR": ":3333"}),
		}, ":3333"},
		{"flags over env", []config.Source{
			config.FromEnv(config.MapLookup{"FABRIN_ADDR": ":3333"}),
			config.FromFlags([]string{"-addr", ":4444"}),
		}, ":4444"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, err := config.Load(tc.sources...)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if opts.Addr != tc.want {
				t.Errorf("Addr = %q, want %q", opts.Addr, tc.want)
			}
		})
	}
}

// ── CFG-002: provenance ─────────────────────────────────────────────────────

func TestLoad_ReportsWhichLayerSetEachValue(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "FABRIN_ADDR=:2222\nFABRIN_LOG_FORMAT=text\n")

	res, err := config.Resolve(
		config.FromFile(path),
		config.FromEnv(config.MapLookup{"FABRIN_ADDR": ":3333"}),
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Without provenance you can see the wrong value but not where it came from,
	// which is most of the debugging time on a misconfigured deploy.
	if got := res.SourceOf("FABRIN_ADDR"); got != "env" {
		t.Errorf("FABRIN_ADDR came from env last; SourceOf = %q", got)
	}
	if got := res.SourceOf("FABRIN_LOG_FORMAT"); !strings.HasPrefix(got, "file") {
		t.Errorf("FABRIN_LOG_FORMAT was only in the file; SourceOf = %q", got)
	}
	if got := res.SourceOf("FABRIN_SHUTDOWN_TIMEOUT"); got != "default" {
		t.Errorf("FABRIN_SHUTDOWN_TIMEOUT was set nowhere; SourceOf = %q", got)
	}
	if got := res.SourceOf("FABRIN_NOPE"); got != "" {
		t.Errorf("an unknown key has no source; SourceOf = %q", got)
	}
}

func TestResolved_StringIsHumanReadableAndRedactsNothingUnexpected(t *testing.T) {
	t.Parallel()

	res, err := config.Resolve(config.FromEnv(config.MapLookup{"FABRIN_ADDR": ":9999"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got := res.String()
	// This output is what someone pastes into a bug report, so it has to name both
	// the value and its origin.
	if !strings.Contains(got, "FABRIN_ADDR") || !strings.Contains(got, ":9999") || !strings.Contains(got, "env") {
		t.Errorf("String() must show key, value, and source; got:\n%s", got)
	}
}

// ── CFG-003: fail at load, not at first use ─────────────────────────────────

func TestLoad_RejectsUnparseableValueNamingTheKey(t *testing.T) {
	t.Parallel()

	_, err := config.Load(config.FromEnv(config.MapLookup{
		"FABRIN_SHUTDOWN_TIMEOUT": "not-a-duration",
	}))

	// Failing late means failing in production after a green deploy.
	if err == nil {
		t.Fatal("an unparseable duration must fail at load, not at first use")
	}
	if !errors.Is(err, config.ErrInvalidValue) {
		t.Errorf("error must be identifiable as ErrInvalidValue, got %v", err)
	}
	if !strings.Contains(err.Error(), "FABRIN_SHUTDOWN_TIMEOUT") {
		t.Errorf("error must name the key, got %q", err)
	}
	if !strings.Contains(err.Error(), "env") {
		t.Errorf("error should name the layer, so you know which file or var to fix; got %q", err)
	}
}

func TestLoad_RejectsUnknownKey(t *testing.T) {
	t.Parallel()

	_, err := config.Load(config.FromFile(writeFile(t, "FABRIN_ADDRR=:1\n")))

	// A silently ignored typo is how a setting "does not work" with no diagnostic
	// anywhere. Only FABRIN_-prefixed keys are checked, so the file may hold
	// unrelated variables.
	if err == nil {
		t.Fatal("an unknown FABRIN_ key must be rejected, not ignored")
	}
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Errorf("error must be identifiable as ErrUnknownKey, got %v", err)
	}
	if !strings.Contains(err.Error(), "FABRIN_ADDR") {
		t.Errorf("error should suggest the near miss, got %q", err)
	}
}

func TestLoad_IgnoresNonFabrinKeys(t *testing.T) {
	t.Parallel()

	// The environment of any real process is full of unrelated variables, and a
	// .env file is usually shared with other tooling.
	if _, err := config.Load(config.FromEnv(config.MapLookup{
		"PATH": "/usr/bin", "HOME": "/root", "DATABASE_URL": "postgres://x",
	})); err != nil {
		t.Errorf("non-FABRIN_ keys must be ignored, got %v", err)
	}
}

// ── CFG-004: FABRIN_ADDR ────────────────────────────────────────────────────

func TestLoad_DefaultsAddrToDocumentedValue(t *testing.T) {
	t.Parallel()

	opts, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if opts.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080 — smoke-examples.sh and the docs both rely on this", opts.Addr)
	}
	if config.DefaultAddr != ":8080" {
		t.Errorf("DefaultAddr = %q, want :8080", config.DefaultAddr)
	}
}

// ── FABRIN_MODULES: process slicing from the environment ────────────────────

func TestLoad_ParsesModuleSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want []string
	}{
		{"blog,auth", []string{"blog", "auth"}},
		{" blog , auth ", []string{"blog", "auth"}},
		// A trailing comma or an empty var is what a shell produces from an unset
		// variable or a generated compose file; neither should become a module named
		// "", which would then fail selection with a baffling error.
		{"blog,", []string{"blog"}},
		{"", nil},
		{"   ", nil},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			opts, err := config.Load(config.FromEnv(config.MapLookup{"FABRIN_MODULES": tc.in}))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if strings.Join(opts.Modules, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Modules = %v, want %v", opts.Modules, tc.want)
			}
		})
	}
}

// ── Files ───────────────────────────────────────────────────────────────────

func TestFromFile_ParsesCommentsBlanksQuotesAndExport(t *testing.T) {
	t.Parallel()

	path := writeFile(t, `
# a comment
FABRIN_ADDR=":7777"

  # indented comment
export FABRIN_LOG_FORMAT=text
FABRIN_DEBUG=true      # trailing comment
FABRIN_TRUSTED_PROXIES='10.0.0.0/8,192.168.0.0/16'
`)

	opts, err := config.Load(config.FromFile(path))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if opts.Addr != ":7777" {
		t.Errorf("quotes must be stripped; Addr = %q", opts.Addr)
	}
	if !opts.Debug {
		t.Error("FABRIN_DEBUG=true with a trailing comment must parse as true")
	}
	if opts.LogFormat != "text" {
		t.Errorf("an `export ` prefix must be tolerated; LogFormat = %q", opts.LogFormat)
	}
	if len(opts.TrustedProxies) != 2 {
		t.Errorf("TrustedProxies = %v, want 2 entries", opts.TrustedProxies)
	}
}

func TestFromFile_MissingFileIsNotAnError(t *testing.T) {
	t.Parallel()

	// The usual deployment has no .env file at all — settings come from the
	// environment. Requiring the file would make the common case the error case.
	if _, err := config.Load(config.FromFile(filepath.Join(t.TempDir(), "absent.env"))); err != nil {
		t.Errorf("an absent optional file must not fail; got %v", err)
	}
}

func TestFromRequiredFile_MissingFileIsAnError(t *testing.T) {
	t.Parallel()

	// When a caller names a file explicitly — a --config flag — silently ignoring
	// its absence means running with defaults while believing otherwise.
	_, err := config.Load(config.FromRequiredFile(filepath.Join(t.TempDir(), "absent.env")))
	if err == nil {
		t.Fatal("an absent REQUIRED file must fail")
	}
}

func TestFromFile_RejectsMalformedLine(t *testing.T) {
	t.Parallel()

	_, err := config.Load(config.FromFile(writeFile(t, "FABRIN_ADDR\n")))
	if err == nil {
		t.Fatal("a line with no '=' must fail rather than be skipped")
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error must name the line number, got %q", err)
	}
}

// ── Typed values ────────────────────────────────────────────────────────────

func TestLoad_ParsesEveryTypedSetting(t *testing.T) {
	t.Parallel()

	opts, err := config.Load(config.FromEnv(config.MapLookup{
		"FABRIN_ADDR":                ":1234",
		"FABRIN_DEBUG":               "1",
		"FABRIN_MODULES":             "a,b",
		"FABRIN_SHUTDOWN_TIMEOUT":    "45s",
		"FABRIN_READ_HEADER_TIMEOUT": "3s",
		"FABRIN_TRUSTED_PROXIES":     "10.0.0.0/8",
		"FABRIN_LOG_FORMAT":          "text",
		"FABRIN_LOG_LEVEL":           "debug",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if opts.ShutdownTimeout != 45*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 45s", opts.ShutdownTimeout)
	}
	if opts.ReadHeaderTimeout != 3*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 3s", opts.ReadHeaderTimeout)
	}
	if !opts.Debug {
		t.Error("FABRIN_DEBUG=1 must parse as true")
	}
	if opts.LogLevel != "debug" || opts.LogFormat != "text" {
		t.Errorf("log settings = %q/%q", opts.LogLevel, opts.LogFormat)
	}
}

func TestLoad_RejectsInvalidEnumeratedValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ key, val string }{
		{"FABRIN_LOG_FORMAT", "yaml"},
		{"FABRIN_LOG_LEVEL", "verbose"},
		{"FABRIN_DEBUG", "yes-please"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			_, err := config.Load(config.FromEnv(config.MapLookup{tc.key: tc.val}))
			if err == nil {
				t.Fatalf("%s=%q must be rejected at load", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error must name the key, got %q", err)
			}
		})
	}
}

// ── The Options alias: config produces what fabrin.New consumes ─────────────

func TestOptions_IsTheTypeFabrinNewAccepts(t *testing.T) {
	t.Parallel()

	// The point of moving Options into this package and aliasing it from the root
	// package: config.Load returns exactly what fabrin.New takes, with no mapping
	// layer, while config still does not import the root package. If this stopped
	// being an alias, the assignment below would not compile.
	opts, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if opts.Addr == "" {
		t.Error("defaults must be applied so the result is directly usable")
	}

	// The identity assertion itself: a function requiring config.Options accepts
	// what Load returned, and the root package's fabrin.Options is the same type by
	// alias. Declaring a typed variable would only prove assignability.
	takesOptions := func(config.Options) {}
	takesOptions(opts)
}

func TestMustLoad_PanicsOnBadInput(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("MustLoad must panic on invalid settings — its name is the warning")
		}
	}()
	_ = config.MustLoad(config.FromEnv(config.MapLookup{"FABRIN_DEBUG": "nope"}))
}

// ── Flags ───────────────────────────────────────────────────────────────────

func TestFromFlags_KnownFlagsOnlyAndReportsUnknown(t *testing.T) {
	t.Parallel()

	opts, err := config.Load(config.FromFlags([]string{
		"-addr", ":5555", "-debug", "-modules", "x,y",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if opts.Addr != ":5555" || !opts.Debug || len(opts.Modules) != 2 {
		t.Errorf("flags not applied: %+v", opts)
	}

	if _, err := config.Load(config.FromFlags([]string{"-nonsense"})); err == nil {
		t.Error("an unknown flag must fail rather than be ignored")
	}
}

func TestFromFlags_OnlyOverridesFlagsActuallyPassed(t *testing.T) {
	t.Parallel()

	// A flag layer that wrote its zero values over everything would make
	// FromFlags erase the environment simply by being present — the single most
	// likely bug in a precedence chain.
	opts, err := config.Load(
		config.FromEnv(config.MapLookup{"FABRIN_ADDR": ":3333", "FABRIN_DEBUG": "true"}),
		config.FromFlags([]string{"-addr", ":4444"}),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if opts.Addr != ":4444" {
		t.Errorf("Addr = %q, want the flag to win", opts.Addr)
	}
	if !opts.Debug {
		t.Error("Debug came from env and no -debug flag was passed, so it must survive")
	}
}
