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

`just bench` runs `go test -bench . -benchmem ./...`. The benchmark that matters
serves one trivial route two ways — through a bare `gin.Engine`, and through a
`fabrin.App` mounting one module — and reports ns/op, B/op, and allocs/op for
each.

Allocations per request are the number to watch most closely. Nanoseconds vary
with the machine; an allocation that appears in the hot path is a design mistake
that shows up identically everywhere.

## Baseline

_Not yet recorded — the benchmark lands with the core
([#6](https://github.com/usefabrin/fabrin/issues/6))._

Fill in from a **local** run, not from CI: shared runners vary enough between jobs
that their absolute numbers are not comparable over time.

| Benchmark | ns/op | B/op | allocs/op | vs raw Gin |
|---|---|---|---|---|
| `BenchmarkRawGin_OneRoute` | — | — | — | baseline |
| `BenchmarkFabrin_OneRoute` | — | — | — | — |

Recorded on: _(Go version, OS, CPU)_

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
- **A middleware that allocates per request** to carry a value — request ids
  included. One allocation, deliberate and measured.
- **Readiness checks consulted on the hot path.** `/readyz` aggregates checks;
  ordinary routes must not.
- **Deep `Context` copying** to make a value available downstream.
