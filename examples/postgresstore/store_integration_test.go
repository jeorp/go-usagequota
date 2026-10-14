//go:build integration

package postgresstore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/jeorp/go-usagequota/internal/quotatest"
	"github.com/jeorp/go-usagequota/quota"
)

var tableSequence atomic.Uint64

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("integration tests require POSTGRES_TEST_DSN as a PostgreSQL URL pointing to a loopback server")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatal("POSTGRES_TEST_DSN must point to a loopback server")
	}
	return dsn
}

func testDB(t *testing.T, naming schema.Namer) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(testDSN(t)), &gorm.Config{NamingStrategy: naming, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(20)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

func testStore(t *testing.T) *Store {
	t.Helper()
	prefix := fmt.Sprintf("quota_test_%d_%d_", time.Now().UnixNano(), tableSequence.Add(1))
	db := testDB(t, schema.NamingStrategy{TablePrefix: prefix})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.WithContext(ctx).Migrator().DropTable(&quotaBudget{}); err != nil {
			t.Error(err)
		}
	})
	store := New(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestContract(t *testing.T) {
	quotatest.Run(t, func(t *testing.T) quota.Store { return testStore(t) })
}

func TestIndependentClientsShareBudget(t *testing.T) {
	store := testStore(t)
	db := testDB(t, store.db.NamingStrategy)
	other := New(db)
	ctx, key := context.Background(), quotatest.Key()
	clock := quota.WithClock(func() time.Time { return quotatest.Now })
	first, second := quota.New(store, clock), quota.New(other, clock)
	if _, err := first.Reserve(ctx, key, "shared", 1, 1); err != nil {
		t.Fatal(err)
	}
	if result, err := second.Reserve(ctx, key, "shared", 1, 1); err != nil || result.Created {
		t.Fatalf("other client replay = %+v, %v", result, err)
	}
	if err := second.Commit(ctx, key, "shared"); err != nil {
		t.Fatal(err)
	}
	if u, err := first.Usage(ctx, key); err != nil || u != (quota.Usage{Committed: 1}) {
		t.Fatalf("shared usage = %+v, %v", u, err)
	}
}

func TestCancelledUpdateRollsBack(t *testing.T) {
	store := testStore(t)
	ctx, key := context.Background(), quotatest.Key()
	limiter := quota.New(store, quota.WithClock(func() time.Time { return quotatest.Now }))
	if _, err := limiter.Reserve(ctx, key, "id", 1, 1); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	err := store.Update(cancelled, key, "id", func(usage *quota.Usage, reservation *quota.Reservation) error {
		*usage = quota.Usage{Committed: 1}
		reservation.State = quota.Committed
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled update: %v", err)
	}
	usage, err := store.Read(ctx, key)
	if err != nil || usage != (quota.Usage{Reserved: 1}) {
		t.Fatalf("usage after rollback = %+v, %v", usage, err)
	}
	if err := limiter.Commit(ctx, key, "id"); err != nil {
		t.Fatalf("original reservation was not preserved: %v", err)
	}
}

func TestUpdateClearsReservation(t *testing.T) {
	store := testStore(t)
	ctx, key := context.Background(), quotatest.Key()
	limiter := quota.New(store, quota.WithClock(func() time.Time { return quotatest.Now }))
	for _, id := range []string{"remove", "keep"} {
		if _, err := limiter.Reserve(ctx, key, id, 1, 2); err != nil {
			t.Fatal(err)
		}
	}
	err := store.Update(ctx, key, "remove", func(usage *quota.Usage, reservation *quota.Reservation) error {
		usage.Reserved -= reservation.Amount
		*reservation = quota.Reservation{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := limiter.Reserve(ctx, key, "remove", 1, 2)
	if err != nil || !result.Created {
		t.Fatalf("reserve after deletion = %+v, %v", result, err)
	}
	if err := limiter.Commit(ctx, key, "keep"); err != nil {
		t.Fatalf("unrelated reservation was deleted: %v", err)
	}
}

func TestInitKeepsExistingUsage(t *testing.T) {
	store := testStore(t)
	limiter := quota.New(store, quota.WithClock(func() time.Time { return quotatest.Now }))
	ctx, key := context.Background(), quotatest.Key()
	if _, err := limiter.Reserve(ctx, key, "id", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Commit(ctx, key, "id"); err != nil {
		t.Fatal(err)
	}
	if usage, err := limiter.Usage(ctx, key); err != nil || usage != (quota.Usage{Committed: 1}) {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
}

func TestRequestIDsSurviveJSON(t *testing.T) {
	store := testStore(t)
	limiter := quota.New(store, quota.WithClock(func() time.Time { return quotatest.Now }))
	ctx, key := context.Background(), quotatest.Key()
	ids := []string{"\xff", "\xfe", "\ufffd", "a\x00b"}
	for _, id := range ids {
		result, err := limiter.Reserve(ctx, key, id, 1, int64(len(ids)))
		if err != nil || !result.Created {
			t.Fatalf("reserve %q = %+v, %v", id, result, err)
		}
	}
	for _, id := range ids {
		result, err := limiter.Reserve(ctx, key, id, 1, int64(len(ids)))
		if err != nil || result.Created {
			t.Fatalf("replay %q = %+v, %v", id, result, err)
		}
		if err := limiter.Commit(ctx, key, id); err != nil {
			t.Fatal(err)
		}
	}
	if usage, err := limiter.Usage(ctx, key); err != nil || usage != (quota.Usage{Committed: int64(len(ids))}) {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
}
