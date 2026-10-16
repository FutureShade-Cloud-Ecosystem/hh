package main

// qd1b_filed_pending_test.go is the dashboard count's "approvals you filed" leg (2D gap 12): the
// logistics manager's Needs-you row counts approvals the caller filed that are still pending, scoped
// to the branches the caller reaches, and never exposes another person's request or its content.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// needsFiledPendingCount reads the Needs queue and returns the filed-pending row's count, or -1
// when the row is absent (nothing waiting).
func needsFiledPendingCount(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards) int {
	t.Helper()
	rec := driveJSON(t, mux, sg, "GET", "/api/v1/dashboard/needs", nil)
	if rec.Code != 200 {
		t.Fatalf("needs queue = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var queue struct {
		Rows []struct {
			SourceKey string `json:"source_key"`
			Count     int    `json:"count"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &queue); err != nil {
		t.Fatalf("decode needs queue: %v", err)
	}
	for _, row := range queue.Rows {
		if row.SourceKey == dashboard.NeedsFiledPending {
			return row.Count
		}
	}
	return -1
}

// qd1bLogisticsMgr sets the staff guard to an active logistics manager scoped to one branch.
func qd1bLogisticsMgr(sg *testkit.StaffGuards, branchID string) {
	sg.SetAuthority(identity.StaffAuthority{
		RoleID:   "logistics_manager",
		Tier:     identity.TierManager,
		Scope:    identity.ScopeBranch,
		Branches: []string{branchID},
	})
}

// qd1bOtherUser plants a second linked staff user and returns its ERP users.id. No role grant is
// needed: the filed-pending adapter counts only by requester user id, and the rail fills an empty
// requester_role when none is configured.
func qd1bOtherUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, display_name, platform_user_id, status)
		VALUES ('fxother.qd1b@dashboard.invalid', 'QD1B Other', 'fxstaff-qd1b-other', 'ACTIVE')
		ON CONFLICT (platform_user_id) WHERE platform_user_id IS NOT NULL DO UPDATE SET status = 'ACTIVE'
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("plant the other user: %v", err)
	}
	return id
}

// qd1bPlantFreightApproval plants one PENDING FREIGHT_ABOVE_QUOTE request filed by the named user
// and deletes it when the test ends.
func qd1bPlantFreightApproval(t *testing.T, pool *pgxpool.Pool, rail *staffcontext.Approvals, requester, branchID string) string {
	t.Helper()
	rec, err := rail.FileFreightApproval(context.Background(), "raise-qd1b-"+branchID[:8], branchID, requester,
		"qd1b filed pending fixture")
	if err != nil {
		t.Fatalf("plant the freight request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, rec.ID)
	})
	return rec.ID
}

// TestQD1B_NeedsFiledPendingCountsOnlyTheCallersOwnPendingRequests: the logistics manager's row
// counts what they filed and is still pending, excludes another person's filing, excludes a request
// outside their branch reach, and disappears once the request is decided.
func TestQD1B_NeedsFiledPendingCountsOnlyTheCallersOwnPendingRequests(t *testing.T) {
	pool := wiringPool(t)
	manager, branchA := plantDashboardCaller(t, pool)
	branchB := qd1bOtherBranch(t, pool, branchA)
	other := qd1bOtherUser(t, pool)
	rail := staffcontext.NewApprovals(staffcontext.NewApprovalRepository(pool))

	sg := testkit.NewStaffGuards(t)
	qd1bLogisticsMgr(sg, branchA)
	mux := dashFold(t, pool, sg)

	// Nothing filed yet: the row is absent rather than a zero.
	if n := needsFiledPendingCount(t, mux, sg); n >= 0 {
		t.Fatalf("the queue counts %d filed-pending before anything is planted, want no row", n)
	}

	// The logistics manager files their own freight raise in their own branch.
	ownID := qd1bPlantFreightApproval(t, pool, rail, manager, branchA)
	if n := needsFiledPendingCount(t, mux, sg); n != 1 {
		t.Fatalf("the queue counts %d filed-pending after one own filing, want 1", n)
	}

	// Another person's pending filing in the same branch does not count.
	qd1bPlantFreightApproval(t, pool, rail, other, branchA)
	if n := needsFiledPendingCount(t, mux, sg); n != 1 {
		t.Fatalf("the queue counts %d filed-pending, want 1: another person's request must not count", n)
	}

	// A filing by the caller in a branch they do not reach does not count.
	qd1bPlantFreightApproval(t, pool, rail, manager, branchB)
	if n := needsFiledPendingCount(t, mux, sg); n != 1 {
		t.Fatalf("the queue counts %d filed-pending, want 1: a filing outside the caller's reach must not count", n)
	}

	// Deciding the caller's own request removes it from the count.
	_, err := pool.Exec(context.Background(),
		`UPDATE permission_approval_requests SET status = 'APPROVED', decided_by = $1, decided_at = now() WHERE id = $2`,
		other, ownID)
	if err != nil {
		t.Fatalf("decide the own request: %v", err)
	}
	if n := needsFiledPendingCount(t, mux, sg); n >= 0 {
		t.Fatalf("the queue counts %d filed-pending after the own request was decided, want no row", n)
	}
}
