package quotatest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/quota"
)

func runReservationReads(t *testing.T, factory func(*testing.T) quota.Store) {
	t.Helper()
	store := factory(t)
	if _, ok := store.(quota.ReservationReader); !ok {
		t.Skip("store does not implement ReservationReader")
	}
	ctx, key, at := context.Background(), Key(), Now
	l := quota.New(store, quota.WithClock(func() time.Time { return at }))
	wantMissing := func(key quota.Key, id string) {
		t.Helper()
		if r, err := l.Reservation(ctx, key, id); !errors.Is(err, quota.ErrNotFound) || r != (quota.Reservation{}) {
			t.Fatalf("missing %q = %+v, %v", id, r, err)
		}
	}
	wantMissing(key, "pending")
	wantUsage(t, l, key, 0, 0)
	for _, id := range []string{"pending", "commit", "cancel", "\xff", "\xfe", "a\x00b"} {
		result, err := l.Reserve(ctx, key, id, 2, 20)
		if err != nil || !result.Created {
			t.Fatalf("reserve %q = %+v, %v", id, result, err)
		}
	}
	if err := l.Commit(ctx, key, "commit"); err != nil {
		t.Fatal(err)
	}
	if err := l.Cancel(ctx, key, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Spend(ctx, key, "spend", 3, 20); err != nil {
		t.Fatal(err)
	}
	at = key.Period.End
	key.Period.Start = key.Period.Start.In(time.FixedZone("test", 9*60*60))
	for _, tc := range []struct {
		id     string
		amount int64
		state  quota.State
	}{
		{"pending", 2, quota.Pending}, {"commit", 2, quota.Committed},
		{"cancel", 2, quota.Cancelled}, {"spend", 3, quota.Committed},
		{"\xff", 2, quota.Pending}, {"\xfe", 2, quota.Pending}, {"a\x00b", 2, quota.Pending},
	} {
		want := quota.Reservation{Amount: tc.amount, State: tc.state}
		r, err := l.Reservation(ctx, key, tc.id)
		if err != nil || r != want {
			t.Fatalf("lookup %q = %+v, %v; want %+v", tc.id, r, err, want)
		}
		// Changing a returned snapshot must not modify stored state.
		r.Amount = 99
		again, err := l.Reservation(ctx, key, tc.id)
		if err != nil || again != want {
			t.Fatalf("snapshot mutation leaked: %+v, %v (local %+v)", again, err, r)
		}
	}
	wantMissing(key, "missing")
	for _, other := range []quota.Key{
		{Subject: "other", Resource: key.Resource, Period: key.Period},
		{Subject: key.Subject, Resource: "other", Period: key.Period},
		{Subject: key.Subject, Resource: key.Resource, Period: quota.Month(at)},
	} {
		wantMissing(other, "pending")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := l.Reservation(cancelled, key, "pending"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lookup = %v", err)
	}
	wantUsage(t, l, key, 8, 5)
}

func runExceededDetails(t *testing.T, factory func(*testing.T) quota.Store) {
	t.Helper()
	ctx, key := context.Background(), Key()
	l := quota.New(factory(t), quota.WithClock(func() time.Time { return Now }))
	if _, err := l.Reserve(ctx, key, "pending", 3, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Spend(ctx, key, "committed", 2, 10); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"reserve", "spend"} {
		t.Run(op, func(t *testing.T) {
			var err error
			if op == "reserve" {
				_, err = l.Reserve(ctx, key, "rejected", 6, 10)
			} else {
				_, err = l.Spend(ctx, key, "rejected", 6, 10)
			}
			var detail *quota.ExceededError
			if !errors.Is(err, quota.ErrExceeded) || !errors.As(err, &detail) {
				t.Fatalf("exceeded error = %v", err)
			}
			if detail.Limit != 10 || detail.Used != 5 || detail.Amount != 6 || detail.Remaining() != 5 {
				t.Fatalf("exceeded details = %+v, remaining = %d", detail, detail.Remaining())
			}
			wantUsage(t, l, key, 3, 2)
		})
	}
	// Rejection must not consume the ID or the available capacity.
	if result, err := l.Spend(ctx, key, "rejected", 5, 10); err != nil || !result.Created {
		t.Fatalf("spend after rejection = %+v, %v", result, err)
	}
	wantUsage(t, l, key, 3, 7)
}
