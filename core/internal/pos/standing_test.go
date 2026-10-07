package pos

// standing_test.go, the counter's account standing and its marshal-layer gate (standing.go, 0181).
//
// Ryan Bostock's reason for it, verbatim: he starts a counter sale to get the price profile, the
// inventory and the lead time, "and it also gives me an opportunity to ask them to pay their bill."
//
// What is pinned here is that every case answers distinctly (a walk-in, a clear account, a past-due
// one and a held one each carry their own standing word), that the fact is still a fact (it never
// carries a verdict, it never blocks), that a caller the dealer's policy does not allow is REFUSED
// with a 403 and never answered empty, and that a read that cannot be made is an error and never an
// empty answer that reads as a clear account.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const standingCust = "5d5a0000-0000-4000-8000-0000000000a1"

type fakeAging struct {
	out  PastDue
	err  error
	asks []string
}

func (f *fakeAging) CustomerAging(_ context.Context, customerID string) (PastDue, error) {
	f.asks = append(f.asks, customerID)
	return f.out, f.err
}

type fakeHolds struct {
	held bool
	err  error
}

func (f *fakeHolds) HasActiveHold(context.Context, string) (bool, error) { return f.held, f.err }

type fakeAccounts struct {
	exists bool
	err    error
}

func (f *fakeAccounts) AccountExists(context.Context, string) (bool, error) { return f.exists, f.err }

// wireStanding wires the three standing seams over a harness: an account that exists, no hold, and
// the given aging answer.
func wireStanding(h *harness, due PastDue, err error) (*fakeAging, *fakeHolds, *fakeAccounts) {
	a := &fakeAging{out: due, err: err}
	hd := &fakeHolds{}
	ac := &fakeAccounts{exists: true}
	h.svc.WithAgingReader(a).WithHoldReader(hd).WithAccountChecker(ac)
	return a, hd, ac
}

func standingHarness(t *testing.T, due PastDue, err error) (*harness, *fakeAging) {
	t.Helper()
	h := newHarness()
	a, _, _ := wireStanding(h, due, err)
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

func standingStatus(err error) int {
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status
	}
	return 0
}

// -- every case answers distinctly ---------------------------------------------------------------

func TestStanding_AnAccountThatIsBehindSaysSo(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	out := standingOf(t, h, standingCust)
	if out.Standing != StandingPastDue || out.PastDueCents != 148_23 || out.OldestBucket != "60" {
		t.Fatalf("the counter needs the word, the number and how late it is: %+v", out)
	}
}

func TestStanding_ACurrentAccountSaysClear(t *testing.T) {
	// Not an empty object any more: "clear" is an answer, and a client that gets it knows the read
	// was made. The old fields stay absent, so a client that reads them by name sees what it saw.
	h, _ := standingHarness(t, PastDue{}, nil)
	out := standingOf(t, h, standingCust)
	raw, _ := json.Marshal(out)
	if string(raw) != `{"standing":"clear"}` {
		t.Fatalf("a current account answers clear and nothing else: %s", raw)
	}
}

func TestStanding_AWalkInIsItsOwnAnswerAndIsNeverAsked(t *testing.T) {
	h, a := standingHarness(t, behind(), nil)
	out := standingOf(t, h, "")
	raw, _ := json.Marshal(out)
	if string(raw) != `{"standing":"walk_in"}` {
		t.Fatalf("a walk-in has no account and says so: %s", raw)
	}
	if len(a.asks) != 0 {
		t.Fatalf("and must cost no AR read at all: %v", a.asks)
	}
}

func TestStanding_AHeldAccountSaysSoWithNothingPastDue(t *testing.T) {
	// The aging read drops an account with no open invoices, so a hold on one must come from the
	// hold read itself.
	h, _ := standingHarness(t, PastDue{}, nil)
	h.svc.WithHoldReader(&fakeHolds{held: true})
	out := standingOf(t, h, standingCust)
	if out.Standing != StandingOnHold || !out.OnHold || out.PastDueCents != 0 {
		t.Fatalf("a held account answers on_hold: %+v", out)
	}
}

