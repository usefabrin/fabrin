// Package authpg provides Fabrin's PostgreSQL authentication store. It imports
// no driver; applications open a *sql.DB and own its lifecycle.
package authpg

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/usefabrin/fabrin/auth"
	"github.com/usefabrin/fabrin/migrate"
)

const migrationVersion = "20260911160000"

// Store implements auth.Store with transactions and PostgreSQL advisory locks.
type Store struct{ db *sql.DB }

// New binds a caller-owned database without connecting or mutating its schema.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("authpg: database is required")
	}
	return &Store{db: db}, nil
}

// Migration returns the explicit schema migration required by Store. Serving
// never invokes it; applications run Fabrin's migrate command separately.
func Migration() migrate.M {
	return migrate.M{
		Version: migrationVersion,
		Name:    "create Fabrin authentication tables",
		Up: func(ctx context.Context, h migrate.Handle) error {
			for _, statement := range schemaUp {
				if _, err := h.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("authpg: create schema: %w", err)
				}
			}
			return nil
		},
		Down: func(ctx context.Context, h migrate.Handle) error {
			for _, statement := range schemaDown {
				if _, err := h.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("authpg: drop schema: %w", err)
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
	`CREATE TABLE fabrin_auth_challenges (
		id TEXT PRIMARY KEY,
		email TEXT NOT NULL,
		key_id TEXT NOT NULL,
		purpose TEXT NOT NULL CHECK (purpose IN ('browser', 'native')),
		verifier BYTEA NOT NULL CHECK (octet_length(verifier) = 32),
		issued_at TIMESTAMPTZ NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		source TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
		active BOOLEAN NOT NULL DEFAULT TRUE,
		CHECK (expires_at > issued_at)
	)`,
	`CREATE UNIQUE INDEX fabrin_auth_one_active_challenge ON fabrin_auth_challenges (email, purpose) WHERE active`,
	`CREATE TABLE fabrin_auth_send_events (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		kind TEXT NOT NULL CHECK (kind IN ('address', 'source')),
		budget_key TEXT NOT NULL,
		occurred_at TIMESTAMPTZ NOT NULL
	)`,
	`CREATE INDEX fabrin_auth_send_budget ON fabrin_auth_send_events (kind, budget_key, occurred_at)`,
	`CREATE TABLE fabrin_auth_verify_source_events (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		source TEXT NOT NULL,
		occurred_at TIMESTAMPTZ NOT NULL
	)`,
	`CREATE INDEX fabrin_auth_verify_source_budget ON fabrin_auth_verify_source_events (source, occurred_at)`,
	`CREATE TABLE fabrin_auth_verify_address_failures (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		email TEXT NOT NULL,
		occurred_at TIMESTAMPTZ NOT NULL
	)`,
	`CREATE INDEX fabrin_auth_verify_address_budget ON fabrin_auth_verify_address_failures (email, occurred_at)`,
	`CREATE TABLE fabrin_auth_sessions (
		id TEXT PRIMARY KEY,
		identity_id TEXT NOT NULL REFERENCES fabrin_auth_identities(id) ON DELETE CASCADE,
		digest BYTEA NOT NULL CHECK (octet_length(digest) = 32),
		created_at TIMESTAMPTZ NOT NULL,
		last_seen_at TIMESTAMPTZ NOT NULL,
		expires_at TIMESTAMPTZ NOT NULL,
		active BOOLEAN NOT NULL DEFAULT TRUE,
		CHECK (expires_at > created_at),
		CHECK (last_seen_at >= created_at)
	)`,
	`CREATE INDEX fabrin_auth_sessions_identity ON fabrin_auth_sessions (identity_id) WHERE active`,
}

var schemaDown = []string{
	`DROP TABLE fabrin_auth_sessions`,
	`DROP TABLE fabrin_auth_verify_address_failures`,
	`DROP TABLE fabrin_auth_verify_source_events`,
	`DROP TABLE fabrin_auth_send_events`,
	`DROP TABLE fabrin_auth_challenges`,
	`DROP TABLE fabrin_auth_identities`,
}

// Reserve implements auth.Store.
func (s *Store) Reserve(ctx context.Context, reservation auth.Reservation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := advisoryLock(ctx, tx, "send-source\x00"+reservation.Source); err != nil {
		return err
	}
	var sourceCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fabrin_auth_send_events WHERE kind = 'source' AND budget_key = $1 AND occurred_at > $2`, reservation.Source, reservation.IssuedAt.Add(-time.Hour)).Scan(&sourceCount); err != nil {
		return err
	}
	if sourceCount >= 20 {
		return auth.ErrRateLimited
	}
	if err := advisoryLock(ctx, tx, "address\x00"+reservation.Email); err != nil {
		return err
	}
	var addressCount int
	var last sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT count(*), max(occurred_at) FROM fabrin_auth_send_events WHERE kind = 'address' AND budget_key = $1 AND occurred_at > $2`, reservation.Email, reservation.IssuedAt.Add(-time.Hour)).Scan(&addressCount, &last); err != nil {
		return err
	}
	if addressCount >= 5 || (last.Valid && reservation.IssuedAt.Sub(last.Time) < time.Minute) {
		return auth.ErrRateLimited
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fabrin_auth_send_events (kind, budget_key, occurred_at) VALUES ('source', $1, $2), ('address', $3, $2)`, reservation.Source, reservation.IssuedAt, reservation.Email); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_challenges SET active = FALSE WHERE email = $1 AND purpose = $2 AND active`, reservation.Email, reservation.Purpose); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fabrin_auth_challenges (id, email, key_id, purpose, verifier, issued_at, expires_at, source) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, reservation.ID, reservation.Email, reservation.KeyID, reservation.Purpose, reservation.Verifier[:], reservation.IssuedAt, reservation.ExpiresAt, reservation.Source); err != nil {
		return err
	}
	return tx.Commit()
}

