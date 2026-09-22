package pos

import (
	"context"
	"testing"
	"time"
)

// sale_lookup_day_test.go, THE DAY THE COUNTER MEANS.
//
// These tests exist because the defect they close was invisible for four hours a day and correct for
// the other twenty. `ps.created_at >= $N::date` reads right, and it IS right until 8pm Ontario time,
// at which point Postgres, promoting the date at the session zone nobody sets, which is UTC, has
// already rolled over into tomorrow while the yard is still open and still ringing sales.
//
// So every assertion here is anchored at 21:00 Toronto. A test anchored at noon passes against the
// defect and against the fix and tells nobody anything.
//
// They test saleLookupBounds rather than the SQL because the SQL cannot be observed without a
// database, and the thing that was wrong was never the SQL, it was WHERE the midnight came from.
// saleLookupBounds is now the only place that answers that, for both the validation and the
// predicate, so a test of it is a test of the boundary the counter actually gets.

// toronto is the yard's calendar, loaded rather than assumed. A machine with no tzdata skips instead
// of asserting against a silent UTC fallback, which would make these tests agree with the defect.
func toronto(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(houseZone)
	if err != nil {
		t.Skipf("no tzdata for %s on this machine: %v", houseZone, err)
	}
	return loc
}

// A sale rung at 21:00 belongs to the day the yard was open, on BOTH bounds. In UTC that instant is
// already tomorrow, which is the whole defect: from= let last night in, and to= shut this evening
// out.
func TestSaleLookupBounds_TheEveningSaleBelongsToTheDayTheYardWasOpen(t *testing.T) {
	loc := toronto(t)
	const day, nextDay = "2026-08-05", "2026-08-06"
	sale := time.Date(2026, 8, 5, 21, 0, 0, 0, loc) // 21:00 EDT, 01:00 UTC on the 6th

	since, until, ok := saleLookupBounds(SaleLookupQuery{From: day, To: day})
	if !ok {
		t.Fatal("a well-formed pair of dates must resolve")
	}
	if sale.Before(since) {
		t.Errorf("a sale rung at 21:00 on %s falls before that day's own from-bound (%s), the search "+
			"the cashier ran on the day they rang it would not find it", day, since.In(loc))
	}
	if !sale.Before(until) {
		t.Errorf("a sale rung at 21:00 on %s falls on or after that day's to-bound (%s), this is the "+
			"defect the clause's own comment promised it did not have", day, until.In(loc))
	}

	// And the other half of the same coin: the NEXT day's search must not sweep this evening back up.
	nextSince, _, ok := saleLookupBounds(SaleLookupQuery{From: nextDay})
	if !ok {
		t.Fatal("a well-formed from-date must resolve")
	}
	if !sale.Before(nextSince) {
		t.Errorf("a sale rung at 21:00 on %s is still inside a from=%s search (bound %s), that is "+
			"last night's takings appearing under today", day, nextDay, nextSince.In(loc))
	}
}

// THE REGRESSION ITSELF, NAMED. `'2026-08-05'::date` at a UTC session is 2026-08-05T00:00Z, which is
// 2026-08-04 20:00 in the yard. If either bound ever lands on a Greenwich midnight again, this
// fails, and it fails without asking the reader to reason about which four hours moved.
func TestSaleLookupBounds_TheBoundIsNotGreenwichsMidnight(t *testing.T) {
	loc := toronto(t)
	since, until, ok := saleLookupBounds(SaleLookupQuery{From: "2026-08-05", To: "2026-08-05"})
	if !ok {
		t.Fatal("bounds must resolve")
	}
	if since.Equal(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)) {
		t.Error("the from-bound is midnight UTC, the ::date cast is back, and with it the four hours " +
			"of 4 August after 8pm that no cashier asked for")
	}
	if want := time.Date(2026, 8, 5, 0, 0, 0, 0, loc); !since.Equal(want) {
		t.Errorf("from-bound = %s, want the yard's own midnight %s", since.In(loc), want)
	}
	if want := time.Date(2026, 8, 6, 0, 0, 0, 0, loc); !until.Equal(want) {
		t.Errorf("to-bound = %s, want the next yard midnight %s", until.In(loc), want)
	}
	// Half-open, as dashboard/window.go:46-53 ruled for this same shape: the instant the bound names
	// is EXCLUDED, so a sale at 00:00:00 on the 6th belongs to the 6th and to no search for the 5th.
	if !until.Before(until.Add(time.Nanosecond)) {
		t.Fatal("the to-bound must be an instant the predicate compares strictly less-than")
	}
}

