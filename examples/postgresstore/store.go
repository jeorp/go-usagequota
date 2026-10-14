// Package postgresstore shows how to implement quota.Store with PostgreSQL transactions.
// It accepts a database/sql connection; the caller supplies a PostgreSQL driver.
package postgresstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jeorp/go-usagequota/quota"
)

// Store keeps budgets in a caller-selected PostgreSQL schema. The caller owns db.
// Keep budget and reservation rows for as long as request IDs must be deduplicated.
type Store struct {
	db     *sql.DB
	schema string
}

var _ quota.Store = (*Store)(nil)

// New constructs a store for an existing schema, such as "public". Init must
// be called once before use, or the same tables must be created by a migration.
func New(db *sql.DB, schema string) *Store {
	if db == nil || schema == "" || len(schema) > 63 || strings.ContainsRune(schema, 0) {
		panic("postgresstore: invalid database or schema")
	}
	return &Store{db: db, schema: quoteIdentifier(schema)}
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *Store) budgets() string      { return s.schema + `.usagequota_budgets` }
func (s *Store) reservations() string { return s.schema + `.usagequota_reservations` }

// Init creates the two tables if they do not exist. It does not create the
// schema or modify existing tables. Applications may run equivalent DDL in
// their own migration system instead.
func (s *Store) Init(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		key_hash BYTEA PRIMARY KEY CHECK (octet_length(key_hash) = 32),
		reserved BIGINT NOT NULL DEFAULT 0 CHECK (reserved >= 0),
		committed BIGINT NOT NULL DEFAULT 0 CHECK (committed >= 0),
		CHECK (reserved <= 9223372036854775807 - committed)
	)`, s.budgets()))
	if err != nil {
		return fmt.Errorf("create budgets: %w", err)
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		key_hash BYTEA NOT NULL REFERENCES %s(key_hash) ON DELETE CASCADE,
		request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
		amount BIGINT NOT NULL CHECK (amount > 0),
		state TEXT NOT NULL CHECK (state IN ('pending', 'committed', 'cancelled')),
		PRIMARY KEY (key_hash, request_hash)
	)`, s.reservations(), s.budgets()))
	if err != nil {
		return fmt.Errorf("create reservations: %w", err)
	}
	return tx.Commit()
}

func budgetDigest(key quota.Key) []byte {
	key = key.Canonical()
	identity, _ := json.Marshal([4]string{key.Subject, key.Resource,
		key.Period.Start.Format(time.RFC3339Nano), key.Period.End.Format(time.RFC3339Nano)})
	sum := sha256.Sum256(identity)
	return sum[:]
}

func requestDigest(requestID string) []byte {
	sum := sha256.Sum256([]byte(requestID))
	return sum[:]
}

// Update locks the budget row before reading its reservation. Every writer to
// a budget therefore sees the previous writer's committed state. The callback
// runs inside the transaction and must not perform external side effects.
func (s *Store) Update(ctx context.Context, key quota.Key, requestID string, fn func(*quota.Usage, *quota.Reservation) error) error {
	keyHash, requestHash := budgetDigest(key), requestDigest(requestID)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (key_hash) VALUES ($1) ON CONFLICT (key_hash) DO NOTHING`, s.budgets()), keyHash); err != nil {
		return err
	}
	var usage quota.Usage
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT reserved, committed FROM %s WHERE key_hash = $1 FOR UPDATE`, s.budgets()), keyHash).Scan(&usage.Reserved, &usage.Committed); err != nil {
		return err
	}
	var reservation quota.Reservation
	var state string
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT amount, state FROM %s WHERE key_hash = $1 AND request_hash = $2`, s.reservations()), keyHash, requestHash).Scan(&reservation.Amount, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		reservation.State = quota.State(state)
	}
	beforeUsage, beforeReservation := usage, reservation
	if err := fn(&usage, &reservation); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if usage == beforeUsage && reservation == beforeReservation {
		return tx.Commit()
	}
	if usage != beforeUsage {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET reserved = $2, committed = $3 WHERE key_hash = $1`, s.budgets()), keyHash, usage.Reserved, usage.Committed); err != nil {
			return err
		}
	}
	if reservation != beforeReservation {
		if reservation == (quota.Reservation{}) {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE key_hash = $1 AND request_hash = $2`, s.reservations()), keyHash, requestHash); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (key_hash, request_hash, amount, state)
				VALUES ($1, $2, $3, $4) ON CONFLICT (key_hash, request_hash)
				DO UPDATE SET amount = EXCLUDED.amount, state = EXCLUDED.state`, s.reservations()),
				keyHash, requestHash, reservation.Amount, string(reservation.State)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Read returns a consistent counter snapshot, or zero usage for a missing key.
func (s *Store) Read(ctx context.Context, key quota.Key) (quota.Usage, error) {
	var usage quota.Usage
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT reserved, committed FROM %s WHERE key_hash = $1`, s.budgets()), budgetDigest(key)).Scan(&usage.Reserved, &usage.Committed)
	if errors.Is(err, sql.ErrNoRows) {
		return quota.Usage{}, nil
	}
	return usage, err
}
