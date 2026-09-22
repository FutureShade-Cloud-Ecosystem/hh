package dispatch

// freightraise_test.go, the second-truck gate's INVARIANT, and the byte-absence of its money.
//
// No database. The invariant is a pure function of four numbers on purpose (freightraise.go says
// why), and the money gate is exercised against a Service with a nil repository, which falls back to
// DefaultPolicy(), MANAGER_UP, the shipped column default. Both halves of the feature that can be
// reasoned about without Postgres are reasoned about here; everything that needs real rows is in
// freightraise_integration_test.go.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - the invariant -

// Each row of this table is one sentence somebody said out loud about this feature. The gate is not
// "did a split happen" and it is not "did a truck get added"; it is "would the customer be charged
// more for delivery than they were quoted, for the first time".
func TestGatesFreightRaise_TheInvariantAndItsThreeExemptions(t *testing.T) {
	cases := []struct {
		name                      string
		proposed, current, quoted int64
		quotedKnown, wantGated    bool
	}{
		{
			name: "a quoted order split three ways within the quoted freight sails through",
			// $260 quoted, $260 charged, $260 proposed, divided over three trucks instead of one.
			proposed: 26000, current: 26000, quoted: 26000, quotedKnown: true, wantGated: false,
		},
		{
			name:     "the same order with a fourth truck raising the charge is gated",
			proposed: 34000, current: 26000, quoted: 26000, quotedKnown: true, wantGated: true,
		},
		{
			name:     "an order that never came from a quote is never gated, whatever it proposes",
			proposed: 99900, current: 0, quoted: 0, quotedKnown: false, wantGated: false,
		},
		{
			name: "an order QUOTED ZERO is gated the moment freight appears, 0 is a baseline, not a blank",
			// The case a `<= 0 means unknown` rule would silently open: free delivery, promised.
			proposed: 15000, current: 0, quoted: 0, quotedKnown: true, wantGated: true,
		},
		{
			name:     "a proposal that LOWERS the charge is not a raise",
			proposed: 10000, current: 26000, quoted: 26000, quotedKnown: true, wantGated: false,
		},
		{
			name:     "a proposal exactly AT the quoted figure is not above it",
			proposed: 26000, current: 20000, quoted: 26000, quotedKnown: true, wantGated: false,
		},
		{
			name: "re-splitting an ALREADY APPROVED raise does not ask the same approver twice",
			// The charge already stands at $340 above a $260 quote because somebody approved it.
			// Dividing that same $340 over the same trucks is not a new ask.
			proposed: 34000, current: 34000, quoted: 26000, quotedKnown: true, wantGated: false,
		},
		{
			name:     "…but raising it AGAIN, above where it already stands, is a new ask",
			proposed: 39000, current: 34000, quoted: 26000, quotedKnown: true, wantGated: true,
		},
		{
			name:     "one cent above the quote is above the quote",
			proposed: 26001, current: 26000, quoted: 26000, quotedKnown: true, wantGated: true,
		},
	}
	// The table guards itself: it must contain at least one of each verdict, or a predicate stuck on
	// a constant would pass it.
	var gated, open int
	for _, c := range cases {
		if c.wantGated {
			gated++
		} else {
			open++
		}
	}
	if gated == 0 || open == 0 {
		t.Fatal("this table asserts nothing unless it contains both verdicts")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := GatesFreightRaise(c.proposed, c.current, c.quoted, c.quotedKnown); got != c.wantGated {
				t.Fatalf("GatesFreightRaise(proposed=%d current=%d quoted=%d known=%v) = %v, want %v",
					c.proposed, c.current, c.quoted, c.quotedKnown, got, c.wantGated)
			}
		})
	}
}

