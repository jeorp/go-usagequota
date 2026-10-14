package quota

import (
	"context"
	"fmt"
)

// ReservationReader is an optional Store capability used by Limiter.Reservation.
// ReadReservation returns an isolated snapshot for the canonical key and request
// ID, or ErrNotFound if absent. It must not create or modify records, must honor
// context cancellation, and must preserve wrapped errors.
// Existing Store implementations can add this method without changing Update or Read.
type ReservationReader interface {
	ReadReservation(ctx context.Context, key Key, requestID string) (Reservation, error)
}

// Reservation reads a reservation or immediate charge without changing usage
// or enforcing the period. It returns ErrNotFound if absent and ErrUnsupported
// if the store does not implement ReservationReader. The snapshot describes
// accounting state, not whether application work ran or permission to start it.
func (l *Limiter) Reservation(ctx context.Context, key Key, requestID string) (Reservation, error) {
	if !key.valid() || requestID == "" {
		return Reservation{}, ErrInvalid
	}
	reader, ok := l.store.(ReservationReader)
	if !ok {
		return Reservation{}, ErrUnsupported
	}
	reservation, err := reader.ReadReservation(ctx, key.Canonical(), requestID)
	if err != nil {
		return Reservation{}, fmt.Errorf("reservation: %w", err)
	}
	if !reservation.valid() {
		return Reservation{}, ErrCorrupt
	}
	return reservation, nil
}

func (r Reservation) valid() bool {
	return r.Amount > 0 && (r.State == Pending || r.State == Committed || r.State == Cancelled)
}
