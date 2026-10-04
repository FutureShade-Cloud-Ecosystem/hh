package staffcontext

// approvals_floor_test.go, the quote floor's rail changes (plan items E,
// defects D3, D7, D11, CP0-1 answer B):
//
//   - D7: a caller may not file a margin approval with a subject and detail
//     of its own choosing; the margin request is filed by the quote module
//     alone, through FileFloorApproval, whose figures the quote module owns
//   - D11: the margin decide authority is read from the dealer's role
//     configuration (the floor list plus the branch scoped Sales manager),
//     never from a code constant
//   - four eyes holds: the requester never decides its own request, even
//     when it holds an approver role
import (
	"context"
	"errors"
	"testing"
)

// qd1GM is a General Manager caller shape.
func qd1GM(userID string) StaffContext {
	return StaffContext{UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: TierLeadership,
		Scope: ScopeGlobal, Status: StatusActive, Branches: map[string]bool{}}
}

// qd1SM is a Sales manager caller shape reaching one branch.
func qd1SM(userID, branch string) StaffContext {
	return StaffContext{UserID: userID, RoleCode: "SALES_MANAGER", Tier: TierManager,
		Scope: ScopeBranch, Status: StatusActive, Branches: map[string]bool{branch: true}}
}

// - D7: the caller facing door refuses margin -

func TestQD1_CreateApprovalRefusesMarginPolicy(t *testing.T) {
	s := NewApprovals(&fakeStore{})
	_, err := s.CreateApproval(context.Background(), "u1", "b1", PolicyMargin, "self chosen words", "subject of my choosing")
	if err == nil {
		t.Fatal("D7: the caller facing door accepted a margin request, want the refusal that sends it to the quote module")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("D7: want a 403 shaped refusal, got %v", err)
	}
}

// - the quote module's filing door -

func TestQD1_FileFloorApprovalBindsSubjectAndFigures(t *testing.T) {
	f := &fakeStore{}
	s := NewApprovals(f)
	rec, err := s.FileFloorApproval(context.Background(), FloorApprovalFiling{
		RequestedBy: "u1", BranchID: "b1", SubjectRef: "quote-1",
		Detail: "quote #Q-9 line 2 pavers at 25 percent, margin 500 bps, floor 1200 bps",
		LineNo: 2, DiscPct: 25, LineID: "22222222-2222-4222-8222-222222222222", ProductID: "prod-pavers",
		Note: "customer pushed hard",
	})
	if err != nil {
		t.Fatalf("FileFloorApproval: %v", err)
	}
	if rec.Status != ApprovalPending || rec.LineNo != 2 || rec.DiscPct != 25 {
		t.Errorf("the filed record must carry its state, got %+v", rec)
	}
	got := f.lastFiled
	if got == nil {
		t.Fatal("nothing reached the store")
	}
	if got.SubjectRef != "quote-1" {
		t.Errorf("the subject must be the quote, got %q", got.SubjectRef)
	}
	if got.Policy != PolicyMargin {
		t.Errorf("the policy must be the margin floor, got %q", got.Policy)
	}
	if got.LineNo != 2 || got.DiscPct != 25 || got.Note != "customer pushed hard" {
		t.Errorf("the binding figures must ride the request, got %+v", got)
	}
	if got.LineID != "22222222-2222-4222-8222-222222222222" || got.ProductID != "prod-pavers" {
		t.Errorf("the line's own row and product must ride the request, got %+v", got)
	}
}

// - D11 and CP0-1 B: the margin decide authority from the configuration -

// qd1FloorApprovers is the CP0-1 B shape: the configuration's floor list
// (financial_controller, general_manager) plus the branch scoped Sales
// manager, plus the platform admin, as ERP role codes.
func qd1FloorApprovers() FloorApprovers {
	return FloorApprovers{
		DealerWide: []string{"FINANCIAL_CONTROLLER", "GENERAL_MANAGER", RoleSuperAdmin},
		OwnBranch:  []string{"SALES_MANAGER"},
	}
}

// qd1LineID is the line binding a quote desk floor request carries.
const qd1LineID = "22222222-2222-4222-8222-222222222223"

func TestQD1_SalesManagerDecidesFloorRequestInOwnBranch(t *testing.T) {
	f := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "u9", RequesterRole: "INSIDE_SALES", BranchID: "b1", Policy: PolicyMargin, LineID: qd1LineID, Status: ApprovalPending,
	}}
	s := NewApprovals(f).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), "11111111-1111-4111-8111-111111111111", qd1SM("sm1", "b1"), true, "qd1"); err != nil {
		t.Fatalf("CP0-1 B: the Sales manager must decide a floor request in its own branch: %v", err)
	}
}

func TestQD1_Row8_SalesManagerRefusedOutsideItsBranch(t *testing.T) {
	f := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "u9", BranchID: "b2", Policy: PolicyMargin, LineID: qd1LineID, Status: ApprovalPending,
	}}
	s := NewApprovals(f).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), "11111111-1111-4111-8111-111111111111", qd1SM("sm1", "b1"), true, "qd1"); err == nil {
		t.Fatal("row 8: a Sales manager decided a floor request outside its branch")
	}
}

func TestQD1_Row8_SalesManagerRefusedForOwnRequest(t *testing.T) {
	f := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "sm1", BranchID: "b1", Policy: PolicyMargin, LineID: qd1LineID, Status: ApprovalPending,
	}}
	s := NewApprovals(f).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), "11111111-1111-4111-8111-111111111111", qd1SM("sm1", "b1"), true, "qd1"); err == nil {
		t.Fatal("row 8: a Sales manager decided its own request, four eyes must hold")
	}
}