// Verify implements auth.Store.
func (s *Store) Verify(ctx context.Context, attempt auth.Verification) (auth.Identity, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.Identity{}, err
	}
	defer rollback(tx)
	if err := advisoryLock(ctx, tx, "verify-source\x00"+attempt.Source); err != nil {
		return auth.Identity{}, err
	}
	var sourceCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fabrin_auth_verify_source_events WHERE source = $1 AND occurred_at > $2`, attempt.Source, attempt.Now.Add(-time.Hour)).Scan(&sourceCount); err != nil {
		return auth.Identity{}, err
	}
	if sourceCount >= 100 {
		return auth.Identity{}, auth.ErrRateLimited
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fabrin_auth_verify_source_events (source, occurred_at) VALUES ($1,$2)`, attempt.Source, attempt.Now); err != nil {
		return auth.Identity{}, err
	}

	var email string
	err = tx.QueryRowContext(ctx, `SELECT email FROM fabrin_auth_challenges WHERE id = $1`, attempt.ID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Identity{}, commitResult(tx, auth.ErrAuthentication)
	}
	if err != nil {
		return auth.Identity{}, err
	}
	// Reserve takes the address lock before changing the active challenge. Taking
	// the same locks in the same order prevents resend and verification from
	// deadlocking while the row lock still supplies single-consumer semantics.
	if err := advisoryLock(ctx, tx, "address\x00"+email); err != nil {
		return auth.Identity{}, err
	}

	var keyID, purpose string
	var storedVerifier []byte
	var issuedAt, expiresAt time.Time
	var attempts int
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT key_id,purpose,verifier,issued_at,expires_at,attempts,active FROM fabrin_auth_challenges WHERE id = $1 FOR UPDATE`, attempt.ID).Scan(&keyID, &purpose, &storedVerifier, &issuedAt, &expiresAt, &attempts, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Identity{}, commitResult(tx, auth.ErrAuthentication)
	}
	if err != nil {
		return auth.Identity{}, err
	}
	valid := active && attempts < 5 && !attempt.Now.Before(issuedAt) && attempt.Now.Before(expiresAt) && email == attempt.Email && keyID == attempt.KeyID && purpose == string(attempt.Purpose) && hmac.Equal(storedVerifier, attempt.Verifier[:])
	if !valid {
		var failures int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fabrin_auth_verify_address_failures WHERE email = $1 AND occurred_at > $2`, email, attempt.Now.Add(-time.Hour)).Scan(&failures); err != nil {
			return auth.Identity{}, err
		}
		if failures >= 20 {
			return auth.Identity{}, commitResult(tx, auth.ErrRateLimited)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fabrin_auth_verify_address_failures (email, occurred_at) VALUES ($1,$2)`, email, attempt.Now); err != nil {
			return auth.Identity{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_challenges SET attempts = attempts + 1, active = CASE WHEN attempts + 1 >= 5 OR expires_at <= $2 THEN FALSE ELSE active END WHERE id = $1`, attempt.ID, attempt.Now); err != nil {
			return auth.Identity{}, err
		}
		return auth.Identity{}, commitResult(tx, auth.ErrAuthentication)
	}

	var identity auth.Identity
	var disabled bool
	err = tx.QueryRowContext(ctx, `SELECT id,email,created_at,disabled FROM fabrin_auth_identities WHERE email = $1 FOR UPDATE`, email).Scan(&identity.ID, &identity.Email, &identity.CreatedAt, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		identity = auth.Identity{ID: attempt.IdentityID, Email: email, CreatedAt: attempt.Now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fabrin_auth_identities (id,email,created_at) VALUES ($1,$2,$3)`, identity.ID, identity.Email, identity.CreatedAt); err != nil {
			return auth.Identity{}, err
		}
	} else if err != nil {
		return auth.Identity{}, err
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_challenges SET active = FALSE WHERE id = $1`, attempt.ID); err != nil {
			return auth.Identity{}, err
		}
		return auth.Identity{}, commitResult(tx, auth.ErrAuthentication)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fabrin_auth_sessions (id,identity_id,digest,created_at,last_seen_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`, attempt.Session.ID, identity.ID, attempt.Session.Digest[:], attempt.Session.CreatedAt, attempt.Session.LastSeenAt, attempt.Session.ExpiresAt); err != nil {
		return auth.Identity{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_challenges SET active = FALSE WHERE id = $1`, attempt.ID); err != nil {
		return auth.Identity{}, err
	}
	if err := tx.Commit(); err != nil {
		return auth.Identity{}, err
	}
	return identity, nil
}

