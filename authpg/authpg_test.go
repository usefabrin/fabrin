package authpg_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/usefabrin/fabrin/auth"
	"github.com/usefabrin/fabrin/authpg"
	"github.com/usefabrin/fabrin/migrate"
)

func TestNew_RejectsNilAndPerformsNoDatabaseIO(t *testing.T) {
	if _, err := authpg.New(nil); err == nil {
		t.Fatal("accepted nil database")
	}
	connector := &rejectConnect{}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := authpg.New(db, authpg.WithInvitationsRequired()); err != nil {
		t.Fatal(err)
	}
	if connector.calls.Load() != 0 {
		t.Fatalf("database connections: %d", connector.calls.Load())
	}
}

func TestStore_PostgresResolvesOneEligibleIdentity(t *testing.T) {
	db := postgresDB(t)
	store, err := authpg.New(db, authpg.WithInvitationsRequired())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	request := auth.IdentityResolution{Email: "alice@example.com", ProposedID: "proposed-a", VerifiedAt: now}
	if _, err := store.ResolveVerified(t.Context(), request); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatalf("uninvited resolution: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO fabrin_auth_invitations (email, created_at) VALUES ($1,$2)`, request.Email, now); err != nil {
		t.Fatal(err)
	}

	var identities []auth.Identity
	var failures atomic.Int32
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 8 {
		wg.Go(func() {
			<-start
			candidate := request
			candidate.ProposedID = request.ProposedID + string(rune('a'+i))
			identity, resolveErr := store.ResolveVerified(t.Context(), candidate)
			if resolveErr != nil {
				failures.Add(1)
				return
			}
			mu.Lock()
			identities = append(identities, identity)
			mu.Unlock()
		})
	}
	close(start)
	wg.Wait()
	if failures.Load() != 0 || len(identities) != 8 {
		t.Fatalf("resolutions=%d failures=%d", len(identities), failures.Load())
	}
	for _, identity := range identities[1:] {
		if identity != identities[0] {
			t.Fatalf("identities differ: %+v != %+v", identity, identities[0])
		}
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM fabrin_auth_identities WHERE email=$1`, request.Email).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("identity rows: %d", count)
	}
	var consumed sql.NullTime
	if err := db.QueryRowContext(t.Context(), `SELECT consumed_at FROM fabrin_auth_invitations WHERE email=$1`, request.Email).Scan(&consumed); err != nil || !consumed.Valid {
		t.Fatalf("invitation consumption: %v valid=%v", err, consumed.Valid)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE fabrin_auth_identities SET disabled=TRUE WHERE email=$1`, request.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveVerified(t.Context(), request); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatalf("disabled resolution: %v", err)
	}
}

func postgresDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("FABRIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FABRIN_TEST_PG_DSN not set; skipping live PostgreSQL identity test")
	}
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := "fabrin_identity_" + randomHex(t, 8)
	if _, err := adminDB.ExecContext(t.Context(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Run(t.Context(), db, []migrate.M{authpg.Migration()}); err != nil {
		t.Fatal(err)
	}
	return db
}

func randomHex(t *testing.T, size int) string {
	t.Helper()
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

type rejectConnect struct{ calls atomic.Int32 }

func (c *rejectConnect) Connect(context.Context) (driver.Conn, error) {
	c.calls.Add(1)
	return nil, errors.New("unexpected database connection")
}

func (*rejectConnect) Driver() driver.Driver { return rejectDriver{} }

type rejectDriver struct{}

func (rejectDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unexpected database connection")
}
