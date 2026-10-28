//go:build integration

package main

// w2_5_fix_r10_integration_test.go, W2-5 round 10 (finance wave 2): S5's P2-7, the count's
// variance journal carries the COUNT move's own instant, proven through the production wiring.
//
// THE DEFECT. `invcount.count:<sheet>` is invcount's own journal: the reason-coded variance
// journal a count posts. It carried no document date, and it posts AFTER the count's own
// transaction commits, so the GL dated it at the moment it was posted. A count whose COUNT
// move lands in a month's last moment and whose variance journal posts after that month's
// end leaves the two sides of one variance on two sides of the month's end: the stock ledger
// holds the count and 1200 does not, and the close refuses that month by the count's whole
// variance.
//
// THE FIX. The journal carries the COUNT move's own instant as its document date, on the first
// press and on a re-press. The count's posting transaction writes the COUNT move and stamps
// the sheet's post instant in that one transaction, so the sheet's recorded instant IS the
// move's instant; the journal reads it back and carries it.
//
// THE FRAME. The legs close the house's LAST ENDED month, the month before the running one, in
// the dealer's own zone (the round 8 frame). The COUNT move lands in that month's last moment
// and the variance journal posts after that month's end, so the month's end falls between the
// two sides of the count.
//
// THE RULE the legs keep: a month that tied at its end must not be refused falsely, and a
// month that was out of tie at its end by a real difference must still be refused.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25R10 -count=1 -v

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/invcount"
)

// w25r10CountedQty is how many of the leg product the count finds: the leg product carries
// w25Units at w25UnitCost, so the count's variance is two units short, a loss of 2000 cents.
const w25r10CountedQty = 8

// w25r10CountService builds the count service over the leg's pool, wired the way wireOperations
// wires it: the production stock, GL, rule, balance and audit adapters, so the leg proves the
// production adapter carries the document date and not a test double.
func w25r10CountService(t *testing.T, leg *w25Leg) *invcount.Service {
	t.Helper()
	return invcount.NewService(invcount.NewRepository(leg.pool)).
		WithStockCounter(invCountAdapter{svc: leg.money.inventorySvc}).
		WithGLPoster(invcountGLPoster{svc: leg.money.glSvc}).
		WithRuleResolver(glRuleAdapter{svc: leg.money.glSvc}).
		WithGLBalanceReader(glBalanceAdapter{svc: leg.money.glSvc}).
		WithAudit(leg.money.auditSvc)
}

