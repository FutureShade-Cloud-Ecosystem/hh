package staffcontext

// freight_approval_test.go: the identity half of the second-truck gate (0195).
//
// Six touch points make a fifth policy real, and this file pins the four that live in Go: the
// authority arm, the system-only create path, the filing that carries no money, and the decide bridge
// that applies or refuses inside the decision's own transaction. The CHECK constraint is pinned by
// the migration and by the dispatch integration suite; the counter-readable label is pinned in the app.

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// -- the authority arm ------------------------------------------------------

// A freight raise goes on a CUSTOMER'S invoice, so it answers to the same three ruled roles the two
// existing rail policies do, and to nobody else. The exclusions are the assertion that matters:
// YARD_MANAGER and LOGISTICS_MANAGER are the very people who ask for the extra truck, and HR is a
// Tier-1 GLOBAL role that a plain IsAdmin() check would have let through.
func TestCanApprove_FreightAboveQuote_RailAuthorityOnly(t *testing.T) {
	allowed := []StaffContext{
		ctx("GENERAL_MANAGER", TierLeadership, ScopeGlobal),
		ctx("FINANCIAL_CONTROLLER", TierLeadership, ScopeGlobal),
		ctx(RoleSuperAdmin, TierLeadership, ScopeGlobal),
	}
	denied := []StaffContext{
		ctx("HR", TierLeadership, ScopeGlobal),
		ctx("YARD_MANAGER", TierManager, ScopeBranch, kgn),
		ctx("LOGISTICS_MANAGER", TierManager, ScopeBranch, trt),
		ctx("BRANCH_MANAGER", TierManager, ScopeBranch, trt),
		ctx("SALES_MANAGER", TierManager, ScopeBranch, trt),
		ctx("INSIDE_SALES", TierFrontline, ScopeBranch, trt),
	}
	// The test guards itself: an empty side would make either loop below pass without asserting.
	if len(allowed) == 0 || len(denied) == 0 {
		t.Fatal("this test asserts nothing unless both sides are populated")
	}
	for _, c := range allowed {
		if !CanApprove(c, PolicyFreightAboveQuote, trt) || !CanApprove(c, PolicyFreightAboveQuote, kgn) {
			t.Errorf("%s must be able to approve a freight raise in any branch", c.RoleCode)
		}
	}
	for _, c := range denied {
		if CanApprove(c, PolicyFreightAboveQuote, trt) || CanApprove(c, PolicyFreightAboveQuote, kgn) {
			t.Errorf("%s must NOT approve a freight raise: it is a charge on a customer", c.RoleCode)
		}
	}
}

func TestCanApprove_UnnamedPolicyIsUndecidableByEveryone(t *testing.T) {
	for _, c := range []StaffContext{
		ctx("GENERAL_MANAGER", TierLeadership, ScopeGlobal),
		ctx(RoleSuperAdmin, TierLeadership, ScopeGlobal),
	} {
		if CanApprove(c, PolicyType("SOME_FUTURE_POLICY"), trt) {
			t.Fatalf("%s decided a policy nothing has ever named: the default arm is not false", c.RoleCode)
		}
	}
	// and the same caller CAN decide a named one, so the assertion above is not passing because
	// CanApprove refuses everything.
	if !CanApprove(ctx("GENERAL_MANAGER", TierLeadership, ScopeGlobal), PolicyFreightAboveQuote, trt) {
		t.Fatal("positive control failed: a GM cannot approve the policy this file is about")
	}
}

// -- the create path is system-only ----------------------------------------

// POST /api/v1/staff/approvals is open to every authenticated staff member. A freight card filed
// there could name any subject_ref its author liked (including a raise nobody proposed), and a rail
// approver pressing Approve is what MOVES THE MONEY. So the caller-facing door refuses it, exactly as
// it refuses a hand-filed journal card, and the two refusals are asserted together so neither can be
// deleted without the other's test noticing.
func TestCreateApproval_SystemOnlyPoliciesAreRefusedOnTheCallerPath(t *testing.T) {
	s := NewApprovals(&fakeStore{})
	for _, policy := range []PolicyType{PolicyFreightAboveQuote, PolicyJEThreshold} {
		if _, err := s.CreateApproval(context.Background(), "u1", trt, policy, "hand-filed", "sub"); err == nil {
			t.Fatalf("%s must not be filable from the caller-facing create path", policy)
		}
	}
	// Positive control: the path itself works, so the two refusals above are about the POLICY and not
	// about a create path that has stopped creating anything.
	if _, err := s.CreateApproval(context.Background(), "u1", trt, PolicyMargin, "below floor", ""); err != nil {
		t.Fatalf("positive control failed: a margin override must still file: %v", err)
	}
}

// -- the filing carries no money -------------------------------------------

// The approvals list has no money gate of its own: it was built for margin overrides and journal
// thresholds, which are internal numbers. A customer's delivery charge is not, so nothing about the
// figures may ride on the approval row: not amount_cents (which the JE filer beside it DOES use), and
// not a figure smuggled into the sentence.
func TestFileFreightApproval_CarriesNoFigures(t *testing.T) {
	f := &fakeStore{}
	s := NewApprovals(f)
	req, err := s.FileFreightApproval(context.Background(), "raise-1", trt, "dispatcher-1",
		"Freight on #SO-TRT-1041 for Bay of Quinte Landscaping is being raised above the delivery the customer was quoted.")
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if req.AmountCents != 0 {
		t.Errorf("a freight approval must carry no amount, got %d", req.AmountCents)
	}
	if req.SubjectRef != "raise-1" {
		t.Errorf("the request must bind the parked raise, got %q", req.SubjectRef)
	}
	if req.Policy != PolicyFreightAboveQuote {
		t.Errorf("wrong policy filed: %s", req.Policy)
	}
	if digits := regexp.MustCompile(`\d`).FindAllString(strings.ReplaceAll(req.Detail, "#SO-TRT-1041", ""), -1); len(digits) > 0 {
		t.Errorf("the approval sentence carries figures a viewer has no money gate for: %q", req.Detail)
	}
	// Positive control on the digit scan: it does find digits when there are some.
	if got := regexp.MustCompile(`\d`).FindAllString("raised by 4200 cents", -1); len(got) == 0 {
		t.Fatal("positive control failed: the digit scan finds nothing anywhere")
	}
}

