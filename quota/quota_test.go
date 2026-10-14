package quota_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/internal/memory"
	"github.com/jeorp/go-usagequota/internal/quotatest"
	"github.com/jeorp/go-usagequota/quota"
)

func TestInvalidArguments(t *testing.T) {
	key := quotatest.Key()
	for _, tc := range []struct {
		name          string
		key           quota.Key
		id            string
		amount, limit int64
	}{
		{"empty subject", quota.Key{Resource: key.Resource, Period: key.Period}, "id", 1, 1},
		{"empty resource", quota.Key{Subject: key.Subject, Period: key.Period}, "id", 1, 1},
		{"invalid UTF-8", quota.Key{Subject: string([]byte{255}), Resource: key.Resource, Period: key.Period}, "id", 1, 1},
		{"empty period", quota.Key{Subject: key.Subject, Resource: key.Resource}, "id", 1, 1},
		{"reversed period", quota.Key{Subject: key.Subject, Resource: key.Resource, Period: quota.Period{Start: key.Period.End, End: key.Period.Start}}, "id", 1, 1},
		{"empty id", key, "", 1, 1},
		{"zero amount", key, "id", 0, 1},
		{"negative amount", key, "id", -1, 1},
		{"negative limit", key, "id", 1, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := quota.New(memory.New())
			if _, err := l.Reserve(context.Background(), tc.key, tc.id, tc.amount, tc.limit); !errors.Is(err, quota.ErrInvalid) {
				t.Fatalf("reserve = %v", err)
			}
		})
	}
}

type fakeStore struct {
	update func(func(*quota.Usage, *quota.Reservation) error) error
	usage  quota.Usage
	err    error
}

func (s fakeStore) Update(_ context.Context, _ quota.Key, _ string, fn func(*quota.Usage, *quota.Reservation) error) error {
	if s.err != nil {
		return s.err
	}
	return s.update(fn)
}
func (s fakeStore) Read(context.Context, quota.Key) (quota.Usage, error) { return s.usage, s.err }

func TestRetryDoesNotLeakCreatedResult(t *testing.T) {
	store := fakeStore{update: func(fn func(*quota.Usage, *quota.Reservation) error) error {
		u, r := quota.Usage{}, quota.Reservation{}
		if err := fn(&u, &r); err != nil {
			return err
		}
		// Another transaction won; the next attempt sees its reservation.
		return fn(&u, &r)
	}}
	l := quota.New(store, quota.WithClock(func() time.Time { return quotatest.Now }))
	r, err := l.Reserve(context.Background(), quotatest.Key(), "id", 1, 1)
	if err != nil || r.Created {
		t.Fatalf("retry = %+v, %v", r, err)
	}
}

func TestTransactionRetryRechecksClock(t *testing.T) {
	at, key := quotatest.Now, quotatest.Key()
	store := fakeStore{update: func(fn func(*quota.Usage, *quota.Reservation) error) error {
		if err := fn(&quota.Usage{}, &quota.Reservation{}); err != nil {
			return err
		}
		at = key.Period.End
		return fn(&quota.Usage{}, &quota.Reservation{})
	}}
	l := quota.New(store, quota.WithClock(func() time.Time { return at }))
	r, err := l.Reserve(context.Background(), key, "id", 1, 1)
	if !errors.Is(err, quota.ErrOutsidePeriod) || r != (quota.Result{}) {
		t.Fatalf("retry = %+v, %v", r, err)
	}
}

func TestStoreErrorIsWrapped(t *testing.T) {
	sentinel := errors.New("unavailable")
	l := quota.New(fakeStore{err: sentinel})
	ctx, key := context.Background(), quotatest.Key()
	_, reserveErr := l.Reserve(ctx, key, "id", 1, 1)
	_, usageErr := l.Usage(ctx, key)
	for _, err := range []error{reserveErr, usageErr, l.Commit(ctx, key, "id"), l.Cancel(ctx, key, "id")} {
		if !errors.Is(err, sentinel) {
			t.Fatalf("lost store error: %v", err)
		}
	}
}

func TestCorruptCountersFailClosed(t *testing.T) {
	for _, usage := range []quota.Usage{{Reserved: -1}, {Committed: -1}, {Reserved: math.MaxInt64, Committed: 1}} {
		l := quota.New(fakeStore{usage: usage})
		if _, err := l.Usage(context.Background(), quotatest.Key()); !errors.Is(err, quota.ErrCorrupt) {
			t.Fatalf("corrupt usage %+v: %v", usage, err)
		}
	}
}

func TestRemainingAfterLimitDecrease(t *testing.T) {
	u := quota.Usage{Reserved: 2, Committed: 5}
	if u.Remaining(3) != 0 || u.Remaining(10) != 3 {
		t.Fatalf("remaining for %+v", u)
	}
}

func FuzzAccounting(f *testing.F) {
	f.Add([]byte{0, 0, 1, 2, 3, 0, 2, 1})
	f.Add([]byte{255, 254, 253, 252, 0, 1})
	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 1024 {
			operations = operations[:1024]
		}
		l := quota.New(memory.New(), quota.WithClock(func() time.Time { return quotatest.Now }))
		ctx, key := context.Background(), quotatest.Key()
		states := map[string]quota.State{}
		// Each byte selects one of eight request IDs and an operation.
		for step, op := range operations {
			id := string(rune('a' + (op/3)%8))
			var wantErr, gotErr error
			switch op % 3 {
			case 0:
				_, gotErr = l.Reserve(ctx, key, id, 1, 3)
				switch states[id] {
				case quota.Cancelled:
					wantErr = quota.ErrFinalized
				case "":
					if modeledUsage(states).Total() == 3 {
						wantErr = quota.ErrExceeded
					} else {
						states[id] = quota.Pending
					}
				}
			case 1, 2:
				target := quota.Committed
				if op%3 == 1 {
					gotErr = l.Commit(ctx, key, id)
				} else {
					target = quota.Cancelled
					gotErr = l.Cancel(ctx, key, id)
				}
				switch states[id] {
				case "":
					wantErr = quota.ErrNotFound
				case quota.Pending:
					states[id] = target
				default:
					if states[id] != target {
						wantErr = quota.ErrFinalized
					}
				}
			}
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("step %d, operation %d, request %q: got %v, want %v", step, op%3, id, gotErr, wantErr)
			}
			u, err := l.Usage(ctx, key)
			wantUsage := modeledUsage(states)
			if err != nil || u != wantUsage {
				t.Fatalf("step %d: usage = %+v, %v; want %+v", step, u, err, wantUsage)
			}
		}
	})
}

func modeledUsage(states map[string]quota.State) quota.Usage {
	var usage quota.Usage
	for _, state := range states {
		switch state {
		case quota.Pending:
			usage.Reserved++
		case quota.Committed:
			usage.Committed++
		}
	}
	return usage
}
