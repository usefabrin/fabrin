# examples/hello

The smallest Fabrin app that demonstrates the two claims F0 makes.

```bash
go run ./examples/hello                       # both modules
FABRIN_MODULES=greet go run ./examples/hello  # only greet — /time is a 404
```

```bash
curl 'localhost:8080/greet?name=you'
curl localhost:8080/time
curl localhost:8080/healthz   # liveness — consults nothing
curl localhost:8080/readyz    # readiness — aggregates mounted modules' checks
```

## 1. Ports, not imports

`greet` needs the current time, which the `clock` module owns. It does **not**
import `clock`. It declares the single method it needs and takes it as an
argument:

```go
// greet/greet.go — the consumer declares what it requires
type Clock interface {
    Now() time.Time
}

func New(clock Clock) *Module { return &Module{clock: clock} }
```

`main.go` is the only place that knows both sides exist. That makes the interface
an **extraction seam**: moving the clock to its own service means passing an HTTP
client adapter here and changing nothing in `greet`.

## 2. Process slicing

`FABRIN_MODULES=greet` mounts only `greet`. `/time` is not a handler returning
404 — the route is never registered, which you can see in the startup route
table. One binary, N deployment shapes.

## Both claims are tested, not just described

`hello_test.go` is the point of this directory as much as the app is:

| Test | Proves |
|---|---|
| `TestModules_NeverImportEachOther` | reads the import graph, so it catches even a blank import — which compiles cleanly and which no behavioural test could detect |
| `TestSlicing_MountsOnlyTheSelectedModule` | `/greet` 200, `/time` 404 under a selection |
| `TestSlicing_RejectsAnUnknownModuleName` | a typo'd selection fails at construction, never silently serving nothing |
| `TestProbes_AnswerOnAStockApp` | `/healthz` and `/readyz` work with no module opting in |

The tests call the same `newApp` the binary does. A test that built a different
app would prove nothing about the example anyone actually runs.

`just examples` builds this and boots it until it answers `/healthz`; `just
check` includes that.
