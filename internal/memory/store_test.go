package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/internal/memory"
	"github.com/jeorp/go-usagequota/internal/quotatest"
	"github.com/jeorp/go-usagequota/quota"
)

func TestContract(t *testing.T) {
	quotatest.Run(t, func(*testing.T) quota.Store { return memory.New() })
}

func TestBlockedUpdateHonorsContext(t *testing.T) {
	store := memory.New()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- store.Update(context.Background(), quotatest.Key(), "first", func(*quota.Usage, *quota.Reservation) error {
			close(entered)
			<-release
			return errors.New("rollback")
		})
	}()
	<-entered
	defer func() { close(release); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := store.Update(ctx, quotatest.Key(), "second", func(*quota.Usage, *quota.Reservation) error {
		t.Error("cancelled callback ran")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked update = %v", err)
	}
}

func TestCallbackPointersDoNotAliasStorage(t *testing.T) {
	store := memory.New()
	var leakedUsage *quota.Usage
	var leakedReservation *quota.Reservation
	err := store.Update(context.Background(), quotatest.Key(), "id", func(u *quota.Usage, r *quota.Reservation) error {
		*u = quota.Usage{Reserved: 1}
		*r = quota.Reservation{Amount: 1, State: quota.Pending}
		leakedUsage, leakedReservation = u, r
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	leakedUsage.Reserved = 99
	leakedReservation.State = quota.Cancelled
	l := quota.New(store)
	if err := l.Commit(context.Background(), quotatest.Key(), "id"); err != nil {
		t.Fatal(err)
	}
	u, err := l.Usage(context.Background(), quotatest.Key())
	if err != nil || u != (quota.Usage{Committed: 1}) {
		t.Fatalf("aliased state: %+v, %v", u, err)
	}
}
