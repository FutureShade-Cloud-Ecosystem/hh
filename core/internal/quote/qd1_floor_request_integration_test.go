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
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQD1_RequestDoor_FilesBoundRequest(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

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
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.RequestFloorApproval(ctx, quoteID, 1, 10, ""); err == nil {
		t.Fatal("a line not below the floor must be refused: nothing to approve")
	}
}

// The refusal body carries the caller's pending request when one exists.
func TestQD1_RefusalCarriesPendingRequest(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	lineID, prodID := qd1LineBinding(t, pool, quoteID, 1)
	filed, err := rail.FileFloorApproval(context.Background(), identity.FloorApprovalFiling{
		RequestedBy: seller, BranchID: intTrenton, SubjectRef: quoteID,
		Detail: "qd1: pending", LineNo: 1, DiscPct: 25, LineID: lineID, ProductID: prodID,
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

// qd1LineBinding reads one line's own row id and product, the figures the
// request is bound to at filing.
func qd1LineBinding(t *testing.T, pool *pgxpool.Pool, quoteID string, lineNo int) (lineID, productID string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text, COALESCE(product_id::text,'') FROM quote_lines WHERE quote_id = $1 AND line_no = $2`,
		quoteID, lineNo).Scan(&lineID, &productID); err != nil {
		t.Fatalf("read line binding: %v", err)
	}
	return lineID, productID
}

// P1-2: an approval is bound to the line's OWN identity, not its number.
// Line numbers are reused (a new line takes MAX(line_no)+1), so a request
// bound to a number can clear a line that did not exist when it was filed.
// The review's probe: file and approve line 1 at 40 percent, delete line 1,
// add a new line, set 40 percent on it. The new line must be refused: the
// approval belongs to a row that is gone.
func TestQD1_RequestBoundToTheLinesOwnIdentityNotItsNumber(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	// File through the request door for line 1 at 40 percent and approve it.
	ans, err := svc.RequestFloorApproval(ctx, quoteID, 1, 40, "qd1 probe")
	if err != nil {
		t.Fatalf("the request door: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, ans.Request.ID)
	})
	decider := identity.StaffContext{UserID: qd1Approver(t, pool), Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, RoleCode: "GENERAL_MANAGER"}
	if err := rail.DecideApproval(context.Background(), ans.Request.ID, decider, true, "qd1"); err != nil {
		t.Fatalf("decide the request: %v", err)
	}

	// Delete line 1 and add a new line: it takes line_no 1 back (MAX+1).
	if _, err := svc.RemoveLine(ctx, quoteID, 1); err != nil {
		t.Fatalf("remove line 1: %v", err)
	}
	if _, err := svc.AddLine(ctx, quoteID, prod, "qd1 probe line", "50", uom); err != nil {
		t.Fatalf("add the new line: %v", err)
	}
	newLineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	if newLineID == "" {
		t.Fatal("the new line must exist at line_no 1")
	}

	// The same 40 percent on the new line: the approved request belongs to
	// a different row and must not clear it.
	_, err = svc.SetLineDiscount(ctx, quoteID, 1, 40)
	ref, ok := err.(*FloorRefusalError)
	if !ok {
		t.Fatalf("the reused line number must be refused the old approval, got %T: %v", err, err)
	}
	if ref.Request != nil {
		t.Errorf("the refusal must not offer the dead line's request as standing, got %+v", ref.Request)
	}
	// And the refused write must not have used the approval up.
	var used any
	if err := pool.QueryRow(context.Background(),
		`SELECT data->>'used' FROM permission_approval_requests WHERE id = $1`, ans.Request.ID).Scan(&used); err != nil {
		t.Fatalf("read the request's used state: %v", err)
	}
	if used != nil {
		t.Errorf("a refused write must not use the request up, found used=%v", used)
	}
}

// FloorRequestFor must not hide an older APPROVED request behind a newer
// PENDING one: the caller asked twice (a pending twin filed beside an
// approved request), and the newer pending row used to win the lookup, so
// a request that had already been decided sat unusable behind it.
func TestQD1_NewerPendingDoesNotHideOlderApproved(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	// The approved request, then a newer pending twin on the same binding.
	approved := qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, mustLineID(t, pool, quoteID), prod, 1, 40)
	pending, err := rail.FileFloorApproval(context.Background(), identity.FloorApprovalFiling{
		RequestedBy: seller, BranchID: intTrenton, SubjectRef: quoteID,
		Detail: "qd1: the newer pending twin", LineNo: 1, DiscPct: 40,
		LineID: mustLineID(t, pool, quoteID), ProductID: prod,
	})
	if err != nil {
		t.Fatalf("file the pending twin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, pending.ID)
	})

	// The write must ride the APPROVED request, not be refused behind the
	// newer pending one.
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 25); err != nil {
		t.Fatalf("an approved request must not be hidden behind a newer pending twin: %v", err)
	}
	var usedOn any
	if err := pool.QueryRow(context.Background(),
		`SELECT data->>'used_by_disc_pct' FROM permission_approval_requests WHERE id = $1`, approved.ID).
		Scan(&usedOn); err != nil {
		t.Fatalf("read the approved request's used state: %v", err)
	}
	if usedOn == nil {
		t.Error("the write must have consumed the APPROVED request")
	}
}

// mustLineID reads one line's own row id, the strict form the twin test
// wants (a wrong id fails loudly, it never files unbound).
func mustLineID(t *testing.T, pool *pgxpool.Pool, quoteID string) string {
	t.Helper()
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	if lineID == "" {
		t.Fatal("the line must exist")
	}
	return lineID
}

// The request lookup's line binding, pinned directly against the store: a
// request bound to one line's own row answers only that row and that
// product. Dropping the line_id or the product_id predicate from
// FloorRequestFor lets a dead line's request answer a new line that reuses
// the number, so the request door would answer it instead of filing, and
// this test goes red.
func TestQD1_FloorRequestForHoldsTheLineBinding(t *testing.T) {
	pool := intPool(t)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	filed := qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	look := func(lineNo int, line, product string) bool {
		t.Helper()
		_, ok, err := rail.FloorRequestFor(context.Background(), seller, intTrenton, quoteID, lineNo, line, product)
		if err != nil {
			t.Fatalf("FloorRequestFor: %v", err)
		}
		return ok
	}
	if !look(1, lineID, prod) {
		t.Fatalf("the request %s must answer its own row and product", filed.ID)
	}
	const otherRow = "99999999-9999-4999-8999-999999999999"
	if look(1, otherRow, prod) {
		t.Error("a request bound to one row must not answer another row with the same number and product")
	}
	if look(1, lineID, otherRow) {
		t.Error("a request bound to one product must not answer the same row with another product")
	}
}

// A tombstoned request does not stand as the line's open request: the door's
// existing-request check must not answer the dead row's state, it files a
// fresh request in its place.
func TestQD1_RequestDoor_TombstonedRequestIsNotTheOpenOne(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	first, err := svc.RequestFloorApproval(ctx, quoteID, 1, 25, "")
	if err != nil || !first.Filed || first.Request == nil {
		t.Fatalf("the first request must be filed, got %+v err %v", first, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE subject_ref = $1`, quoteID)
	})
	// The live request stands: asking again answers its state, files nothing.
	again, err := svc.RequestFloorApproval(ctx, quoteID, 1, 25, "")
	if err != nil || again.Filed || again.Request == nil || again.Request.ID != first.Request.ID {
		t.Fatalf("a live request must be answered, not twinned, got %+v err %v", again, err)
	}

	if _, err := pool.Exec(context.Background(),
		`UPDATE permission_approval_requests SET deleted_at = to_timestamp(0) WHERE id = $1`, first.Request.ID); err != nil {
		t.Fatalf("tombstone the request: %v", err)
	}
	fresh, err := svc.RequestFloorApproval(ctx, quoteID, 1, 25, "")
	if err != nil {
		t.Fatalf("the door after the tombstone: %v", err)
	}
	if !fresh.Filed || fresh.Request == nil || fresh.Request.ID == first.Request.ID {
		t.Fatalf("a tombstoned request must not satisfy the existing-request check: want a fresh filing, got %+v", fresh)
	}
}
