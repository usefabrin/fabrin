package logging_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/usefabrin/fabrin/logging"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// engine wires the middleware under test plus one route that reports what it saw
// on the context.
func engine(log *bytes.Buffer, level string) *gin.Engine {
	e := gin.New()
	e.Use(logging.RequestID(), logging.Logger(logging.New(log, "json", level)))
	e.GET("/x", func(c *gin.Context) {
		c.String(http.StatusOK, logging.RequestIDFromContext(c.Request.Context()))
	})
	e.GET("/boom", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })
	return e
}

func do(e *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// ── LOG-001: the id must be on the context AND in the response ──────────────

func TestRequestID_OnContextAndInResponseHeaderAndLog(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	rec := do(engine(&out, "info"), "/x", nil)

	header := rec.Header().Get(logging.HeaderRequestID)
	if header == "" {
		t.Fatal("response must carry the request id: it is what a user can quote from a failed request")
	}

	// The handler echoed what it read off the context. If these differ, a log line
	// written inside the handler cannot be correlated with the header the user saw.
	if body := rec.Body.String(); body != header {
		t.Errorf("context id %q != header id %q; both halves must agree or correlation is impossible", body, header)
	}

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, out.String())
	}
	if line[logging.LogKeyRequestID] != header {
		t.Errorf("log %s = %v, want %q", logging.LogKeyRequestID, line[logging.LogKeyRequestID], header)
	}
	if line["status"] != float64(200) || line["path"] != "/x" {
		t.Errorf("log line missing request detail: %v", line)
	}
}

func TestRequestID_IsUniquePerRequest(t *testing.T) {
	t.Parallel()

	e := engine(&bytes.Buffer{}, "info")
	seen := map[string]bool{}
	for range 50 {
		id := do(e, "/x", nil).Header().Get(logging.HeaderRequestID)
		if seen[id] {
			t.Fatalf("duplicate request id %q", id)
		}
		seen[id] = true
	}
}

func TestRequestID_HonoursInboundIDSoTracesSurviveAHop(t *testing.T) {
	t.Parallel()

	const upstream = "trace-from-upstream-123"
	rec := do(engine(&bytes.Buffer{}, "info"), "/x", map[string]string{
		logging.HeaderRequestID: upstream,
	})

	// Replacing the inbound id breaks the trace at every service boundary, which is
	// exactly where a distributed trace is most needed.
	if got := rec.Header().Get(logging.HeaderRequestID); got != upstream {
		t.Errorf("inbound id not honoured: got %q, want %q", got, upstream)
	}
	if body := rec.Body.String(); body != upstream {
		t.Errorf("context id = %q, want the inbound %q", body, upstream)
	}
}

func TestRequestID_RejectsHostileInboundValues(t *testing.T) {
	t.Parallel()

	hostile := map[string]string{
		"header injection":  "abc\r\nX-Admin: true",
		"newline log forge": "abc\nlevel=INFO msg=\"fake line\"",
		"control char":      "abc\x00def",
		"too long":          strings.Repeat("a", 65),
		"spaces":            "not a valid id",
		"unicode":           "id-→-arrow",
	}

	e := engine(&bytes.Buffer{}, "info")
	for name, val := range hostile {
		t.Run(name, func(t *testing.T) {
			rec := do(e, "/x", map[string]string{logging.HeaderRequestID: val})
			got := rec.Header().Get(logging.HeaderRequestID)

			// The id reaches log files and response headers, so an unvalidated inbound
			// value is a header-injection and log-forging vector. Generating a fresh id
			// is always a valid answer.
			if got == val {
				t.Errorf("hostile id %q was echoed back verbatim", val)
			}
			if got == "" {
				t.Error("a rejected inbound id must still yield a generated one")
			}
			for _, bad := range []string{"\r", "\n", "\x00", " "} {
				if strings.Contains(got, bad) {
					t.Errorf("emitted id %q contains %q", got, bad)
				}
			}
		})
	}
}

