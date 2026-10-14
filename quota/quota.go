// Package quota manages finite usage budgets with atomic, idempotent
// reservations. It accounts for usage; it does not execute application work.
package quota

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid       = errors.New("quota: invalid argument")
	ErrExceeded      = errors.New("quota: limit exceeded")
	ErrConflict      = errors.New("quota: request ID reused with a different amount")
	ErrNotFound      = errors.New("quota: reservation not found")
	ErrFinalized     = errors.New("quota: incompatible terminal state")
	ErrOutsidePeriod = errors.New("quota: outside period")
	ErrCorrupt       = errors.New("quota: invalid stored state")
)

// Key identifies an independent budget. Subject may be a user or tenant ID;
// Resource may be "ai-reviews" or "speech-characters". Both must be nonempty UTF-8.
// A different period means a different budget, even if the periods overlap.
type Key struct {
	Subject  string
	Resource string
	Period   Period
}

// Canonical removes location and monotonic-clock differences from equal keys.
// Store implementations must use the canonical key as their identity.
func (k Key) Canonical() Key {
	k.Period.Start = k.Period.Start.UTC().Round(0)
	k.Period.End = k.Period.End.UTC().Round(0)
	return k
}

func (k Key) valid() bool {
	return k.Subject != "" && utf8.ValidString(k.Subject) &&
		k.Resource != "" && utf8.ValidString(k.Resource) &&
		k.Period.valid()
}

// Usage includes pending reservations in the amount charged against the limit.
type Usage struct {
	Reserved  int64
	Committed int64
}

// Total returns the reserved and committed units combined.
func (u Usage) Total() int64 { return u.Reserved + u.Committed }

// Remaining clamps at zero if a caller lowers its limit below existing usage.
func (u Usage) Remaining(limit int64) int64 {
	total := u.Total()
	if limit <= total {
		return 0
	}
	return limit - total
}

func (u Usage) valid() bool {
	return u.Reserved >= 0 && u.Committed >= 0 && u.Reserved <= math.MaxInt64-u.Committed
}

// State is a reservation's lifecycle state. The empty state means absent.
type State string

const (
	Pending   State = "pending"
	Committed State = "committed"
	Cancelled State = "cancelled"
)

// Reservation is stored for the lifetime of the idempotency guarantee.
// A zero value represents a missing reservation inside Store.Update.
type Reservation struct {
	Amount int64
	State  State
}

// Result distinguishes a new reservation from a replay. Only the caller that
// receives Created=true should start new work. Replays are not execution leases.
type Result struct {
	Reservation Reservation
	Created     bool
}

// Store persists a budget and its reservations. Implementations must serialize
// concurrent updates to the same Key across all clients sharing the store.
//
// Update supplies isolated copies (zero values if absent), and atomically saves
// both only when fn returns nil. A callback error must leave storage unchanged.
// The callback may be retried; it must not perform external side effects. The
// store must not retain or expose the pointers passed to the callback.
//
// Read returns a consistent usage snapshot, or zero usage for a missing key.
// Implementations must honor context cancellation and preserve wrapped errors.
type Store interface {
	Update(ctx context.Context, key Key, requestID string, fn func(*Usage, *Reservation) error) error
	Read(ctx context.Context, key Key) (Usage, error)
}

// Limiter applies policy using an atomic Store. Its clock must be safe for
// concurrent use. WithClock configures a deterministic clock for tests.
type Limiter struct {
	store Store
	now   func() time.Time
}

// Option configures a limiter before use.
type Option func(*Limiter)

// WithClock sets the admission clock. Commit and Cancel also work after a
// period ends, and always update the original period's budget.
func WithClock(now func() time.Time) Option {
	return func(l *Limiter) { l.now = now }
}

// New constructs a limiter. A nil store or clock is a programming error.
func New(store Store, options ...Option) *Limiter {
	if store == nil {
		panic("quota: nil store")
	}
	l := &Limiter{store: store, now: time.Now}
	for _, option := range options {
		option(l)
	}
	if l.now == nil {
		panic("quota: nil clock")
	}
	return l
}

