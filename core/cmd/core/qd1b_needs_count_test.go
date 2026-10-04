package main

// qd1b_needs_count_test.go is the dashboard count's grant leg (row 740,
// item 4): the Home queue's approvals row must count what waits on THIS
// person under the same decide rule the service enforces, which the
// configured floor approvers widen (D11, CP0-1 answer B): a branch Sales
// manager's count includes its own branch's floor requests and nothing
// it cannot decide. The wiring is the composition harness the HH-189
// queue tests ride (dashFold: wireMoney, wireOperations, wireTops over
// the kit's guards on the gate database), so the count reads the same
// configured Approvals the routes serve, and the planted rows ride the
// rail's own doors and are cleaned by id.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1bCountClerk plants the filings' requester (a distinct linked user)
// and answers its ERP users.id.
func qd1bCountClerk(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (email, display_name, platform_user_id, status) VALUES
			('fxstaff.qd1b.clerk@dashboard.invalid', 'QD1B Count Clerk', 'fxstaff-qd1b-count-clerk', 'ACTIVE')
			ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("plant the count clerk: %v", err)
	}
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE platform_user_id = 'fxstaff-qd1b-count-clerk'`).Scan(&id); err != nil {
		t.Fatalf("read the count clerk: %v", err)
	}
	return id
}

// qd1bOtherBranch reads one location row the caller's grant does not
// reach, the other branch leg's fence.
func qd1bOtherBranch(t *testing.T, pool *pgxpool.Pool, branchA string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM locations WHERE id::text <> $1 ORDER BY id LIMIT 1`, branchA).Scan(&id); err != nil {
		t.Fatalf("no second location row for the other branch leg: %v", err)
	}
	return id
}

// qd1bPlantApproval plants one PENDING request through the rail's doors
// (the floor door for margin, the caller door for credit) and deletes it
// when the test ends.
func qd1bPlantApproval(t *testing.T, pool *pgxpool.Pool, rail *staffcontext.Approvals,
	kind staffcontext.PolicyType, requester, branchID, lineIDSuffix string) string {
	t.Helper()
	if kind == staffcontext.PolicyMargin {
		rec, err := rail.FileFloorApproval(context.Background(), staffcontext.FloorApprovalFiling{
			RequestedBy: requester,
			BranchID:    branchID,
			SubjectRef:  "3ef10000-0000-4000-8000-00000000a001",
			Detail:      "qd1b count fixture",
			LineNo:      1,
			DiscPct:     40,
			LineID:      "11111111-1111-4111-8111-" + lineIDSuffix,
			ProductID:   "3ef10000-0000-4000-8000-000000000051",
		})
		if err != nil {
			t.Fatalf("plant the floor request: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(),
				`DELETE FROM permission_approval_requests WHERE id = $1`, rec.ID)
		})
		return rec.ID
	}
	rec, err := rail.CreateApproval(context.Background(), requester, branchID, kind, "qd1b count fixture", "")
	if err != nil {
		t.Fatalf("plant the credit request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, rec.ID)
	})
	return rec.ID
}

// TestQD1B_NeedsApprovalsCountFollowsTheDecideRule is the count's leg:
// under the dealer's decided default the branch Sales manager's queue
// counts its own branch's floor request (its configured branch arm) and
// nothing else: not its own filing (four eyes), not a credit request of
// its branch (a leadership decision), not another branch's floor request
// (the branch fence).
func TestQD1B_NeedsApprovalsCountFollowsTheDecideRule(t *testing.T) {
	pool := wiringPool(t)
	manager, branchA := plantDashboardCaller(t, pool)
	branchB := qd1bOtherBranch(t, pool, branchA)
	clerk := qd1bCountClerk(t, pool)
	rail := staffcontext.NewApprovals(staffcontext.NewApprovalRepository(pool))

	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{
		RoleID:   "sales_manager",
		Tier:     identity.TierManager,
		Scope:    identity.ScopeBranch,
		Branches: []string{branchA},
	})
	mux := dashFold(t, pool, sg)

	// Nothing waits yet: the queue starts honest.
	if n := needsApprovalsCount(t, mux, sg); n >= 0 {
		t.Fatalf("the queue counts %d before anything is planted, want no row", n)
	}

	qd1bPlantApproval(t, pool, rail, staffcontext.PolicyCredit, clerk, branchA, "000000000d03")
	qd1bPlantApproval(t, pool, rail, staffcontext.PolicyMargin, clerk, branchB, "000000000d04")
	qd1bPlantApproval(t, pool, rail, staffcontext.PolicyMargin, manager, branchA, "000000000d05")
	qd1bPlantApproval(t, pool, rail, staffcontext.PolicyMargin, clerk, branchA, "000000000d06")

	// Exactly the branch's own floor request waits on the Sales manager.
	if n := needsApprovalsCount(t, mux, sg); n != 1 {
		t.Fatalf("the queue counts %d approvals waiting on the branch Sales manager, want 1: its own branch's floor request alone (the credit request is a leadership decision, the other branch's request is out of reach, its own filing is its own ask)",
			n)
	}
}
