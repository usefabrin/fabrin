// Package config resolves Fabrin's settings — the equivalent of Django's
// settings.py, without the global.
//
// Settings resolve through ordered layers, later winning over earlier:
//
//	defaults ← file ← environment ← flags
//
// Every resolved value remembers which layer set it, because on a misconfigured
// deploy you can usually see the wrong value and not where it came from, and that
// is most of the debugging time.
//
// # Why Options lives here
//
// [Options] is the type [github.com/usefabrin/fabrin.New] accepts; the root
// package aliases it. It is declared here rather than there because the boundary
// rules forbid this package from importing the root package — settings must load
// from a CLI, from a test, or from a migrate-only process without constructing an
// HTTP stack. Declaring Options in the root package would make the obvious
// signature, Load() (fabrin.Options, error), a rule violation. Settings are a
// lower-level concern than the application, so the dependency runs root → config,
// and the alias keeps fabrin.Options working for every caller.
package config

import (
	"log/slog"
	"time"
)

// Defaults applied when no layer sets the corresponding value.
const (
	// DefaultAddr is the listen address, overridden by FABRIN_ADDR.
	DefaultAddr = ":8080"

	// DefaultShutdownTimeout bounds how long Run waits for in-flight requests.
	// Unbounded shutdown hangs a deploy; zero drops requests that were nearly done.
	DefaultShutdownTimeout = 15 * time.Second

	// DefaultReadHeaderTimeout guards against a client that opens a connection and
	// sends headers slowly, holding a goroutine indefinitely. Go's http.Server zero
	// value for this is "no timeout", which is why it must be set explicitly.
	DefaultReadHeaderTimeout = 10 * time.Second

	// DefaultLogFormat and DefaultLogLevel configure fabrin/logging.
	// JSON by default because the default deployment target is a log aggregator,
	// not a terminal.
	DefaultLogFormat = "json"
	DefaultLogLevel  = "info"
)

// Options configures a Fabrin application. Every field has a working default, so
// Options{} is valid and [Load] with no sources returns a usable value.
//
// This is a plain struct rather than variadic functional options because it is
// what [Load] produces, and an options-function API would force every caller to
// translate between two shapes. The consequence is a constraint on this type:
// fields may only be ADDED, never removed or retyped, without a breaking change.
type Options struct {
	// Addr is the listen address. Empty means [DefaultAddr]. Env: FABRIN_ADDR.
	Addr string

	// Modules selects which registered modules this process mounts. Empty means
	// all of them, and a name matching no registered module is a startup error
	// rather than a silent no-op.
	//
	// This is the process-slicing mechanism: one binary, many deployment shapes.
	// Env: FABRIN_MODULES (comma-separated).
	//
	// Note the asymmetry with the App's Modules method: this field is the
	// SELECTION, while that reports what actually got MOUNTED. They differ whenever
	// a selection is in effect — which is exactly when you are debugging a 404.
	Modules []string

	// Debug enables development conveniences. Env: FABRIN_DEBUG.
	Debug bool

	// Logger receives Fabrin's own lifecycle messages, and is the logger the
	// request-logging middleware writes to. Nil means one built from [Options.LogFormat]
	// and [Options.LogLevel] — see [Options.WithDefaults] for why that happens in the
	// root package rather than here.
	//
	// Passed in rather than read from a package-level global so a consumer can
	// silence or redirect Fabrin without affecting anything else in their process.
	// Not settable from a config layer — it is a value, not a string.
	Logger *slog.Logger

	// LogFormat is "json" or "text". Empty means [DefaultLogFormat].
	// Env: FABRIN_LOG_FORMAT.
	LogFormat string

	// LogLevel is "debug", "info", "warn", or "error". Empty means
	// [DefaultLogLevel]. Env: FABRIN_LOG_LEVEL.
	LogLevel string

	// ShutdownTimeout bounds graceful shutdown. Zero means
	// [DefaultShutdownTimeout]. Env: FABRIN_SHUTDOWN_TIMEOUT.
	ShutdownTimeout time.Duration

	// ReadHeaderTimeout bounds how long a client may take to send request headers.
	// Zero means [DefaultReadHeaderTimeout]. Env: FABRIN_READ_HEADER_TIMEOUT.
	ReadHeaderTimeout time.Duration

	// TrustedProxies is passed to Gin. Nil means trust none, which is the safe
	// default: Gin's own default trusts every proxy, so a spoofed X-Forwarded-For
	// becomes the client IP. Env: FABRIN_TRUSTED_PROXIES (comma-separated).
	TrustedProxies []string
}

// WithDefaults returns a copy of o with zero fields replaced by their defaults.
//
// Exported because the root package applies it too: a caller who builds Options
// by hand rather than through [Load] must get the same defaults, and duplicating
// the list in two packages is how they drift.
//
// # Logger is the one field left zero, deliberately
//
// A nil Logger stays nil here. Its default is not a constant — it is a logger
// built from [Options.LogFormat] and [Options.LogLevel] by fabrin/logging, and
// the boundary rules forbid this package from importing that one (settings must
// load without dragging in half the framework). So the root package fills it
// immediately after calling this, and [github.com/usefabrin/fabrin.New] is the
// only place a Logger is constructed.
//
// This used to read `o.Logger = slog.Default()`, which looked like a harmless
// default and quietly made LogFormat and LogLevel inert: both resolved from the
// environment, both validated, and neither ever reached a handler. Restoring
// that line reintroduces the bug.
func (o Options) WithDefaults() Options {
	if o.Addr == "" {
		o.Addr = DefaultAddr
	}
	if o.LogFormat == "" {
		o.LogFormat = DefaultLogFormat
	}
	if o.LogLevel == "" {
		o.LogLevel = DefaultLogLevel
	}
	if o.ShutdownTimeout == 0 {
		o.ShutdownTimeout = DefaultShutdownTimeout
	}
	if o.ReadHeaderTimeout == 0 {
		o.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	return o
}
