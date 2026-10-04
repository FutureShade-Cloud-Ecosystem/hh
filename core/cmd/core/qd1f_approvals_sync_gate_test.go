package main

// qd1f_approvals_sync_gate_test.go holds the approvals sync door to the REST
// routes' role gate (the round 4 audit): POST /api/v1/staff/approvals needs
// Work on the counter, the Quote Desk or Manager Home, and the decide route
// needs Manage on Manager Home or the Quote Desk, before the service runs.
// The sync door carried neither wall (any org staff member filed, and HR, a
// leadership tier role the decide route refuses, decided a credit request).
// Each leg drives the REST route and the sync door as the same principal and
// requires the same admission.

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

var qd1fPrincipals = []string{
	testkit.SubInsideSales, testkit.SubSalesManager, testkit.SubGeneralMgr,
	xrSubFinancialController, xrSubBranchManager, xrSubProcurementManager, xrSubYardTeam, xrSubHR,
}

func TestQD1F_ApprovalsSyncCreateHoldsTheRestRoleGate(t *testing.T) {
	q := newQD1BFixture(t)
	for _, sub := range qd1fPrincipals {
		code, body := qd1bCall(t, q, http.MethodPost, "/api/v1/staff/approvals", sub, map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "qd1f parity filing"})
		if code != http.StatusCreated && code != http.StatusForbidden {
			t.Fatalf("REST filing as %s: %d %s", sub, code, body)
		}
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE detail = 'qd1f parity filing'`)
		key := uuid.NewString()
		res := qd1bPush(t, q, sub, qd1bPushOp{ClientID: key, Op: "create", Payload: map[string]any{
			"branch_id": testkit.BranchNorthAID, "policy_type": "CREDIT_LIMIT_EXCEEDED", "detail": "qd1f parity filing"}})
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE idempotency_key = $1`, key)
		restAdmits := code == http.StatusCreated
		if syncAdmits := res.Status == "applied"; syncAdmits != restAdmits {
			t.Errorf("%s: REST filing answered %d but the sync door answered %q", sub, code, res.Status)
		}
	}
}

func TestQD1F_ApprovalsSyncDecideRefusesWhatTheRestRouteGateRefuses(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	rail := identity.NewApprovals(identity.NewApprovalRepository(q.pool))
	plant := func() string {
		req, err := rail.CreateApproval(context.Background(), clerk, testkit.BranchNorthAID,
			identity.PolicyCredit, "qd1f credit request", "")
		if err != nil {
			t.Fatalf("plant a credit request: %v", err)
		}
		t.Cleanup(func() {
			_, _ = q.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE id = $1`, req.ID)
		})
		return req.ID
	}
	// HR is leadership tier (the carried credit rule admits it) but holds no
	// Manage on Manager Home or the Quote Desk: the route is a 403 for it.
	id := plant()
	code, _ := qd1bCall(t, q, http.MethodPost, qd1bDecidePath(id), xrSubHR, map[string]any{"approve": true})
	if code != http.StatusForbidden {
		t.Fatalf("REST decide as HR answered %d, want 403", code)
	}
	res := qd1bPush(t, q, xrSubHR, qd1bPushOp{ClientID: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": id, "status": "APPROVED"}})
	qd1bRejected(t, "HR decides a credit request through the sync door", res, "")
	var status string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT status FROM permission_approval_requests WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read the request back: %v", err)
	}
	if status != "PENDING" {
		t.Errorf("HR decided the request through the sync door: status %s", status)
	}
	// A role the route admits still decides from the device.
	res = qd1bPush(t, q, testkit.SubGeneralMgr, qd1bPushOp{ClientID: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": id, "status": "APPROVED"}})
	qd1bApplied(t, "the General Manager decides a credit request through the sync door", res)
}
