// Package authpg persists Fabrin identities and authorization state in
// PostgreSQL. It imports no driver; applications own *sql.DB lifecycle.
package authpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/usefabrin/fabrin/auth"
	"github.com/usefabrin/fabrin/migrate"
)

const migrationVersion = "20260911160000"

// Option configures Store.
type Option func(*settings)

// WithInvitationsRequired denies creation of an identity unless an unconsumed
// invitation for its canonical email exists.
func WithInvitationsRequired() Option {
	return func(s *settings) { s.invitationsRequired = true }
}

type settings struct{ invitationsRequired bool }

// Store implements auth.IdentityStore over a caller-owned database.
type Store struct {
	db                  *sql.DB
	invitationsRequired bool
}

// New binds a caller-owned database without connecting or mutating its schema.
func New(db *sql.DB, options ...Option) (*Store, error) {
	if db == nil {
		return nil, errors.New("authpg: database is required")
	}
	settings := settings{}
	for i, option := range options {
		if option == nil {
			return nil, fmt.Errorf("authpg: option %d is nil", i)
		}
		option(&settings)
	}
	return &Store{db: db, invitationsRequired: settings.invitationsRequired}, nil
}

// Migration returns the explicit identity schema migration. Serving never
// invokes it; applications run Fabrin's migrate command separately.
func Migration() migrate.M {
	return migrate.M{
		Version: migrationVersion,
		Name:    "create Fabrin identity tables",
		Up: func(ctx context.Context, h migrate.Handle) error {
			for _, statement := range schemaUp {
				if _, err := h.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("authpg: create identity schema: %w", err)
				}
			}
			return nil
		},
		Down: func(ctx context.Context, h migrate.Handle) error {
			for _, statement := range schemaDown {
				if _, err := h.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("authpg: drop identity schema: %w", err)
				}
			}
			return nil
		},
	}
}

var schemaUp = []string{
	`CREATE TABLE fabrin_auth_identities (
		id TEXT PRIMARY KEY,
		email TEXT NOT NULL UNIQUE,
		created_at TIMESTAMPTZ NOT NULL,
		disabled BOOLEAN NOT NULL DEFAULT FALSE
	)`,
	`CREATE TABLE fabrin_auth_invitations (
		email TEXT PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL,
		consumed_at TIMESTAMPTZ
	)`,
}

var schemaDown = []string{
	`DROP TABLE fabrin_auth_invitations`,
	`DROP TABLE fabrin_auth_identities`,
}

// ResolveVerified returns the unique eligible identity for a verified email.
// Concurrent first logins serialize by canonical email; invitation consumption
// and identity creation commit in the same PostgreSQL transaction.
func (s *Store) ResolveVerified(ctx context.Context, resolution auth.IdentityResolution) (auth.Identity, error) {
	if resolution.Email == "" || resolution.ProposedID == "" || resolution.VerifiedAt.IsZero() {
		return auth.Identity{}, auth.ErrAuthentication
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.Identity{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "identity\x00"+resolution.Email); err != nil {
		return auth.Identity{}, err
	}
	var identity auth.Identity
	var disabled bool
	err = tx.QueryRowContext(ctx, `SELECT id,email,created_at,disabled FROM fabrin_auth_identities WHERE email=$1 FOR UPDATE`, resolution.Email).
		Scan(&identity.ID, &identity.Email, &identity.CreatedAt, &disabled)
	if err == nil {
		if disabled {
			return auth.Identity{}, auth.ErrAuthentication
		}
		if err := tx.Commit(); err != nil {
			return auth.Identity{}, err
		}
		return identity, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return auth.Identity{}, err
	}
	if s.invitationsRequired {
		var consumed sql.NullTime
		err := tx.QueryRowContext(ctx, `SELECT consumed_at FROM fabrin_auth_invitations WHERE email=$1 FOR UPDATE`, resolution.Email).Scan(&consumed)
		if errors.Is(err, sql.ErrNoRows) || consumed.Valid {
			return auth.Identity{}, auth.ErrAuthentication
		}
		if err != nil {
			return auth.Identity{}, err
		}
	}
	identity = auth.Identity{ID: resolution.ProposedID, Email: resolution.Email, CreatedAt: resolution.VerifiedAt}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fabrin_auth_identities (id,email,created_at) VALUES ($1,$2,$3)`, identity.ID, identity.Email, identity.CreatedAt); err != nil {
		return auth.Identity{}, err
	}
	if s.invitationsRequired {
		if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_invitations SET consumed_at=$2 WHERE email=$1`, identity.Email, resolution.VerifiedAt); err != nil {
			return auth.Identity{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return auth.Identity{}, err
	}
	return identity, nil
}

var _ auth.IdentityStore = (*Store)(nil)
