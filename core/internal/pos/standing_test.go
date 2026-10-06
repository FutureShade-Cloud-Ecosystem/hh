package pos

// standing_test.go, the counter's past-due FACT and its marshal-layer gate (standing.go, 0181).
//
// Ryan Bostock's reason for it, verbatim: he starts a counter sale to get the price profile, the
// inventory and the lead time, "and it also gives me an opportunity to ask them to pay their bill."
//
// What is pinned here is that the fact is a fact: it appears only when there is one, it never
// carries a verdict, it never blocks, and the number is byte-absent for a caller the dealer's
// policy does not allow. The gating proof is the portal/DIB-488 marshal-and-assert-absent idiom -
// the value is marshalled exactly as the handler writes it and the field names are asserted gone.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type fakeAging struct {
	out  PastDue
	err  error
	asks []string
}

func (f *fakeAging) CustomerAging(_ context.Context, customerID string) (PastDue, error) {
	f.asks = append(f.asks, customerID)
	return f.out, f.err
}

func standingHarness(t *testing.T, due PastDue, err error) (*harness, *fakeAging) {
	t.Helper()
	h := newHarness()
	a := &fakeAging{out: due, err: err}
	h.svc.WithAgingReader(a)
	return h, a
}

func behind() PastDue { return PastDue{PastDueCents: 148_23, OldestBucket: "60"} }

func standingOf(t *testing.T, h *harness, customerID string) AccountStanding {
	t.Helper()
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{
		BranchID: "branch-trt", CustomerID: customerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// -- the fact ------------------------------------------------------------------------------------

func TestStanding_AnAccountThatIsBehindSaysSo(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	out := standingOf(t, h, "cust-abc")
	if out.PastDueCents != 148_23 || out.OldestBucket != "60" {
		t.Fatalf("the counter needs the number and how late it is: %+v", out)
	}
}

func TestStanding_ACURRENTAccountSaysNOTHING(t *testing.T) {
	// Not "$0.00 past due", not "in good standing", nothing. The ordinary case is most of the
	// tickets on a counter, and a chip that renders on all of them stops being read on any of them.
	h, _ := standingHarness(t, PastDue{}, nil)
	out := standingOf(t, h, "cust-abc")
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("a current account renders nothing: %s", raw)
	}
}

func TestStanding_AWalkInIsNeverAsked(t *testing.T) {
	h, a := standingHarness(t, behind(), nil)
	out := standingOf(t, h, "")
	if out.PastDueCents != 0 {
		t.Fatalf("a walk-in has no account: %+v", out)
	}
	if len(a.asks) != 0 {
		t.Fatalf("and must cost no AR read at all: %v", a.asks)
	}
}

func TestStanding_ItCarriesNoVERDICTAtAll(t *testing.T) {
	// The type is deliberately incapable of warning, refusing or advising. Whether a sale may
	// proceed is the credit gate's question, and two systems both saying "this account is behind"
	//, one of them advisory, is how a counter person learns to ignore both.
	h, _ := standingHarness(t, PastDue{PastDueCents: 1, OldestBucket: "30", OnHold: true}, nil)
	raw, _ := json.Marshal(standingOf(t, h, "cust-abc"))
	for _, word := range []string{"message", "warning", "reason", "blocked", "refus", "verdict", "severity"} {
		if strings.Contains(strings.ToLower(string(raw)), word) {
			t.Fatalf("the counter's standing fact must carry no verdict (%q): %s", word, raw)
		}
	}
}

// -- every failure looks like "nothing to say", and that is the point ----------------------------

func TestStanding_AFailedARReadIsSILENTNotAFault(t *testing.T) {
	// A counter must never be told a customer is current because a query timed out, and it must
	// never be shown a fault banner over a fact nobody asked for. Both cases render nothing, and
	// the ONE case that differs is the one worth a clerk's attention.
	h, _ := standingHarness(t, behind(), errors.New("AR unreachable"))
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "b", CustomerID: "c"})
	if err != nil {
		t.Fatalf("an AR outage is not a POS error: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("a failed read says nothing: %s", raw)
	}
}

