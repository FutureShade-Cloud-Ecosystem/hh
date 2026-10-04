//go:build integration

// qd1_floor_consume_integration_test.go pins the rules on USING an approved
// floor request (the review's P2-7): the used up check, the discount depth
// check and the line binding inside ConsumeApprovedFloorTx, an eight way
// race on one request (exactly one winner), and ledger row 10's "another
// line" half. Each rule is pinned directly against the store, so dropping
// the predicate inside the query turns this file red by itself.
package quote

import (
	"context"
	"errors"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd1FileAndApprove files one floor request for a line at discPct and has
// the General Manager approve it. It returns the filed record.
func qd1FileAndApprove(t *testing.T, pool *pgxpool.Pool, requester, branchID, quoteID, lineID, productID string, lineNo, discPct int) identity.FloorRequestRecord {
	t.Helper()
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	filed, err := rail.FileFloorApproval(context.Background(), identity.FloorApprovalFiling{
		RequestedBy: requester, BranchID: branchID, SubjectRef: quoteID,
		Detail: "qd1: consumption pin", LineNo: lineNo, DiscPct: discPct,
		LineID: lineID, ProductID: productID,
	})
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, filed.ID)
	})
	decider := identity.StaffContext{UserID: qd1Approver(t, pool), Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, RoleCode: "GENERAL_MANAGER"}
	if err := rail.DecideApproval(context.Background(), filed.ID, decider, true, "qd1"); err != nil {
		t.Fatalf("decide: %v", err)
	}
	return filed
}

// qd1Consume consumes one request on its own transaction, the store's
// direct door, so a rule inside the query is pinned without the write
// path's ladders in front of it.
func qd1Consume(t *testing.T, pool *pgxpool.Pool, requester, branchID, quoteID string, lineNo, discPct int, lineID, productID string) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, c, err := identity.NewApprovals(identity.NewApprovalRepository(pool)).
		ConsumeApprovedFloorTx(ctx, tx, requester, branchID, quoteID, lineNo, discPct, lineID, productID)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return c.ApprovalID != ""
}

// - the three rules inside the consumption query -

// The used up check: the first consumption takes the request, the second
// finds nothing. Dropping the unused predicate from the query lets the
// second consumption through, and this test goes red.
func TestQD1_ConsumePin_UsedUp(t *testing.T) {
	pool := intPool(t)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)

	if !qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 25, lineID, prod) {
		t.Fatal("the first consumption must take the approved request")
	}
	if qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 25, lineID, prod) {
		t.Fatal("a used request must not be consumed a second time")
	}
}

// The discount depth: a request filed at 25 percent covers no deeper a
// write. Dropping the disc_pct predicate lets a 40 percent write ride a
// 25 percent request, and this test goes red.
func TestQD1_ConsumePin_DiscountDepth(t *testing.T) {
	pool := intPool(t)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 25)

	if qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 40, lineID, prod) {
		t.Fatal("a deeper discount than the request was filed at must not consume it")
	}
	// A shallower write is covered.
	if !qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 20, lineID, prod) {
		t.Fatal("a shallower discount than the request was filed at is covered")
	}
}

// The line binding: a request bound to one line's own row answers no
// other row, even the same line number on another quote and not even the
// same number with a different product. Dropping the line_id or
// product_id predicate lets it, and this test goes red.
func TestQD1_ConsumePin_LineBinding(t *testing.T) {
	pool := intPool(t)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	_, otherQuote, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	otherLineID, _ := qd1LineBinding(t, pool, otherQuote, 1)
	qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)

	// Another quote's line 1: same number, different row.
	if qd1Consume(t, pool, seller, intTrenton, otherQuote, 1, 25, otherLineID, prod) {
		t.Fatal("a request bound to one line must not consume against another quote's line")
	}
	// The same row named with another product: the product binds too.
	if qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 25, lineID, "00000000-0000-4000-8000-00000000ffff") {
		t.Fatal("a request bound to one line and product must not consume under another product id")
	}
	// The bound row itself consumes.
	if !qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 25, lineID, prod) {
		t.Fatal("the bound row consumes")
	}
}

