package quotatest

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/quota"
)

func runSpend(t *testing.T, factory func(*testing.T) quota.Store) {
	t.Helper()
	ctx := context.Background()
	newLimiter := func(t *testing.T) *quota.Limiter {
		return quota.New(factory(t), quota.WithClock(func() time.Time { return Now }))
	}
	t.Run("commit_and_replay", func(t *testing.T) {
		l, key := newLimiter(t), Key()
		first, err := l.Spend(ctx, key, "one", 2, 3)
		if err != nil || !first.Created || first.Usage != (quota.Usage{Committed: 2}) {
			t.Fatalf("first spend = %+v, %v", first, err)
		}
		replay, err := l.Spend(ctx, key, "one", 2, 1)
		if err != nil || replay.Created || replay.Usage != first.Usage {
			t.Fatalf("replayed spend = %+v, %v", replay, err)
		}
		if _, err := l.Spend(ctx, key, "one", 1, 3); !errors.Is(err, quota.ErrConflict) {
			t.Fatalf("conflicting spend = %v", err)
		}
		if _, err := l.Spend(ctx, key, "two", 2, 3); !errors.Is(err, quota.ErrExceeded) {
			t.Fatalf("over-limit spend = %v", err)
		}
		wantUsage(t, l, key, 0, 2)
	})
	for _, sameID := range []bool{false, true} {
		t.Run(fmt.Sprintf("100_contenders/same_id=%t", sameID), func(t *testing.T) {
			l, key := newLimiter(t), Key()
			var created atomic.Int64
			parallel(t, 100, func(i int) error {
				id := fmt.Sprint(i)
				if sameID {
					id = "same"
				}
				result, err := l.Spend(ctx, key, id, 1, 1)
				if !sameID && errors.Is(err, quota.ErrExceeded) {
					return nil
				}
				if err == nil && result.Created {
					created.Add(1)
				}
				return err
			})
			if created.Load() != 1 {
				t.Fatalf("created = %d, want 1", created.Load())
			}
			wantUsage(t, l, key, 0, 1)
		})
	}
	t.Run("period_boundary_and_late_replay", func(t *testing.T) {
		key := Key()
		at := key.Period.Start.Add(-time.Nanosecond)
		l := quota.New(factory(t), quota.WithClock(func() time.Time { return at }))
		if _, err := l.Spend(ctx, key, "early", 1, 2); !errors.Is(err, quota.ErrOutsidePeriod) {
			t.Fatalf("before start: %v", err)
		}
		at = key.Period.Start
		if result, err := l.Spend(ctx, key, "one", 1, 2); err != nil || !result.Created {
			t.Fatalf("at start = %+v, %v", result, err)
		}
		at = key.Period.End
		if _, err := l.Spend(ctx, key, "late", 1, 2); !errors.Is(err, quota.ErrOutsidePeriod) {
			t.Fatalf("at end: %v", err)
		}
		result, err := l.Spend(ctx, key, "one", 1, 0)
		if err != nil || result.Created || result.Usage != (quota.Usage{Committed: 1}) {
			t.Fatalf("late replay = %+v, %v", result, err)
		}
		wantUsage(t, l, key, 0, 1)
	})
	for _, state := range []quota.State{quota.Pending, quota.Cancelled, quota.Committed} {
		t.Run("existing_"+string(state), func(t *testing.T) {
			l, key := newLimiter(t), Key()
			if _, err := l.Reserve(ctx, key, "one", 2, 3); err != nil {
				t.Fatal(err)
			}
			want := quota.Usage{Reserved: 2}
			switch state {
			case quota.Cancelled:
				if err := l.Cancel(ctx, key, "one"); err != nil {
					t.Fatal(err)
				}
				want = quota.Usage{}
			case quota.Committed:
				if err := l.Commit(ctx, key, "one"); err != nil {
					t.Fatal(err)
				}
				want = quota.Usage{Committed: 2}
			}
			var wantErr error
			if state != quota.Committed {
				wantErr = quota.ErrFinalized
			}
			result, err := l.Spend(ctx, key, "one", 2, 3)
			if !errors.Is(err, wantErr) || result.Created {
				t.Fatalf("spend over %s = %+v, %v; want %v", state, result, err, wantErr)
			}
			wantUsage(t, l, key, want.Reserved, want.Committed)
			if state == quota.Pending {
				if err := l.Commit(ctx, key, "one"); err != nil {
					t.Fatalf("pending reservation after rejected spend: %v", err)
				}
				wantUsage(t, l, key, 0, 2)
			}
		})
	}
}