func TestQD1_Row8_InsideSalesCannotDecide(t *testing.T) {
	f := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "u9", BranchID: "b1", Policy: PolicyMargin, LineID: qd1LineID, Status: ApprovalPending,
	}}
	s := NewApprovals(f).WithFloorApprovers(qd1FloorApprovers())
	clerk := StaffContext{UserID: "c1", RoleCode: "INSIDE_SALES", Tier: TierFrontline,
		Scope: ScopeBranch, Status: StatusActive, Branches: map[string]bool{"b1": true}}
	if err := s.DecideApproval(context.Background(), "11111111-1111-4111-8111-111111111111", clerk, true, "qd1"); err == nil {
		t.Fatal("row 8: an inside sales caller decided an approval")
	}
}

// - ledger row 9: the General Manager cannot decide its own request -

// P2-C: the branch scoped floor arm decides LINE-BOUND requests alone. A
// margin card with no line binding is a legacy filing from before the
// quote desk's door bound margin to a line, a subject and detail of the
// filer's choosing, and it stays decidable by the dealer wide set alone.
func TestQD1_UnboundMarginCardIsNotTheBranchArmsToDecide(t *testing.T) {
	legacy := ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111121", RequestedBy: "u9", RequesterRole: "INSIDE_SALES", BranchID: "b1",
		Policy: PolicyMargin, Status: ApprovalPending,
	}
	s := NewApprovals(&fakeStore{approval: legacy}).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), legacy.ID, qd1SM("sm1", "b1"), true, "qd1"); err == nil {
		t.Fatal("P2-C: the branch Sales manager decided a margin card with no line binding")
	}

	// The dealer wide floor set keeps the card, and a line-bound request of
	// the same branch still rides the branch arm.
	wide := NewApprovals(&fakeStore{approval: legacy}).WithFloorApprovers(qd1FloorApprovers())
	if err := wide.DecideApproval(context.Background(), legacy.ID, qd1GM("gm3"), true, "qd1"); err != nil {
		t.Fatalf("the dealer wide floor set keeps an unbound margin card: %v", err)
	}
	bound := legacy
	bound.LineID = qd1LineID
	branch := NewApprovals(&fakeStore{approval: bound}).WithFloorApprovers(qd1FloorApprovers())
	if err := branch.DecideApproval(context.Background(), bound.ID, qd1SM("sm1", "b1"), true, "qd1"); err != nil {
		t.Fatalf("a line-bound floor request still rides the branch arm: %v", err)
	}
}

func TestQD1_Row9_GeneralManagerRefusedForOwnRequest(t *testing.T) {
	f := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "gm1", BranchID: "b1", Policy: PolicyMargin, Status: ApprovalPending,
	}}
	s := NewApprovals(f).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), "11111111-1111-4111-8111-111111111111", qd1GM("gm1"), true, "qd1"); err == nil {
		t.Fatal("row 9: the General Manager decided its own request, four eyes must hold")
	}
}

// A rail with no approvers wired keeps the carried default, which equals
// r3's ruling: the General Manager, the Financial Controller and the
// platform admin, no branch scoped arm.
func TestQD1_UnwiredFloorApproversKeepTheCarriedDefault(t *testing.T) {
	f := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "u9", BranchID: "b1", Policy: PolicyMargin, Status: ApprovalPending,
	}}
	s := NewApprovals(f)
	if err := s.DecideApproval(context.Background(), "11111111-1111-4111-8111-111111111111", qd1GM("gm2"), true, "qd1"); err != nil {
		t.Fatalf("the carried default must keep the General Manager: %v", err)
	}
	f2 := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "u9", BranchID: "b1", Policy: PolicyMargin, Status: ApprovalPending,
	}}
	s2 := NewApprovals(f2)
	if err := s2.DecideApproval(context.Background(), "ar1", qd1SM("sm1", "b1"), true, "qd1"); err == nil {
		t.Fatal("the carried default has no branch scoped arm, the Sales manager must be refused")
	}
}

// The General Manager remains an approver under the configured set.
func TestQD1_GeneralManagerDecidesUnderConfiguredSet(t *testing.T) {
	f := &fakeStore{approval: ApprovalRequest{
		ID: "11111111-1111-4111-8111-111111111111", RequestedBy: "u9", BranchID: "b1", Policy: PolicyMargin, Status: ApprovalPending,
	}}
	s := NewApprovals(f).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), "11111111-1111-4111-8111-111111111111", qd1GM("gm2"), true, "qd1"); err != nil {
		t.Fatalf("the General Manager must decide under the configured set: %v", err)
	}
}

// The filing's row binding is required: a request without the line's own
// row id or its product would match a line NUMBER, which a later line can
// reuse. Dropping the required check lets either filing through, and this
// test goes red (the store receives the request).
func TestQD1_FileFloorApprovalRefusesAnUnboundFiling(t *testing.T) {
	base := FloorApprovalFiling{
		RequestedBy: "u1", BranchID: "b1", SubjectRef: "quote-1", Detail: "d",
		LineNo: 1, DiscPct: 25, LineID: qd1LineID, ProductID: "prod-pavers",
	}
	noLine, noProduct := base, base
	noLine.LineID = ""
	noProduct.ProductID = ""
	for name, in := range map[string]FloorApprovalFiling{"no line_id": noLine, "no product_id": noProduct} {
		f := &fakeStore{}
		_, err := NewApprovals(f).FileFloorApproval(context.Background(), in)
		var ce clientError
		if !errors.As(err, &ce) || ce.status != 400 {
			t.Errorf("%s: want a 400 refusal, got %v", name, err)
		}
		if f.lastFiled != nil {
			t.Errorf("%s: an unbound floor request reached the store", name)
		}
	}
}