// The line binding against a REUSED number: the bound row is deleted and a
// new row takes its line_no with the same product, and the consumption is
// asked for the new row. Subject, number, depth and product all match;
// only the row identity differs, and that alone must refuse.
func TestQD1_ConsumePin_ReusedLineNumber(t *testing.T) {
	pool := intPool(t)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)

	// The bound row goes away and a new row takes line_no 1 back.
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM quote_lines WHERE quote_id = $1 AND line_no = 1`, quoteID); err != nil {
		t.Fatalf("delete the bound row: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'qd1 replacement line', 50, $3, 50, 1000, $3, 50000)`,
		quoteID, prod, uom); err != nil {
		t.Fatalf("seed the replacement row: %v", err)
	}
	newLineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	if newLineID == lineID {
		t.Fatal("the replacement row must be a different row")
	}

	if qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 25, newLineID, prod) {
		t.Fatal("a request bound to a deleted row must not consume against the row that reused its number")
	}
}

// - the eight way race -

// TestQD1_ConsumePin_EightWayRaceOneWinner: eight writers race the same
// approved request on the same line. Exactly one write lands, seven answer
// the typed floor refusal, and the request carries exactly one used stamp
// and the pricing log exactly one BYPASS row.
func TestQD1_ConsumePin_EightWayRaceOneWinner(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	filed := qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	// A straggler whose decision runs AFTER the winner commits may ride
	// the standing clearing the winner stamped (plan 3.3: a later write no
	// deeper and no lower needs no new clearing), so a nil-error answer is
	// not by itself the consumption. What the race must guarantee: at least
	// one write lands, every refused writer gets the TYPED floor refusal,
	// and the request is consumed EXACTLY once, one used stamp and one
	// BYPASS row, however many writers there were.
	const writers = 8
	start := make(chan struct{})
	results := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func() {
			<-start
			_, err := svc.SetLineDiscount(ctx, quoteID, 1, 25)
			results <- err
		}()
	}
	close(start)
	var wins, refusals, other int
	for i := 0; i < writers; i++ {
		err := <-results
		switch {
		case err == nil:
			wins++
		case errors.As(err, new(*FloorRefusalError)):
			refusals++
			// The loser's refusal is the typed one in full: the approvers
			// are named, so the loser knows who to ask (plan 3.4).
			var ref *FloorRefusalError
			if errors.As(err, &ref) && len(ref.Approvers) == 0 {
				t.Error("a race loser's refusal must list the approvers, got the bare policy refusal")
			}
		default:
			other++
			t.Errorf("unexpected error from a race writer: %T: %v", err, err)
		}
	}
	if wins < 1 {
		t.Fatalf("the race must land at least one write, got %d wins, %d refusals, %d other", wins, refusals, other)
	}
	if wins+refusals+other != writers {
		t.Fatalf("every writer must answer, counted %d+%d+%d of %d", wins, refusals, other, writers)
	}

	// Exactly one used stamp on the request, exactly one BYPASS row on the
	// log: the compare-and-swap on the unused state makes the request
	// usable once however many writers reached for it.
	var used int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM permission_approval_requests WHERE id = $1 AND (data->>'used')::boolean`,
		filed.ID).Scan(&used); err != nil {
		t.Fatalf("read used stamps: %v", err)
	}
	if used != 1 {
		t.Fatalf("the request must carry exactly one used stamp, got %d", used)
	}
	rows := qd1BypassRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0] != "BYPASS" {
		t.Fatalf("the pricing log must carry exactly one BYPASS row for the winner, got %v", rows)
	}
	// The standing-clearing ride is bounded by the clearing the winner
	// stamped: the line's discount is the asked 25 percent and the line
	// total follows it.
	if got := liveLine(t, pool, quoteID, 1); got.LineTotalCents != 1500 {
		t.Errorf("the line must carry the discounted total 1500, got %d", got.LineTotalCents)
	}
}

// - ledger row 10, the "another line" half -

// TestQD1_Row10_AnotherLine: an approval covers one line. The same
// discount offered on a DIFFERENT line of the same quote, before or after
// the first use, is refused.
func TestQD1_Row10_AnotherLine(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	cust, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	// A second line on the quote.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 2, $2::uuid, 'qd1 second line', 2, $3, 2, 1000, $3, 2000)`,
		quoteID, prod, uom); err != nil {
		t.Fatalf("seed second line: %v", err)
	}
	_ = cust
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	// Line 2 is refused BEFORE the request is used: the approval covers
	// line 1 alone.
	_, err := svc.SetLineDiscount(ctx, quoteID, 2, 25)
	if !errors.As(err, new(*FloorRefusalError)) {
		t.Fatalf("row 10: another line must be refused the approval before its first use, got %T: %v", err, err)
	}
	// Line 1 consumes it, and line 2 is still refused after.
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 25); err != nil {
		t.Fatalf("line 1 must consume the approval: %v", err)
	}
	if _, err := svc.SetLineDiscount(ctx, quoteID, 2, 25); !errors.As(err, new(*FloorRefusalError)) {
		t.Fatalf("row 10: another line must be refused after the approval is used up, got %T: %v", err, err)
	}
	// And line 2 was never touched by any of it.
	if got := liveLine(t, pool, quoteID, 2); got.LineTotalCents != 2000 {
		t.Errorf("a refused line must be untouched, total now %d", got.LineTotalCents)
	}
}

