package staffcontext

// approvals_filer_test.go, the floor approver ruling (decision log row 743):
// the branch Sales manager decides the below floor requests filed by Inside
// Sales and Outside Sales in its OWN branch, and nothing else. A request
// filed by a Branch manager, a Sales manager (a peer's included) or the Yard
// manager goes to the General Manager or the Financial Controller. The rule
// lives in one place and every surface follows it: the decide path, the
// fan-out, the list a role sees and the count.
import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	filerReqID = "11111111-1111-4111-8111-111111111131"
	filerOther = "5d1b3f6e-0000-4000-9000-000000000002"
)

func filerFC(userID string) StaffContext {
	return StaffContext{UserID: userID, RoleCode: "FINANCIAL_CONTROLLER", Tier: TierLeadership,
		Scope: ScopeGlobal, Status: StatusActive, Branches: map[string]bool{}}
}

// filerRequest is a pending, line-bound floor request in Trenton filed by
// the given role.
func filerRequest(role string) ApprovalRequest {
	return ApprovalRequest{
		ID: filerReqID, RequestedBy: "filer", RequesterRole: role, BranchID: trt,
		Policy: PolicyMargin, LineID: qd1LineID, Status: ApprovalPending,
	}
}

// filerRoles splits the filer roles by who may decide for them in a branch.
var (
	salespersonFilers = []string{"INSIDE_SALES", "OUTSIDE_SALES"}
	leadershipFilers  = []string{"BRANCH_MANAGER", "SALES_MANAGER", "YARD_MANAGER", "LOGISTICS_MANAGER",
		"PROCUREMENT_MANAGER", "YARD_TEAM", RoleDrivers, "GENERAL_MANAGER", "FINANCIAL_CONTROLLER", "HR",
		RoleSuperAdmin, "NOT_A_ROLE", ""}
)

func TestFloorFiler_SalesManagerDecidesASalespersonsRequestInItsBranch(t *testing.T) {
	for _, role := range salespersonFilers {
		s := NewApprovals(&fakeStore{approval: filerRequest(role)}).WithFloorApprovers(qd1FloorApprovers())
		if err := s.DecideApproval(context.Background(), filerReqID, qd1SM("sm1", trt), true, "ok"); err != nil {
			t.Errorf("a %s filing in the branch must be the branch Sales manager's to decide: %v", role, err)
		}
	}
}

func TestFloorFiler_SalesManagerRefusedEveryOtherFilersRequest(t *testing.T) {
	for _, role := range leadershipFilers {
		f := &fakeStore{approval: filerRequest(role)}
		s := NewApprovals(f).WithFloorApprovers(qd1FloorApprovers())
		err := s.DecideApproval(context.Background(), filerReqID, qd1SM("sm1", trt), true, "no")
		if err == nil {
			t.Errorf("a %q filing was decided by the branch Sales manager, it is leadership's", role)
		}
		if f.saved != nil {
			t.Errorf("a %q filing refused to the Sales manager still persisted a decision", role)
		}
	}
}

func TestFloorFiler_LeadershipDecidesEveryFilersRequest(t *testing.T) {
	for _, role := range append(append([]string{}, salespersonFilers...), leadershipFilers...) {
		for _, caller := range []StaffContext{qd1GM("gm1"), filerFC("fc1")} {
			s := NewApprovals(&fakeStore{approval: filerRequest(role)}).WithFloorApprovers(qd1FloorApprovers())
			if err := s.DecideApproval(context.Background(), filerReqID, caller, true, "ok"); err != nil {
				t.Errorf("%s must decide a %q filing: %v", caller.RoleCode, role, err)
			}
		}
	}
}

func TestFloorFiler_AnotherBranchsSalespersonFilingRefusedToThisSalesManager(t *testing.T) {
	r := filerRequest("INSIDE_SALES")
	r.BranchID = filerOther
	s := NewApprovals(&fakeStore{approval: r}).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), filerReqID, qd1SM("sm1", trt), true, "no"); err == nil {
		t.Fatal("a Sales manager decided another branch's Inside Sales filing")
	}
}

func TestFloorFiler_FourEyesHolds(t *testing.T) {
	// The General Manager filing its own request, and a salesperson's
	// request decided by the same person who filed it, are both refused.
	own := filerRequest("GENERAL_MANAGER")
	own.RequestedBy = "gm1"
	s := NewApprovals(&fakeStore{approval: own}).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), filerReqID, qd1GM("gm1"), true, "no"); err == nil {
		t.Error("the General Manager decided its own filing")
	}
	own = filerRequest("SALES_MANAGER")
	own.RequestedBy = "sm1"
	s = NewApprovals(&fakeStore{approval: own}).WithFloorApprovers(qd1FloorApprovers())
	if err := s.DecideApproval(context.Background(), filerReqID, qd1SM("sm1", trt), true, "no"); err == nil {
		t.Error("a Sales manager decided its own filing")
	}
}

// The approvals list a role sees: a Sales manager reads a salesperson's
// request of its branch and nobody else's floor request (its own aside), a
// General Manager reads all.
type filerListStore struct {
	fakeStore
	list []ApprovalRequest
}

func (f *filerListStore) ListApprovals(_ context.Context, _ string, _ []string) ([]ApprovalRequest, error) {
	return f.list, nil
}

func filerListOf(roles []string) []ApprovalRequest {
	var out []ApprovalRequest
	for i, role := range roles {
		r := filerRequest(role)
		r.ID = filerReqID[:len(filerReqID)-2] + string(rune('a'+i/10)) + string(rune('0'+i%10))
		out = append(out, r)
	}
	return out
}