// -- the decide bridge ------------------------------------------------------

type recordingFreightDecider struct {
	applied []string
	refused []string
	fail    error
}

func (d *recordingFreightDecider) ApplyFreightRaiseTx(_ context.Context, _ pgx.Tx, raiseID, _ string) error {
	if d.fail != nil {
		return d.fail
	}
	d.applied = append(d.applied, raiseID)
	return nil
}

func (d *recordingFreightDecider) RefuseFreightRaiseTx(_ context.Context, _ pgx.Tx, raiseID, _ string) error {
	if d.fail != nil {
		return d.fail
	}
	d.refused = append(d.refused, raiseID)
	return nil
}

func freightDecideFixture(t *testing.T, d FreightDecider) (*fakeStore, *Approvals) {
	t.Helper()
	f := &fakeStore{
		approval: ApprovalRequest{
			ID: "ar-freight", RequestedBy: "dispatcher-1", BranchID: trt,
			Policy: PolicyFreightAboveQuote, SubjectRef: "raise-1", Status: ApprovalPending,
		},
		ctxs: map[string]StaffContext{
			"gm": {UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal, Status: StatusActive},
		},
	}
	return f, NewApprovals(f).WithFreightDecider(d)
}

// BOTH outcomes cross the bridge, and that is the difference from the JE bridge beside it: a declined
// journal simply stays DRAFT, whereas a declined freight raise has to be marked refused or it holds
// the order's parked slot forever and tells the next dispatcher a decision is still coming.
func TestDecideApproval_FreightBridge_BothOutcomesCross(t *testing.T) {
	t.Run("approve applies the parked split", func(t *testing.T) {
		d := &recordingFreightDecider{}
		f, s := freightDecideFixture(t, d)
		if err := s.DecideApproval(context.Background(), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", gmCtx(), true, ""); err != nil {
			t.Fatalf("approve: %v", err)
		}
		if len(d.applied) != 1 || d.applied[0] != "raise-1" {
			t.Fatalf("the approve did not apply the parked raise: %+v", d.applied)
		}
		if len(d.refused) != 0 {
			t.Fatalf("an approve must not also refuse: %+v", d.refused)
		}
		if f.saved == nil || f.saved.status != ApprovalApproved {
			t.Fatalf("the request was not stamped APPROVED: %+v", f.saved)
		}
		if f.lastTx == nil || !f.lastTx.committed {
			t.Fatal("the decision and the apply must commit on ONE transaction")
		}
	})

	t.Run("reject refuses the parked split and applies nothing", func(t *testing.T) {
		d := &recordingFreightDecider{}
		f, s := freightDecideFixture(t, d)
		if err := s.DecideApproval(context.Background(), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", gmCtx(), false, "not this time"); err != nil {
			t.Fatalf("reject: %v", err)
		}
		if len(d.applied) != 0 {
			t.Fatalf("a rejection must apply nothing, got %+v", d.applied)
		}
		if len(d.refused) != 1 || d.refused[0] != "raise-1" {
			t.Fatalf("the rejection did not reach the parked raise: %+v", d.refused)
		}
		if f.saved == nil || f.saved.status != ApprovalRejected {
			t.Fatalf("the request was not stamped REJECTED: %+v", f.saved)
		}
	})
}

// A bridge that refuses takes the STAMP down with it. Without this the request would read APPROVED
// while dispatch had applied nothing: the post-then-stamp divergence 0075 closed for journals,
// arriving a second time through a different door.
func TestDecideApproval_FreightBridgeFailure_RollsBackTheStamp(t *testing.T) {
	d := &recordingFreightDecider{fail: NewClientError(409, "dispatch: this order has no live loads any more")}
	f, s := freightDecideFixture(t, d)
	err := s.DecideApproval(context.Background(), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", gmCtx(), true, "")
	if err == nil {
		t.Fatal("a failed apply must fail the decision")
	}
	if !strings.Contains(err.Error(), "no live loads") {
		t.Fatalf("the approver must read dispatch's own sentence, got %q", err)
	}
	if f.lastTx == nil || f.lastTx.committed {
		t.Fatal("a failed apply must not commit the stamp")
	}
	if !f.lastTx.rolledBack {
		t.Fatal("a failed apply must roll the stamp back")
	}
}

// The kill switch. With no bridge wired, a decision still stamps the request and applies NOTHING,
// which is the safe direction for a seam whose job is to move money.
func TestDecideApproval_FreightWithNoBridge_MovesNoMoney(t *testing.T) {
	f, s := freightDecideFixture(t, nil)
	s.freightDecider = nil
	if err := s.DecideApproval(context.Background(), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", gmCtx(), true, ""); err != nil {
		t.Fatalf("an unwired bridge must not break the decision: %v", err)
	}
	if f.saved == nil || f.saved.status != ApprovalApproved {
		t.Fatalf("the request should still have been stamped: %+v", f.saved)
	}
}
