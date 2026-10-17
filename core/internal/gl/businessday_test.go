package gl

import (
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// businessday_test.go, the ledger's day boundary, pinned at the two instants where it used to move.
//
// These are written as CALENDAR FACTS, not as "whatever the machine says", so they hold identically
// on a developer's laptop at UTC−7, on a UTC server, and in CI. That is the whole point: the defect
// this closes was that the answer depended on the machine.

// THE MONTH-END CASE, and the one that costs money. 9pm in Ontario on the last day of August is
// already 1 September in UTC. Before this, a server running UTC filed that sale into SEPTEMBER's
// books, a month of revenue quietly leaking across the boundary, one evening at a time.
func TestFiscalYearMonth_AnOntarioEveningStaysInItsOwnMonth(t *testing.T) {
	toronto, err := time.LoadLocation(houseZone)
	if err != nil {
		t.Skipf("no tzdata for %s on this machine: %v", houseZone, err)
	}

	// 31 August 2026, 21:00, in the yard's own time. EDT is UTC−4, so this is 1 September 01:00 UTC.
	sale := time.Date(2026, 8, 31, 21, 0, 0, 0, toronto)
	if got := sale.UTC().Format("2006-01-02"); got != "2026-09-01" {
		t.Fatalf("the fixture is not the case this test is about: in UTC it is %s, want 2026-09-01", got)
	}

	y, m := fiscalYearMonth(sale)
	if y != 2026 || m != 8 {
		t.Fatalf("a sale rung at 9pm on 31 August in Ontario was filed into %04d-%02d, the ledger's "+
			"month boundary is not the yard's. want 2026-08", y, m)
	}

	// The same instant expressed in UTC must reach the SAME period. This is the arm that matters in
	// production, because on a UTC server that is the only form the timestamp ever takes.
	y2, m2 := fiscalYearMonth(sale.UTC())
	if y2 != y || m2 != m {
		t.Fatalf("the same instant filed into %04d-%02d as UTC and %04d-%02d as local, the conversion "+
			"is not happening, only the timestamp's own location is being read", y2, m2, y, m)
	}
}

// The mirror case: the first evening of a month must not fall BACK into the previous one. A boundary
// that only holds in one direction is an off-by-one waiting for the other.
func TestFiscalYearMonth_TheFirstMorningDoesNotFallBack(t *testing.T) {
	toronto, err := time.LoadLocation(houseZone)
	if err != nil {
		t.Skipf("no tzdata for %s on this machine: %v", houseZone, err)
	}
	// 1 September 2026, 00:30 Ontario, 04:30 UTC the same day, so this one agrees either way and is
	// the control for the case above rather than a second finding.
	open := time.Date(2026, 9, 1, 0, 30, 0, 0, toronto)
	if y, m := fiscalYearMonth(open); y != 2026 || m != 9 {
		t.Fatalf("00:30 on 1 September filed into %04d-%02d, want 2026-09", y, m)
	}
}

// A YEAR boundary is a month boundary with worse consequences: it decides which fiscal YEAR a posting
// closes into, and a year-end close is not something anybody wants to reopen.
func TestFiscalYearMonth_NewYearsEveStaysInTheOldYear(t *testing.T) {
	toronto, err := time.LoadLocation(houseZone)
	if err != nil {
		t.Skipf("no tzdata for %s on this machine: %v", houseZone, err)
	}
	// 31 December 2026, 20:00 Ontario. EST is UTC−5, so this is 1 January 2027 01:00 UTC.
	lastSale := time.Date(2026, 12, 31, 20, 0, 0, 0, toronto)
	if got := lastSale.UTC().Format("2006"); got != "2027" {
		t.Fatalf("the fixture is not the case this test is about: in UTC the year is %s, want 2027", got)
	}
	if y, m := fiscalYearMonth(lastSale); y != 2026 || m != 12 {
		t.Fatalf("the last sale of 2026 was filed into %04d-%02d, into the wrong FISCAL YEAR. want 2026-12", y, m)
	}
}

// The year-end close writes its own instant deliberately: `time.Date(year, 12, 31, 12, 0, 0, 0, UTC)`
// at close.go:198. Noon UTC is 7am Ontario on the same date, which is why that choice survives the
// conversion, but it survives it by luck of the hour, so this pins it. A close entry that landed in
// the following January would be a year-end close in the wrong year.
func TestFiscalYearMonth_TheYearEndCloseInstantSurvivesTheConversion(t *testing.T) {
	closeAt := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)
	if y, m := fiscalYearMonth(closeAt); y != 2026 || m != 12 {
		t.Fatalf("the year-end close instant (close.go:198) resolves to %04d-%02d, want 2026-12. "+
			"If that instant is ever moved to midnight it will cross the boundary the other way.", y, m)
	}
}

// GUARD THE GUARD, and it is not decoration. Every assertion above passes trivially if
// houseLocation() silently fell back to UTC, the conversion would be a no-op and the tests would
// still be green while the ledger's boundary had quietly moved to Greenwich. Distroless and scratch
// images ship no tzdata, so this is a real deployment failure mode, not a hypothetical.
func TestHouseLocation_ResolvesRatherThanFallingBack(t *testing.T) {
	loc := houseLocation()
	if loc == time.UTC {
		t.Fatal("houseLocation() fell back to UTC, the tzdata for America/Toronto is missing from this " +
			"machine or image. Every boundary test above is passing vacuously and the ledger's month " +
			"boundary has moved to Greenwich. Ship tzdata, or import _ \"time/tzdata\".")
	}
	if loc.String() != houseZone {
		t.Fatalf("houseLocation() resolved to %q, want %q", loc.String(), houseZone)
	}
	// And the offset must actually differ from UTC in summer, or the conversion cannot move anything.
	summer := time.Date(2026, 8, 31, 21, 0, 0, 0, loc)
	if _, off := summer.Zone(); off == 0 {
		t.Fatalf("the house zone reports a zero offset in August, it is not Ontario")
	}
}

// A Los Angeles dealer's books close at Los Angeles midnight (item 3b slice 3): 05:00 UTC on 1 September
// is already September in Toronto and still 31 August in Los Angeles. The zero Zone is Toronto exactly.
func TestFiscalYearMonthIn_FollowsTheDealersZone(t *testing.T) {
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.September, 1, 5, 0, 0, 0, time.UTC)
	if y, m := fiscalYearMonthIn(la, at); y != 2026 || m != 8 {
		t.Fatalf("Los Angeles = %d-%02d, want 2026-08", y, m)
	}
	if y, m := fiscalYearMonthIn(houseday.DefaultZone(), at); y != 2026 || m != 9 {
		t.Fatalf("Toronto = %d-%02d, want 2026-09", y, m)
	}
	if y, m := fiscalYearMonth(at); y != 2026 || m != 9 {
		t.Fatalf("the default fiscalYearMonth moved: %d-%02d", y, m)
	}
}
