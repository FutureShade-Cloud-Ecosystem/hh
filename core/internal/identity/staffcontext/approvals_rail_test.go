package staffcontext

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// gmCtx / fcCtx are ruled rail approvers used across the rail tests.
func gmCtx() StaffContext {
	return StaffContext{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal}
}

// fakeNotifier records the fan-out + decision-echo calls the service makes.
type fakeNotifier struct {
	requested []ApprovalNotice
	decided   []ApprovalDecidedNotice
}

func (f *fakeNotifier) ApprovalRequested(_ context.Context, n ApprovalNotice) {
	f.requested = append(f.requested, n)
}
func (f *fakeNotifier) ApprovalDecided(_ context.Context, n ApprovalDecidedNotice) {
	f.decided = append(f.decided, n)
}

// fakeJEDecider stands in for the GL bridge: it records the call + returns a scripted error.
type fakeJEDecider struct {
	called    bool
	journalID string
	deciderID string
	err       error
}

func (f *fakeJEDecider) ApproveJournalTx(_ context.Context, _ pgx.Tx, journalID, deciderID string) error {
	f.called = true
	f.journalID = journalID
	f.deciderID = deciderID
	return f.err
}

func mustClientStatus(t *testing.T, err error) int {
	t.Helper()
	var ce clientError
	if !errors.As(err, &ce) {
		t.Fatalf("want a clientError, got %T: %v", err, err)
	}
	return ce.status
}

// TestDecideApproval_CASConflict409 pins the compare-and-swap: when the persistence layer reports the
// request was already decided (applied=false, a concurrent-decide race), the decide returns 409 and fires
// NO echo (the loser never re-stamps or re-echoes a decision it did not make).
func TestDecideApproval_CASConflict409(t *testing.T) {
	notifier := &fakeNotifier{}
	f := &fakeStore{
		approval:       ApprovalRequest{ID: "ar1", BranchID: trt, Policy: PolicyMargin, Status: ApprovalPending},
		ctxs:           map[string]StaffContext{"gm": gmCtx()},
		saveNotApplied: true, // CAS loses: someone already decided this request
	}
	s := NewApprovals(f).WithApprovalNotifier(notifier)

	err := s.DecideApproval(context.Background(), tstApproval, gmCtx(), true, "")
	if err == nil {
		t.Fatal("a lost CAS must return an error, not succeed")
	}
	if got := mustClientStatus(t, err); got != http.StatusConflict {
		t.Fatalf("lost CAS should be 409, got %d", got)
	}
	if len(notifier.decided) != 0 {
		t.Fatalf("no decision echo should fire when the CAS lost, got %d", len(notifier.decided))
	}
}

// TestDecideApproval_JEOrdering pins the ATOMIC JE decide (0075): the request CAS stamp and the JE-to-GL post
// run on ONE transaction, so they commit or roll back together:
//   - GL 409 on approve: the decide returns the error AND the tx is ROLLED BACK (not committed): in the
//     real DB the stamp is undone, so the request stays PENDING and the ledger is untouched.
//   - GL success on approve: the request is stamped APPROVED, the GL bridge got the decider id, tx COMMITS.
//   - DECLINE never calls GL (the journal stays DRAFT), the request is stamped REJECTED, tx COMMITS.
//
// (The fake store is not transactional, so it cannot un-set f.saved on rollback: the real rollback-to-undo
// is covered by the integration suite. Here we assert ordering, error propagation, and commit/rollback.)
func TestDecideApproval_JEOrdering(t *testing.T) {
	newStore := func() *fakeStore {
		return &fakeStore{
			approval: ApprovalRequest{ID: "ar1", RequestedBy: "maker", BranchID: trt, Policy: PolicyJEThreshold, SubjectRef: "journal-1", Status: ApprovalPending},
			ctxs:     map[string]StaffContext{"gm": gmCtx()},
		}
	}

	// (a) GL refuses (journal meanwhile posted/voided, 409): decide returns 409 and the tx ROLLS BACK, so
	// the stamp is undone with the ledger: no "APPROVED without a POST".
	f := newStore()
	jd := &fakeJEDecider{err: NewClientError(http.StatusConflict, "gl: journal is POSTED, not DRAFT")}
	s := NewApprovals(f).WithJEDecider(jd)
	err := s.DecideApproval(context.Background(), tstApproval, gmCtx(), true, "")
	if err == nil {
		t.Fatal("a GL refusal on approve must surface as an error")
	}
	if got := mustClientStatus(t, err); got != http.StatusConflict {
		t.Fatalf("GL 409 should surface as 409, got %d", got)
	}
	if !jd.called || jd.journalID != "journal-1" {
		t.Fatalf("GL bridge should have been called with the journal id, got called=%v id=%q", jd.called, jd.journalID)
	}
	if jd.deciderID != "gm" {
		t.Fatalf("GL bridge must receive the decider id, got %q", jd.deciderID)
	}
	if f.lastTx == nil || f.lastTx.committed {
		t.Fatalf("the decision tx must NOT commit when GL refuses, got %+v", f.lastTx)
	}
	if !f.lastTx.rolledBack {
		t.Fatal("the decision tx must roll back when GL refuses (undoing the stamp with the ledger)")
	}

	// (b) GL succeeds: the request is stamped APPROVED, the bridge got the decider id, and the tx COMMITS.
	f = newStore()
	jd = &fakeJEDecider{}
	s = NewApprovals(f).WithJEDecider(jd)
	if err := s.DecideApproval(context.Background(), tstApproval, gmCtx(), true, "ok"); err != nil {
		t.Fatalf("GL success should let the decide succeed: %v", err)
	}
	if !jd.called || jd.deciderID != "gm" {
		t.Fatalf("GL bridge should be called on a JE approve with the decider id, got called=%v decider=%q", jd.called, jd.deciderID)
	}
	if f.saved == nil || f.saved.status != ApprovalApproved {
		t.Fatalf("request should be stamped APPROVED after GL posts, got %+v", f.saved)
	}
	if f.lastTx == nil || !f.lastTx.committed {
		t.Fatalf("a successful JE approve must COMMIT the tx, got %+v", f.lastTx)
	}

	// (c) DECLINE bridges nothing: GL is never called, the request is REJECTED, the journal stays DRAFT.
	f = newStore()
	jd = &fakeJEDecider{}
	s = NewApprovals(f).WithJEDecider(jd)
	if err := s.DecideApproval(context.Background(), tstApproval, gmCtx(), false, "no"); err != nil {
		t.Fatalf("decline should succeed: %v", err)
	}
	if jd.called {
		t.Fatal("a DECLINE must NOT call the GL bridge (the journal stays DRAFT)")
	}
	if f.saved == nil || f.saved.status != ApprovalRejected {
		t.Fatalf("request should be stamped REJECTED on decline, got %+v", f.saved)
	}
	if f.lastTx == nil || !f.lastTx.committed {
		t.Fatalf("a decline must COMMIT the tx, got %+v", f.lastTx)
	}
}