// Reserve reserves amount units from a finite limit. Zero limit denies new
// reservations; negative limits and nonpositive amounts are invalid.
//
// Identity is (key, requestID). Reusing it with a different amount conflicts.
// An existing pending or committed reservation is returned with Created=false,
// even after the period ends or the limit decreases. Cancelled IDs stay closed.
// Callers supply trusted limits; this library does not resolve billing plans.
func (l *Limiter) Reserve(ctx context.Context, key Key, requestID string, amount, limit int64) (Result, error) {
	if !key.valid() || requestID == "" || amount <= 0 || limit < 0 {
		return Result{}, ErrInvalid
	}
	var result Result
	err := l.store.Update(ctx, key.Canonical(), requestID, func(usage *Usage, reservation *Reservation) error {
		if err := validate(*usage, *reservation); err != nil {
			return err
		}
		if reservation.State != "" {
			if reservation.Amount != amount {
				return ErrConflict
			}
			if reservation.State == Cancelled {
				return ErrFinalized
			}
			result = Result{Reservation: *reservation}
			return nil
		}
		// Evaluate time on every transaction retry, not just on API entry.
		if !key.Period.Contains(l.now()) {
			return ErrOutsidePeriod
		}
		if _, err := Consume(usage.Total(), amount, limit); err != nil {
			return err
		}
		usage.Reserved += amount
		*reservation = Reservation{Amount: amount, State: Pending}
		result = Result{Reservation: *reservation, Created: true}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("reserve: %w", err)
	}
	return result, nil
}

// Commit converts a pending reservation to committed usage. Repeated commits
// are successful. A cancelled reservation cannot be committed.
func (l *Limiter) Commit(ctx context.Context, key Key, requestID string) error {
	return l.finalize(ctx, key, requestID, Committed)
}

// Cancel releases a pending reservation. Repeated cancellations are successful.
// Committed reservations cannot be cancelled or refunded by this API.
func (l *Limiter) Cancel(ctx context.Context, key Key, requestID string) error {
	return l.finalize(ctx, key, requestID, Cancelled)
}

func (l *Limiter) finalize(ctx context.Context, key Key, requestID string, state State) error {
	if !key.valid() || requestID == "" {
		return ErrInvalid
	}
	err := l.store.Update(ctx, key.Canonical(), requestID, func(usage *Usage, reservation *Reservation) error {
		if err := validate(*usage, *reservation); err != nil {
			return err
		}
		if reservation.State == "" {
			return ErrNotFound
		}
		if reservation.State == state {
			return nil
		}
		if reservation.State != Pending {
			return ErrFinalized
		}
		usage.Reserved -= reservation.Amount
		if state == Committed {
			usage.Committed += reservation.Amount
		}
		reservation.State = state
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", state, err)
	}
	return nil
}

// Usage reads current counters without creating a budget or enforcing time.
func (l *Limiter) Usage(ctx context.Context, key Key) (Usage, error) {
	if !key.valid() {
		return Usage{}, ErrInvalid
	}
	usage, err := l.store.Read(ctx, key.Canonical())
	if err != nil {
		return Usage{}, fmt.Errorf("usage: %w", err)
	}
	if !usage.valid() {
		return Usage{}, ErrCorrupt
	}
	return usage, nil
}

func validate(usage Usage, reservation Reservation) error {
	if !usage.valid() {
		return ErrCorrupt
	}
	switch reservation.State {
	case "":
		if reservation.Amount != 0 {
			return ErrCorrupt
		}
	case Pending:
		if reservation.Amount <= 0 || reservation.Amount > usage.Reserved {
			return ErrCorrupt
		}
	case Committed:
		if reservation.Amount <= 0 || reservation.Amount > usage.Committed {
			return ErrCorrupt
		}
	case Cancelled:
		if reservation.Amount <= 0 {
			return ErrCorrupt
		}
	default:
		return ErrCorrupt
	}
	return nil
}
