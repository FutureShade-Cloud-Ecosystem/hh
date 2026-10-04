package main

// qd1f_approvals_rail_test.go holds the rail's real-store reads the sync and
// REST doors lean on (round 4): the requester's role, read where a request is
// filed and where the reconcile sweep rebuilds its cards (a read that returns
// '' leaves the branch Sales manager silently out of every salesperson's
// request), and the tombstone, which every read and stamp of a live request
// must honour.

import (
	"context"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestQD1F_RequesterRoleIsReadAtFilingAndByTheReconcileSweep(t *testing.T) {
	q := newQD1BFixture(t)
	ctx := context.Background()
	repo := identity.NewApprovalRepository(q.pool)
	for _, c := range []struct {
		name, sub, role string
	}{
		{"Inside Sales", testkit.SubInsideSales, "INSIDE_SALES"},
		{"the Sales manager", testkit.SubSalesManager, "SALES_MANAGER"},
		// Two grants (General Manager and Inside Sales): no single role, so no
		// salesperson, so leadership alone.
		{"a two-grant caller", xrSubTwoGrants, ""},
	} {
		user := qd1bUserID(t, q.pool, c.sub)
		filed, err := repo.CreateApproval(ctx, identity.ApprovalRequest{
			RequestedBy: user, BranchID: testkit.BranchNorthAID, Policy: identity.PolicyCredit,
			Detail: "qd1f role read", Status: identity.ApprovalPending})
		if err != nil {
			t.Fatalf("%s files: %v", c.name, err)
		}
		t.Cleanup(func() {
			_, _ = q.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE id = $1`, filed.ID)
		})
		if filed.RequesterRole != c.role {
			t.Errorf("%s: the role read at filing is %q, want %q", c.name, filed.RequesterRole, c.role)
		}
		swept, err := repo.ApprovalsForReconcile(ctx, time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("reconcile read: %v", err)
		}
		found := false
		for _, r := range swept {
			if r.ID == filed.ID {
				found = true
				if r.RequesterRole != c.role {
					t.Errorf("%s: the role the reconcile sweep reads is %q, want %q", c.name, r.RequesterRole, c.role)
				}
			}
		}
		if !found {
			t.Errorf("%s: the reconcile sweep did not list the pending request", c.name)
		}
	}
}

func TestQD1F_ATombstonedRequestIsGoneFromTheSweepTheOverrideAndTheDecisionStamp(t *testing.T) {
	q := newQD1BFixture(t)
	ctx := context.Background()
	repo := identity.NewApprovalRepository(q.pool)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	gm := qd1bUserID(t, q.pool, testkit.SubGeneralMgr)
	file := func() string {
		r, err := repo.CreateApproval(ctx, identity.ApprovalRequest{
			RequestedBy: clerk, BranchID: testkit.BranchNorthAID, Policy: identity.PolicyCredit,
			SubjectRef: "qd1f-subject", Detail: "qd1f tombstone", Status: identity.ApprovalPending})
		if err != nil {
			t.Fatalf("file: %v", err)
		}
		t.Cleanup(func() {
			_, _ = q.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE id = $1`, r.ID)
		})
		return r.ID
	}
	tombstone := func(id string) {
		if _, err := q.pool.Exec(ctx, `UPDATE permission_approval_requests SET deleted_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("tombstone: %v", err)
		}
	}

	// A tombstoned PENDING request is not rebuilt as a card, and no decision
	// stamps it (the decide path's read refuses it, the stamp's own guard
	// closes the race between that read and the write).
	pending := file()
	tombstone(pending)
	swept, err := repo.ApprovalsForReconcile(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("reconcile read: %v", err)
	}
	for _, r := range swept {
		if r.ID == pending {
			t.Errorf("the reconcile sweep lists a tombstoned request")
		}
	}
	if applied, err := repo.SaveDecision(ctx, pending, gm, identity.ApprovalApproved, ""); err != nil || applied {
		t.Errorf("a decision stamped a tombstoned request: applied %v err %v", applied, err)
	}
	pendingTx := file()
	tombstone(pendingTx)
	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	applied, err := repo.SaveDecisionTx(ctx, tx, pendingTx, gm, identity.ApprovalApproved, "")
	_ = tx.Rollback(ctx)
	if err != nil || applied {
		t.Errorf("a transactional decision stamped a tombstoned request: applied %v err %v", applied, err)
	}

	// A tombstoned APPROVED override no longer counts as an override.
	approved := file()
	if applied, err := repo.SaveDecision(ctx, approved, gm, identity.ApprovalApproved, ""); err != nil || !applied {
		t.Fatalf("approve: applied %v err %v", applied, err)
	}
	if ok, err := repo.HasApprovedOverride(ctx, clerk, testkit.BranchNorthAID, "qd1f-subject", identity.PolicyCredit); err != nil || !ok {
		t.Fatalf("the live override is not found: %v %v", ok, err)
	}
	tombstone(approved)
	if ok, err := repo.HasApprovedOverride(ctx, clerk, testkit.BranchNorthAID, "qd1f-subject", identity.PolicyCredit); err != nil || ok {
		t.Errorf("a tombstoned override still counts: %v %v", ok, err)
	}
}
