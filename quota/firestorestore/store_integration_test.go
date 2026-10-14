//go:build integration

package firestorestore

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jeorp/go-usagequota/internal/quotatest"
	"github.com/jeorp/go-usagequota/quota"
)

func emulatorClient(t *testing.T) *firestore.Client {
	t.Helper()
	host, _, err := net.SplitHostPort(os.Getenv("FIRESTORE_EMULATOR_HOST"))
	ip := net.ParseIP(host)
	if err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		t.Fatal("integration tests require FIRESTORE_EMULATOR_HOST pointing to a loopback emulator (e.g. 127.0.0.1:8086)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := firestore.NewClient(ctx, "demo-go-usagequota")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func testStore(t *testing.T) *Store {
	t.Helper()
	client := emulatorClient(t)
	collection := client.Collection(fmt.Sprintf("quota-test-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano()))
	t.Cleanup(func() {
		// Delete only documents owned by this individual test, never flush the emulator.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		iter := collection.Documents(ctx)
		defer iter.Stop()
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			requests := doc.Ref.Collection("reservations").Documents(ctx)
			for {
				request, err := requests.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					t.Error(err)
					requests.Stop()
					return
				}
				if _, err := request.Ref.Delete(ctx); err != nil {
					t.Error(err)
				}
			}
			requests.Stop()
			if _, err := doc.Ref.Delete(ctx); err != nil {
				t.Error(err)
			}
		}
	})
	return New(client, collection)
}

func TestContract(t *testing.T) {
	quotatest.Run(t, func(t *testing.T) quota.Store { return testStore(t) }, func(err error) bool {
		return status.Code(err) == codes.Aborted
	})
}

func TestIndependentClientsShareBudget(t *testing.T) {
	store := testStore(t)
	client := emulatorClient(t)
	other := New(client, client.Collection(store.collection.ID))
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
