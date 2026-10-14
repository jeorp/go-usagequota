package quota

// Consume adds amount to used if the result fits within limit. On failure it
// returns used unchanged. Zero limit denies consumption; negative limits and
// nonpositive amounts are invalid. Negative stored usage is corrupt.
//
// The caller must read the counter, call Consume, and save it in one transaction.
// Retries of completed requests count again, and failed work is not refunded.
// Use Limiter for deduplication or cancellation.
func Consume(used, amount, limit int64) (int64, error) {
	if amount <= 0 || limit < 0 {
		return used, ErrInvalid
	}
	if used < 0 {
		return used, ErrCorrupt
	}
	// Compare by subtraction to avoid overflowing used + amount.
	if used > limit || amount > limit-used {
		return used, ErrExceeded
	}
	return used + amount, nil
}
