package billing

// account_position_gate_test.go, UX-97: the shared account position gate and the customer workspace
// under it. A role leg each way (allowed and refused), one per HIDDEN branch, and the fail closed
// legs. The figures are asserted ABSENT from the wire, never zero.

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type fakePosition struct{ allow bool }

func (f fakePosition) AccountPositionAllowed(context.Context, string) bool { return f.allow }

type policyByBranch struct {
	byBranch map[string]string
	err      error
	asked    []string
}

func (p *policyByBranch) RegisterPolicyFor(_ context.Context, branchID string) (RegisterPolicy, error) {
	p.asked = append(p.asked, branchID)
	if p.err != nil {
		return RegisterPolicy{}, p.err
	}
	v, ok := p.byBranch[branchID]
	if !ok {
		v = p.byBranch[""]
	}
	return RegisterPolicy{AccountPositionVisibility: v}, nil
}

func gateFor(pol *policyByBranch, sc identity.StaffContext, authed bool) *AccountPositionGate {
	g := NewAccountPositionGate(pol)
	g.callerFrom = func(context.Context) (identity.StaffContext, bool) { return sc, authed }
	return g
}

func TestAccountPositionGate_RoleLegsAndHiddenBranch(t *testing.T) {
	clerk := identity.StaffContext{UserID: "u-clerk", Tier: identity.TierFrontline}
	manager := identity.StaffContext{UserID: "u-mgr", Tier: identity.TierManager}
	pol := &policyByBranch{byBranch: map[string]string{"": VisibilityManagerUp, "b-open": VisibilityAllStaff, "b-hidden": VisibilityHidden}}
	cases := []struct {
		name   string
		sc     identity.StaffContext
		branch string
		want   bool
	}{
		{"manager on the default MANAGER_UP", manager, "", true},
		{"clerk on the default MANAGER_UP is refused", clerk, "", false},
		{"clerk on an ALL_STAFF branch", clerk, "b-open", true},
		{"manager on a HIDDEN branch is refused", manager, "b-hidden", false},
		{"clerk on a HIDDEN branch is refused", clerk, "b-hidden", false},
	}
	for _, c := range cases {
		if got := gateFor(pol, c.sc, true).AccountPositionAllowed(context.Background(), c.branch); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestAccountPositionGate_FailsClosed(t *testing.T) {
	manager := identity.StaffContext{UserID: "u-mgr", Tier: identity.TierManager}
	if gateFor(&policyByBranch{err: errors.New("db down")}, manager, true).AccountPositionAllowed(context.Background(), "") {
		t.Error("an unreadable policy must refuse")
	}
	if gateFor(&policyByBranch{byBranch: map[string]string{"": VisibilityAllStaff}}, manager, false).AccountPositionAllowed(context.Background(), "") {
		t.Error("an unauthenticated caller must refuse")
	}
	if gateFor(&policyByBranch{byBranch: map[string]string{"": "SOMETHING_NEW"}}, manager, true).AccountPositionAllowed(context.Background(), "") {
		t.Error("an unknown policy value must refuse")
	}
	var nilGate *AccountPositionGate
	if nilGate.AccountPositionAllowed(context.Background(), "") {
		t.Error("a nil gate must refuse")
	}
}

func workspaceBody(t *testing.T, h *WorkspaceHandler) string {
	t.Helper()
	rr := httptest.NewRecorder()
	h.workspace(rr, newWorkspaceRequest(trailCustomerID, nil), staffCaller())
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func positionStore() *fakeWorkspaceStore {
	at := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	st := trailStore()
	st.header.CreditLimitCents, st.header.OpenCents, st.header.OverdueCents = 5_000_000, 140_000, 40_000
	st.header.HomeBranchID = "b-hidden"
	st.invoicesOpen = []CustomerWorkspaceInvoice{
		{ID: "inv-open-1", InvoiceNo: "INV-OPEN-1", DocType: "INVOICE", Status: "FINALIZED", IssueDate: at.AddDate(0, 0, -45), DueDate: at.AddDate(0, 0, -15), TotalCents: 88_000, BalanceCents: 66_000, DaysOverdue: 15, DaysOverdueBucket: "1-30"},
	}
	st.invoicesClosed = []CustomerWorkspaceInvoice{
		{ID: "inv-closed-1", InvoiceNo: "INV-CLOSED-1", DocType: "INVOICE", Status: "PAID", IssueDate: at.AddDate(0, 0, -60), DueDate: at.AddDate(0, 0, -30), TotalCents: 33_000, BalanceCents: 0, DaysOverdue: 0, DaysOverdueBucket: "CURRENT"},
	}
	st.invoicesDisputed = []CustomerWorkspaceInvoice{
		{ID: "inv-disp-1", InvoiceNo: "INV-DISP-1", DocType: "INVOICE", Status: "FINALIZED", IssueDate: at.AddDate(0, 0, -50), DueDate: at.AddDate(0, 0, -20), TotalCents: 44_000, BalanceCents: 44_000, DaysOverdue: 20, DaysOverdueBucket: "1-30", IsDisputed: true},
	}
	st.statements = []CustomerWorkspaceStatement{
		{ID: "stmt-1", RunID: "run-1", Mode: "MASS", AsOfDate: at, ClosingBalanceCents: 123_456, DeliveryStatus: "SENT", CreatedAt: at},
	}
	return st
}

func positionCredit() *fakeCreditForWorkspace {
	at := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	bal := int64(234_567)
	return &fakeCreditForWorkspace{
		agingRow: AgingRow{
			CurrentCents: 100_000, Days30Cents: 40_000, TotalCents: 140_000,
		},
		asOf:  at,
		found: true,
		activity: []ARActivity{
			{OccurredAt: at, Kind: "INVOICE", Reference: "INV-OPEN-1", AmountCents: 88_000, BalanceLeft: &bal},
		},
	}
}

func TestWorkspace_AccountPositionAllowedCarriesTheFigures(t *testing.T) {
	h := NewWorkspaceHandler(positionStore(), positionCredit(), nil).WithPaymentTrail(&fakeTrail{held: true}).
		WithCustomerReach(&fakeReach{ok: true}).WithAccountPosition(fakePosition{allow: true})
	body := workspaceBody(t, h)
	for _, key := range []string{`"credit_limit_cents":5000000`, `"open_cents":140000`, `"available_credit_cents":4860000`,
		`"overdue_cents":40000`, `"utilization_bps":`, `"aging":`, `"account_position_withheld":false`,
		`"days_overdue":15`, `"days_overdue_bucket":"1-30"`, `"closing_balance_cents":123456`, `"balance_after_cents":234567`} {
		if !strings.Contains(body, key) {
			t.Errorf("allowed workspace is missing %s in %s", key, body)
		}
	}
}

func TestWorkspace_AccountPositionRefusedIsByteAbsent(t *testing.T) {
	for name, pos := range map[string]AccountPositionReader{"refused": fakePosition{allow: false}, "unbound": nil} {
		h := NewWorkspaceHandler(positionStore(), positionCredit(), nil).WithPaymentTrail(&fakeTrail{held: true}).WithCustomerReach(&fakeReach{ok: true})
		if pos != nil {
			h = h.WithAccountPosition(pos)
		}
		body := workspaceBody(t, h)
		for _, key := range []string{"credit_limit_cents", "available_credit_cents", `"open_cents"`, `"overdue_cents"`,
			"utilization_bps", `"aging"`, "days_overdue", "days_overdue_bucket", "closing_balance_cents", "balance_after_cents",
			"5000000", "140000", "123456", "234567", `"1-30"`} {
			if strings.Contains(body, key) {
				t.Errorf("%s: %s must be off the wire, got %s", name, key, body)
			}
		}
		// Each invoice's own document figures (number, issue date, due date, total and balance) stay.
		for _, keep := range []string{`"invoice_no":"INV-OPEN-1"`, `"total_cents":88000`, `"balance_cents":66000`, `"issue_date":`, `"due_date":`} {
			if !strings.Contains(body, keep) {
				t.Errorf("%s: invoice document figure %s must remain on the wire: %s", name, keep, body)
			}
		}
		if !strings.Contains(body, `"account_position_withheld":true`) {
			t.Errorf("%s: the withheld flag must say so: %s", name, body)
		}
		// The hold standing is operational, not sensitive, and the payment trail is its own fence.
		if !strings.Contains(body, `"status":"CURRENT"`) || !strings.Contains(body, `"payments_withheld":false`) {
			t.Errorf("%s: status and the payment trail are unchanged: %s", name, body)
		}
	}
}

func TestWorkspace_PolicyIsAskedForTheCustomersHomeBranch(t *testing.T) {
	pol := &policyByBranch{byBranch: map[string]string{"": VisibilityAllStaff, "b-hidden": VisibilityHidden}}
	gate := gateFor(pol, identity.StaffContext{UserID: "u", Tier: identity.TierFrontline}, true)
	h := NewWorkspaceHandler(positionStore(), nil, nil).WithPaymentTrail(&fakeTrail{held: true}).
		WithCustomerReach(&fakeReach{ok: true}).WithAccountPosition(gate)
	body := workspaceBody(t, h)
	if strings.Contains(body, "credit_limit_cents") {
		t.Errorf("a HIDDEN home branch refuses even where the default would admit: %s", body)
	}
	if len(pol.asked) != 1 || pol.asked[0] != "b-hidden" {
		t.Errorf("policy asked for %v, want the home branch", pol.asked)
	}
}