// The NULL arm on its own, because it is the acceptance clause most easily lost to a refactor: a
// baseline nobody knows is a baseline nothing can be above, at ANY proposed figure.
func TestGatesFreightRaise_NoBaselineIsNeverGatedAtAnyFigure(t *testing.T) {
	for _, proposed := range []int64{0, 1, 100, 1_000_000, 1 << 40} {
		if GatesFreightRaise(proposed, 0, 0, false) {
			t.Fatalf("an order with no quoted baseline was gated at %d cents", proposed)
		}
	}
	// Positive control: the same figures WITH a baseline do gate, so the loop above is not passing
	// because the predicate never fires.
	if !GatesFreightRaise(100, 0, 0, true) {
		t.Fatal("positive control failed: a quoted-zero order is not gated by a raise")
	}
}

// - the money is byte-absent for a role that may not see it -

func raiseFixture() FreightRaise {
	return FreightRaise{
		ID: "r-1", OrderID: "o-1", BranchID: "b-1", OrderNo: "#SO-TRT-1041",
		CustomerName: "Bay of Quinte Landscaping",
		QuotedCents:  26000, CurrentCents: 26000, ProposedCents: 34000,
		Loads: 4, RequestedBy: "u-1", RequestedByRole: "LOGISTICS_MANAGER",
		RequestedAt: time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC), State: RaiseParked,
	}
}

// svcAs builds a policy-asking Service for one caller. The repository is nil on purpose: PolicyFor
// fails closed to DefaultPolicy(), which is MANAGER_UP, the shipped column default and the setting
// this assertion is about.
func svcAs(c identity.StaffContext) *Service {
	return NewService(nil).withCaller(func(context.Context) (identity.StaffContext, bool) { return c, true })
}

// THE MONEY IS ABSENT, NOT ZEROED AND NOT NULL.
//
// The positive control runs FIRST and is not optional: it marshals the ALLOWED caller's payload and
// proves every figure and every key this test then hunts for is actually produced by this code. A
// byte scan with no positive control passes just as happily against a struct that stopped carrying
// money for everybody, or against a field that was renamed.
//
// It scans for the literal string "null" as well, because `null` is NOT absence, a nulled pointer
// still tells the reader there is a figure here they are not being shown, and three separate trains
// on this tree have shipped that mistake.
func TestFreightRaiseView_MoneyIsByteAbsentForARefusedRole(t *testing.T) {
	raise := raiseFixture()
	manager := identity.StaffContext{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive}
	clerk := identity.StaffContext{UserID: "is", RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeGlobal, Status: identity.StatusActive}

	// - POSITIVE CONTROL -
	allowedBytes, err := json.Marshal(svcAs(manager).raiseView(context.Background(), raise))
	if err != nil {
		t.Fatalf("marshal (allowed): %v", err)
	}
	allowed := string(allowedBytes)
	for _, must := range []string{`"money"`, `"quoted_freight_cents":26000`, `"current_freight_cents":26000`, `"proposed_freight_cents":34000`, `"delta_cents":8000`} {
		if !strings.Contains(allowed, must) {
			t.Fatalf("positive control failed: an ALLOWED caller's payload is missing %s\n%s", must, allowed)
		}
	}
	// The unguarded half of the payload must be there for both, or "absent" below would be trivial.
	for _, must := range []string{`"#SO-TRT-1041"`, `"Bay of Quinte Landscaping"`} {
		if !strings.Contains(allowed, must) {
			t.Fatalf("positive control failed: the subject is missing %s", must)
		}
	}

	// - THE SCAN -
	refusedBytes, err := json.Marshal(svcAs(clerk).raiseView(context.Background(), raise))
	if err != nil {
		t.Fatalf("marshal (refused): %v", err)
	}
	refused := string(refusedBytes)
	for _, forbidden := range []string{
		"money", "quoted_freight_cents", "current_freight_cents", "proposed_freight_cents", "delta_cents",
		"26000", "34000", "8000", "null",
	} {
		if strings.Contains(refused, forbidden) {
			t.Fatalf("a refused caller received %q in the payload:\n%s", forbidden, refused)
		}
	}
	// And the caller still learns WHAT they are being asked about, absence of money is not absence
	// of the record.
	if !strings.Contains(refused, "#SO-TRT-1041") || !strings.Contains(refused, "Bay of Quinte Landscaping") {
		t.Fatalf("a refused caller lost the subject as well as the money:\n%s", refused)
	}
}

