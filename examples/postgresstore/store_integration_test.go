//go:build integration

package postgresstore

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/jeorp/go-usagequota/internal/quotatest"
	"github.com/jeorp/go-usagequota/quota"
)

var schemaSequence atomic.Uint64

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

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(20)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testStore(t *testing.T) *Store {
	t.Helper()
	db := testDB(t)
	schema := fmt.Sprintf("quota_test_%d_%d", time.Now().UnixNano(), schemaSequence.Add(1))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "DROP SCHEMA "+quoteIdentifier(schema)+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	store := New(db, schema)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestContract(t *testing.T) {
	quotatest.Run(t, func(t *testing.T) quota.Store { return testStore(t) })
}

func TestIndependentClientsShareBudget(t *testing.T) {
	store := testStore(t)
	db := testDB(t)
	other := New(db, strings.Trim(store.schema, `"`))
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
