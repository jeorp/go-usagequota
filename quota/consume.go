package quota

// Consume checks a finite limit and returns the counter after consuming amount.
// On failure it returns used unchanged. Zero limit denies consumption; negative
// limits and nonpositive amounts are invalid. Negative stored usage is corrupt.
//
// This is a pure calculation, not a storage operation. The caller must read the
// counter, call Consume, and persist the result in the same transaction. It is
// suitable for attempt budgets that stay consumed even when later work fails.
// Repeating a committed transaction consumes again: this API does not deduplicate
// requests or provide refunds. Use Limiter for idempotent reservations instead.
func Consume(used, amount, limit int64) (int64, error) {
	if amount <= 0 || limit < 0 {
		return used, ErrInvalid
	}
	if used < 0 {
		return used, ErrCorrupt
	}
	// Subtract only after checking used <= limit, avoiding integer overflow even
	// when used or amount is MaxInt64.
	if used > limit || amount > limit-used {
		return used, ErrExceeded
	}
	return used + amount, nil
}
