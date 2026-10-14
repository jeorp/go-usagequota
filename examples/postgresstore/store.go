// Package postgresstore implements quota.Store using GORM and PostgreSQL.
package postgresstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jeorp/go-usagequota/quota"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store saves budgets and reservations in separate tables.
// The caller owns the database connection.
type Store struct {
	db *gorm.DB
}

type quotaBudget struct {
	Key         string `gorm:"primaryKey"`
	quota.Usage `gorm:"embedded"`
}

type quotaReservation struct {
	BudgetKey         string `gorm:"primaryKey"`
	RequestID         []byte `gorm:"primaryKey"`
	quota.Reservation `gorm:"embedded"`
}

// New accepts a base GORM connection or an active transaction. With an active
// transaction, Update uses a savepoint and the caller controls the final commit.
func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

// Init creates the quota_budgets and quota_reservations tables.
func (s *Store) Init(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&quotaBudget{}, &quotaReservation{})
}

func (s *Store) Update(ctx context.Context, key quota.Key, requestID string, fn func(*quota.Usage, *quota.Reservation) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		budget := quotaBudget{Key: budgetKey(key)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&budget).Error; err != nil {
			return err
		}
		// Every writer locks the budget, including when creating a reservation.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&budget).Error; err != nil {
			return err
		}

		row := quotaReservation{BudgetKey: budget.Key, RequestID: []byte(requestID)}
		if err := tx.Where("budget_key = ? AND request_id = ?", row.BudgetKey, row.RequestID).Find(&row).Error; err != nil {
			return err
		}
		usage, reservation := budget.Usage, row.Reservation
		if err := fn(&usage, &reservation); err != nil {
			return err
		}

		budget.Usage = usage
		if err := tx.Save(&budget).Error; err != nil {
			return err
		}
		if reservation == (quota.Reservation{}) {
			return tx.Delete(&row).Error
		}
		row.Reservation = reservation
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
