// A deterministic, credential-free demonstration of the reservation lifecycle.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jeorp/go-usagequota/quota"
	"github.com/jeorp/go-usagequota/quota/memory"
)

func main() {
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	l := quota.New(memory.New(), quota.WithClock(func() time.Time { return now }))
	key := quota.Key{Subject: "user-123", Resource: "ai-reviews", Period: quota.Month(now)}
	first, err := l.Reserve(ctx, key, "request-1", 1, 1)
	check(err)
	fmt.Printf("first request: created=%t state=%s\n", first.Created, first.Reservation.State)
	replay, err := l.Reserve(ctx, key, "request-1", 1, 1)
	check(err)
	fmt.Printf("same request: created=%t state=%s\n", replay.Created, replay.Reservation.State)
	_, err = l.Reserve(ctx, key, "request-2", 1, 1)
	if !errors.Is(err, quota.ErrExceeded) {
		log.Fatalf("expected limit error, got %v", err)
	}
	fmt.Println("another request: limit exceeded")
	// The first job is known not to have run. Release its budget.
	check(l.Cancel(ctx, key, "request-1"))
	fmt.Println("first request cancelled: capacity returned")
	second, err := l.Reserve(ctx, key, "request-2", 1, 1)
	check(err)
	if second.Created {
		// Application work belongs here. The example has no external side effects.
		check(l.Commit(ctx, key, "request-2"))
	}
	u, err := l.Usage(ctx, key)
	check(err)
	fmt.Printf("usage: reserved=%d committed=%d remaining=%d\n", u.Reserved, u.Committed, u.Remaining(1))
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