// A caller with no resolved identity at all is refused too, the gate answers "no" to every question
// it cannot answer (loadValueAllowed's own posture, asserted through the payload it produces).
func TestFreightRaiseView_UnresolvableCallerGetsNoMoney(t *testing.T) {
	s := NewService(nil).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{}, false
	})
	b, err := json.Marshal(s.raiseView(context.Background(), raiseFixture()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "money") {
		t.Fatalf("an unresolvable caller received money:\n%s", b)
	}
}

// The role is rendered as a WORD, never as the role code, the same rule the counter-vocabulary gate
// enforces on the client, held on the server where the string is produced.
func TestFreightRaiseView_NamesTheAskerInWords(t *testing.T) {
	manager := identity.StaffContext{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive}
	view := svcAs(manager).raiseView(context.Background(), raiseFixture())
	if view.AskedBy != "a dispatch manager" {
		t.Fatalf("asked_by should be a word, got %q", view.AskedBy)
	}
	if strings.Contains(view.AskedBy, "_") || strings.ToUpper(view.AskedBy) == view.AskedBy {
		t.Fatalf("a role code reached a rendered field: %q", view.AskedBy)
	}
}

// - the approval sentence -

// The sentence that lands on permission_approval_requests.detail is read by every authenticated staff
// member, through a list with no money gate. It names the subject and carries no figures.
func TestRaiseDetail_NamesTheSubjectAndCarriesNoFigures(t *testing.T) {
	d := raiseDetail("#SO-TRT-1041", "Bay of Quinte Landscaping")
	if !strings.Contains(d, "#SO-TRT-1041") || !strings.Contains(d, "Bay of Quinte Landscaping") {
		t.Fatalf("the approver cannot tell what this is about: %q", d)
	}
	// Strip the order number, which is an identifier, not money, then hunt for any digit at all.
	money := regexp.MustCompile(`\d`).FindAllString(strings.ReplaceAll(d, "#SO-TRT-1041", ""), -1)
	if len(money) > 0 {
		t.Fatalf("a figure reached the ungated approvals list: %q", d)
	}
	// A raise on an order with no number and no customer still says something.
	if bare := raiseDetail("", ""); len(bare) < 20 || !strings.Contains(bare, "quoted") {
		t.Fatalf("a raise with no subject must still read as a sentence: %q", bare)
	}
}

// - idempotent re-ask -

func TestSameAsk_ConvergesOnAnIdenticalReAskAndRefusesADifferentOne(t *testing.T) {
	parked := FreightRaise{ProposedCents: 34000, Loads: 4, Amounts: []int64{9000, 9000, 8000, 8000}}
	if !sameAsk(parked, 34000, 4, []int64{9000, 9000, 8000, 8000}) {
		t.Fatal("an identical re-ask must converge on the raise already waiting")
	}
	for _, different := range []struct {
		name     string
		proposed int64
		loads    int
		amounts  []int64
	}{
		{"a different total", 39000, 4, []int64{9000, 9000, 8000, 8000}},
		{"a different truck count", 34000, 3, []int64{9000, 9000, 8000, 8000}},
		{"a different division", 34000, 4, []int64{8500, 8500, 8500, 8500}},
		{"an even split where shares were named", 34000, 4, nil},
	} {
		if sameAsk(parked, different.proposed, different.loads, different.amounts) {
			t.Fatalf("%s must not be treated as the same ask", different.name)
		}
	}
	// An even-split ask converges on an even-split parking of the same shape.
	even := FreightRaise{ProposedCents: 34000, Loads: 4}
	if !sameAsk(even, 34000, 4, nil) {
		t.Fatal("an identical even-split re-ask must converge")
	}
}
