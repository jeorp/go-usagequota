package quota_test

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/jeorp/go-usagequota/quota"
)

func TestConsume(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		used, amount, limit, want int64
		err                       error
	}{
		{"first attempt", 0, 1, 10, 1, nil},
		{"existing usage", 8, 1, 10, 9, nil},
		{"exactly full", 9, 1, 10, 10, nil},
		{"full", 10, 1, 10, 10, quota.ErrExceeded},
		{"lowered limit", 10, 1, 5, 10, quota.ErrExceeded},
		{"characters", 7, 3, 10, 10, nil},
		{"zero limit", 0, 1, 0, 0, quota.ErrExceeded},
		{"invalid amount", 3, 0, 10, 3, quota.ErrInvalid},
		{"negative amount", 3, -1, 10, 3, quota.ErrInvalid},
		{"negative limit", 3, 1, -1, 3, quota.ErrInvalid},
		{"corrupt usage", -1, 1, 10, -1, quota.ErrCorrupt},
		{"max integer", math.MaxInt64 - 1, 1, math.MaxInt64, math.MaxInt64, nil},
		{"overflow", math.MaxInt64, 1, math.MaxInt64, math.MaxInt64, quota.ErrExceeded},
		{"large amount", 1, math.MaxInt64, math.MaxInt64, 1, quota.ErrExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := quota.Consume(tc.used, tc.amount, tc.limit)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("Consume = %d, %v; want %d, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func FuzzConsume(f *testing.F) {
	f.Add(int64(0), int64(1), int64(10))
	f.Add(int64(math.MaxInt64), int64(1), int64(math.MaxInt64))
	f.Fuzz(func(t *testing.T, used, amount, limit int64) {
		got, err := quota.Consume(used, amount, limit)
		total := new(big.Int).Add(big.NewInt(used), big.NewInt(amount))
		var wantErr error
		switch {
		case amount <= 0 || limit < 0:
			wantErr = quota.ErrInvalid
		case used < 0:
			wantErr = quota.ErrCorrupt
		case total.Cmp(big.NewInt(limit)) > 0:
			wantErr = quota.ErrExceeded
		}
		want := used
		if wantErr == nil {
			want = total.Int64()
		}
		if got != want || !errors.Is(err, wantErr) {
			t.Fatalf("Consume(%d,%d,%d) = %d,%v; want %d,%v", used, amount, limit, got, err, want, wantErr)
		}
	})
}
