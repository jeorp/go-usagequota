// Package quotatest holds the same behavioral tests for every store adapter.
package quotatest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/quota"
)

var Now = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func Key() quota.Key {
	return quota.Key{Subject: "tenant/user", Resource: "ai/reviews", Period: quota.Month(Now)}
}

// Run exercises accounting, replay, concurrency and the atomic-store contract.
// retryable may identify transport errors that guarantee no commit. Those calls
// are retried once after a concurrent burst; unknown outcomes are never retried.
func Run(t *testing.T, factory func(*testing.T) quota.Store, retryable func(error) bool) {
	t.Helper()
	ctx := context.Background()
	newLimiter := func(store quota.Store) *quota.Limiter {
		return quota.New(store, quota.WithClock(func() time.Time { return Now }))
	}
	t.Run("lifecycle", func(t *testing.T) {
		l, key := newLimiter(factory(t)), Key()
		wantUsage(t, l, key, 0, 0)
		result, err := l.Reserve(ctx, key, "request/1", 7, 10)
		if err != nil || !result.Created || result.Reservation.State != quota.Pending {
			t.Fatalf("reserve = %+v, %v", result, err)
		}
		wantUsage(t, l, key, 7, 0)
		if _, err := l.Reserve(ctx, key, "request/2", 4, 10); !errors.Is(err, quota.ErrExceeded) {
			t.Fatalf("over limit: %v", err)
		}
		if _, err := l.Reserve(ctx, key, "request/1", 8, 10); !errors.Is(err, quota.ErrConflict) {
			t.Fatalf("changed amount: %v", err)
		}
		result, err = l.Reserve(ctx, key, "request/1", 7, 0)
		if err != nil || result.Created || result.Reservation.State != quota.Pending {
			t.Fatalf("pending replay = %+v, %v", result, err)
		}
		for i := 0; i < 2; i++ {
			if err := l.Commit(ctx, key, "request/1"); err != nil {
				t.Fatal(err)
			}
		}
		wantUsage(t, l, key, 0, 7)
		result, err = l.Reserve(ctx, key, "request/1", 7, 10)
		if err != nil || result.Created || result.Reservation.State != quota.Committed {
			t.Fatalf("committed replay = %+v, %v", result, err)
		}
		if err := l.Cancel(ctx, key, "request/1"); !errors.Is(err, quota.ErrFinalized) {
			t.Fatalf("cancel committed: %v", err)
		}
		// A rejected request did not consume its ID or leave any accounting behind.
		if _, err := l.Reserve(ctx, key, "request/2", 3, 10); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := l.Cancel(ctx, key, "request/2"); err != nil {
				t.Fatal(err)
			}
		}
		wantUsage(t, l, key, 0, 7)
		if err := l.Commit(ctx, key, "request/2"); !errors.Is(err, quota.ErrFinalized) {
			t.Fatalf("commit cancelled: %v", err)
		}
		if _, err := l.Reserve(ctx, key, "request/2", 3, 10); !errors.Is(err, quota.ErrFinalized) {
			t.Fatalf("reserve cancelled: %v", err)
		}
		if err := l.Commit(ctx, key, "missing"); !errors.Is(err, quota.ErrNotFound) {
			t.Fatalf("commit missing: %v", err)
		}
		if err := l.Cancel(ctx, key, "missing"); !errors.Is(err, quota.ErrNotFound) {
			t.Fatalf("cancel missing: %v", err)
		}
		if _, err := l.Reserve(ctx, key, "request/3", 3, 10); err != nil {
			t.Fatal(err)
		}
		wantUsage(t, l, key, 3, 7)
	})
	t.Run("one_slot_100_contenders", func(t *testing.T) {
		store, key := factory(t), Key()
		// Independent limiters must coordinate through the shared store.
		var accepted atomic.Int64
		parallel(t, 100, retryable, func(i int) error {
			result, err := newLimiter(store).Reserve(ctx, key, fmt.Sprint(i), 1, 1)
			if errors.Is(err, quota.ErrExceeded) {
				return nil
			}
			if err == nil && result.Created {
				accepted.Add(1)
			}
			return err
		})
		if accepted.Load() != 1 {
			t.Fatalf("accepted = %d, want 1", accepted.Load())
		}
		wantUsage(t, newLimiter(store), key, 1, 0)
	})
	t.Run("same_request_100_contenders", func(t *testing.T) {
		store, key := factory(t), Key()
		var created atomic.Int64
		parallel(t, 100, retryable, func(_ int) error {
			result, err := newLimiter(store).Reserve(ctx, key, "same", 1, 1)
			if result.Created {
				created.Add(1)
			}
			return err
		})
		if created.Load() != 1 {
			t.Fatalf("created = %d, want 1", created.Load())
		}
		wantUsage(t, newLimiter(store), key, 1, 0)
	})
	t.Run("commit_cancel_race", func(t *testing.T) {
		l, key := newLimiter(factory(t)), Key()
		if _, err := l.Reserve(ctx, key, "race", 1, 1); err != nil {
			t.Fatal(err)
		}
		var succeeded atomic.Int64
		parallel(t, 2, retryable, func(i int) error {
			var err error
			if i == 0 {
				err = l.Commit(ctx, key, "race")
			} else {
				err = l.Cancel(ctx, key, "race")
			}
			if err == nil {
				succeeded.Add(1)
			}
			if errors.Is(err, quota.ErrFinalized) {
				return nil
			}
			return err
		})
		if succeeded.Load() != 1 {
			t.Fatalf("terminal transitions = %d, want 1", succeeded.Load())
		}
		u, err := l.Usage(ctx, key)
		if err != nil || u.Reserved != 0 || (u.Committed != 0 && u.Committed != 1) {
			t.Fatalf("usage = %+v, %v", u, err)
		}
	})
	t.Run("period_boundary_and_late_finalization", func(t *testing.T) {
		store, key := factory(t), Key()
		at := key.Period.Start.Add(-time.Nanosecond)
		l := quota.New(store, quota.WithClock(func() time.Time { return at }))
		if _, err := l.Reserve(ctx, key, "early", 1, 2); !errors.Is(err, quota.ErrOutsidePeriod) {
			t.Fatalf("before start: %v", err)
		}
		at = key.Period.Start
		for _, id := range []string{"commit", "cancel"} {
			if _, err := l.Reserve(ctx, key, id, 1, 2); err != nil {
				t.Fatal(err)
			}
		}
		at = key.Period.End
		if _, err := l.Reserve(ctx, key, "late", 1, 3); !errors.Is(err, quota.ErrOutsidePeriod) {
			t.Fatalf("at end: %v", err)
		}
		if result, err := l.Reserve(ctx, key, "commit", 1, 0); err != nil || result.Created {
			t.Fatalf("late replay = %+v, %v", result, err)
		}
		if err := l.Commit(ctx, key, "commit"); err != nil {
			t.Fatal(err)
		}
		if err := l.Cancel(ctx, key, "cancel"); err != nil {
			t.Fatal(err)
		}
		wantUsage(t, l, key, 0, 1)
		next := key
		next.Period = quota.Month(at)
		wantUsage(t, l, next, 0, 0)
	})
	t.Run("isolation_and_canonical_time", func(t *testing.T) {
		l, key := newLimiter(factory(t)), Key()
		keys := []quota.Key{key, key, key, key}
		keys[1].Subject = "another"
		keys[2].Resource = "another"
		keys[3].Period = quota.Day(Now)
		for _, key := range keys {
			if _, err := l.Reserve(ctx, key, "same-id", 1, 1); err != nil {
				t.Fatal(err)
			}
		}
		zone := time.FixedZone("test", 9*60*60)
		key.Period.Start = key.Period.Start.In(zone)
		key.Period.End = key.Period.End.In(zone)
		if result, err := l.Reserve(ctx, key, "same-id", 1, 1); err != nil || result.Created {
			t.Fatalf("canonical replay = %+v, %v", result, err)
		}
	})
	t.Run("zero_limit_and_overflow", func(t *testing.T) {
		l, key := newLimiter(factory(t)), Key()
		if _, err := l.Reserve(ctx, key, "zero", 1, 0); !errors.Is(err, quota.ErrExceeded) {
			t.Fatalf("zero limit: %v", err)
		}
		if _, err := l.Reserve(ctx, key, "max", math.MaxInt64, math.MaxInt64); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Reserve(ctx, key, "overflow", 1, math.MaxInt64); !errors.Is(err, quota.ErrExceeded) {
			t.Fatalf("overflow: %v", err)
		}
		if err := l.Commit(ctx, key, "max"); err != nil {
			t.Fatal(err)
		}
		wantUsage(t, l, key, 0, math.MaxInt64)
	})
	t.Run("callback_rollback", func(t *testing.T) {
		store, key := factory(t), Key()
		l := newLimiter(store)
		if _, err := l.Reserve(ctx, key, "id", 1, 2); err != nil {
			t.Fatal(err)
		}
		sentinel := errors.New("do not persist")
		err := store.Update(ctx, key, "id", func(u *quota.Usage, r *quota.Reservation) error {
			u.Reserved = 0
			r.State = quota.Cancelled
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("callback error: %v", err)
		}
		wantUsage(t, l, key, 1, 0)
		if err := l.Commit(ctx, key, "id"); err != nil {
			t.Fatal(err)
		}
		wantUsage(t, l, key, 0, 1)
	})
	t.Run("cancelled_context", func(t *testing.T) {
		l, key := newLimiter(factory(t)), Key()
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := l.Reserve(cancelled, key, "id", 1, 1); err == nil {
			t.Fatal("reserve with cancelled context succeeded")
		}
		if _, err := l.Usage(cancelled, key); err == nil {
			t.Fatal("read with cancelled context succeeded")
		}
		wantUsage(t, l, key, 0, 0)
	})
}

func wantUsage(t *testing.T, l *quota.Limiter, key quota.Key, reserved, committed int64) {
	t.Helper()
	u, err := l.Usage(context.Background(), key)
	if err != nil || u.Reserved != reserved || u.Committed != committed {
		t.Fatalf("usage = %+v, %v; want reserved %d committed %d", u, err, reserved, committed)
	}
}

func parallel(t *testing.T, count int, retryable func(error) bool, fn func(int) error) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	type outcome struct {
		index int
		err   error
	}
	results := make(chan outcome, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- outcome{index: i, err: fn(i)}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var retried int
	for result := range results {
		if result.err != nil && retryable != nil && retryable(result.err) {
			// Exercise client retry after contention has subsided. A retry must
			// still honor the limit and request identity, not create extra usage.
			retried++
			result.err = fn(result.index)
		}
		if result.err != nil {
			t.Error(result.err)
		}
	}
	if retried > 0 {
		t.Logf("retried %d uncommitted transactions after the concurrent burst", retried)
	}
}