func TestStanding_HeldAndPastDueCarriesBoth(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	h.svc.WithHoldReader(&fakeHolds{held: true})
	out := standingOf(t, h, standingCust)
	if out.Standing != StandingOnHold || !out.OnHold || out.PastDueCents != 148_23 || out.OldestBucket != "60" {
		t.Fatalf("a held, late account keeps its number and its bucket: %+v", out)
	}
}

func TestStanding_TheAgingRowsOwnHoldFlagStillCounts(t *testing.T) {
	h, _ := standingHarness(t, PastDue{PastDueCents: 1, OldestBucket: "30", OnHold: true}, nil)
	if out := standingOf(t, h, standingCust); out.Standing != StandingOnHold || !out.OnHold {
		t.Fatalf("the credit gate's own flag on the aging row is kept: %+v", out)
	}
}

func TestStanding_AnUnknownAccountIsA404NotClear(t *testing.T) {
	h, a := standingHarness(t, behind(), nil)
	h.svc.WithAccountChecker(&fakeAccounts{exists: false})
	_, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if standingStatus(err) != 404 {
		t.Fatalf("an account nobody has is a 404: %v", err)
	}
	if len(a.asks) != 0 {
		t.Fatalf("an account the caller cannot see costs no AR read, so no figure of it is ever fetched: %v", a.asks)
	}
}

func TestStanding_AMalformedIdIsA400(t *testing.T) {
	h, a := standingHarness(t, behind(), nil)
	_, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: "cust-abc"})
	if standingStatus(err) != 400 {
		t.Fatalf("a customer id that is not a uuid is a 400: %v", err)
	}
	if len(a.asks) != 0 {
		t.Fatalf("and reaches no read: %v", a.asks)
	}
}

func TestStanding_ItCarriesNoVERDICTAtAll(t *testing.T) {
	// The type is deliberately incapable of warning, refusing or advising. Whether a sale may
	// proceed is the credit gate's question, and two systems both saying "this account is behind",
	// one of them advisory, is how a counter person learns to ignore both.
	h, _ := standingHarness(t, PastDue{PastDueCents: 1, OldestBucket: "30", OnHold: true}, nil)
	raw, _ := json.Marshal(standingOf(t, h, standingCust))
	for _, word := range []string{"message", "warning", "reason", "blocked", "refus", "verdict", "severity"} {
		if strings.Contains(strings.ToLower(string(raw)), word) {
			t.Fatalf("the counter's standing fact must carry no verdict (%q): %s", word, raw)
		}
	}
}

// -- a read that cannot be made is an error, never an empty answer ------------------------------

func TestStanding_AFailedARReadIsAnErrorNotAClearAccount(t *testing.T) {
	// The first form of this read swallowed the failure and answered {}, which a client cannot tell
	// from a current account. A counter must never be told a customer is current because a query
	// timed out.
	h, _ := standingHarness(t, behind(), errors.New("AR unreachable"))
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "b", CustomerID: standingCust})
	if !errors.Is(err, ErrStandingUnavailable) {
		t.Fatalf("an AR outage is the unavailable error: %v", err)
	}
	if out.Standing != "" {
		t.Fatalf("and no standing rides with it: %+v", out)
	}
}

func TestStanding_AFailedHoldReadIsAnError(t *testing.T) {
	h, _ := standingHarness(t, PastDue{}, nil)
	h.svc.WithHoldReader(&fakeHolds{err: errors.New("holds unreachable")})
	if _, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "b", CustomerID: standingCust}); !errors.Is(err, ErrStandingUnavailable) {
		t.Fatalf("a hold read that failed is the unavailable error: %v", err)
	}
}

