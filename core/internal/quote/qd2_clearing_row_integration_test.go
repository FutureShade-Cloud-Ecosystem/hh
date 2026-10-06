//go:build integration

// qd2_clearing_row_integration_test.go pins when the BYPASS row is owed
// (plan 4.2, "a below floor line that was cleared is a BYPASS row"): the
// row belongs to the CLEARING, not to the line moving. A write that stamps
// a new clearing (or uses up an approval) on a line that sits below the
// floor writes the row even when the discount and the figures stand
// unchanged, and a repeat of that write under the now standing clearing
// writes no second row. The suite runs the REAL pricing arbiter composed as
// production wires it, on the seeded database: a 40 percent discount on the
// 1000 cent line is margin 1666 bps against the seeded 2000 floor.
package quote

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// The Sales manager sets the discount the line already carries, on a line
// whose standing discount no clearing covers (cost rose or the floor was
// raised): the MANAGER_SCOPE clearing is stamped and the BYPASS row is
// written though the line does not move. The same write again adds no row.
func TestQD2_ClearingRow_ManagerSetsTheSameDiscount(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd1SetDisc(t, pool, quoteID, 1, 40)
	qd2CleanAudit(t, pool, quoteID)

	svc := qd1RealFloorSvc(t, pool, now)
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "SALES_MANAGER", intTrenton, mgr)

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 40); err != nil {
		t.Fatalf("the Sales manager clears a below floor price in its branch: %v", err)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c == nil || c["authority"] != "MANAGER_SCOPE" {
		t.Fatalf("the manager's write must stamp MANAGER_SCOPE, got %v", c)
	}
	rows := qd1FloorLogRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0] != "BYPASS net 600 margin 1666 floor 2000" {
		t.Fatalf("a new clearing owes one BYPASS row though the line did not move, got %v", rows)
	}

	// The repeat rides the standing clearing: no second row.
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 40); err != nil {
		t.Fatalf("the repeat under the standing clearing: %v", err)
	}
	if rows := qd1FloorLogRows(t, pool, quoteID); len(rows) != 1 {
		t.Fatalf("a repeat under the standing clearing must add no row, got %v", rows)
	}
}

// Inside Sales re-asserts the discount the line already carries with an
// approved request in hand: the approval is used up, the APPROVAL clearing
// is stamped and the BYPASS row is written though the line does not move.
func TestQD2_ClearingRow_ApprovedRequestReassertsTheSameDiscount(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd1SetDisc(t, pool, quoteID, 1, 40)
	qd2CleanAudit(t, pool, quoteID)

	svc := qd1RealFloorSvc(t, pool, now)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "INSIDE_SALES", intTrenton, seller)

	ans, err := svc.RequestFloorApproval(ctx, quoteID, 1, 40, "qd2 clearing row")
	if err != nil {
		t.Fatalf("file the request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, ans.Request.ID)
	})
	decider := identity.StaffContext{UserID: qd1Approver(t, pool), Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, RoleCode: "GENERAL_MANAGER"}
	if err := rail.DecideApproval(context.Background(), ans.Request.ID, decider, true, "qd2"); err != nil {
		t.Fatalf("decide the request: %v", err)
	}

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 40); err != nil {
		t.Fatalf("the approved request must carry the write: %v", err)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c == nil || c["authority"] != "APPROVAL" {
		t.Fatalf("the used approval must stamp APPROVAL, got %v", c)
	}
	rows := qd1FloorLogRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0] != "BYPASS net 600 margin 1666 floor 2000" {
		t.Fatalf("a used up approval owes one BYPASS row though the line did not move, got %v", rows)
	}

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 40); err != nil {
		t.Fatalf("the repeat under the standing clearing: %v", err)
	}
	if rows := qd1FloorLogRows(t, pool, quoteID); len(rows) != 1 {
		t.Fatalf("a repeat under the standing clearing must add no row, got %v", rows)
	}
}

// The Sales manager reprices a below floor discounted line at the same
// figures (the quantity it already carries): the clearing is stamped and
// the BYPASS row is written though the line is identical before and after.
func TestQD2_ClearingRow_ManagerRepricesAtTheSameFigures(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd1SetDisc(t, pool, quoteID, 1, 40)
	qd2CleanAudit(t, pool, quoteID)

	// The pricer answers the line's own unit of measure at the 1000 cent rule
	// price; the arbiter stays the real one.
	svc := qd1RealFloorSvc(t, pool, now).WithPricing(qd2Pricer{})
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "SALES_MANAGER", intTrenton, mgr)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "2"); err != nil {
		t.Fatalf("the Sales manager clears the reprice in its branch: %v", err)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c == nil || c["authority"] != "MANAGER_SCOPE" {
		t.Fatalf("the manager's reprice must stamp MANAGER_SCOPE, got %v", c)
	}
	rows := qd1FloorLogRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0] != "BYPASS net 600 margin 1666 floor 2000" {
		t.Fatalf("a new clearing owes one BYPASS row though the line did not move, got %v", rows)
	}

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "2"); err != nil {
		t.Fatalf("the repeat under the standing clearing: %v", err)
	}
	if rows := qd1FloorLogRows(t, pool, quoteID); len(rows) != 1 {
		t.Fatalf("a repeat under the standing clearing must add no row, got %v", rows)
	}
}
