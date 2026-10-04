//go:build integration

// qd1_floor_request_integration_test.go, the request door (plan 1.1): for a
// line the caller may edit whose asked discount lands below the floor, it
// files one approval request bound to that quote, that line and that
// discount. The server builds the subject and the detail from its own
// figures; the caller supplies neither. A caller who may clear the floor
// itself is told so and no request is filed. A line not below the floor is
// a 409: nothing to approve.
package quote

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestQD1_RequestDoor_FilesBoundRequest(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alice := qd1User(t, pool, "alice")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", alice, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, alice)

	ans, err := svc.RequestFloorApproval(ctx, quoteID, 1, 25, "customer pushed hard")
	if err != nil {
		t.Fatalf("the request door: %v", err)
	}
	if !ans.Filed || ans.Request == nil {
		t.Fatalf("the request must be filed and answered with its state, got %+v", ans)
	}
	if ans.Request.Status != "PENDING" || ans.Request.LineNo != 1 || ans.Request.DiscPct != 25 {
		t.Errorf("the answer must carry the request's state, got %+v", ans.Request)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, ans.Request.ID)
	})

	// The binding: exactly that quote, that line, that discount, and the
	// detail the server built from its own figures.
	var subject, detail, branchID string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(subject_ref,''), detail, branch_id::text
		FROM permission_approval_requests WHERE id = $1`, ans.Request.ID).
		Scan(&subject, &detail, &branchID); err != nil {
		t.Fatalf("read the filed request: %v", err)
	}
	if subject != quoteID {
		t.Errorf("the subject must be the quote id, got %q", subject)
	}
	if branchID != intTrenton {
		t.Errorf("the request must be filed in the quote's branch, got %q", branchID)
	}
	for _, want := range []string{"25", "margin", "floor"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the server built detail must carry %q, got %q", want, detail)
		}
	}
	if strings.Contains(detail, "customer pushed hard") {
		// The note may ride the request's data, never the detail the approver
		// reads as the server's own figures.
		t.Error("the caller's note must not be the approver's detail")
	}
}

func TestQD1_RequestDoor_CallerWhoMayClearToldSo(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "BRANCH_MANAGER", intTrenton, bm)

	ans, err := svc.RequestFloorApproval(ctx, quoteID, 1, 25, "")
	if err != nil {
		t.Fatalf("the request door: %v", err)
	}
	if ans.Filed || ans.Request != nil {
		t.Fatalf("a caller who may clear the floor itself must be told so with no request filed, got %+v", ans)
	}
	if !ans.MayClearItself {
		t.Error("the answer must say the caller may clear it itself")
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM permission_approval_requests WHERE subject_ref = $1`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if n != 0 {
		t.Errorf("no request must be filed for a caller who may clear it itself, found %d", n)
	}
}

func TestQD1_RequestDoor_NotBelowFloorIs409(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alice := qd1User(t, pool, "alice")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", alice, true)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, alice)

	if _, err := svc.RequestFloorApproval(ctx, quoteID, 1, 10, ""); err == nil {
		t.Fatal("a line not below the floor must be refused: nothing to approve")
	}
}

// The refusal body carries the caller's pending request when one exists.
func TestQD1_RefusalCarriesPendingRequest(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alice := qd1User(t, pool, "alice")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", alice, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, alice)

	filed, err := rail.FileFloorApproval(context.Background(), identity.FloorApprovalFiling{
		RequestedBy: alice, BranchID: intTrenton, SubjectRef: quoteID,
		Detail: "qd1: pending", LineNo: 1, DiscPct: 25,
	})
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, filed.ID)
	})

	_, err = svc.SetLineDiscount(ctx, quoteID, 1, 25)
	ref, ok := err.(*FloorRefusalError)
	if !ok {
		t.Fatalf("want the typed refusal, got %T: %v", err, err)
	}
	if ref.Request == nil || ref.Request.ID != filed.ID || ref.Request.Status != "PENDING" {
		t.Errorf("the refusal must carry the caller's pending request, got %+v", ref.Request)
	}
}
