# Performance baseline

Fabrin's cost over raw Gin, measured rather than claimed.

## Why this file exists

A framework that says "fast" without a number is marketing. More usefully: the
number is what makes a **regression** visible. Fabrin adds a module registry, a
lifecycle, request ids, and a readiness aggregator to what Gin already does. Each
of those is defensible; all of them together, unmeasured, is how a framework ends
up 3× slower than the router it wraps and nobody can say which commit did it.

The comparison is deliberately against **raw `gin.Engine`**, not against other
frameworks. A benchmark against a competitor measures two sets of choices at once
and tells you nothing actionable. Against bare Gin it answers the only question a
user actually has: *what does Fabrin cost me over the router I would have used
anyway?*

## What is measured

`just bench` runs `go test -bench . -benchmem ./...`. Two groups matter:

- The root package serves one trivial route two ways — through a bare
  `gin.Engine`, and through a `fabrin.App` mounting one module — which is the
  headline "what does Fabrin cost me" number.
- `logging/` isolates each middleware against the same bare engine, so when the
  headline number moves, there is a reading rather than a bisect.

Every benchmark points its logger at `io.Discard`. That is not flattery: raw Gin
logs nothing, so a stderr-backed logger would measure the terminal or the CI
runner's pipe buffer rather than the framework, and the number would move with
the machine instead of with the code. The sink's cost is the deployment's to
choose; the stack's cost is ours to keep honest.

Allocations per request are the number to watch most closely. Nanoseconds vary
with the machine; an allocation that appears in the hot path is a design mistake
that shows up identically everywhere.

## Baseline

Recorded from a **local** run, not from CI: shared runners vary enough between
jobs that their absolute numbers are not comparable over time.

Go 1.26.0 · darwin/arm64 · Apple M3 Pro · `-count=6`, median of six.

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `BenchmarkRawGin_OneRoute` | 347 | 1040 | **9** |
| `BenchmarkFabrin_OneRoute` | 1518 | 1794 | **22** |

Attribution, from `logging/`:

| Benchmark | ns/op | B/op | allocs/op | delta vs bare |
|---|---|---|---|---|
| `BenchmarkBare` | 337 | 1040 | 9 | — |
| `BenchmarkRequestID` | 793 | 1536 | 17 | +8 allocs |
| `BenchmarkLogger` | 857 | 1089 | 12 | +3 allocs |
| `BenchmarkRequestIDAndLogger` | 1456 | 1794 | **22** | +13 allocs |

### The claim, in two halves

**Fabrin's own abstractions still add zero allocations per request.** That is the
half to defend, and the table proves it: `BenchmarkFabrin_OneRoute` and
`BenchmarkRequestIDAndLogger` both land on **22 allocs and 1794 B**. Everything
Fabrin adds beyond the two middleware — the module registry, the per-module route
group, the capability map, the readiness registry — costs nothing measurable,
because all of it resolves at construction time and none of it touches the
request path.

**The default observability stack adds 13 allocations.** Request ids and one
structured log line per request are per-request work by definition; unlike a
registry, they cannot be moved to startup. Itemised:

- **`RequestID` (+8).** Five are `http.Request.WithContext` — a shallow clone of
  the request plus the context value — which is the standard way to attach a value
  to a request and the feature does not exist without it. Two are the response
  header. One is the hex id itself.
- **`Logger` (+3).** The `slog.Record` and its attribute slice. The handler is
  chosen once at construction, not per request.

The largest *time* component is not an allocation at all: `crypto/rand.Read` for
16 bytes costs ~200 ns on this machine, about a fifth of the total. That is the
deliberate price of ids that are unguessable, which some consumers rely on.
Batching randomness into a per-P pool would recover it and is not worth the
concurrency machinery at this scale.

Two things were removed rather than recorded, when measurement showed them to be
waste and not features — see the comments in `logging/logging.go`:

- `c.Set(LogKeyRequestID, id)`, which allocated Gin's `Keys` map on every request
  to store a second copy of a string already reachable through
  `RequestIDFromContext`. **−3 allocs.**
- `fmt.Sprintf("%s %s", method, path)` as the log message. A constant message with
  the varying parts as attributes is better structured logging — an interpolated
  message makes every distinct path its own message string and defeats grouping —
  and the allocation went away as a side effect. **−3 allocs.**

### What a future change has to answer for

The tracked metric is unchanged in spirit: **22 is now the number**, and moving
off it needs a reason in this file. Specifically:

- A change that moves `BenchmarkFabrin_OneRoute` above
  `BenchmarkRequestIDAndLogger` has put framework work on the request path. That
  is the design regression the original 9-alloc rule was written to catch, and the
  two-row comparison still catches it.
- A change that moves both together has changed what the middleware do, which may
  be legitimate — record it here with the reason.

The one structural difference to be aware of: Fabrin mounts each module under its
own `gin.RouterGroup` so a module can apply middleware scoped to its own routes.
That is one extra group in the tree, resolved when routes are registered, not per
request.

### Is 1.5 µs acceptable?

For the framework's target — applications whose handlers talk to a database, where
a request costs milliseconds — 1.2 µs of observability is well under a percent,
and it buys the two things an operator cannot add afterwards: a request id the
user can quote, and a log line that can be grouped. `gin.Default()` installs a
logger too, and an unstructured one.

It is recorded here rather than waved away because a framework that adds a
microsecond per release without noticing ends up somewhere indefensible, and
nobody can say which release did it.

## How to read a change

CI runs `just bench` on every push to `main`, and the result is **informational,
never a gate**. A threshold on a shared runner would fail on a neighbour's noise,
and a check that fails for reasons unrelated to your change is a check people
learn to ignore.

So a suspected regression is confirmed locally:

```bash
git switch main && just bench          # or use benchstat over several runs
git switch your-branch && just bench
```

If the regression is real, either fix it or record the new number here **with the
reason it is acceptable**. A baseline that quietly drifts upward is the same as no
baseline; one that drifts upward with a written justification per step is a design
history.

## Things that will be tempting and are not free

Noted here because each is a plausible-sounding feature whose cost lands on every
request:

- **Reflection at request time** to bind or dispatch. Compile-time wiring costs
  nothing per request.
- **Another middleware installed by default.** The request-id and logging pair
  cost 13 allocations between them and are justified above; the third one needs
  the same argument made in this file before it lands, not after.
- **Readiness checks consulted on the hot path.** `/readyz` aggregates checks;
  ordinary routes must not. `LivenessHandler` deliberately consults nothing.
- **Deep `Context` copying** to make a value available downstream.
- **A second copy of a value already on the request context**, for convenience.
  That is precisely what `c.Set(LogKeyRequestID, …)` was, and it cost three
  allocations per request to save callers one function call.
