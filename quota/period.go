package quota

import "time"

// Period is a half-open interval [Start, End). Its boundaries identify a budget.
// Keep these boundaries stable when retrying an operation or changing its limit.
type Period struct {
	Start time.Time
	End   time.Time
}

// Month returns the UTC calendar month containing at.
func Month(at time.Time) Period {
	at = at.UTC()
	start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	return Period{Start: start, End: start.AddDate(0, 1, 0)}
}

// Day returns the calendar day containing at in its location. It uses calendar
// arithmetic, so a daylight-saving transition can produce a 23- or 25-hour day.
// Call Day(at.In(location)) to select an account's time zone.
func Day(at time.Time) Period {
	start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	return Period{Start: start.UTC(), End: start.AddDate(0, 0, 1).UTC()}
}

// Contains reports whether at belongs to the period.
func (p Period) Contains(at time.Time) bool {
	return !at.Before(p.Start) && at.Before(p.End)
}

func (p Period) valid() bool {
	return !p.Start.IsZero() && !p.End.IsZero() && p.Start.Before(p.End)
}
