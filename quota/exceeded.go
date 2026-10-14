package quota

import "fmt"

// ExceededError describes a rejected Consume, Reserve, or Spend operation.
// Used is the total usage before the attempt, including pending reservations
// for Reserve and Spend. These values describe the admission check, not a live
// snapshot or a guarantee that a later request will fit.
// Use errors.Is(err, ErrExceeded) to classify the error and errors.As to read it.
type ExceededError struct {
	Limit  int64
	Used   int64
	Amount int64
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("%s: used=%d amount=%d limit=%d", ErrExceeded, e.Used, e.Amount, e.Limit)
}

// Unwrap preserves errors.Is(err, ErrExceeded).
func (e *ExceededError) Unwrap() error { return ErrExceeded }

// Remaining returns capacity at the rejected attempt, clamped to zero when
// the limit was lowered below existing usage.
func (e *ExceededError) Remaining() int64 {
	if e.Used >= e.Limit {
		return 0
	}
	return e.Limit - e.Used
}