// w25r10StaffCtx is a caller the branch fence accepts, on the seeded user at the given offset.
// Two of them are the count's maker and its checker: the four eyes gate wants two people.
func w25r10StaffCtx(t *testing.T, leg *w25Leg, offset int) context.Context {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1 OFFSET $1`, offset).Scan(&userID); err != nil {
		t.Fatalf("a seeded user: %v", err)
	}
	return staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER",
		Tier: staffctx.TierLeadership, Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
}

// w25r10SetInventoryRule turns the posting rule the count's 1200 leg resolves on and off.
//
// It stands for the deployment whose rule the count's journal could not land on: the rule is
// not configured, the count's own transaction commits anyway (the COUNT move and the sheet's
// post instant are on the books), and the variance journal does not exist until the rule is in
// place and the count is pressed again.
func w25r10SetInventoryRule(t *testing.T, leg *w25Leg, active bool) {
	t.Helper()
	mustExec(t, context.Background(), leg.pool, `
		UPDATE posting_rules SET is_active = $1
		 WHERE event_type = 'inventory.adjusted' AND line_role = 'inventory'`, active)
}

// w25r10CarryTheCountInstant carries the count's own instant into the leg's frame: the COUNT
// move row is re-dated and the sheet's recorded post instant moves with it.
//
// THE TWO MOVE TOGETHER because they are one instant: the count's posting transaction writes
// the COUNT move stamped with the transaction's own clock and stamps the sheet's post instant
// with the same clock, so a count's recorded instant IS the move's instant. A leg that moved
// only one of them would describe a count whose move and whose record disagree, which the
// production road cannot produce.
//
// THE RE-DATING UNLINKS THE COUNT LINE FROM ITS MOVE: the move row is deleted and inserted again
// with the new instant, and count_lines.move_id is ON DELETE SET NULL. The leg puts the link
// back (it is the same row with a new instant), so the world stays the world the count wrote.
func w25r10CarryTheCountInstant(t *testing.T, leg *w25Leg, sheetID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	var moveID string
	if err := leg.pool.QueryRow(ctx, `
		SELECT move_id::text FROM count_lines
		 WHERE sheet_id = $1::uuid AND move_id IS NOT NULL LIMIT 1`, sheetID).Scan(&moveID); err != nil {
		t.Fatalf("the count's own move: %v", err)
	}
	w25r5RealignMove(t, leg, moveID, at)
	mustExec(t, ctx, leg.pool, `
		UPDATE count_sheets SET posted_at = $2 WHERE id = $1::uuid`, sheetID, at)
	mustExec(t, ctx, leg.pool, `
		UPDATE count_lines SET move_id = $2 WHERE sheet_id = $1::uuid AND move_id IS NULL`, sheetID, moveID)
}

// w25r10PressInTheFrame presses the count with its own instant carried into the frame: the
// count's GL post cannot land while the rule is off, the count's own instant moves into the
// frame with its COUNT move, and the re-press lands the variance journal dated at the count's
// own instant.
func w25r10PressInTheFrame(t *testing.T, leg *w25Leg, sheetID string, at time.Time, caller context.Context) {
	t.Helper()
	svc := w25r10CountService(t, leg)
	w25r10SetInventoryRule(t, leg, false)
	if err := svc.PostSheet(caller, sheetID); err == nil {
		t.Fatalf("the count posted its variance journal with no posting rule for its 1200 leg")
	}
	if status := w25r10SheetStatus(t, leg, sheetID); status != "POSTED" {
		t.Fatalf("the count sheet reads %s after the press that could not post its journal, want POSTED", status)
	}
	if n := w25r10JournalCount(t, leg, sheetID); n != 0 {
		t.Fatalf("the count's variance journal exists (%d) although its press could not post it", n)
	}
	w25r10CarryTheCountInstant(t, leg, sheetID, at)
	w25r10SetInventoryRule(t, leg, true)
	if err := svc.PostSheet(caller, sheetID); err != nil {
		t.Fatalf("the re-press of the count: %v", err)
	}
}

// w25r10Count builds a count sheet through the counts service, counts the leg product short,
// and has a second person approve it. It answers the sheet id.
func w25r10Count(t *testing.T, leg *w25Leg, maker, checker context.Context) string {
	t.Helper()
	svc := w25r10CountService(t, leg)
	sheet, err := svc.CreateSheet(maker, invcount.CreateSheetRequest{BranchID: leg.branchID, Scope: "FULL"})
	if err != nil {
		t.Fatalf("opening the count sheet: %v", err)
	}
	if _, err := svc.FreezeSheet(maker, sheet.ID); err != nil {
		t.Fatalf("freezing the count sheet: %v", err)
	}
	if _, err := svc.SubmitCounts(maker, sheet.ID, invcount.SubmitCountsRequest{
		Lines: []invcount.CountLineInput{{
			ProductID: leg.productID, LocationID: leg.branchID,
			CountedQtyBase: w25r10CountedQty, ReasonCode: "SHRINK",
		}},
	}); err != nil {
		t.Fatalf("submitting the count: %v", err)
	}
	if _, err := svc.ApproveSheet(checker, sheet.ID); err != nil {
		t.Fatalf("approving the count: %v", err)
	}
	return sheet.ID
}

// w25r10JournalInstant answers the instant a journal is posted at.
func w25r10JournalInstant(t *testing.T, leg *w25Leg, key string) time.Time {
	t.Helper()
	var at time.Time
	if err := leg.pool.QueryRow(context.Background(),
		`SELECT posted_at FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&at); err != nil {
		t.Fatalf("the journal %s: %v", key, err)
	}
	return at
}

// w25r10MoveInstant answers the instant a movement happened.
func w25r10MoveInstant(t *testing.T, leg *w25Leg, moveID string) time.Time {
	t.Helper()
	var at time.Time
	if err := leg.pool.QueryRow(context.Background(),
		`SELECT occurred_at FROM stock_moves WHERE id = $1::uuid`, moveID).Scan(&at); err != nil {
		t.Fatalf("the movement's instant: %v", err)
	}
	return at
}

