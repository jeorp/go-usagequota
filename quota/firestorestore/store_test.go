package firestorestore

import (
	"context"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/jeorp/go-usagequota/internal/quotatest"
)

func TestBudgetIdentity(t *testing.T) {
	// No network calls; client is used only to construct references.
	client, err := firestore.NewClient(context.Background(), "demo-go-usagequota", option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	store := New(client, client.Collection("quotas"))
	key := quotatest.Key()
	id := store.budget(key).ID
	if len(id) != 64 || strings.Contains(id, "/") {
		t.Fatalf("unsafe ID: %q", id)
	}
	other := key
	other.Period.Start = other.Period.Start.In(time.FixedZone("JST", 9*60*60))
	if store.budget(other).ID != id {
		t.Fatal("same instant generated a different ID")
	}
	other = key
	other.Period.End = other.Period.End.Add(time.Nanosecond)
	if store.budget(other).ID == id {
		t.Fatal("different end time collided")
	}
	key.Subject, key.Resource = "a/b", "c"
	other.Subject, other.Resource, other.Period = "a", "b/c", key.Period
	if store.budget(key).ID == store.budget(other).ID {
		t.Fatal("delimiter collision")
	}
}
