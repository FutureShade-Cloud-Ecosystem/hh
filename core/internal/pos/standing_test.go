package pos

// standing_test.go, the counter's account standing and its marshal-layer gate (standing.go, 0181).
//
// Ryan Bostock's reason for it, verbatim: he starts a counter sale to get the price profile, the
// inventory and the lead time, "and it also gives me an opportunity to ask them to pay their bill."
//
// What is pinned here is that every case answers distinctly (a walk-in, a clear account, a past-due
// one and a held one each carry their own standing word), that the fact is still a fact (it never
// carries a verdict, it never blocks), that a caller the dealer's MANAGER_UP policy does not allow
// is answered without a single money byte in it while the hold state stays, that HIDDEN is off for
// everyone with a 403, and that a read that cannot be made is an error and never an empty answer
// that reads as a clear account.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

type fakePosition struct {
	vis string
	svc *Service
}

func (f *fakePosition) AccountPositionAllowed(ctx context.Context, _ string) bool {
	if f == nil || f.svc == nil {
		return false
	}
	caller, ok := f.svc.callerFrom(ctx)
	if !ok || caller.UserID == "" {
		return false
	}
	switch f.vis {
	case "ALL_STAFF":
		return true
	case "MANAGER_UP":
		return caller.IsManager()
	default:
		return false
	}
}

// wireStanding wires the standing seams over a harness: an account that exists, no hold, the
// given aging answer, and the dealer's default MANAGER_UP account position gate.
func wireStanding(h *harness, due PastDue, err error) (*fakeAging, *fakeHolds, *fakeAccounts) {
	a := &fakeAging{out: due, err: err}
	hd := &fakeHolds{}
	ac := &fakeAccounts{exists: true}
	h.svc.WithAgingReader(a).WithHoldReader(hd).WithAccountChecker(ac).
		WithPositionGate(&fakePosition{vis: "MANAGER_UP", svc: h.svc})
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

func TestStanding_AllStaffPositionPolicyAdmitsFrontlineClerk(t *testing.T) {
	// Under ar_invoice_policy.account_position_visibility = ALL_STAFF, a frontline clerk sees the
	// past-due balance; under the shipped MANAGER_UP default (pinned below) they receive withheld.
	h, _ := standingHarness(t, behind(), nil)
	h.svc.WithPositionGate(&fakePosition{vis: "ALL_STAFF", svc: h.svc})
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	if standingOf(t, h, standingCust).PastDueCents == 0 {
		t.Fatal("a frontline clerk sees the balance when account_position_visibility is ALL_STAFF")
	}
}

func TestStanding_NilPositionGateFailsClosedToWithheld(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil)
	h.svc.WithPositionGate(nil)
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if err != nil {
		t.Fatalf("nil position gate answers withheld, not 500: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != `{"standing":"withheld"}` {
		t.Fatalf("nil position gate fails closed to withheld: %s", raw)
	}
}

func TestStanding_ManagerUpPolicy_APastDueAccountReadsWithheld(t *testing.T) {
	// Round 4/5 (principal items 206, 208): on a default box (MANAGER_UP position policy,
	// ALL_STAFF pos balance_visibility), a frontline clerk on a past due account reads `withheld`
	// with no figure key or past_due word in the bytes.
	h, _ := standingHarness(t, behind(), nil)
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if err != nil {
		t.Fatalf("a withheld caller is answered, not refused: %v", err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != `{"standing":"withheld"}` {
		t.Fatalf("a past due account reads withheld with no money in the bytes: %s", raw)
	}
}

func TestStanding_ManagerUpPolicy_AClearAccountAlsoReadsWithheld(t *testing.T) {
	// Clear is a position too: nothing past due and no hold is exactly what the default MANAGER_UP
	// position policy withholds.
	h, _ := standingHarness(t, PastDue{}, nil)
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != `{"standing":"withheld"}` {
		t.Fatalf("a clear account reads withheld for a withheld caller, never clear: %s", raw)
	}
}

func TestStanding_ManagerUpPolicy_AHoldThroughAgingAloneStillReadsOnHold(t *testing.T) {
	// The hold is the same for every caller: the aging-derived hold flag counts for a withheld
	// caller exactly as it does for an admitted one, and the figure behind it still never leaves.
	h, _ := standingHarness(t, PastDue{PastDueCents: 148_23, OldestBucket: "60", OnHold: true}, nil)
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != `{"standing":"on_hold","on_hold":true}` {
		t.Fatalf("an aging-only hold reads on_hold with no money in the bytes: %s", raw)
	}
}

func TestStanding_ManagerUpPolicy_AWithheldCallerStillHearsAnActiveHoldRow(t *testing.T) {
	// The hold read beside the aging still counts on its own: an account with no open invoices
	// (the aging read drops it) but an active hold row reads on_hold for a withheld caller.
	h, _ := standingHarness(t, PastDue{}, nil)
	h.svc.WithHoldReader(&fakeHolds{held: true})
	h.caller = identity.StaffContext{UserID: "u-counter", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	out, err := h.svc.Standing(context.Background(), AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != `{"standing":"on_hold","on_hold":true}` {
		t.Fatalf("an active hold row reads on_hold for a withheld caller: %s", raw)
	}
}

func TestStanding_ManagerUpPolicy_ManagerStillSeesIt(t *testing.T) {
	h, _ := standingHarness(t, behind(), nil) // harness caller is TierManager
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

func TestOnAccountRefusal_WithholdsCreditLimitForRefusedCaller(t *testing.T) {
	// Default policy: account_position_visibility is MANAGER_UP.
	// On a Cash On Demand account (zero-day terms with a $5,000.00 line), a frontline clerk
	// who rings an ON_ACCOUNT tender gets the 422 refusal in words with no credit_limit_cents.
	h := depositHarness(0, 500_000)
	h.svc.WithPositionGate(&fakePosition{vis: "MANAGER_UP", svc: h.svc})
	h.caller = identity.StaffContext{
		UserID: "u-clerk", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal,
	}
	in := depositSaleInput()
	in.SaleType = SaleCollectLater
	in.Tender = TenderAccount
	_, err := h.svc.Sale(context.Background(), in)
	if err == nil {
		t.Fatal("expected on_account_not_available refusal")
	}
	hdl := NewHandler(h.svc, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", nil)
	hdl.writeErr(rec, req, err)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "credit_limit_cents") || strings.Contains(body, "500000") || strings.Contains(body, "5,000") {
		t.Fatalf("refused caller's 422 body must not contain credit_limit_cents or limit figure: %s", body)
	}
	if !strings.Contains(body, `"error":"on_account_not_available"`) || !strings.Contains(body, `"message"`) {
		t.Fatalf("refused caller's 422 body must carry outcome in words: %s", body)
	}

	// Manager caller under the same default MANAGER_UP policy still receives credit_limit_cents.
	h.caller = identity.StaffContext{
		UserID: "u-mgr", RoleCode: "BRANCH_MANAGER",
		Tier: identity.TierManager, Scope: identity.ScopeGlobal,
	}
	_, errMgr := h.svc.Sale(context.Background(), in)
	recMgr := httptest.NewRecorder()
	hdl.writeErr(recMgr, req, errMgr)
	if !strings.Contains(recMgr.Body.String(), `"credit_limit_cents":500000`) {
		t.Fatalf("manager caller's 422 body must carry credit_limit_cents: %s", recMgr.Body.String())
	}
}