func TestStanding_AnUnwiredReaderSaysNothing(t *testing.T) {
	h := newHarness() // no aging reader
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "b", CustomerID: "c"})
	if err != nil || out.PastDueCents != 0 {
		t.Fatalf("an unwired seam is a wiring state, not a fault: %+v / %v", out, err)
	}
}

func TestStanding_BranchIsRequiredBecauseThePolICYIsPerBranch(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	if _, err := h.svc.Standing(context.Background(), AccountStandingInput{CustomerID: "c"}); err == nil {
		t.Fatal("the gate reads a branch policy, a missing branch cannot resolve one")
	}
}

// -- THE GATE: byte-absence, at the marshal layer ------------------------------------------------

func TestStanding_AllStaffIsTheDefaultBecauseTheCOUNTERAsked(t *testing.T) {
	// The default differs from margin's on purpose: margin is the dealer's own position, a past-due
	// balance is the customer's own fact and the counter person is the one being asked to raise it.
	// Shipping this MANAGER_UP would switch the feature off for the only role that asked for it.
	h, _ := standingHarness(t, behind(), nil)
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	if standingOf(t, h, "cust-abc").PastDueCents == 0 {
		t.Fatal("a frontline clerk sees the balance under the default policy")
	}
}

func TestStanding_ManagerUpPolicy_ClerkGetsBYTEABSENCE(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	p := h.store.policies[""]
	p.BalanceVisibility = BalanceVisibilityManagerUp
	h.store.policies[""] = p
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	out := standingOf(t, h, "cust-abc")
	// Marshalled exactly as the handler writes it: `{}`. Not zeroed, not nulled, not CSS-hidden.
	raw, _ := json.Marshal(out)
	if string(raw) != "{}" {
		t.Fatalf("a frontline caller under MANAGER_UP must receive {}: %s", raw)
	}
	for _, field := range []string{"past_due", "oldest_bucket", "on_hold"} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("%s must be byte-absent: %s", field, raw)
		}
	}
}

func TestStanding_ManagerUpPolicy_ManagerStillSeesIt(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil) // harness caller is TierManager
	p := h.store.policies[""]
	p.BalanceVisibility = BalanceVisibilityManagerUp
	h.store.policies[""] = p
	if standingOf(t, h, "cust-abc").PastDueCents == 0 {
		t.Fatal("MANAGER_UP still means managers")
	}
}

func TestStanding_HiddenPolicy_NOBODYGetsIt(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil) // manager-tier caller…
	p := h.store.policies[""]
	p.BalanceVisibility = BalanceVisibilityHidden
	h.store.policies[""] = p
	raw, _ := json.Marshal(standingOf(t, h, "cust-abc"))
	if string(raw) != "{}" {
		t.Fatalf("HIDDEN means the surface is off for everyone: %s", raw)
	}
}

func TestStanding_AnUnauthenticatedCallerGetsNothing(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	h.caller = identity.StaffContext{}
	raw, _ := json.Marshal(standingOf(t, h, "cust-abc"))
	if string(raw) != "{}" {
		t.Fatalf("no caller, no balance: %s", raw)
	}
}

// -- the policy knob -----------------------------------------------------------------------------

func TestBalanceVisibility_ValidatesInWordsBeforeTheCHECKCouldFire(t *testing.T) {
	if v, err := validateBalanceVisibility(""); err != nil || v != BalanceVisibilityAllStaff {
		t.Fatalf("omitted normalizes to the column default: %q / %v", v, err)
	}
	for _, ok := range []string{BalanceVisibilityAllStaff, BalanceVisibilityManagerUp, BalanceVisibilityHidden} {
		if v, err := validateBalanceVisibility(ok); err != nil || v != ok {
			t.Fatalf("%q must be accepted: %v", ok, err)
		}
	}
	if _, err := validateBalanceVisibility("EVERYONE"); err == nil {
		t.Fatal("a bad word is a 422 in words, never a 500 from the CHECK")
	} else if !strings.Contains(err.Error(), "MANAGER_UP, ALL_STAFF or HIDDEN") {
		t.Fatalf("the refusal must name the way forward: %v", err)
	}
}
