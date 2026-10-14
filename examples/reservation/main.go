// This example shows that retries share a reservation, cancellation restores
// capacity, and completed work remains charged against the limit.
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
	now := time.Now()
	limiter := quota.New(memory.New(), quota.WithClock(func() time.Time { return now }))
	key := quota.Key{
		Subject:  "user-123",
		Resource: "reviews",
		Period:   quota.Month(now),
	}
	const limit int64 = 1
	const firstRequest = "request-1"
	const secondRequest = "request-2"

	first, err := limiter.Reserve(ctx, key, firstRequest, 1, limit)
	check(err)
	fmt.Printf("first request: new reservation = %t\n", first.Created)

	// The same request ID prevents a retry from starting duplicate work.
	retry, err := limiter.Reserve(ctx, key, firstRequest, 1, limit)
	check(err)
	fmt.Printf("retry: new reservation = %t\n", retry.Created)

	// Pending work counts against the limit, so another request is denied.
	_, err = limiter.Reserve(ctx, key, secondRequest, 1, limit)
	if !errors.Is(err, quota.ErrExceeded) {
		log.Fatalf("expected limit error, got %v", err)
	}
	fmt.Println("second request: limit exceeded")

	// Cancellation is safe here because the first request never ran.
	check(limiter.Cancel(ctx, key, firstRequest))
	fmt.Println("first request: cancelled")

	second, err := limiter.Reserve(ctx, key, secondRequest, 1, limit)
	check(err)
	if second.Created {
		// Run the work here, then commit after it succeeds.
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
