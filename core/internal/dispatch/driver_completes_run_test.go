package dispatch

// driver_completes_run_test.go, "a driver may complete their own run", as a question over values.
//
// WHY THE RULE IS TESTED HERE AND NOT ONLY AGAINST A DATABASE. completionAuthority is a pure function
// precisely so that the sentence it encodes can be read off a table rather than assembled from a
// fixture with a truck, a driver row, four stops and a signature provider in it. The integration
// suite still walks the whole path, a rule that is only true in a unit test is a rule about a unit
// test, but the DISCRIMINATION it makes (this run is yours, that one is not) belongs somewhere a
// person can see every case at once.
//
// THE CASE THAT MATTERS MOST IS THE THIRD ROW. A driver with a driver record, at a branch they
// legitimately reach, addressing a run that belongs to a colleague. Every fence in this module says
// yes to that request today, requireStaff at the mux, and guardRun's branch check, which a
// BRANCH-scoped driver with a grant on their own yard passes. Nothing has ever asked whether the run
// was theirs, which is why "a driver may complete their own run" is a TIGHTENING of what a driver can
// do and not a loosening.

import (
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestCompletionAuthority_ADriverMayCloseTheirOwnRunAndOnlyTheirOwn(t *testing.T) {
	driver := func(user string) identity.StaffContext {
		return identity.StaffContext{
			UserID: user, RoleCode: identity.RoleDrivers, Tier: identity.TierFrontline,
			Scope: identity.ScopeBranch, Status: identity.StatusActive,
			Branches: map[string]bool{"b-1": true},
		}
	}
	staff := func(role string, tier identity.Tier) identity.StaffContext {
		return identity.StaffContext{
			UserID: "u-x", RoleCode: role, Tier: tier, Scope: identity.ScopeGlobal,
			Status: identity.StatusActive,
		}
	}
	mine := Run{ID: "r-1", BranchID: "b-1", DriverID: "d-priya"}
	theirs := Run{ID: "r-2", BranchID: "b-1", DriverID: "d-sam"}
	crewless := Run{ID: "r-3", BranchID: "b-1"}

	for _, tc := range []struct {
		name     string
		caller   identity.StaffContext
		run      Run
		driverID string
		want     error
	}{
		{"a driver closing their own run", driver("u-priya"), mine, "d-priya", nil},

		// THE ROW THIS RULING EXISTS FOR. Same branch, same role, real driver record, and not their
		// run. Refused by CAPABILITY, in the service, where no route refactor can reach it.
		{"a driver closing a colleague's run", driver("u-priya"), theirs, "d-priya", ErrForbidden},

		// A run nobody is crewed on is nobody's run. "" == "" must not read as ownership; that is the
		// empty-string comparison that turns a fence into a doorway.
		{"a driver closing an uncrewed run", driver("u-priya"), crewless, "d-priya", ErrForbidden},
		{"a driver with no driver record", driver("u-ghost"), mine, "", ErrForbidden},

		// EVERY OTHER ROLE IS UNTOUCHED, which is half the ruling. A dispatcher who could close a run
		// yesterday closes it today; the ownership question is not asked of them at all, and the
		// driver id they are handed is empty because nothing looked one up.
		{"a dispatch manager", staff("LOGISTICS_MANAGER", identity.TierManager), theirs, "", nil},
		{"a yard team member", staff("YARD_TEAM", identity.TierFrontline), theirs, "", nil},
		{"the general manager", staff("GENERAL_MANAGER", identity.TierLeadership), crewless, "", nil},
		{"a platform admin", staff(identity.RoleSuperAdmin, identity.TierLeadership), theirs, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := completionAuthority(tc.caller, tc.run, tc.driverID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("completionAuthority = %v, want %v", err, tc.want)
			}
		})
	}
}

// - the precondition, and the word it refuses in -

