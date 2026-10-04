package staffcontext

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The rail-narrowed half of the ERP fakeStore (service_test.go at the pin):
// the fields and doubles the approvals-rail tests drive, carried with the
// session-world fields dropped along with the session-world store methods
// they served. The bodies are the carried ones.

type savedDecision struct {
	id, by string
	status ApprovalStatus
	reason string
}

type fakeStore struct {
	ctxs      map[string]StaffContext
	overrides map[string]bool // keyed by requestedBy|branchID|policy
	approval  ApprovalRequest
	saved     *savedDecision
	// lastFiled is the most recent request fileApproval handed the store
	// (the quote floor door's binding assertions read it back).
	lastFiled *ApprovalRequest

	// approvals rail (0075):
	saveNotApplied bool              // when set, SaveDecision reports applied=false (CAS lost / already decided)
	candidates     []StaffContext    // ListApproverCandidates result (fan-out pool)
	reconcile      []ApprovalRequest // ApprovalsForReconcile result (sweep window)
	lastTx         *fakeTx           // the tx BeginTx handed the atomic decide (assert commit/rollback)
}

func (f *fakeStore) HasApprovedOverride(_ context.Context, requestedBy, branchID, _ string, policy PolicyType) (bool, error) {
	return f.overrides[requestedBy+"|"+branchID+"|"+string(policy)], nil
}

func (f *fakeStore) ResolveStaff(_ context.Context, userID string) (StaffContext, error) {
	if c, ok := f.ctxs[userID]; ok {
		return c, nil
	}
	return StaffContext{UserID: userID}, nil
}

func (f *fakeStore) CreateApproval(_ context.Context, r ApprovalRequest) (ApprovalRequest, error) {
	r.ID = "ar-new"
	r.Status = ApprovalPending
	filed := r
	f.lastFiled = &filed
	return r, nil
}

func (f *fakeStore) ListApprovals(_ context.Context, _ string, _ []string) ([]ApprovalRequest, error) {
	return []ApprovalRequest{{ID: "ar1", Status: ApprovalPending}}, nil
}

func (f *fakeStore) GetApproval(_ context.Context, _ string) (ApprovalRequest, error) {
	return f.approval, nil
}

func (f *fakeStore) SaveDecision(_ context.Context, id, by string, status ApprovalStatus, reason string) (bool, error) {
	if f.saveNotApplied {
		return false, nil // CAS lost: request was already decided (no state recorded)
	}
	f.saved = &savedDecision{id: id, by: by, status: status, reason: reason}
	return true, nil
}

// BeginTx + SaveDecisionTx back the atomic decide (0075). The fake ignores the tx (state is in memory);
// SaveDecisionTx shares SaveDecision's CAS semantics so the ordering/echo assertions still hold.
func (f *fakeStore) BeginTx(_ context.Context) (pgx.Tx, error) {
	f.lastTx = &fakeTx{}
	return f.lastTx, nil
}
func (f *fakeStore) SaveDecisionTx(ctx context.Context, _ pgx.Tx, id, by string, status ApprovalStatus, reason string) (bool, error) {
	return f.SaveDecision(ctx, id, by, status, reason)
}

func (f *fakeStore) ListApproverCandidates(_ context.Context) ([]StaffContext, error) {
	return f.candidates, nil
}

func (f *fakeStore) ApprovalsForReconcile(_ context.Context, _ time.Time) ([]ApprovalRequest, error) {
	return f.reconcile, nil
}

// clerkCtx and mgrCtx are the non-approver halves of the authority matrix
// (the signature re-point moved the caller resolution out of the service,
// so the tests hand the caller in directly).
func clerkCtx() StaffContext {
	return StaffContext{UserID: "clerk", RoleCode: "INSIDE_SALES", Tier: TierFrontline, Scope: ScopeBranch, Branches: map[string]bool{trt: true}}
}

func mgrCtx() StaffContext {
	return StaffContext{UserID: "mgr", RoleCode: "BRANCH_MANAGER", Tier: TierManager, Scope: ScopeBranch, Branches: map[string]bool{trt: true}}
}

func TestApprovals_DecideApproval_AuthorityEnforced(t *testing.T) {
	f := &fakeStore{
		approval: ApprovalRequest{ID: "ar1", BranchID: trt, Policy: PolicyMargin, Status: ApprovalPending},
	}
	s := NewApprovals(f)

	if err := s.DecideApproval(context.Background(), tstApproval, clerkCtx(), true, ""); err == nil {
		t.Error("front-line decide should be rejected by the authority model")
	}
	if f.saved != nil {
		t.Error("nothing should persist when the authority check fails")
	}
	if err := s.DecideApproval(context.Background(), tstApproval, gmCtx(), true, ""); err != nil {
		t.Fatalf("GM approve should succeed: %v", err)
	}
	if f.saved == nil || f.saved.status != ApprovalApproved || f.saved.by != "gm" {
		t.Fatalf("expected APPROVED by gm persisted, got %+v", f.saved)
	}
}

// Four-eyes: a requester cannot decide their own override even with the authority.
func TestApprovals_DecideApproval_NoSelfApproval(t *testing.T) {
	f := &fakeStore{
		// the request was raised by the sales manager themself
		approval: ApprovalRequest{ID: "ar1", RequestedBy: "mgr", BranchID: trt, Policy: PolicyMargin, Status: ApprovalPending},
		ctxs: map[string]StaffContext{
			// the requester (four-eyes blocks self-approval regardless of their authority)
			"mgr": {UserID: "mgr", RoleCode: "SALES_MANAGER", Tier: TierManager, Scope: ScopeBranch, Branches: map[string]bool{trt: true}},
			"gm":  {UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal},
		},
	}
	s := NewApprovals(f)

	if err := s.DecideApproval(context.Background(), tstApproval, mgrCtx(), true, ""); err == nil {
		t.Fatal("the requester must not approve their own request (separation of duties)")
	}
	if f.saved != nil {
		t.Fatal("nothing should persist on a self-approval attempt")
	}
	// a DIFFERENT authorized approver succeeds.
	if err := s.DecideApproval(context.Background(), tstApproval, gmCtx(), true, ""); err != nil {
		t.Fatalf("a different authorized approver should succeed: %v", err)
	}
}

func TestApprovals_CreateApproval_ValidatesPolicy(t *testing.T) {
	s := NewApprovals(&fakeStore{})
	if _, err := s.CreateApproval(context.Background(), "u1", trt, "NONSENSE", "x", ""); err == nil {
		t.Error("unknown policy should be rejected")
	}
	if _, err := s.CreateApproval(context.Background(), "", trt, PolicyCredit, "x", ""); err == nil {
		t.Error("missing requested_by should be rejected")
	}
	// D7: the margin policy moved to the quote module's door; the caller
	// facing door's valid-create control is the credit policy.
	got, err := s.CreateApproval(context.Background(), "u1", trt, PolicyCredit, "over the limit", "")
	if err != nil {
		t.Fatalf("valid create should succeed: %v", err)
	}
	if got.Status != ApprovalPending {
		t.Errorf("new approval should be PENDING, got %s", got.Status)
	}
}