// TestCreateApproval_FansOutToEligibleApprovers pins the fan-out wiring: a filed request notifies with the
// eligible-approver set resolved from the candidate pool via CanApprove.
func TestCreateApproval_FansOutToEligibleApprovers(t *testing.T) {
	notifier := &fakeNotifier{}
	f := &fakeStore{
		candidates: []StaffContext{
			{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal},
			{UserID: "fc", RoleCode: "FINANCIAL_CONTROLLER", Tier: TierLeadership, Scope: ScopeGlobal},
			{UserID: "hr", RoleCode: "HR", Tier: TierLeadership, Scope: ScopeGlobal}, // excluded
		},
	}
	s := NewApprovals(f).WithApprovalNotifier(notifier)

	// D7: the margin request is filed by the quote module alone, so this
	// test files through the quote module's door.
	if _, err := s.FileFloorApproval(context.Background(), FloorApprovalFiling{
		RequestedBy: "clerk", BranchID: trt, SubjectRef: "q1", Detail: "6% margin", LineNo: 1, DiscPct: 25, LineID: "11111111-1111-4111-8111-111111111111", ProductID: "prod-1",
	}); err != nil {
		t.Fatalf("create should succeed: %v", err)
	}
	if len(notifier.requested) != 1 {
		t.Fatalf("expected exactly one fan-out notice, got %d", len(notifier.requested))
	}
	if got := len(notifier.requested[0].ApproverIDs); got != 2 {
		t.Fatalf("expected 2 eligible approvers (gm/fc), got %d: %v", got, notifier.requested[0].ApproverIDs)
	}
}

// TestFileJEApproval_UsesJEPolicyAndAmount pins the GL park-bridge entry: it files a JE_OVER_THRESHOLD
// request carrying the journal subject_ref + amount, and fans out.
func TestFileJEApproval_UsesJEPolicyAndAmount(t *testing.T) {
	notifier := &fakeNotifier{}
	f := &fakeStore{candidates: []StaffContext{{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal}}}
	s := NewApprovals(f).WithApprovalNotifier(notifier)

	req, err := s.FileJEApproval(context.Background(), "journal-9", trt, 600000, "maker")
	if err != nil {
		t.Fatalf("file JE approval: %v", err)
	}
	if req.Policy != PolicyJEThreshold || req.SubjectRef != "journal-9" || req.AmountCents != 600000 {
		t.Fatalf("filed request has wrong shape: %+v", req)
	}
	if len(notifier.requested) != 1 || notifier.requested[0].AmountCents != 600000 {
		t.Fatalf("fan-out notice should carry the amount, got %+v", notifier.requested)
	}
}

// TestApprovalReconcile_SplitsPendingAndDecided pins the reconcile source: PENDING rows get eligible
// approvers resolved; decided rows come back for the stamp/echo backfill.
func TestApprovalReconcile_SplitsPendingAndDecided(t *testing.T) {
	f := &fakeStore{
		candidates: []StaffContext{{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal}},
		reconcile: []ApprovalRequest{
			{ID: "p1", BranchID: trt, Policy: PolicyMargin, Status: ApprovalPending},
			{ID: "d1", BranchID: trt, Policy: PolicyJEThreshold, Status: ApprovalApproved, DecidedBy: "gm"},
		},
	}
	s := NewApprovals(f)
	pending, decided, err := s.ApprovalReconcile(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(pending) != 1 || len(pending[0].ApproverIDs) != 1 {
		t.Fatalf("expected one pending row with its resolved approver, got %+v", pending)
	}
	if len(decided) != 1 || decided[0].Status != string(ApprovalApproved) {
		t.Fatalf("expected one decided row, got %+v", decided)
	}
}
