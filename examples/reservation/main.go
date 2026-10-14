// This example shows how to reserve, cancel, and commit usage.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jeorp/go-usagequota/internal/memory"
	"github.com/jeorp/go-usagequota/quota"
)

func main() {
	ctx := context.Background()
	demoTime := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	limiter := quota.New(memory.New(), quota.WithClock(func() time.Time { return demoTime }))
	key := quota.Key{
		Subject:  "user-123",
		Resource: "reviews",
		Period:   quota.Month(demoTime),
	}
	const limit int64 = 1
	const firstRequest = "request-1"
	const secondRequest = "request-2"

	// Reserve the only available unit for the first request.
	first, err := limiter.Reserve(ctx, key, firstRequest, 1, limit)
	check(err)
	fmt.Printf("first request: new reservation = %t\n", first.Created)

	// A retry with the same request ID uses the existing reservation.
	retry, err := limiter.Reserve(ctx, key, firstRequest, 1, limit)
	check(err)
	fmt.Printf("retry: new reservation = %t\n", retry.Created)

	// The second request cannot start while the first holds the unit.
	_, err = limiter.Reserve(ctx, key, secondRequest, 1, limit)
	if !errors.Is(err, quota.ErrExceeded) {
		log.Fatalf("expected limit error, got %v", err)
	}
	fmt.Println("second request: limit exceeded")

	// The first request did not run, so release its reserved unit.
	check(limiter.Cancel(ctx, key, firstRequest))
	fmt.Println("first request: cancelled")

	// The second request can now reserve the unit.
	second, err := limiter.Reserve(ctx, key, secondRequest, 1, limit)
	check(err)
	if second.Created {
		// Only a new reservation starts work. This demo has no external work;
		// a real caller would commit only after its work succeeds.
		check(limiter.Commit(ctx, key, secondRequest))
		fmt.Println("second request: committed")
	}

	usage, err := limiter.Usage(ctx, key)
	check(err)
	fmt.Printf("usage: reserved=%d committed=%d remaining=%d\n",
		usage.Reserved, usage.Committed, usage.Remaining(limit))
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