// TestW25R10ACountVarianceJournalPostedAfterItsMonthsEndStillCloses is the red: a count
// pressed with its COUNT move in the closing month's last moment, whose variance journal
// posts after that month's end, must not refuse that month.
//
// On the code before this round the close is refused: the stock ledger holds the count and
// 1200 does not, because the journal is dated at the moment it is posted (after the month's
// end) while the move is dated inside the month.
func TestW25R10ACountVarianceJournalPostedAfterItsMonthsEndStillCloses(t *testing.T) {
	frame := w25r8Leg(t, "r10red")
	leg := frame.leg
	ctx := context.Background()
	maker := w25r10StaffCtx(t, leg, 0)
	checker := w25r10StaffCtx(t, leg, 1)

	sheetID := w25r10Count(t, leg, maker, checker)

	// The count's own instant, carried into the closing month's last moment: the COUNT move
	// and the sheet's recorded post instant move together, and the variance journal lands after
	// the closing month's end.
	w25r10PressInTheFrame(t, leg, sheetID, frame.receiptAt, checker)

	// The month closes: a count in the month's last moment, journaled after the month's end,
	// does not refuse that month.
	code, body := w25Close(t, leg, frame.year, frame.month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a count in the month's last moment, journaled after the month's end: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, frame.year, frame.month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}

	// The count's journal carries the COUNT move's own instant: the two sides of one count are
	// cut at the same moment, so a month end finds them on the same side of itself.
	key := "invcount.count:" + sheetID
	journalAt := w25r10JournalInstant(t, leg, key)
	var moveID string
	if err := leg.pool.QueryRow(ctx, `
		SELECT move_id::text FROM count_lines WHERE sheet_id = $1::uuid AND move_id IS NOT NULL LIMIT 1`, sheetID).Scan(&moveID); err != nil {
		t.Fatalf("the count's own move: %v", err)
	}
	moveAt := w25r10MoveInstant(t, leg, moveID)
	if !journalAt.Equal(moveAt) {
		t.Fatalf("the count's variance journal is posted at %s, want the COUNT move's own instant %s: a journal dated at another instant puts the two sides of one count on two sides of a month end",
			journalAt.Format(time.RFC3339Nano), moveAt.Format(time.RFC3339Nano))
	}
}

// TestW25R10ARealMonthEndDiscrepancyStillRefuses is the opposite leg: the month tied at its
// end, the count's own journal rides its COUNT move, and a real difference lands in the month
// with no movement behind it. The close of that month is still refused, naming the difference
// the month itself carries.
func TestW25R10ARealMonthEndDiscrepancyStillRefuses(t *testing.T) {
	frame := w25r8Leg(t, "r10refuse")
	leg := frame.leg
	maker := w25r10StaffCtx(t, leg, 0)
	checker := w25r10StaffCtx(t, leg, 1)

	sheetID := w25r10Count(t, leg, maker, checker)
	w25r10PressInTheFrame(t, leg, sheetID, frame.receiptAt, checker)

	// A real difference in the month: a write down on the book with no movement behind it.
	w25BookOnly(t, leg, frame.receiptAt, "w2-5:r10:refuse:write-down", "a book only stock write down", true)

	// The close of the month that was out of tie at its end: refused, naming the month's own
	// difference, never the count's variance cancelled.
	code, body := w25Close(t, leg, frame.year, frame.month)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that was out of tie at its end answered %d, want 409: %s", code, body)
	}
	for _, want := range []string{"stock is out of tie with 1200", "$20.00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if status := w25PeriodStatus(t, leg, frame.year, frame.month); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// w25r10SheetStatus answers one count sheet's status.
func w25r10SheetStatus(t *testing.T, leg *w25Leg, sheetID string) string {
	t.Helper()
	var status string
	if err := leg.pool.QueryRow(context.Background(),
		`SELECT status FROM count_sheets WHERE id = $1::uuid`, sheetID).Scan(&status); err != nil {
		t.Fatalf("the count sheet's status: %v", err)
	}
	return status
}

// w25r10JournalCount answers how many journals the count's own key names.
func w25r10JournalCount(t *testing.T, leg *w25Leg, sheetID string) int {
	t.Helper()
	var n int
	if err := leg.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "invcount.count:"+sheetID).Scan(&n); err != nil {
		t.Fatalf("the count's journals: %v", err)
	}
	return n
}
