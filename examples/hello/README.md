# examples/hello

The smallest Fabrin app that demonstrates the two claims F0 makes — ports rather
than imports, and process slicing — plus the one F2 adds: a module reaching its
**data** through a port it declares, with `main` as the only file that names SQL.

```bash
go run ./examples/hello                       # every module
FABRIN_MODULES=greet go run ./examples/hello  # only greet — /time is a 404

go run ./examples/hello routes                # who owns which URL
go run ./examples/hello version
```

No arguments means *serve*. `main.go` hands `os.Args[1:]` to `app.Execute`, so the
binary that has the modules linked in is the one that answers `routes` — Go
compiles, and no separate tool can introspect an application it did not build.

```bash
curl 'localhost:8080/greet?name=you'
# {"at":"2026-08-02T09:46:12+06:30","greeting":"hello, you"}

curl -X POST localhost:8080/orders \
  -H 'Content-Type: application/json' -d '{"item":"widget"}'
# {"id":1,"item":"widget"}

curl localhost:8080/orders/1
# {"id":1,"item":"widget"}

curl localhost:8080/orders/99
# 404 {"error":"no such order"}

curl localhost:8080/time
curl localhost:8080/healthz   # liveness — consults nothing
curl localhost:8080/readyz    # readiness — aggregates mounted modules' checks
```

The database is SQLite **in memory**, so the ids start at 1 on every boot and
nothing is left on disk.

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

`FABRIN_MODULES=greet` builds and mounts only `greet`. `/time` is not a handler
returning 404 — the route is **never registered** — and the `orders` database
opener is never invoked. `routes` shows the mounted half directly:

```console
$ go run ./examples/hello routes
GET   /greet       greet
GET   /healthz     (framework)
POST  /orders      orders
GET   /orders/:id  orders
GET   /readyz      (framework)
GET   /time        clock

$ FABRIN_MODULES=greet go run ./examples/hello routes
GET  /greet    greet
GET  /healthz  (framework)
GET  /readyz   (framework)
```

One binary, N deployment shapes. Note that `routes` describes *this* process
rather than the binary's full catalogue — listing what is not mounted would be
actively misleading in exactly the deployment shape slicing exists to serve. The
narrower second listing is that same fact showing through the formatting: the
columns are sized from the rows actually present.

## 3. The data port, and `main` holds the database

`orders` owns a table. It does **not** import an ORM, a driver, or
`database/sql`. It declares the two methods it actually calls and takes an
implementation from `main`:

```go
// orders/orders.go — the consumer declares what it requires
type Store interface {
    Find(ctx context.Context, id int64) (*Order, error)
    Create(ctx context.Context, o *Order) error
}

func New(s Store) *Module { return &Module{store: s} }
```

That is [ADR 0002](../../docs/adr/0002-database-sql-is-the-orm-seam.md) made
concrete: the seam Fabrin ships for data is not a blessed ORM type, it is the
interface the consuming module declares for itself — the same shape as `Clock`
above, one layer down. Swapping SQLite for Postgres, for GORM, or for an HTTP
call to a service that owns orders is a change in `main.go` and nowhere else.

`main.go` is the only file that names SQL. The selected `orders` factory builds
the typed store and module without opening a connection. Its `Lifecycle.Start`
opens SQLite and runs a `migrate.M` to create the table; `Lifecycle.Stop` closes
it. The store translates `sql.ErrNoRows` into `orders.ErrNotFound` at that
boundary — so the module answers 404 without importing `database/sql` to
recognise the error, which is the difference between a port and an indirection.

There are **two** implementations of that one interface — `main`'s SQLite store
and the in-memory one in `orders/orders_test.go` — because an interface with
exactly one implementation forever is a wrapper wearing a disguise.

`orders` also implements `Modeler`, so `App.Models()` reports the table it owns
and a migration generator has something real to diff. The migration itself is
hand-written and says so in a comment: `fabrin makemigrations` does not exist
yet, and the on-disk migration format is undecided.

## Every claim here is tested, not just described

`hello_test.go` and `orders/orders_test.go` are the point of this directory as
much as the app is:

| Test | Proves |
|---|---|
| `TestModules_NeverImportEachOther` | reads the import graph, so it catches even a blank import — which compiles cleanly and which no behavioural test could detect |
| `TestOrders_ImportsNoDatabaseHandleNorAnythingOutsideFabrin` | the same trick for the data port: `orders` names no ORM, no driver, and not `database/sql`. An **allowlist** — standard library plus Fabrin — rather than a deny list, because "no ORM" is a claim about every ORM there will ever be |
| `TestSlicing_MountsOnlyTheSelectedModule` | `/greet` 200, `/time` 404 under a selection |
| `TestSlicing_RejectsAnUnknownModuleName` | a typo'd selection fails at construction, never silently serving nothing |
| `TestSlicing_DoesNotOpenAnUnselectedModulesResources` | the greet-only shape reaches lifecycle startup without invoking the orders database opener |
| `TestOrders_RoundTripsAnOrderThroughTheStoreMainWiredIn` | POST then GET against whatever `main` opened. Two requests, not one: the second can only answer if the first reached storage that outlives a request, which an echo cannot fake |
| `orders_test.go::TestModule_ReachesItsDataOnlyThroughTheStoreItWasGiven` | the *same* module answering the same requests against a map, with no database anywhere. The in-memory store counts its writes, because a response body cannot distinguish a handler that went through the port from one that echoed the request back |
| `TestOrders_DeclaresTheTableItOwnsSoAGeneratorHasSomethingToDiff` | `App.Models()` attributes a table to `orders`, so `makemigrations` will have something to diff |
| `TestProbes_AnswerOnAStockApp` | `/healthz` and `/readyz` work with no module opting in |

The tests call the same `newApp` the binary does. A test that built a different
app would prove nothing about the example anyone actually runs.

`just examples` builds this and boots it until it answers `/healthz`; `just
check` includes that.