func TestFloorFiler_ListedOnlyToWhoCouldDecide(t *testing.T) {
	roles := append(append([]string{}, salespersonFilers...), leadershipFilers...)
	st := &filerListStore{list: filerListOf(roles)}
	s := NewApprovals(st).WithFloorApprovers(qd1FloorApprovers())

	got, err := s.ListApprovals(context.Background(), qd1SM("sm1", trt), "")
	if err != nil {
		t.Fatalf("list as the Sales manager: %v", err)
	}
	if len(got) != len(salespersonFilers) {
		t.Fatalf("the Sales manager's list holds %d requests, want only the %d salesperson filings", len(got), len(salespersonFilers))
	}
	for _, r := range got {
		if r.RequesterRole != "INSIDE_SALES" && r.RequesterRole != "OUTSIDE_SALES" {
			t.Errorf("a %q filing is listed to the Sales manager", r.RequesterRole)
		}
	}

	all, err := s.ListApprovals(context.Background(), qd1GM("gm1"), "")
	if err != nil {
		t.Fatalf("list as the General Manager: %v", err)
	}
	if len(all) != len(roles) {
		t.Fatalf("the General Manager's list holds %d requests, want all %d", len(all), len(roles))
	}
}

func TestFloorFiler_CountOnlyWhatTheCallerCouldDecide(t *testing.T) {
	s := NewApprovals(&fakeStore{}).WithFloorApprovers(qd1FloorApprovers())
	sm := qd1SM("sm1", trt)
	for _, role := range salespersonFilers {
		if !s.CanDecideRequest(sm, filerRequest(role)) {
			t.Errorf("the count misses the Sales manager's own %s filing", role)
		}
	}
	for _, role := range leadershipFilers {
		if s.CanDecideRequest(sm, filerRequest(role)) {
			t.Errorf("the count holds a %q filing the Sales manager cannot decide", role)
		}
		if !s.CanDecideRequest(qd1GM("gm1"), filerRequest(role)) {
			t.Errorf("the count misses a %q filing the General Manager decides", role)
		}
	}
}

func TestFloorFiler_FanOutNotifiesWhoCouldDecide(t *testing.T) {
	candidates := []StaffContext{
		{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal},
		{UserID: "fc", RoleCode: "FINANCIAL_CONTROLLER", Tier: TierLeadership, Scope: ScopeGlobal},
		{UserID: "sm", RoleCode: "SALES_MANAGER", Tier: TierManager, Scope: ScopeBranch, Branches: map[string]bool{trt: true}},
	}
	roles := map[string]string{"filer-inside": "INSIDE_SALES", "filer-outside": "OUTSIDE_SALES",
		"filer-bm": "BRANCH_MANAGER", "filer-sm": "SALES_MANAGER", "filer-ym": "YARD_MANAGER", "filer-none": ""}
	want := map[string][]string{
		"filer-inside": {"gm", "fc", "sm"}, "filer-outside": {"gm", "fc", "sm"},
		"filer-bm": {"gm", "fc"}, "filer-sm": {"gm", "fc"}, "filer-ym": {"gm", "fc"}, "filer-none": {"gm", "fc"},
	}
	for user, wantIDs := range want {
		notifier := &fakeNotifier{}
		s := NewApprovals(&fakeStore{candidates: candidates, requesterRoles: roles}).
			WithApprovalNotifier(notifier).WithFloorApprovers(qd1FloorApprovers())
		if _, err := s.FileFloorApproval(context.Background(), FloorApprovalFiling{
			RequestedBy: user, BranchID: trt, SubjectRef: "q1", Detail: "below floor", LineNo: 1, DiscPct: 25,
			LineID: qd1LineID, ProductID: "prod-1",
		}); err != nil {
			t.Fatalf("file as %s: %v", user, err)
		}
		if len(notifier.requested) != 1 || !equalIDs(notifier.requested[0].ApproverIDs, wantIDs) {
			t.Errorf("a %s filing notified %+v, want %v", user, notifier.requested, wantIDs)
		}
	}

	// The reconcile sweep re-fans the same authority.
	for _, role := range append(append([]string{}, salespersonFilers...), leadershipFilers...) {
		r := filerRequest(role)
		s := NewApprovals(&fakeStore{candidates: candidates, reconcile: []ApprovalRequest{r}}).WithFloorApprovers(qd1FloorApprovers())
		pending, _, err := s.ApprovalReconcile(context.Background(), time.Now().Add(-time.Hour))
		if err != nil || len(pending) != 1 {
			t.Fatalf("reconcile %q: %v %+v", role, err, pending)
		}
		wantIDs := []string{"gm", "fc"}
		if role == "INSIDE_SALES" || role == "OUTSIDE_SALES" {
			wantIDs = []string{"gm", "fc", "sm"}
		}
		if !equalIDs(pending[0].ApproverIDs, wantIDs) {
			t.Errorf("the sweep for a %q filing fans to %v, want %v", role, pending[0].ApproverIDs, wantIDs)
		}
	}
}

// An unwired floor set keeps leadership only whatever the filer's role.
func TestFloorFiler_CarriedDefaultStaysLeadershipOnly(t *testing.T) {
	err := NewApprovals(&fakeStore{approval: filerRequest("INSIDE_SALES")}).
		DecideApproval(context.Background(), filerReqID, qd1SM("sm1", trt), true, "no")
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("the carried default must refuse the Sales manager with a 403, got %v", err)
	}
}
