// Package postgresstore implements quota.Store using GORM and PostgreSQL.
package postgresstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/jeorp/go-usagequota/quota"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store keeps budgets in a caller-selected PostgreSQL schema. The caller owns db.
// Keep budget and reservation rows for as long as request IDs must be deduplicated.
type Store struct {
	db     *gorm.DB
	schema string
}

type budgetRow struct {
	KeyHash     []byte `gorm:"primaryKey"`
	quota.Usage `gorm:"embedded"`
}

type reservationRow struct {
	KeyHash           []byte `gorm:"primaryKey"`
	RequestHash       []byte `gorm:"primaryKey"`
	quota.Reservation `gorm:"embedded"`
}

var _ quota.Store = (*Store)(nil)

// New constructs a store for an existing schema, such as "public". Pass a base
// GORM connection, not an active transaction. Call Init before first use, or
// create the same tables with a migration.
func New(db *gorm.DB, schema string) *Store {
	if db == nil || schema == "" || len(schema) > 63 || strings.ContainsRune(schema, 0) {
		panic("postgresstore: invalid database or schema")
	}
	return &Store{db: db, schema: schema}
}

// Update locks the budget before reading its reservation. All writers to the
// same budget take this lock, so the callback sees the last committed state.
func (s *Store) Update(ctx context.Context, key quota.Key, requestID string, fn func(*quota.Usage, *quota.Reservation) error) error {
	keyHash, requestHash := budgetDigest(key), requestDigest(requestID)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		budget := budgetRow{KeyHash: keyHash}
		if err := tx.Table(s.budgets()).Clauses(clause.OnConflict{DoNothing: true}).Create(&budget).Error; err != nil {
			return err
		}
		if err := tx.Table(s.budgets()).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("key_hash = ?", keyHash).Take(&budget).Error; err != nil {
			return err
		}

		var row reservationRow
		if err := tx.Table(s.reservations()).Where("key_hash = ? AND request_hash = ?", keyHash, requestHash).
			Find(&row).Error; err != nil {
			return err
		}

		usage, reservation := budget.Usage, row.Reservation
		if err := fn(&usage, &reservation); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if usage != budget.Usage {
			// A map also writes zero values when reservations are released.
			if err := tx.Table(s.budgets()).Where("key_hash = ?", keyHash).
				Updates(map[string]any{"reserved": usage.Reserved, "committed": usage.Committed}).Error; err != nil {
				return err
			}
		}
		if reservation == row.Reservation {
			return nil
		}
		if reservation == (quota.Reservation{}) {
			return tx.Table(s.reservations()).Where("key_hash = ? AND request_hash = ?", keyHash, requestHash).
				Delete(&reservationRow{}).Error
		}
		row = reservationRow{KeyHash: keyHash, RequestHash: requestHash, Reservation: reservation}
		return tx.Table(s.reservations()).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key_hash"}, {Name: "request_hash"}},
			DoUpdates: clause.AssignmentColumns([]string{"amount", "state"}),
		}).Create(&row).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

// Read returns a consistent usage snapshot, or zero usage for a missing key.
func (s *Store) Read(ctx context.Context, key quota.Key) (quota.Usage, error) {
	var row budgetRow
	err := s.db.WithContext(ctx).Table(s.budgets()).Where("key_hash = ?", budgetDigest(key)).Find(&row).Error
	return row.Usage, err
}

func budgetDigest(key quota.Key) []byte {
	key = key.Canonical()
	identity, _ := json.Marshal([4]string{
		key.Subject,
		key.Resource,
		key.Period.Start.Format(time.RFC3339Nano),
		key.Period.End.Format(time.RFC3339Nano),
	})
	sum := sha256.Sum256(identity)
	return sum[:]
}

func requestDigest(requestID string) []byte {
	sum := sha256.Sum256([]byte(requestID))
	return sum[:]
}
