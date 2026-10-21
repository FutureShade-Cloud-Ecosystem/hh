//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock leg for permission_approval_requests
// (PR 556, round 3c). The decide used to draw the approvals counter in a CTE and then lock the request row; a
// writer that relies on the sync seq trigger takes the row first. The decide now relies on the trigger too. The
// trigger relying writer (a plain touch) is parked between its row lock and its draw with a BEFORE INSERT
// trigger on scope_sequences (testkit.DrawPark); the real decide runs on the same request. Red at 099f2e27:
// the decide draws the counter, waits for the row, the park releases, the touch wants the counter, 40P01.
package sync_test

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_DrawBeforeLock_ApprovalDecideBesideARequestWrite(t *testing.T) {
	f := newGapFixture(t)
	repo := identity.NewApprovalRepository(f.pool)
	req, err := repo.CreateApproval(context.Background(), identity.ApprovalRequest{
		RequestedBy: testkit.UserInsideSalesID, BranchID: testkit.BranchNorthAID,
		Policy: identity.PolicyCredit, Detail: "draw before lock fixture",
	})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	park := testkit.NewDrawPark(t, f.pool, "approvals", 55010004)
	touch := park.TouchRow("permission_approval_requests", "detail = detail || '.'", req.ID)
	park.WaitParked("the request touch stands between its row lock and its draw")

	decide := make(chan error, 1)
	go func() {
		_, err := repo.SaveDecision(context.Background(), req.ID, testkit.UserGeneralMgrID, identity.ApprovalRejected, "no")
		decide <- err
	}()
	park.WaitBlocked("the decide waits")

	park.Answers(map[string]chan error{"touch": touch, "decide": decide})
}
