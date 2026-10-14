package postgresstore

import (
	"bytes"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/internal/quotatest"
)

func TestBudgetIdentity(t *testing.T) {
	key := quotatest.Key()
	id := budgetDigest(key)
	if len(id) != 32 {
		t.Fatalf("digest length = %d", len(id))
	}
	other := key
	other.Period.Start = other.Period.Start.In(time.FixedZone("JST", 9*60*60))
	if !bytes.Equal(budgetDigest(other), id) {
		t.Fatal("same instant generated a different ID")
	}
	other = key
	other.Period.End = other.Period.End.Add(time.Nanosecond)
	if bytes.Equal(budgetDigest(other), id) {
		t.Fatal("different end time generated the same ID")
	}
	key.Subject, key.Resource = "a/b", "c"
	other.Subject, other.Resource, other.Period = "a", "b/c", key.Period
	if bytes.Equal(budgetDigest(key), budgetDigest(other)) {
		t.Fatal("delimiter collision")
	}
}