// Invalidate implements auth.Store without affecting a newer replacement.
func (s *Store) Invalidate(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE fabrin_auth_challenges SET active = FALSE WHERE id = $1`, id)
	return err
}

// AuthenticateSession implements auth.SessionStore.
func (s *Store) AuthenticateSession(ctx context.Context, proof auth.SessionProof) (auth.Identity, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.Identity{}, err
	}
	defer rollback(tx)
	var identity auth.Identity
	var digest []byte
	var createdAt, lastSeenAt, expiresAt time.Time
	var active, disabled bool
	err = tx.QueryRowContext(ctx, `SELECT i.id,i.email,i.created_at,i.disabled,s.digest,s.created_at,s.last_seen_at,s.expires_at,s.active FROM fabrin_auth_sessions s JOIN fabrin_auth_identities i ON i.id=s.identity_id WHERE s.id=$1 FOR UPDATE OF s`, proof.ID).Scan(&identity.ID, &identity.Email, &identity.CreatedAt, &disabled, &digest, &createdAt, &lastSeenAt, &expiresAt, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Identity{}, auth.ErrSession
	}
	if err != nil {
		return auth.Identity{}, err
	}
	valid := active && !disabled && !proof.Now.Before(createdAt) && proof.Now.Before(expiresAt) && proof.Now.Before(lastSeenAt.Add(24*time.Hour)) && hmac.Equal(digest, proof.Digest[:])
	if !valid {
		if active && (disabled || !proof.Now.Before(expiresAt) || !proof.Now.Before(lastSeenAt.Add(24*time.Hour))) {
			if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_sessions SET active=FALSE WHERE id=$1`, proof.ID); err != nil {
				return auth.Identity{}, err
			}
			return auth.Identity{}, commitResult(tx, auth.ErrSession)
		}
		return auth.Identity{}, auth.ErrSession
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_sessions SET last_seen_at=$2 WHERE id=$1`, proof.ID, proof.Now); err != nil {
		return auth.Identity{}, err
	}
	if err := tx.Commit(); err != nil {
		return auth.Identity{}, err
	}
	return identity, nil
}

// RevokeSession implements auth.SessionStore.
func (s *Store) RevokeSession(ctx context.Context, proof auth.SessionProof) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var digest []byte
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT digest,active FROM fabrin_auth_sessions WHERE id=$1 FOR UPDATE`, proof.ID).Scan(&digest, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.ErrSession
	}
	if err != nil {
		return err
	}
	if !active {
		return auth.ErrSession
	}
	if !hmac.Equal(digest, proof.Digest[:]) {
		return auth.ErrSession
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fabrin_auth_sessions SET active=FALSE WHERE id=$1`, proof.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func advisoryLock(ctx context.Context, tx *sql.Tx, key string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key)
	return err
}

func commitResult(tx *sql.Tx, result error) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	return result
}

func rollback(tx *sql.Tx) { _ = tx.Rollback() }

var _ auth.Store = (*Store)(nil)
