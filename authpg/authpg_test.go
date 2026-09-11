package authpg_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/usefabrin/fabrin/auth"
	"github.com/usefabrin/fabrin/authpg"
	"github.com/usefabrin/fabrin/mail"
	"github.com/usefabrin/fabrin/migrate"
)

func TestNew_RejectsNilAndPerformsNoDatabaseIO(t *testing.T) {
	if _, err := authpg.New(nil); err == nil {
		t.Fatal("accepted nil database")
	}
	connector := &rejectConnect{}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := authpg.New(db); err != nil {
		t.Fatal(err)
	}
	if connector.calls.Load() != 0 {
		t.Fatalf("database connections: %d", connector.calls.Load())
	}
}

func TestStore_PostgresEndToEnd(t *testing.T) {
	dsn := os.Getenv("FABRIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FABRIN_TEST_PG_DSN not set; skipping live PostgreSQL auth adapter test")
	}
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })

	schema := "fabrin_auth_" + randomHex(t, 8)
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
	db.SetMaxOpenConns(10)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Run(t.Context(), db, []migrate.M{authpg.Migration()}); err != nil {
		t.Fatal(err)
	}
	store, err := authpg.New(db)
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := mail.NewCapture(64)
	service, err := auth.New(store, inbox, "test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	challenge, err := service.Request(t.Context(), "Alice@EXAMPLE.COM", auth.PurposeNative, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(inbox.Messages()[0].Text, "Your Fabrin sign-in code is: ")

	var wins atomic.Int32
	var authentication auth.Authentication
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			<-start
			result, verifyErr := service.Verify(t.Context(), challenge.ID, "Alice@EXAMPLE.COM", code, auth.PurposeNative, "source-a")
			if verifyErr == nil {
				wins.Add(1)
				mu.Lock()
				authentication = result
				mu.Unlock()
				return
			}
			if !errors.Is(verifyErr, auth.ErrAuthentication) {
				t.Errorf("verify: %v", verifyErr)
			}
		})
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("successful verifications: %d", wins.Load())
	}
	if authentication.Identity.Email != "Alice@example.com" || authentication.Session.Credential == "" {
		t.Fatalf("authentication: %+v", authentication)
	}

	manager, err := auth.NewSessionManager(store)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := manager.Current(t.Context(), authentication.Session.Credential)
	if err != nil || identity.ID != authentication.Identity.ID {
		t.Fatalf("current: identity=%+v err=%v", identity, err)
	}
	if err := manager.Logout(t.Context(), authentication.Session.Credential); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Current(t.Context(), authentication.Session.Credential); !errors.Is(err, auth.ErrSession) {
		t.Fatalf("revoked current: %v", err)
	}

	secondStore, err := authpg.New(db)
	if err != nil {
		t.Fatal(err)
	}
	secondService, err := auth.New(secondStore, inbox, "test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondService.Request(t.Context(), "Alice@EXAMPLE.COM", auth.PurposeNative, "source-b"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("shared address budget: %v", err)
	}
	for i := range 20 {
		if _, err := service.Request(t.Context(), fmt.Sprintf("budget-%02d@example.com", i), auth.PurposeNative, "shared-source"); err != nil {
			t.Fatalf("seed shared source budget %d: %v", i, err)
		}
	}
	if _, err := secondService.Request(t.Context(), "budget-over@example.com", auth.PurposeNative, "shared-source"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("shared source budget: %v", err)
	}

	var plaintext bool
	if err := db.QueryRowContext(t.Context(), `SELECT EXISTS (
		SELECT 1 FROM fabrin_auth_challenges WHERE verifier::text LIKE '%' || $1 || '%'
		UNION ALL
		SELECT 1 FROM fabrin_auth_sessions WHERE digest::text LIKE '%' || $2 || '%'
	)`, code, authentication.Session.Credential).Scan(&plaintext); err != nil {
		t.Fatal(err)
	}
	if plaintext {
		t.Fatal("database retained a plaintext code or session credential")
	}
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
