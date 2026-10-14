package quota_test

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/jeorp/go-usagequota/quota"
)

func TestMonthUsesUTCAndHandlesYearBoundary(t *testing.T) {
	at := time.Date(2027, 1, 1, 2, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	p := quota.Month(at)
	wantStart := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if p.Start != wantStart || p.End != wantStart.AddDate(0, 1, 0) {
		t.Fatalf("month = %+v", p)
	}
	if !p.Contains(p.Start) || p.Contains(p.End) || p.Contains(p.Start.Add(-time.Nanosecond)) {
		t.Fatal("period must be half-open")
	}
}

func TestDayUsesCalendarArithmetic(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		month time.Month
		day   int
		hours time.Duration
	}{{time.March, 8, 23}, {time.November, 1, 25}, {time.June, 1, 24}} {
		at := time.Date(2026, tc.month, tc.day, 12, 0, 0, 0, loc)
		p := quota.Day(at)
		if p.End.Sub(p.Start) != tc.hours*time.Hour || !p.Contains(at) {
			t.Fatalf("day %s = %+v, duration %v", at, p, p.End.Sub(p.Start))
		}
	}
}