func TestUnresolvedStop_NamesTheFirstOneInStopOrder(t *testing.T) {
	stops := []Stop{
		{StopNo: 1, Status: StopDelivered, OrderNo: "#SO-A"},
		{StopNo: 2, Status: StopCancelled, OrderNo: "#SO-B"},
		{StopNo: 3, Status: StopLoaded, OrderNo: "#SO-C"},
		{StopNo: 4, Status: StopEnRoute, OrderNo: "#SO-D"},
	}

	// EVERYBODY'S GATE picks the first stop still IN FLIGHT, and walks past the cancelled one to get
	// there: stop 2 is off the truck and is nobody's outstanding work, so a dispatcher told about it
	// would be sent to the wrong card. FIRST, in stop order, not the last one found, and not a
	// count. The refusal names one stop and a driver reads it standing beside a truck.
	st, open := unsettledStop(stops)
	if !open {
		t.Fatal("a run with a stop on the truck has something unsettled")
	}
	if st.StopNo != 3 {
		t.Fatalf("the refusal must name the FIRST stop still in flight, got stop %d", st.StopNo)
	}

	// THE DRIVER'S GATE STOPS EARLIER, on the same list, and this one line is the whole defect. A
	// cancelled stop has nothing recorded on it, no reason column, no handback, so it is not an
	// answer a driver may close a day over, and the refusal names IT rather than the stop three cards
	// further down.
	st, open = unresolvedStop(stops)
	if !open || st.StopNo != 2 {
		t.Fatalf("the driver's gate must name the cancelled stop 2, got stop %d (open=%v)", st.StopNo, open)
	}

	// EVERY STOP SETTLED, one of them by a cancel. The dispatcher's gate is satisfied, this is the
	// run they may still close, and the reason the narrowing costs no other role anything.
	trimmed := []Stop{
		{StopNo: 1, Status: StopDelivered},
		{StopNo: 2, Status: StopFailed},
		{StopNo: 3, Status: StopCancelled},
	}
	if _, open := unsettledStop(trimmed); open {
		t.Fatal("delivered, failed and cancelled are all settled, dispatch may close this run")
	}
	// And the driver's gate refuses the same run, which is the consequence stated rather than hidden:
	// a run carrying a cancelled stop is closed by dispatch, not from the truck.
	if st, open := unresolvedStop(trimmed); !open || st.StopNo != 3 {
		t.Fatalf("a cancelled stop is not an answer a driver recorded; got stop %d (open=%v)", st.StopNo, open)
	}

	// THE ONLY LIST A DRIVER MAY CLOSE OVER: their own two outcomes, every card.
	answered := []Stop{
		{StopNo: 1, Status: StopDelivered},
		{StopNo: 2, Status: StopFailed},
	}
	if _, open := unresolvedStop(answered); open {
		t.Fatal("delivered and failed are the driver's two outcomes, this day is theirs to close")
	}
	// An empty run has nothing outstanding. (DispatchRun refuses to send one out in the first place,
	// so this is a statement about the predicate rather than a reachable day.)
	if _, open := unsettledStop(nil); open {
		t.Fatal("no stops is no unsettled stops")
	}
	if _, open := unresolvedStop(nil); open {
		t.Fatal("no stops is no unresolved stops")
	}
}

// TestUnresolvedStop_ARunOfNothingButCancelledStopsIsRefusedToTheDriver, the verifier's loophole, as
// a question over values.
//
// MEASURED ON A LIVE RUN before this was fixed: the driver cancels stop 1 (200), stop 2 (200), stop 3
// (200), taps Complete, and gets 200 with the run COMPLETED. Nothing delivered, nothing failed, and no
// reason recorded anywhere, FAILED is guarded (the server refuses a blank reason, which is why the
// run sheet collects one in a sheet) and it hands the order back to the yard; CANCELLED has neither.
func TestUnresolvedStop_ARunOfNothingButCancelledStopsIsRefusedToTheDriver(t *testing.T) {
	day := []Stop{
		{StopNo: 1, Status: StopCancelled, OrderNo: "#SO-A"},
		{StopNo: 2, Status: StopCancelled, OrderNo: "#SO-B"},
		{StopNo: 3, Status: StopCancelled, OrderNo: "#SO-C"},
	}
	// OpenStopCount's SQL counts none of these, which is why the count alone could never have caught
	// this and why the driver's gate reads the list.
	if _, open := unsettledStop(day); open {
		t.Fatal("the settled set is what OpenStopCount counts, a cancelled day is zero open stops to it")
	}
	st, open := unresolvedStop(day)
	if !open {
		t.Fatal("a day with nothing delivered, nothing failed and no reason recorded is not a day a driver may file")
	}
	if st.StopNo != 1 {
		t.Fatalf("the refusal names the first stop with no answer on it, got stop %d", st.StopNo)
	}
}