// AddDate walks the calendar, not 24 hours. 8 March 2026 is the spring-forward Sunday in Ontario, so
// that civil day is 23 hours long, and it is still ONE day. Adding 24h would put the bound an hour
// into the 9th and hand a "to 2026-03-08" search the first hour of Monday's sales.
func TestSaleLookupBounds_ADayAcrossADSTTransitionIsStillOneDay(t *testing.T) {
	loc := toronto(t)
	since, until, ok := saleLookupBounds(SaleLookupQuery{From: "2026-03-08", To: "2026-03-08"})
	if !ok {
		t.Fatal("bounds must resolve")
	}
	if h := until.Sub(since).Hours(); h != 23 {
		t.Errorf("the spring-forward day spans %g elapsed hours, want 23, the bound was built by "+
			"adding a duration rather than a calendar day", h)
	}
	if want := time.Date(2026, 3, 9, 0, 0, 0, 0, loc); !until.Equal(want) {
		t.Errorf("to-bound = %s, want the 9th's own midnight %s", until.In(loc), want)
	}
	// The autumn transition the other way: 1 November 2026 is 25 hours long and is also one day.
	fallSince, fallUntil, _ := saleLookupBounds(SaleLookupQuery{From: "2026-11-01", To: "2026-11-01"})
	if h := fallUntil.Sub(fallSince).Hours(); h != 25 {
		t.Errorf("the fall-back day spans %g elapsed hours, want 25", h)
	}
}

// A bound nobody asked for is OPEN, not "the beginning of time". The repository tests IsZero rather
// than being handed year 1, which would become a real predicate the first time a row predated it.
func TestSaleLookupBounds_AnAbsentBoundIsOpen(t *testing.T) {
	since, until, ok := saleLookupBounds(SaleLookupQuery{})
	if !ok {
		t.Fatal("a query with no dates at all is well-formed")
	}
	if !since.IsZero() || !until.IsZero() {
		t.Errorf("an unasked-for bound resolved to %v..%v; it must resolve to nothing", since, until)
	}
	if s, u, _ := saleLookupBounds(SaleLookupQuery{From: "2026-08-05"}); s.IsZero() || !u.IsZero() {
		t.Errorf("from-only resolved to %v..%v; the to-side must stay open", s, u)
	}
	if s, u, _ := saleLookupBounds(SaleLookupQuery{To: "2026-08-05"}); !s.IsZero() || u.IsZero() {
		t.Errorf("to-only resolved to %v..%v; the from-side must stay open", s, u)
	}
}

// A date the counter cannot have meant is refused at the service, with a status the browser can act
// on. Silently dropping it would widen the search: the cashier gets a longer list, assumes it is the
// day they asked for, and reads the wrong row back to the customer.
func TestLookupSales_AnUnreadableDateIsRefusedRatherThanWidened(t *testing.T) {
	for _, q := range []SaleLookupQuery{
		{BranchID: "branch-trt", From: "yesterday"},
		{BranchID: "branch-trt", To: "05/08/2026"},
		{BranchID: "branch-trt", From: "2026-08-05", To: "2026-13-45"},
	} {
		h := newHarness()
		_, err := h.svc.LookupSales(context.Background(), q)
		if err == nil {
			t.Fatalf("%+v was accepted; an unreadable date must be refused", q)
		}
		if st, ok := ClientStatus(err); !ok || st != 400 {
			t.Fatalf("%+v gave status=%d (%v), want 400", q, st, err)
		}
		if h.store.lookupQuery.BranchID != "" {
			t.Fatalf("%+v reached the store despite being unreadable", q)
		}
	}
	// And the well-formed pair still gets through, so the guard is a guard and not a wall.
	h := newHarness()
	if _, err := h.svc.LookupSales(context.Background(),
		SaleLookupQuery{BranchID: "branch-trt", From: "2026-08-05", To: "2026-08-05"}); err != nil {
		t.Fatalf("a well-formed date range must be accepted: %v", err)
	}
}
