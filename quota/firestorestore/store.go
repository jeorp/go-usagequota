// Package firestorestore implements atomic quota storage with Firestore
// transactions. It uses one budget document and one document per request ID.
package firestorestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jeorp/go-usagequota/quota"
)

// Store shares budgets across clients connected to the same collection.
// The caller owns the client. Use a dedicated collection; do not apply TTL to
// live budgets or reservation documents, because deletion loses deduplication.
type Store struct {
	client     *firestore.Client
	collection *firestore.CollectionRef
}

var _ quota.Store = (*Store)(nil)

// New uses a caller-selected collection, for example client.Collection("quotas").
// The collection must belong to client.
func New(client *firestore.Client, collection *firestore.CollectionRef) *Store {
	if client == nil || collection == nil {
		panic("firestorestore: nil client or collection")
	}
	return &Store{client: client, collection: collection}
}

type budgetDocument struct {
	Version   int   `firestore:"version"`
	Reserved  int64 `firestore:"reserved"`
	Committed int64 `firestore:"committed"`
}

type reservationDocument struct {
	Version int         `firestore:"version"`
	Amount  int64       `firestore:"amount"`
	State   quota.State `firestore:"state"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) budget(key quota.Key) *firestore.DocumentRef {
	key = key.Canonical()
	// Encoding an array avoids delimiter collisions and accepts arbitrary IDs,
	// including slashes. Hashes are document IDs, not an anonymization guarantee.
	identity, _ := json.Marshal([4]string{key.Subject, key.Resource,
		key.Period.Start.Format(time.RFC3339Nano), key.Period.End.Format(time.RFC3339Nano)})
	return s.collection.Doc(digest(identity))
}

func readUsage(snapshot *firestore.DocumentSnapshot, err error) (quota.Usage, error) {
	if status.Code(err) == codes.NotFound {
		return quota.Usage{}, nil
	}
	if err != nil {
		return quota.Usage{}, err
	}
	if !snapshot.Exists() {
		return quota.Usage{}, nil
	}
	var doc budgetDocument
	if err := snapshot.DataTo(&doc); err != nil {
		return quota.Usage{}, fmt.Errorf("decode budget: %w", err)
	}
	if doc.Version != 1 {
		return quota.Usage{}, quota.ErrCorrupt
	}
	return quota.Usage{Reserved: doc.Reserved, Committed: doc.Committed}, nil
}

func readReservation(snapshot *firestore.DocumentSnapshot, err error) (quota.Reservation, error) {
	if status.Code(err) == codes.NotFound {
		return quota.Reservation{}, nil
	}
	if err != nil {
		return quota.Reservation{}, err
	}
	if !snapshot.Exists() {
		return quota.Reservation{}, nil
	}
	var doc reservationDocument
	if err := snapshot.DataTo(&doc); err != nil {
		return quota.Reservation{}, fmt.Errorf("decode reservation: %w", err)
	}
	if doc.Version != 1 || doc.Amount <= 0 || (doc.State != quota.Pending && doc.State != quota.Committed && doc.State != quota.Cancelled) {
		return quota.Reservation{}, quota.ErrCorrupt
	}
	return quota.Reservation{Amount: doc.Amount, State: doc.State}, nil
}

func (s *Store) Update(ctx context.Context, key quota.Key, requestID string, fn func(*quota.Usage, *quota.Reservation) error) error {
	budget := s.budget(key)
	request := budget.Collection("reservations").Doc(digest([]byte(requestID)))
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// Acquire the read set together. Separate reads increase contention when
		// many transactions are creating different requests in the same budget.
		snapshots, err := tx.GetAll([]*firestore.DocumentRef{budget, request})
		if err != nil {
			return err
		}
		u, err := readUsage(snapshots[0], nil)
		if err != nil {
			return err
		}
		r, err := readReservation(snapshots[1], nil)
		if err != nil {
			return err
		}
		beforeUsage, beforeReservation := u, r
		if err := fn(&u, &r); err != nil {
			return err
		}
		if u == beforeUsage && r == beforeReservation {
			return nil
		}
		if err := tx.Set(budget, budgetDocument{Version: 1, Reserved: u.Reserved, Committed: u.Committed}); err != nil {
			return err
		}
		return tx.Set(request, reservationDocument{Version: 1, Amount: r.Amount, State: r.State})
	})
}

func (s *Store) Read(ctx context.Context, key quota.Key) (quota.Usage, error) {
	return readUsage(s.budget(key).Get(ctx))
}