func TestStopIsResolved_IsTheSameSetOpenStopCountAsksFor(t *testing.T) {
	// THE NAME OF THIS TEST STILL HOLDS, and it now holds of stopIsSettled. The SQL behind
	// OpenStopCount counts `status NOT IN ('DELIVERED','FAILED','CANCELLED')`; stopIsSettled is that
	// set said twice, which is why the count and the namer can never disagree about whether there is
	// something to refuse everybody over.
	for _, s := range []string{StopDelivered, StopFailed, StopCancelled} {
		if !stopIsSettled(s) {
			t.Fatalf("%s is terminal and must count as settled", s)
		}
	}
	for _, s := range []string{StopReady, StopLoaded, StopEnRoute, "HELD_AT_GATE"} {
		if stopIsSettled(s) {
			t.Fatalf("%s is still in flight and must not count as settled", s)
		}
	}

	// AND stopIsResolved IS ONE STATUS NARROWER, the driver's literal two. The earlier reading
	// counted CANCELLED here on the belief that only dispatch can cancel; transitions.go:19-20 gives
	// CANCELLED as a legal successor of both READY and LOADED, and dispatch-driver-run.ts draws the
	// DRIVER a confirming "Cancel the stop" control. A driver's two outcomes are deliver and fail,
	// both of which record something; cancelling records nothing.
	for _, s := range []string{StopDelivered, StopFailed} {
		if !stopIsResolved(s) {
			t.Fatalf("%s is an answer a driver recorded and must count as resolved", s)
		}
	}
	for _, s := range []string{StopCancelled, StopReady, StopLoaded, StopEnRoute, "HELD_AT_GATE"} {
		if stopIsResolved(s) {
			t.Fatalf("%s records no answer and must not count as resolved", s)
		}
	}

	// The two predicates are one function calling the other, so this relationship cannot drift into
	// two lists that disagree: settled is resolved PLUS the withdrawal, for every status there is.
	for _, s := range []string{StopReady, StopLoaded, StopEnRoute, StopDelivered, StopFailed, StopCancelled, "HELD_AT_GATE"} {
		if stopIsResolved(s) && !stopIsSettled(s) {
			t.Fatalf("%s is resolved but not settled, the two sets have come apart", s)
		}
	}
}

func TestUnresolvedWord_SaysWhyInTheDriversOwnWords(t *testing.T) {
	for status, want := range map[string]string{
		StopReady:       "not loaded yet",
		StopLoaded:      "on the truck",
		StopEnRoute:     "still en route",
		"HELD_AT_GATE":  "held at gate", // degrades honestly rather than confidently wrong
		"SOMETHING_NEW": "something new",
	} {
		if got := unresolvedWord(status); got != want {
			t.Fatalf("unresolvedWord(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestOrderTag_IsOmittedRatherThanEmptyParentheses(t *testing.T) {
	if got := orderTag("#SO-1042"); got != " (#SO-1042)" {
		t.Fatalf("orderTag = %q", got)
	}
	for _, blank := range []string{"", "   "} {
		if got := orderTag(blank); got != "" {
			t.Fatalf("a stop with no order number must produce no parenthetical, got %q", got)
		}
	}
}
