package quota_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/internal/quotatest"
	"github.com/jeorp/go-usagequota/quota"
)

type reservationStore struct {
	fakeStore
	readReservation func(context.Context, quota.Key, string) (quota.Reservation, error)
}

func (s reservationStore) ReadReservation(ctx context.Context, key quota.Key, id string) (quota.Reservation, error) {
	return s.readReservation(ctx, key, id)
}

func TestReservationValidatesArguments(t *testing.T) {
	key := quotatest.Key()
	for _, tc := range []struct {
		key quota.Key
		id  string
	}{{quota.Key{}, "id"}, {key, ""}} {
		l := quota.New(fakeStore{})
		if r, err := l.Reservation(context.Background(), tc.key, tc.id); !errors.Is(err, quota.ErrInvalid) || r != (quota.Reservation{}) {
			t.Fatalf("invalid lookup = %+v, %v", r, err)
		}
	}
}

func TestReservationUnsupportedStore(t *testing.T) {
	l := quota.New(fakeStore{})
	if r, err := l.Reservation(context.Background(), quotatest.Key(), "id"); !errors.Is(err, quota.ErrUnsupported) || r != (quota.Reservation{}) {
		t.Fatalf("unsupported lookup = %+v, %v", r, err)
	}
}

func TestReservationCanonicalKeyAndContext(t *testing.T) {
	key := quotatest.Key()
	key.Period.Start = key.Period.Start.In(time.FixedZone("test", 9*60*60))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := quota.Reservation{Amount: 2, State: quota.Pending}
	store := reservationStore{readReservation: func(gotCtx context.Context, got quota.Key, id string) (quota.Reservation, error) {
		if gotCtx != ctx || got != key.Canonical() || id != "id" {
			t.Fatalf("lookup arguments = %v, %+v, %q", gotCtx, got, id)
		}
		return want, nil
	}}
	// Reading must not consult the admission clock, even for an expired period.
	l := quota.New(store, quota.WithClock(func() time.Time { panic("read called clock") }))
	if r, err := l.Reservation(ctx, key, "id"); err != nil || r != want {
		t.Fatalf("lookup = %+v, %v", r, err)
	}
}

func TestReservationRejectsCorruptState(t *testing.T) {
	for _, r := range []quota.Reservation{
		{}, {Amount: 1}, {State: quota.Pending},
		{Amount: -1, State: quota.Committed}, {Amount: 0, State: quota.Cancelled},
		{Amount: 1, State: "unknown"},
	} {
		store := reservationStore{readReservation: func(context.Context, quota.Key, string) (quota.Reservation, error) {
			return r, nil
		}}
		got, err := quota.New(store).Reservation(context.Background(), quotatest.Key(), "id")
		if !errors.Is(err, quota.ErrCorrupt) || got != (quota.Reservation{}) {
			t.Fatalf("corrupt %+v: got %+v, %v", r, got, err)
		}
	}
}

func TestReservationPreservesStoreErrors(t *testing.T) {
	for _, cause := range []error{quota.ErrNotFound, context.Canceled, errors.New("unavailable")} {
		store := reservationStore{readReservation: func(context.Context, quota.Key, string) (quota.Reservation, error) {
			return quota.Reservation{Amount: 1, State: quota.Pending}, fmt.Errorf("store: %w", cause)
		}}
		r, err := quota.New(store).Reservation(context.Background(), quotatest.Key(), "id")
		if !errors.Is(err, cause) || r != (quota.Reservation{}) {
			t.Fatalf("failed lookup = %+v, %v; want %v", r, err, cause)
		}
	}
}
