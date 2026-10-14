package postgresstore

import (
	"bytes"
	"testing"
	"time"

	"github.com/jeorp/go-usagequota/internal/quotatest"
)

func TestBudgetIdentity(t *testing.T) {
	key := quotatest.Key()
	digest := budgetDigest(key)
	if len(digest) != 32 {
		t.Fatalf("digest length = %d", len(digest))
	}
	t.Run("same_instant_in_another_zone", func(t *testing.T) {
		other := key
		other.Period.Start = other.Period.Start.In(time.FixedZone("JST", 9*60*60))
		if !bytes.Equal(budgetDigest(other), digest) {
			t.Fatal("same instant generated a different ID")
		}
	})
	t.Run("different_end_time", func(t *testing.T) {
		other := key
		other.Period.End = other.Period.End.Add(time.Nanosecond)
		if bytes.Equal(budgetDigest(other), digest) {
			t.Fatal("different end time generated the same ID")
		}
	})
	t.Run("slash_in_subject_or_resource", func(t *testing.T) {
		first, second := key, key
		first.Subject, first.Resource = "a/b", "c"
		second.Subject, second.Resource = "a", "b/c"
		if bytes.Equal(budgetDigest(first), budgetDigest(second)) {
			t.Fatal("different keys generated the same ID")
		}
	})
}