func TestRequestIDFromContext_EmptyWhenAbsent(t *testing.T) {
	t.Parallel()

	if got := logging.RequestIDFromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context()); got != "" {
		t.Errorf("want empty for a context with no id, got %q", got)
	}
}

// ── Logger levels ───────────────────────────────────────────────────────────

func TestLogger_LevelsBySeverityOfTheResponse(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	e := engine(&out, "info")

	do(e, "/x", nil)
	do(e, "/boom", nil)
	do(e, "/missing", nil)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d:\n%s", len(lines), out.String())
	}

	// A 500 logged at info hides an outage in the noise; a 200 logged at error
	// trains people to ignore errors. Both defeat the purpose of levels.
	want := []string{"INFO", "ERROR", "WARN"}
	for i, level := range want {
		var line map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &line); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if line["level"] != level {
			t.Errorf("line %d (%v) level = %v, want %s", i, line["path"], line["level"], level)
		}
	}
}

func TestNew_HonoursFormatAndLevel(t *testing.T) {
	t.Parallel()

	var jsonOut, textOut bytes.Buffer
	logging.New(&jsonOut, "json", "info").Info("hello", "k", "v")
	logging.New(&textOut, "text", "info").Info("hello", "k", "v")

	if !strings.HasPrefix(strings.TrimSpace(jsonOut.String()), "{") {
		t.Errorf("json format must emit JSON, got %s", jsonOut.String())
	}
	if strings.HasPrefix(strings.TrimSpace(textOut.String()), "{") {
		t.Errorf("text format must not emit JSON, got %s", textOut.String())
	}

	var quiet bytes.Buffer
	log := logging.New(&quiet, "json", "error")
	log.Info("suppressed")
	log.Debug("suppressed")
	if quiet.Len() != 0 {
		t.Errorf("level=error must suppress info and debug, got %s", quiet.String())
	}
	log.Error("kept")
	if quiet.Len() == 0 {
		t.Error("level=error must still emit errors")
	}
}

func TestNew_UnrecognisedValuesFallBackRatherThanFailing(t *testing.T) {
	t.Parallel()

	// config validates these before they get here. If something slips through, a
	// logger that refuses to exist leaves the caller with no way to report why.
	var out bytes.Buffer
	log := logging.New(&out, "yaml", "verbose")
	log.Info("still works")

	if out.Len() == 0 {
		t.Fatal("unrecognised format/level must fall back to a working logger")
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("fallback format should be JSON, got %s", out.String())
	}
}

func TestContextWithRequestID_PropagatesToBackgroundWork(t *testing.T) {
	t.Parallel()

	// A job spawned from a request should stay correlated with the request that
	// started it, which needs the id to be settable outside middleware.
	ctx := logging.ContextWithRequestID(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "abc123")
	if got := logging.RequestIDFromContext(ctx); got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
}

// ── Attribution benchmarks ──────────────────────────────────────────────────
//
// These exist so that the per-request cost recorded in perf/BASELINE.md is
// ATTRIBUTABLE. The root package's BenchmarkFabrin_OneRoute reports one number
// for the whole default stack; when that number moves, these say which half
// moved. Without them a future regression is a bisect rather than a reading.
//
// Every one writes to io.Discard: the sink's cost belongs to the deployment, and
// benchmarking against a terminal or a CI pipe measures the machine.

func benchMiddleware(b *testing.B, mw ...gin.HandlerFunc) {
	b.Helper()
	gin.SetMode(gin.ReleaseMode)

	e := gin.New()
	e.Use(mw...)
	e.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// BenchmarkBare is the floor: the same engine and route with no middleware, so
// the others read as deltas rather than absolutes.
func BenchmarkBare(b *testing.B) { benchMiddleware(b) }

func BenchmarkRequestID(b *testing.B) { benchMiddleware(b, logging.RequestID()) }

func BenchmarkLogger(b *testing.B) { benchMiddleware(b, logging.Logger(discardLogger())) }

// BenchmarkRequestIDAndLogger is the combination Fabrin actually installs.
func BenchmarkRequestIDAndLogger(b *testing.B) {
	benchMiddleware(b, logging.RequestID(), logging.Logger(discardLogger()))
}
