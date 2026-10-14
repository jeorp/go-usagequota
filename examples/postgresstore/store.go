// Package postgresstore is a small GORM example for budgets with few reservations.
package postgresstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/jeorp/go-usagequota/quota"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store saves each budget and its reservations in one row.
// The caller owns the database connection.
type Store struct {
	db *gorm.DB
}

type quotaBudget struct {
	Key          string `gorm:"primaryKey"`
	quota.Usage  `gorm:"embedded"`
	Reservations map[string]quota.Reservation `gorm:"serializer:json;type:jsonb"`
}

// New accepts a base GORM connection, not an active transaction.
func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

// Init creates the quota_budgets table using GORM's model definition.
func (s *Store) Init(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&quotaBudget{})
}

func (s *Store) Update(ctx context.Context, key quota.Key, requestID string, fn func(*quota.Usage, *quota.Reservation) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := quotaBudget{Key: budgetKey(key)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		// Every writer locks the budget, including when creating a reservation.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row).Error; err != nil {
			return err
		}

		// Encode IDs so arbitrary bytes, including NUL, are safe as JSON keys.
		id := base64.StdEncoding.EncodeToString([]byte(requestID))
		usage, reservation := row.Usage, row.Reservations[id]
		if err := fn(&usage, &reservation); err != nil {
			return err
		}

		row.Usage = usage
		if row.Reservations == nil {
			row.Reservations = make(map[string]quota.Reservation)
		}
		row.Reservations[id] = reservation
		return tx.Save(&row).Error
	})
}

// Read returns zero usage if the budget does not exist.
func (s *Store) Read(ctx context.Context, key quota.Key) (quota.Usage, error) {
	var row quotaBudget
	err := s.db.WithContext(ctx).Select("reserved", "committed").
		Where("key = ?", budgetKey(key)).Find(&row).Error
	return row.Usage, err
}

func budgetKey(key quota.Key) string {
	key = key.Canonical()
	// JSON keeps fields distinct; strings preserve nanosecond precision.
	data, _ := json.Marshal([4]string{
		key.Subject, key.Resource,
		key.Period.Start.Format(time.RFC3339Nano),
		key.Period.End.Format(time.RFC3339Nano),
	})
	return string(data)
}
