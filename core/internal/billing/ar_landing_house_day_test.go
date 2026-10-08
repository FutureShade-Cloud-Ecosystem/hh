package billing

// ar_landing_house_day_test.go, DaysPastDue on the dealer's day.
//
// DaysPastDue used to truncate both instants at Greenwich, so an account read in the Ontario
// evening (after the UTC day change, before the house zone's own) aged one day early, the defect
// testaudit's day audit carried as its ar_landing.go entry. The legs below fix both instants, so
// they are red on the UTC truncation whatever hour the suite runs, and each span crosses a
// daylight saving change, so a whole-day count that slipped to clock hours (24 hour multiples
// across a zone that skipped or repeated an hour) is red too.

import (
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

func TestDaysPastDue_TheScannedDueDatesDayIsNeverShifted(t *testing.T) {
	// A due date off a DATE column arrives from the scan as midnight UTC, and midnight UTC is
	// still the previous evening in the house zone: reading THAT value in the house zone ages
	// every invoice one day early (the HH-195 leg caught exactly this). The due date's day is the
	// calendar day it carries; only the clock is read in the house zone.
	due := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC) // a DATE column's value, scanned
	asOf := time.Date(2026, 6, 3, 12, 0, 0, 0, houseday.Location())
	if got := DaysPastDue(due, asOf); got != 0 {
		t.Fatalf("a due date scanned at UTC midnight, read at house midday, is %d days past due, want 0", got)
	}
	dueAgo := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	asOfNow := time.Date(2026, 6, 3, 12, 0, 0, 0, houseday.Location())
	if got := DaysPastDue(dueAgo, asOfNow); got != 89 {
		t.Fatalf("days past due from a scanned due date = %d, want 89", got)
	}
}

func TestDaysPastDue_CountsTheHousesDaysAcrossTheSpringChange(t *testing.T) {
	// Due on the March evening before the clocks spring forward (its UTC date is already the next
	// day), read at midday the week after: eight of the dealer's calendar days, seven to any code
	// that truncates at Greenwich.
	due := time.Date(2026, time.March, 8, 20, 30, 0, 0, houseday.Location())
	asOf := time.Date(2026, time.March, 16, 12, 0, 0, 0, houseday.Location())
	if got := DaysPastDue(due, asOf); got != 8 {
		t.Fatalf("DaysPastDue across the spring change = %d, want 8 whole house days", got)
	}
}

func TestDaysPastDue_CountsTheHousesDaysAcrossTheFallChange(t *testing.T) {
	// Due on the October evening before the clocks fall back (its UTC date is already the next
	// day), read at midday the week after: the same eight whole house days, and the repeated hour
	// must not buy the account a day.
	due := time.Date(2026, time.October, 31, 21, 30, 0, 0, houseday.Location())
	asOf := time.Date(2026, time.November, 8, 12, 0, 0, 0, houseday.Location())
	if got := DaysPastDue(due, asOf); got != 8 {
		t.Fatalf("DaysPastDue across the fall change = %d, want 8 whole house days", got)
	}
}