// A tombstoned request is gone from every read and write of the rail
// (round 3, P3): not listed, not decidable, not consumable, not the line's
// open request. The approvals sync Delete tombstones any card, so the reads
// must honour deleted_at where the sync reads already do.
func TestQD1_TombstonedFloorRequestIsGoneFromTheRail(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))

	approved := qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)
	pending, err := rail.FileFloorApproval(ctx, identity.FloorApprovalFiling{
		RequestedBy: seller, BranchID: intTrenton, SubjectRef: quoteID, Detail: "qd1: tombstone pin",
		LineNo: 2, DiscPct: 40, LineID: lineID, ProductID: prod,
	})
	if err != nil {
		t.Fatalf("file the pending request: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM permission_approval_requests WHERE id = $1`, pending.ID)
	})
	gm := identity.StaffContext{UserID: qd1User(t, pool, "gm-tomb"), Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, RoleCode: "GENERAL_MANAGER"}

	listed := func() map[string]bool {
		list, err := rail.ListApprovals(ctx, gm, "")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		got := map[string]bool{}
		for _, r := range list {
			got[r.ID] = true
		}
		return got
	}
	if l := listed(); !l[approved.ID] || !l[pending.ID] {
		t.Fatalf("both live requests must be listed before the tombstone, got %v", l)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE permission_approval_requests SET deleted_at = to_timestamp(0) WHERE id = ANY($1::uuid[])`,
		[]string{approved.ID, pending.ID}); err != nil {
		t.Fatalf("tombstone the requests: %v", err)
	}

	if l := listed(); l[approved.ID] || l[pending.ID] {
		t.Errorf("a tombstoned request must not be listed, got %v", l)
	}
	if err := rail.DecideApproval(ctx, pending.ID, gm, true, "qd1"); err == nil {
		t.Errorf("a tombstoned request must not be decidable")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM permission_approval_requests WHERE id = $1`, pending.ID).Scan(&status); err != nil || status != "PENDING" {
		t.Errorf("the refused decision must leave the request PENDING, got %q err %v", status, err)
	}
	if _, found, err := rail.FloorRequestFor(ctx, seller, intTrenton, quoteID, 1, lineID, prod); err != nil || found {
		t.Errorf("a tombstoned request must not stand as the line's open request, found %v err %v", found, err)
	}
	if qd1Consume(t, pool, seller, intTrenton, quoteID, 1, 40, lineID, prod) {
		t.Errorf("a tombstoned approved request must not be consumed")
	}
}
