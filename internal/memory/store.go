// Package memory provides an in-process quota store for this module's tests and examples.
// State is lost on restart. It is not shared across processes and has no eviction.
package memory

import (
	"context"

	"github.com/jeorp/go-usagequota/quota"
)

type bucket struct {
	usage        quota.Usage
	reservations map[string]quota.Reservation
}

// Store serializes access using a context-aware gate. Use New to construct it.
// A Store must not be copied. All limiters sharing this instance share a budget.
type Store struct {
	gate    chan struct{}
	buckets map[quota.Key]bucket
}

var _ quota.Store = (*Store)(nil)

func New() *Store {
	return &Store{gate: make(chan struct{}, 1), buckets: make(map[quota.Key]bucket)}
}

func (s *Store) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) Update(ctx context.Context, key quota.Key, requestID string, fn func(*quota.Usage, *quota.Reservation) error) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	key = key.Canonical()
	b := s.buckets[key]
	u, r := b.usage, b.reservations[requestID]
	if err := fn(&u, &r); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.reservations == nil {
		b.reservations = make(map[string]quota.Reservation)
	}
	b.usage, b.reservations[requestID] = u, r
	s.buckets[key] = b
	return nil
}

func (s *Store) Read(ctx context.Context, key quota.Key) (quota.Usage, error) {
	if err := s.lock(ctx); err != nil {
		return quota.Usage{}, err
	}
	defer func() { <-s.gate }()
	return s.buckets[key.Canonical()].usage, nil
}