func TestStanding_AFailedAccountCheckIsAnError(t *testing.T) {
	h, _ := standingHarness(t, PastDue{}, nil)
	h.svc.WithAccountChecker(&fakeAccounts{err: errors.New("customers unreachable")})
	if _, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "b", CustomerID: standingCust}); !errors.Is(err, ErrStandingUnavailable) {
		t.Fatalf("an account check that failed is the unavailable error: %v", err)
	}
}

func TestStanding_AnUnreadablePolicyIsAnErrorNotAHiddenBalance(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	h.store.policyErr = errors.New("policy unreachable")
	if _, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "b", CustomerID: standingCust}); !errors.Is(err, ErrStandingUnavailable) {
		t.Fatalf("a policy that cannot be read is the unavailable error: %v", err)
	}
}

func TestStanding_AnUnwiredSeamIsUnavailableNotEmpty(t *testing.T) {
	h := newHarness() // no readers
	_, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "b", CustomerID: standingCust})
	if !errors.Is(err, ErrStandingUnavailable) {
		t.Fatalf("an unwired seam is a wiring fault the caller is told about, never a clear account: %v", err)
	}
}

func TestStanding_BranchIsRequiredBecauseThePolICYIsPerBranch(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	if _, err := h.svc.Standing(context.Background(), AccountStandingInput{CustomerID: standingCust}); standingStatus(err) != 400 {
		t.Fatal("the gate reads a branch policy, a missing branch cannot resolve one")
	}
}

// -- THE GATE: a refusal, at the marshal layer ---------------------------------------------------

func TestStanding_AllStaffIsTheDefaultBecauseTheCOUNTERAsked(t *testing.T) {
	// The default differs from margin's on purpose: margin is the dealer's own position, a past-due
	// balance is the customer's own fact and the counter person is the one being asked to raise it.
	// Shipping this MANAGER_UP would switch the feature off for the only role that asked for it.
	h, _ := standingHarness(t, behind(), nil)
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	if standingOf(t, h, standingCust).PastDueCents == 0 {
		t.Fatal("a frontline clerk sees the balance under the default policy")
	}
}

func TestStanding_ManagerUpPolicy_ClerkIsRefusedAndAgingIsNeverRead(t *testing.T) {
	h, a := standingHarness(t, behind(), nil)
	p := h.store.policies[""]
	p.BalanceVisibility = BalanceVisibilityManagerUp
	h.store.policies[""] = p
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if standingStatus(err) != 403 {
		t.Fatalf("a frontline caller under MANAGER_UP is refused with a 403, never an empty 200: %v", err)
	}
	if out != (AccountStanding{}) {
		t.Fatalf("and carries no bytes of the figure: %+v", out)
	}
	if len(a.asks) != 0 {
		t.Fatalf("the figure is never even read for a refused caller: %v", a.asks)
	}
}

func TestStanding_ManagerUpPolicy_ManagerStillSeesIt(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil) // harness caller is TierManager
	p := h.store.policies[""]
	p.BalanceVisibility = BalanceVisibilityManagerUp
	h.store.policies[""] = p
	if standingOf(t, h, standingCust).PastDueCents == 0 {
		t.Fatal("MANAGER_UP still means managers")
	}
}

func TestStanding_HiddenPolicy_NobodyGetsItAndIsRefused(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil) // manager-tier caller
	p := h.store.policies[""]
	p.BalanceVisibility = BalanceVisibilityHidden
	h.store.policies[""] = p
	_, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if standingStatus(err) != 403 {
		t.Fatalf("HIDDEN means the surface is off for everyone, and the answer is a 403: %v", err)
	}
}

func TestStanding_AnUnauthenticatedCallerIsRefused(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	h.svc.callerFrom = func(context.Context) (identity.StaffContext, bool) { return identity.StaffContext{}, false }
	_, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if standingStatus(err) != 401 {
		t.Fatalf("no caller, no balance, and not an empty 200: %v", err)
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
