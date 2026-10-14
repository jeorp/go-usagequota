package postgresstore

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *Store) budgets() string      { return quoteIdentifier(s.schema) + `.usagequota_budgets` }
func (s *Store) reservations() string { return quoteIdentifier(s.schema) + `.usagequota_reservations` }

// Init creates the two tables if they do not exist. It does not create the
// schema or modify existing tables. Applications may run equivalent DDL in
// their own migration system instead.
func (s *Store) Init(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		key_hash BYTEA PRIMARY KEY CHECK (octet_length(key_hash) = 32),
		reserved BIGINT NOT NULL DEFAULT 0 CHECK (reserved >= 0),
		committed BIGINT NOT NULL DEFAULT 0 CHECK (committed >= 0),
		CHECK (reserved <= 9223372036854775807 - committed)
	)`, s.budgets())).Error
		if err != nil {
			return fmt.Errorf("create budgets: %w", err)
		}
		err = tx.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		key_hash BYTEA NOT NULL REFERENCES %s(key_hash) ON DELETE CASCADE,
		request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
		amount BIGINT NOT NULL CHECK (amount > 0),
		state TEXT NOT NULL CHECK (state IN ('pending', 'committed', 'cancelled')),
		PRIMARY KEY (key_hash, request_hash)
	)`, s.reservations(), s.budgets())).Error
		if err != nil {
			return fmt.Errorf("create reservations: %w", err)
		}
		return nil
	})
}
