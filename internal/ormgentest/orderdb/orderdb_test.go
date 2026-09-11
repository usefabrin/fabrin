package orderdb

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	_ DBTX = (*sql.DB)(nil)
	_ DBTX = (*sql.Tx)(nil)
	_ DBTX = (*sql.Conn)(nil)
)

func TestDBTX_IsSatisfiedByDBTxAndConn(t *testing.T) {
	t.Parallel()

	values := []DBTX{(*sql.DB)(nil), (*sql.Tx)(nil), (*sql.Conn)(nil)}
	if len(values) != 3 {
		t.Fatalf("DBTX implementations = %d, want 3", len(values))
	}
}

type memoryDriver struct {
	mu        sync.Mutex
	row       []driver.Value
	lastExec  string
	lastQuery string
}

func (d *memoryDriver) Open(string) (driver.Conn, error) { return &memoryConn{driver: d}, nil }

type memoryConn struct{ driver *memoryDriver }

func (c *memoryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not used")
}

func (c *memoryConn) Close() error              { return nil }
func (c *memoryConn) Begin() (driver.Tx, error) { return nil, errors.New("transactions are not used") }

func (c *memoryConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.driver.mu.Lock()
	defer c.driver.mu.Unlock()
	c.driver.lastExec = query
	c.driver.row = make([]driver.Value, len(args))
	for i, arg := range args {
		c.driver.row[i] = arg.Value
	}
	return driver.RowsAffected(1), nil
}

func (c *memoryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.driver.mu.Lock()
	defer c.driver.mu.Unlock()
	c.driver.lastQuery = query
	if len(c.driver.row) == 0 || len(args) != 1 || args[0].Value != c.driver.row[0] {
		return &memoryRows{}, nil
	}
	return &memoryRows{row: append([]driver.Value(nil), c.driver.row...)}, nil
}

type memoryRows struct {
	row  []driver.Value
	done bool
}

func (*memoryRows) Columns() []string {
	return []string{"id", "reference", "quantity", "total", "active", "payload", "note", "shipped_at"}
}
func (*memoryRows) Close() error { return nil }

func (r *memoryRows) Next(dest []driver.Value) error {
	if r.done || len(r.row) == 0 {
		return io.EOF
	}
	copy(dest, r.row)
	r.done = true
	return nil
}

func TestQueries_CreateAndGetPreserveNullsAndQuoteIdentifiers(t *testing.T) {
	drv := &memoryDriver{}
	sql.Register("ormgen_order", drv)
	db, err := sql.Open("ormgen_order", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	queries := New(db)
	want := &Order{ID: 41, Reference: "A-41", Quantity: 2, Total: 19.5, Active: true}
	if err := queries.CreateOrder(t.Context(), want); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	got, err := queries.GetOrder(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got.ID != want.ID || got.Reference != want.Reference || got.Quantity != want.Quantity || got.Total != want.Total || got.Active != want.Active || !bytes.Equal(got.Payload, want.Payload) || got.Note != nil || got.ShippedAt != nil {
		t.Errorf("GetOrder = %#v, want %#v", got, want)
	}
	if drv.lastExec != `INSERT INTO "order" ("id", "reference", "quantity", "total", "active", "payload", "note", "shipped_at") VALUES ($1, $2, $3, $4, $5, $6, $7, $8)` {
		t.Errorf("create query = %q", drv.lastExec)
	}
	if drv.lastQuery != `SELECT "id", "reference", "quantity", "total", "active", "payload", "note", "shipped_at" FROM "order" WHERE "id" = $1` {
		t.Errorf("get query = %q", drv.lastQuery)
	}

	_, err = queries.GetOrder(t.Context(), 404)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("missing row error = %v, want sql.ErrNoRows", err)
	}
}

func TestQueries_HonorCancellation(t *testing.T) {
	drv := &memoryDriver{}
	sql.Register("ormgen_cancel", drv)
	db, err := sql.Open("ormgen_cancel", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = New(db).CreateOrder(ctx, &Order{ID: 1, Reference: "cancelled"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("CreateOrder error = %v, want context.Canceled", err)
	}
}

func TestQueries_CreateAndGetAgainstPostgreSQL(t *testing.T) {
	dsn := os.Getenv("FABRIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FABRIN_TEST_PG_DSN is not set; skipping live PostgreSQL generated-query test")
	}

	db, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.SetMaxOpenConns(1) // the temporary table belongs to one PostgreSQL session
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TEMP TABLE "order" (
		"id" BIGINT PRIMARY KEY,
		"reference" VARCHAR(32) NOT NULL,
		"quantity" INTEGER NOT NULL,
		"total" DOUBLE PRECISION NOT NULL,
		"active" BOOLEAN NOT NULL,
		"payload" BYTEA,
		"note" TEXT,
		"shipped_at" TIMESTAMPTZ
	)`); err != nil {
		t.Fatalf("create temporary table: %v", err)
	}

	note := "packed"
	shippedAt := time.Date(2026, 9, 11, 12, 30, 0, 0, time.UTC)
	want := &Order{
		ID: 91, Reference: "PG-91", Quantity: 3, Total: 49.95, Active: true,
		Payload: []byte{0, 1, 2}, Note: &note, ShippedAt: &shippedAt,
	}
	queries := New(db)
	if err := queries.CreateOrder(t.Context(), want); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	got, err := queries.GetOrder(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got.ID != want.ID || got.Reference != want.Reference || got.Quantity != want.Quantity || got.Total != want.Total || got.Active != want.Active || !bytes.Equal(got.Payload, want.Payload) || got.Note == nil || *got.Note != note || got.ShippedAt == nil || !got.ShippedAt.Equal(shippedAt) {
		t.Errorf("GetOrder = %#v, want %#v", got, want)
	}

	_, err = queries.GetOrder(t.Context(), 404)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("missing row error = %v, want sql.ErrNoRows", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = queries.CreateOrder(ctx, &Order{ID: 92, Reference: "cancelled"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled CreateOrder error = %v, want context.Canceled", err)
	}
}
