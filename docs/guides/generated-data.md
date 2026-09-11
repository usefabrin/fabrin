# Generate PostgreSQL models and data access

**Developer preview.** This API is not frozen. It currently generates scalar
records, create/get stores, initial-table SQL, and model metadata. It does not yet
provide generated REST endpoints, admin, relationships, automatic IDs, update,
delete, filtering, or a general query builder. See [v1 delivery](../V1_PLAN.md).

## Install from a development checkout

Until a preview version is published, use a local checkout in your application's
module. Replace the example path with your checkout; no release tag is implied.

```sh
go mod init example.com/myapp
go mod edit -require=github.com/usefabrin/fabrin@v0.0.0
go mod edit -replace=github.com/usefabrin/fabrin=/absolute/path/to/fabrin
```

Fabrin requires the Go version declared in its `go.mod` (currently Go 1.25 or
newer). Applications own database connections and choose their SQL driver.

## Declare and generate

Create `cmd/generate/main.go`:

```go
package main

import (
    "log"
    "os"

    "github.com/usefabrin/fabrin/schema"
)

func main() {
    note, err := schema.New("Note", "notes",
        schema.Int64("id").PrimaryKey(),
        schema.Int64("owner_id"),
        schema.String("body").MaxLen(200),
        schema.String("description").Nullable(),
    )
    if err != nil {
        log.Fatal(err)
    }
    source, err := schema.Generate("records", note)
    if err != nil {
        log.Fatal(err)
    }
    if err := os.MkdirAll("records", 0755); err != nil {
        log.Fatal(err)
    }
    if err := os.WriteFile("records/records.go", source, 0644); err != nil {
        log.Fatal(err)
    }
}
```

Run `go mod tidy`, then `go run ./cmd/generate`, then `go test ./...`. Commit your
schema program and generated source together. Generation is deterministic for
the same ordered declarations; it opens no database and changes no tables.
Use a dedicated package for generated records: the generator checks names within
its input, but cannot detect conflicts in other files you put in that package.

Supported fields are `String`, `Int64`, `Bool`, and `Time`. Fields default to
NOT NULL. `Nullable()` generates `sql.NullString`, `sql.NullInt64`, `sql.NullBool`,
or `sql.NullTime`; their default JSON representation is the standard library
struct, so use your own response DTOs for application APIs. `Time` uses PostgreSQL
`TIMESTAMP` without time zone, matching existing `orm.TypeTime` metadata; normalize
values to UTC in your application. Time-zone-aware metadata is not supported by
this preview. `MaxLen` counts PostgreSQL characters, not UTF-8 bytes.

Models need one non-null primary key. Keys are supplied by the caller, not
allocated by the store. SQL table/column names are lowercase ASCII snake_case,
limited to 63 bytes. Field names become exported Go names (`owner_id` → `OwnerID`).
Invalid or colliding names are errors.

## Create the table through a migration

Generated `records.NoteCreateTableSQL` uses Fabrin’s existing PostgreSQL migration
renderer, including its constraint names and column semantics. It is initial-table DDL. Execute it once in an
explicit migration, never automatically at server startup. For example, the body
of a `migrate.M.Up` callback can use:

```go
func(ctx context.Context, h migrate.Handle) error {
    _, err := h.ExecContext(ctx, records.NoteCreateTableSQL)
    return err
}
```

Choose a migration version and rollback policy as for other Fabrin migrations.
`records.NoteModel()` returns fresh `orm.Model` metadata; a module's `Models()`
can return `[]orm.Model{records.NoteModel()}` for the existing migration tooling.
Do not both hand-apply initial-table SQL and generate another initial-table
migration for the same database. Schema evolution remains an explicit reviewed
migration; regenerating Go code alone does not update a database.

## Use typed access

Given a non-nil `*sql.DB` or `*sql.Tx`:

```go
store, err := records.NewNoteStore(db)
// Handle err before continuing.
err = store.Create(ctx, records.Note{ID: 1, OwnerID: userID, Body: "First note"})
// Handle err before continuing.
note, err := store.Get(ctx, 1)
// errors.Is(err, sql.ErrNoRows) identifies an absent record.
```

The constructor rejects nil interfaces and nil standard SQL handles. Custom
adapters must provide valid implementations of the generated `DB` interface.
Both operations propagate cancellation and wrap database errors with `%w`.
Queries parameterize values and explicitly name columns. Use `*sql.Tx` to include
operations in an application-owned transaction. The caller closes connections
and commits or rolls back transactions.

**Stores do not authorize callers.** Set ownership from the authenticated
identity, not a request body's owner field, and check ownership before returning
a record. No HTTP route is exposed by declaring a schema. Custom SQL and
module-owned store interfaces remain available.

## Verify against PostgreSQL

Use a disposable database, never a database with application data. From the
Fabrin checkout:

```sh
FABRIN_TEST_PG_DSN='postgres://user:password@127.0.0.1:5432/testdb?sslmode=disable' \
  go test ./schema -run TestGenerate_CompilesAndUsesPostgres -v
```

The integration fixture uses a transaction-local schema and rolls it back. It
checks create/get, SQL-like text as data, nulls, cancellation, missing records,
and independent metadata. Without the variable, generated code still compiles
but the live database portion explicitly skips. TLS-disabled connection strings
above are for loopback test databases only.
