// Package logging builds Fabrin's structured logger and the request-id middleware
// that makes a user-reported error findable.
//
// The logger is built and returned, never installed into a package-level global. A
// library that writes to a global sink cannot be silenced or redirected by its
// user, and its output cannot be captured by a test without racing every other
// test in the binary.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// HeaderRequestID is the response header carrying the request id.
//
// X-Request-ID rather than a Fabrin-specific name because proxies, log
// aggregators, and client libraries already understand it — a bespoke header
// would need configuration everywhere to be useful.
const HeaderRequestID = "X-Request-ID"

// LogKeyRequestID is the attribute key under which the request id is logged.
const LogKeyRequestID = "request_id"

// MsgRequest is the message [Logger] writes for every completed request.
//
// Constant rather than interpolated so that an aggregator can group, count, and
// alert on request lines. Exported so a consumer's log query can reference it
// instead of hard-coding the string.
const MsgRequest = "request"

// contextKey is unexported so no other package can collide with our context key,
// which is why a bare string is the wrong choice here.
type contextKey struct{ name string }

var requestIDKey = contextKey{name: "fabrin.request_id"}

// New builds a [slog.Logger] writing to w.
//
// format is "json" or "text"; level is "debug", "info", "warn", or "error". Both
// are validated by fabrin/config before reaching here, so an unrecognised value
// falls back to the safe choice rather than failing — a logger that refuses to
// exist leaves the caller with no way to report why.
func New(w io.Writer, format, level string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	switch strings.ToLower(format) {
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		// JSON by default because the default deployment target is a log
		// aggregator, not a terminal.
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// RequestIDFromContext returns the request id carried by ctx, or "".
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// ContextWithRequestID returns a copy of ctx carrying id.
//
// Exported so a background job spawned from a request can inherit the id and stay
// correlated with the request that started it.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID middleware assigns each request an id, puts it on the request context,
// and returns it in the [HeaderRequestID] response header.
//
// Both halves matter: the header is what a user can quote from a failed request,
// and the context is what lets every log line from that request carry the same id.
// Without one of them, "find the logs for this error" is a text search over
// timestamps.
//
// An inbound [HeaderRequestID] is honoured rather than replaced, so a trace
// started by an upstream proxy or another service survives the hop. Inbound values
// are sanitised: the id reaches log files and response headers, so an unvalidated
// one is a header-injection and log-forging vector.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitizeID(c.GetHeader(HeaderRequestID))
		if id == "" {
			id = newID()
		}

		c.Request = c.Request.WithContext(ContextWithRequestID(c.Request.Context(), id))
		// Set before the handler chain runs, so the id is present even on a response
		// written by a panic recovery.
		c.Header(HeaderRequestID, id)

		// Deliberately NOT also c.Set(LogKeyRequestID, id). That would allocate Gin's
		// Keys map on every request to store a value already reachable through
		// [RequestIDFromContext] — measured at three allocations for a second copy of
		// one string. Gin's types are blessed; duplicating state into them is not.

		c.Next()
	}
}

// maxIDLen bounds an accepted inbound id. Unbounded, a client controls how much
// every log line for its request costs to store.
const maxIDLen = 64

// sanitizeID keeps only characters that are safe in a header value and a log
// field, and returns "" when nothing usable remains.
//
// Rejecting outright rather than escaping: a caller sending a control character in
// a request id is not doing anything Fabrin should try to preserve, and generating
// a fresh id is always a valid answer.
func sanitizeID(s string) string {
	if s == "" || len(s) > maxIDLen {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return ""
		}
	}
	return s
}

// newID returns a random 128-bit hex id.
//
// crypto/rand rather than math/rand: request ids appear in logs and are sometimes
// treated as unguessable by the systems that consume them. It cannot fail on any
// supported platform — Read is documented to always succeed or crash the program —
// so there is no error path to handle.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Logger middleware logs one line per completed request, including the request
// id. It also recovers downstream handler panics so the failure is logged: an
// uncommitted response becomes 500, while an already committed status is kept
// and the request is still logged at error level with panic_recovered=true.
//
// Deliberately not gin.Logger(): that writes unstructured text to a global writer,
// which defeats both the structured-logging and the no-global goals.
func Logger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		recoveredPanic := false
		defer func() {
			if recover() != nil {
				recoveredPanic = true
				c.AbortWithStatus(http.StatusInternalServerError)
			}

			attrs := []any{
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"status", c.Writer.Status(),
			}
			if id := RequestIDFromContext(c.Request.Context()); id != "" {
				attrs = append(attrs, LogKeyRequestID, id)
			}

			// Gin collects handler errors rather than returning them, so they are
			// invisible unless read back here.
			if len(c.Errors) > 0 {
				attrs = append(attrs, "errors", c.Errors.String())
			}
			if recoveredPanic {
				attrs = append(attrs, "panic_recovered", true)
			}

			// A CONSTANT message, with the varying parts as attributes. "GET /users/123"
			// as the message would make every distinct path its own message string,
			// which defeats grouping and alerting in every log aggregator — the thing
			// structured logging exists to enable. Method and path are already attrs
			// above, so the interpolated form also duplicated them.
			switch {
			case recoveredPanic || c.Writer.Status() >= 500:
				log.Error(MsgRequest, attrs...)
			case c.Writer.Status() >= 400:
				log.Warn(MsgRequest, attrs...)
			default:
				log.Info(MsgRequest, attrs...)
			}
		}()

		c.Next()
	}
}
