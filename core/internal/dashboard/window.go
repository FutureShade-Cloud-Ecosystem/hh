package dashboard

import (
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// window.go, WHICH DAYS A WIDGET'S NUMBER COVERS.
//
// -- WHAT THIS CLOSES ----------------------------------------------------------------------------
//
// A widget names a WINDOW ("today", "this month") and the substrate has always stored one. Nothing
// ever turned that name into a pair of instants, so no query could be narrowed by it: the range was
// stored, echoed, printed under the number, and applied by nothing. Migration 0205 made the captions
// true by forbidding the question; this file is what lets a provider ANSWER one.
//
// -- THE HOUSE ZONE IS DUPLICATED HERE, ON PURPOSE -----------------------------------------------
//
// This is the fourth copy of "America/Toronto" in the backend, messaging/rollup.go, inventory/
// transfer_eta.go and gl/businessday.go hold the others, and gl/businessday.go already argues for
// the copy: a shared constant would need a package every module imports, and a module reaching into
// another module's package for a string is the cross-module access the house forbids for exactly the
// reasons it forbids the table access. Four unexported constants that agree beat one import graph
// that does not.
//
// The drift is guarded rather than assumed: window_test.go reads the other three files and fails if
// any of them names a different zone.
//
// -- WHY IT MATTERS THAT THIS IS NOT time.Local -------------------------------------------------
//
// The cockpit's "today" was `sc.Now.Format("2006-01-02")`, the calendar of whatever timezone the
// PROCESS happened to be configured with, which nobody had chosen. This is the identical defect
// gl/businessday.go was written to close for the ledger's fiscal period, and it reached eight
// measurements here. A dealer in Trenton whose server runs UTC saw "sales today" roll over at 8pm.
const houseZone = "America/Toronto"

// houseLocation is the yard's calendar. A tzdata failure degrades to UTC rather than panicking: a
// dashboard that will not start is worse than one whose day boundary is four hours out, and the
// warning is the operator's signal. Note that a distroless image with no tzdata takes this arm
// silently, nothing in this repo imports time/tzdata.
func houseLocation() *time.Location {
	loc, err := time.LoadLocation(houseZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Window is a HALF-OPEN [Since, Until) pair of instants, or the zero value for "no window".
//
// HALF-OPEN, NOT CLOSED, and that is a different choice from assist/dates.go's inclusive [From, To].
// That file answers over plain DAY STRINGS against date-typed columns, where an inclusive end is
// natural. These bounds are compared against TIMESTAMPTZ columns, where an inclusive end has to be
// written as "< the next midnight" anyway and writing it as "<= 23:59:59" loses every row in the
// last second of the day. One shape, no edge case.
//
// THE ZERO VALUE IS MEANINGFUL. `open`, `all_time` and `now` name populations rather than periods,
// so they resolve to no bounds at all, and a provider tests IsZero() rather than being handed a
// window covering all of history, which would silently become a real predicate the first time
// somebody stored a date before it.
type Window struct {
	Since time.Time
	Until time.Time
}

// IsZero reports the "no window" case: this measure's own natural population, unnarrowed.
func (w Window) IsZero() bool { return w.Since.IsZero() && w.Until.IsZero() }

// ResolveWindow turns a named window into instants, in the house zone.
//
// -- THE CONVENTIONS, AND WHERE EACH ONE COMES FROM ---------------------------------------------
//
//	today      [midnight, next midnight). NOT [midnight, now]: widget.go's own header says two people
//	           reading the same widget must be reading the same period, and a bound that moves with
//	           the clock makes the same widget mean something different every time it refreshes.
//	wtd        THE WEEK STARTS MONDAY. Not Go's Sunday-first Weekday, the yard's week, the one on
//	           the whiteboard. assist/dates.go ruled this and pinned it; this is the same answer.
//	mtd/qtd/ytd CALENDAR month, quarter and year. gl_policy carries a per-branch
//	           fiscal_year_start_month (0032) and NOTHING in the product applies it, not even gl's
//	           own day helper, so a fiscal reading here would be the first, and it would disagree
//	           with every other year-to-date figure the product prints. Calendar, until somebody
//	           rules otherwise on its own terms.
//	last_N     N calendar days INCLUDING today, so "last 7 days" is today and the six before it.
//	           assist/dates.go's convention (backDays(n, 6) for 7), so a chat answer and a widget
//	           agree about last week.
//	open,      No window. These name a population, not a period.
//	all_time,
//	now
//
// -- DST -----------------------------------------------------------------------------------------
//
// Every bound is built with time.Date on a civil date, and America/Toronto has no ambiguous or
// nonexistent LOCAL MIDNIGHT, its transitions are at 02:00, so midnight is single-valued for every
// civil date and needs no special case. Day arithmetic uses AddDate, which walks the CALENDAR rather
// than adding 24h, so a window spanning a transition is still N days and not N days ± an hour. That
// is the same argument inventory/transfer_eta.go makes for the same reason.
func ResolveWindow(named string, now time.Time) Window {
	return ResolveWindowIn(houseday.DefaultZone(), named, now)
}

// ResolveWindowIn is ResolveWindow in a named dealer's zone (item 3b slice 3): "today" is the dealer's
// own calendar day. The zero Zone is the Ontario dealer's, so ResolveWindow is exactly the old answer.
func ResolveWindowIn(zone houseday.Zone, named string, now time.Time) Window {
	loc := zone.Location()
	if now.IsZero() {
		return Window{}
	}
	n := now.In(loc)
	midnight := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	today := midnight(n)
	tomorrow := today.AddDate(0, 0, 1)

	switch named {
	case RangeToday:
		return Window{Since: today, Until: tomorrow}
	case RangeWTD:
		// Monday. Go counts Sunday as 0, so Sunday is six days INTO the week, not the start of one.
		back := (int(n.Weekday()) + 6) % 7
		return Window{Since: today.AddDate(0, 0, -back), Until: tomorrow}
	case RangeMTD:
		return Window{Since: time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc), Until: tomorrow}
	case RangeQTD:
		q := time.Month((int(n.Month())-1)/3*3 + 1)
		return Window{Since: time.Date(n.Year(), q, 1, 0, 0, 0, 0, loc), Until: tomorrow}
	case RangeYTD:
		return Window{Since: time.Date(n.Year(), time.January, 1, 0, 0, 0, 0, loc), Until: tomorrow}
	case RangeLast7:
		return Window{Since: today.AddDate(0, 0, -6), Until: tomorrow}
	case RangeLast14:
		return Window{Since: today.AddDate(0, 0, -13), Until: tomorrow}
	case RangeLast30:
		return Window{Since: today.AddDate(0, 0, -29), Until: tomorrow}
	case RangeLast90:
		return Window{Since: today.AddDate(0, 0, -89), Until: tomorrow}
	}
	// open, all_time, now, and anything this build does not recognise. An unknown name resolving to
	// NO window is the safe arm: the measure returns its natural population, which is what it did
	// before this file existed, rather than a window somebody guessed at.
	return Window{}
}

// HouseDay is the calendar day an instant falls on in the house zone, as the date literal a query
// compares against. It replaces formatting the raw instant, which read the process's timezone.
func HouseDay(at time.Time) string { return HouseDayIn(houseday.DefaultZone(), at) }

// HouseDayIn is HouseDay in a named dealer's zone.
func HouseDayIn(zone houseday.Zone, at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.In(zone.Location()).Format("2006-01-02")
}
