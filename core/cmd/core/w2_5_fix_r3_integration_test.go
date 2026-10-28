//go:build integration

package main

// w2_5_fix_r3_integration_test.go, W2-5 round 3 (finance wave 2): K3's two
// P1s outside landed cost, proven through the production wiring the same way
// round 1's legs are: the close pre flight's stock tie reads the stock ledger
// as of the month's end, and every leg lives in a year of its own on a fresh
// migrated and seeded database.
//
// P1 2, the legacy receipt fallback: an unstamped PO receipt row (one written
// before wac_value_delta_cents existed) used to fall back to GRNI only, so a
// receipt whose WAC rounding journal booked one cent more than its GRNI moved
// the as of answer by that cent in every earlier month. The read must not
// guess: an unstamped receipt row that carries no true_up_delta_cents after
// the instant refuses in words, naming the receipt's move id and saying it
// predates the stamp, and the pre flight refuses the close with those words.
// A row that carries true_up_delta_cents is recovered exactly (that branch
// never rounds), and a stamped receipt keeps working.
//
// P1 3, transfer receipt rounding: the destination's new average is blended,
// which revalues the units already on hand, and the inbound move stored only
// outstanding x unit cost, so the blend's rounding cent had no exact move or
// journal and the as of read kept it in every earlier answer. The receive now
// stamps the destination's exact subledger delta on the inbound move and
// books the rounding through the same journal shape a PO receipt's WAC
// rounding uses.
//
// THE RULE both items keep: an out of tie month must never pass. Every leg
// has a tied direction (a month that tied at its end must not be refused
// falsely) and an opposite direction (a month that was out of tie at its end
// by the amount opposite to the later event must still be refused).
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='postgres://.../integration_core_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25FixR3 -count=1 -v

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

// w25r3LegCost sets the leg product's on hand and weighted average at the leg
// branch to the figures a leg names, replacing the round 1 harness's 10 at
// 1000. One destination unit at 100 receiving two units at 101 blends to a
// new average of 101 and revalues the unit already on hand by one cent.
func w25r3LegCost(t *testing.T, leg *w25Leg, onHand float64, avg int64) {
	t.Helper()
	mustExec(t, context.Background(), leg.pool, `
		UPDATE stock_balances SET on_hand_base = $3
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`, leg.productID, leg.branchID, onHand)
	mustExec(t, context.Background(), leg.pool, `
		UPDATE product_branch_costs SET avg_unit_cost_cents = $3
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`, leg.productID, leg.branchID, avg)
}

// w25r3Purchasing builds a purchasing service over the leg's pool wired to
// the leg's own inventory service through the composition's adapters, the
// same wiring wireOperations hands purchasing.
func w25r3Purchasing(t *testing.T, leg *w25Leg) *purchasing.Service {
	t.Helper()
	landedSvc := landedcost.NewService(landedcost.NewRepository(leg.pool)).
		WithCapitalizer(landedAdapter{svc: leg.money.inventorySvc}).
		WithGLPoster(landedGLPoster{svc: leg.money.glSvc})
	return purchasing.NewService(purchasing.NewRepository(leg.pool)).
		WithZone(newDealerZone(leg.pool)).
		WithStockReceiver(w4cStockReceiver{svc: leg.money.inventorySvc}).
		WithVendors(w4cVendorDirectory{pool: leg.pool}).
		WithLandedCoster(w4cLandedCoster{svc: landedSvc}).
		WithGLPoster(w4cGLPoster{svc: leg.money.glSvc}).
		WithRuleResolver(glRuleAdapter{svc: leg.money.glSvc}).
		WithBranches(item5BranchDirectory{pool: leg.pool}).
		WithVendorAddresses(item5VendorAddressBook{pool: leg.pool}).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// w25r3StaffCtx is a caller the branch fence accepts: a global leader on a
// seeded user, the same shape fw1-11's composition leg drives purchasing with.
func w25r3StaffCtx(t *testing.T, leg *w25Leg) context.Context {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("a seeded user: %v", err)
	}
	return staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER",
		Tier: staffctx.TierLeadership, Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
}

// w25r3ReceivePO orders and receives qty of the leg product at the leg branch
// at unitCost cents, through purchasing and the stock seam into inventory's
// applyReceipt, then drains the posting queue. It answers the receipt's move
// id and the inventory receipt id the move and the journals key on.
func w25r3ReceivePO(t *testing.T, leg *w25Leg, staffCtx context.Context, qty float64, unitCost int64) (moveID, receiptID string) {
	t.Helper()
	ctx := context.Background()
	svc := w25r3Purchasing(t, leg)
	var vendorID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY id LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("a seeded vendor: %v", err)
	}
	po, err := svc.CreatePO(staffCtx, purchasing.CreatePORequest{
		VendorID: vendorID, BranchID: leg.branchID,
		ExpectedAt: fmt.Sprintf("%04d-%02d-15", w25Year, w25Month),
		Lines:      []purchasing.AddLineParams{{ProductID: leg.productID, QtyOrderedBase: qty, UOM: "EA", UnitCostCents: unitCost}},
	})
	if err != nil {
		t.Fatalf("create the purchase order: %v", err)
	}
	ordered, err := svc.Transition(staffCtx, po.ID, purchasing.StatusOrdered)
	if err != nil {
		t.Fatalf("place the purchase order: %v", err)
	}
	if _, err := svc.Receive(staffCtx, ordered.ID, purchasing.ReceiveRequest{
		Lines: []purchasing.ReceiveLineParams{{POLineID: ordered.Lines[0].ID, QtyBase: qty}},
	}); err != nil {
		t.Fatalf("receive against the purchase order: %v", err)
	}
	w25Drain(t, leg)
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text, source_ref_id::text FROM stock_moves
		 WHERE source_type = 'po_receipt' AND branch_id = $1::uuid AND product_id = $2::uuid
		 ORDER BY occurred_at DESC LIMIT 1`, leg.branchID, leg.productID).Scan(&moveID, &receiptID); err != nil {
		t.Fatalf("the receipt's move: %v", err)
	}
	return moveID, receiptID
}

// w25r3MoveAnswers reads one move's stamped delta, its plain value and its
// true-up key. A missing key answers 0, a missing stamp answers ok=false.
func w25r3MoveAnswers(t *testing.T, leg *w25Leg, moveID string) (stamp int64, stamped bool, value, trueUp int64) {
	t.Helper()
	var stampPtr *int64
	if err := leg.pool.QueryRow(context.Background(), `
		SELECT (metadata->>'wac_value_delta_cents')::bigint, value_cents,
		       COALESCE((metadata->>'true_up_delta_cents')::bigint, 0)
		  FROM stock_moves WHERE id = $1::uuid`, moveID).Scan(&stampPtr, &value, &trueUp); err != nil {
		t.Fatalf("the move's answers: %v", err)
	}
	if stampPtr != nil {
		stamp, stamped = *stampPtr, true
	}
	return stamp, stamped, value, trueUp
}

// w25r3RealignMove rewrites one move row with the instant the leg names, the
// way w25Move realigns its own fulfilment's rows (a DELETE the trigger allows
// and an INSERT of the same row, occurred_at apart). stripStamp drops the
// wac_value_delta_cents key, the shape a row carried after the true-up journal
// existed but before this PR's stamp: the metadata's true_up_delta_cents, when
// there is one, is left exactly as the row wrote it.
func w25r3RealignMove(t *testing.T, leg *w25Leg, moveID string, at time.Time, stripStamp bool) {
	t.Helper()
	mustExec(t, context.Background(), leg.pool, `
		WITH gone AS (
			DELETE FROM stock_moves WHERE id = $1::uuid RETURNING *
		)
		INSERT INTO stock_moves (id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, occurred_at, created_at, bin_code)
		SELECT id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id,
			CASE WHEN $3 THEN metadata - 'wac_value_delta_cents' ELSE metadata END,
			$2, created_at, bin_code
		FROM gone`, moveID, at, stripStamp)
}

// w25r3RealignTransferMoves realigns both legs' move rows of one transfer to
// the instant the leg names. The transfer lines key on the move rows, so the
// lines detach first and reattach after, the way the ship and receive wrote
// them.
func w25r3RealignTransferMoves(t *testing.T, leg *w25Leg, transferID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	var shipMoveID, recvMoveID string
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text FROM stock_moves
		 WHERE source_ref_id = $1::uuid AND source_type = 'stock_transfer' AND to_location_id IS NULL`, transferID).Scan(&shipMoveID); err != nil {
		t.Fatalf("the transfer's outbound move: %v", err)
	}
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text FROM stock_moves
		 WHERE source_ref_id = $1::uuid AND source_type = 'stock_transfer' AND to_location_id IS NOT NULL`, transferID).Scan(&recvMoveID); err != nil {
		t.Fatalf("the transfer's inbound move: %v", err)
	}
	mustExec(t, ctx, leg.pool, `
		UPDATE stock_transfer_lines SET ship_move_id = NULL, recv_move_id = NULL
		 WHERE transfer_id = $1::uuid`, transferID)
	mustExec(t, ctx, leg.pool, `
		WITH gone AS (
			DELETE FROM stock_moves WHERE source_ref_id = $1::uuid RETURNING *
		)
		INSERT INTO stock_moves (id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, occurred_at, created_at, bin_code)
		SELECT id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, $2, created_at, bin_code
		FROM gone`, transferID, at)
	mustExec(t, ctx, leg.pool, `
		UPDATE stock_transfer_lines SET ship_move_id = $2::uuid, recv_move_id = $3::uuid
		 WHERE transfer_id = $1::uuid`, transferID, shipMoveID, recvMoveID)
}

// w25r3RealignJournals carries the journals the leg names to the leg's frame:
// posted_at takes the movement's own instant and the row re-files into the
// period that instant belongs to, re-minting the entry number under the leg's
// fiscal year the way a journal posted in the leg's frame would have. The GL
// side rides the document date, so both sides of the tie cut the movement at
// the same instant; keys that name no journal (a zero value posts none) are
// no-ops.
func w25r3RealignJournals(t *testing.T, leg *w25Leg, keys []string, at time.Time, year, month int) {
	t.Helper()
	mustExec(t, context.Background(), leg.pool, `
		UPDATE gl_journals j
		   SET posted_at = $1,
		       period_id = p.id,
		       fiscal_year = p.year,
		       entry_no = next_entry_no(p.year)
		  FROM fiscal_periods p
		 WHERE j.idempotency_key = ANY($2::text[])
		   AND p.year = $3 AND p.month = $4`, at, keys, year, month)
}

// w25r3Journal1200 answers the signed debit-minus-credit one journal's 1200
// leg booked, the way fw1-07's glDebit reads it.
func w25r3Journal1200(t *testing.T, leg *w25Leg, key string) int64 {
	t.Helper()
	var v int64
	if err := leg.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code = '1200'`, key).Scan(&v); err != nil {
		t.Fatalf("the journal's 1200 leg: %v", err)
	}
	return v
}

// w25r3BookOnlyCent writes 1200 up by one cent with no stock movement, inside
// the month the leg closes, through the GL's own provider path: the month
// ends out of tie by one cent in the direction a later plus one event must
// never cancel into a pass.
func w25r3BookOnlyCent(t *testing.T, leg *w25Leg, at time.Time, key string) {
	t.Helper()
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: key, Memo: "a one cent book only write up",
		BranchID: leg.branchID, DocumentDate: at,
		Lines: []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: 1, Memo: "one cent up"},
			{AccountCode: "3000", CreditCents: 1, Memo: "one cent up"},
		},
	})
}

// w25r3ReceiptLeg is the shared world of the receipt legs: the month ties at
// its end (or ends one cent out of tie in the opposite direction when
// opposite is true), then the next month receives two units at 101 cents onto
// one unit at 100 through the production receiving path. stripStamp shapes
// the receipt's move as a legacy row: the wac stamp dropped, the true-up key
// left exactly as written. It answers the leg, the month end and the
// receipt's move id.
func w25r3ReceiptLeg(t *testing.T, suffix string, opposite, stripStamp bool) (leg *w25Leg, monthEnd time.Time, moveID string) {
	t.Helper()
	leg = w25Setup(t, "r3"+suffix)
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	w25r3LegCost(t, leg, 1, 100)
	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)
	if opposite {
		w25r3BookOnlyCent(t, leg, openAt, "w2-5:fixr3:"+suffix+":bookonly")
	}

	staffCtx := w25r3StaffCtx(t, leg)
	moveID, receiptID := w25r3ReceivePO(t, leg, staffCtx, 2, 101)
	stamp, stamped, value, trueUp := w25r3MoveAnswers(t, leg, moveID)
	if !stamped {
		t.Fatalf("the receipt's move carries no wac_value_delta_cents stamp")
	}
	// The blended receipt of two at 101 onto one at 100 revalues the unit on
	// hand by one cent: the stamp is the exact subledger delta, the GRNI plus
	// the rounding (this branch carries no true-up).
	if want := value + 1; stamp != want {
		t.Fatalf("the receipt's move stamps %d, want the exact subledger delta %d (value %d, true-up %d)",
			stamp, want, value, trueUp)
	}

	w25r3RealignMove(t, leg, moveID, moveAt, stripStamp)
	w25r3RealignJournals(t, leg, []string{
		"po.received:" + receiptID,
		"po.received.rounding:" + receiptID,
		"pre_receipt_trueup:" + receiptID + ":" + leg.productID,
	}, moveAt, w25Year, w25Month+1)
	return leg, monthEnd, moveID
}

// TestW25FixR3StampedReceiptAfterTheInstantStillCloses is the tied direction
// of P1 2's stamped leg: the month ties at its end, the next month's receipt
// carries a real one cent WAC rounding in its stamp, and the close of the
// earlier month still seals it.
func TestW25FixR3StampedReceiptAfterTheInstantStillCloses(t *testing.T) {
	leg, _, moveID := w25r3ReceiptLeg(t, "stamp", false, false)

	stamp, _, value, _ := w25r3MoveAnswers(t, leg, moveID)
	if want := value + 1; stamp != want {
		t.Fatalf("the stamped receipt moved %d cents, want %d (the GRNI plus the one cent blend)", stamp, want)
	}
	// The rounding journal the stamp includes really booked one cent to 1200.
	if got := w25r3Journal1200(t, leg, "po.received.rounding:"+w25r3ReceiptOfMove(t, leg, moveID)); got != 1 {
		t.Fatalf("the receipt's rounding journal booked %d to 1200, want 1", got)
	}

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a stamped receipt that moved the ledger and 1200 together: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// w25r3ReceiptOfMove answers the receipt id one receipt move keys on.
func w25r3ReceiptOfMove(t *testing.T, leg *w25Leg, moveID string) string {
	t.Helper()
	var receiptID string
	if err := leg.pool.QueryRow(context.Background(), `
		SELECT source_ref_id::text FROM stock_moves WHERE id = $1::uuid`, moveID).Scan(&receiptID); err != nil {
		t.Fatalf("the move's receipt: %v", err)
	}
	return receiptID
}

// TestW25FixR3UnstampedReceiptRefusesATiedMonth is the tied direction of P1
// 2's unstamped leg: the month tied at its end and the next month's receipt
// is a legacy row whose rounding cannot be recovered. The close must refuse
// in words, naming the receipt's move id and saying it predates the stamp,
// never guessing a figure that would falsely refuse or falsely pass.
func TestW25FixR3UnstampedReceiptRefusesATiedMonth(t *testing.T) {
	leg, _, moveID := w25r3ReceiptLeg(t, "unstamped", false, true)

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month with a legacy receipt after its end answered %d, want 409: %s", code, body)
	}
	for _, want := range []string{moveID, "predates the value stamp"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// TestW25FixR3UnstampedReceiptRefusesRatherThanCancels is the opposite
// direction: the month ended one cent out of tie the way K3's example runs
// (stock below 1200), and the legacy receipt's hidden rounding cent would
// cancel exactly that discrepancy into a pass if the read guessed GRNI only.
// The close must still refuse, with the legacy row's words, not pass.
func TestW25FixR3UnstampedReceiptRefusesRatherThanCancels(t *testing.T) {
	leg, _, moveID := w25r3ReceiptLeg(t, "cancel", true, true)

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that was out of tie at its end was passed by a legacy receipt's hidden rounding: %d: %s", code, body)
	}
	for _, want := range []string{moveID, "predates the value stamp"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if strings.Contains(body, "out of tie with 1200") {
		t.Fatalf("the refusal guessed a figure instead of naming the legacy row: %s", body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}

// TestW25FixR3UnstampedTrueUpReceiptRecoversExactly is the recoverable half
// of the fallback: a legacy row written after the true-up journal existed
// carries true_up_delta_cents, and that branch never rounds, so the read
// recovers the exact figure the books booked (the GRNI less the true-up the
// journal credited to 1200) and a tied month still closes. The stamp the same
// receipt carries today holds the same figure, so the module's own new rows
// and the recovered legacy rows unwind to the same answer.
func TestW25FixR3UnstampedTrueUpReceiptRecoversExactly(t *testing.T) {
	leg := w25Setup(t, "r3trueup")
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)

	// The leg product sells one unit before its first receipt, provisionally
	// at zero (no cost anywhere), the move marked and awaiting true-up.
	mustExec(t, ctx, leg.pool, `
		DELETE FROM stock_balances WHERE product_id = $1::uuid AND branch_id = $2::uuid`, leg.productID, leg.branchID)
	mustExec(t, ctx, leg.pool, `
		DELETE FROM product_branch_costs WHERE product_id = $1::uuid AND branch_id = $2::uuid`, leg.productID, leg.branchID)
	var customerID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer: %v", err)
	}
	var orderID string
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('W2-5-R3TU-' || substr(md5(random()::text), 1, 8), $1, $2, 'FULFILLED', 'PICKUP', 0, 0)
		RETURNING id::text`, customerID, leg.branchID).Scan(&orderID); err != nil {
		t.Fatalf("the pre-receipt sale: %v", err)
	}
	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the pre-receipt sale: %v", err)
	}
	if err := leg.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, orderID, leg.branchID, []string{leg.productID}, []float64{1}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the pre-receipt sale: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the pre-receipt sale: %v", err)
	}
	w25Drain(t, leg)
	mustExec(t, ctx, leg.pool, `
		WITH gone AS (
			DELETE FROM stock_moves WHERE source_ref_id = $1::uuid RETURNING *
		)
		INSERT INTO stock_moves (id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, occurred_at, created_at, bin_code)
		SELECT id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, $2, created_at, bin_code
		FROM gone`, orderID, openAt)

	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)

	// The first receipt trues the sale up: the true-up journal credits 1200
	// the delta, so the exact figure the books booked is the GRNI less the
	// true-up, and the stamp the module writes today holds exactly that.
	staffCtx := w25r3StaffCtx(t, leg)
	moveID, receiptID := w25r3ReceivePO(t, leg, staffCtx, 2, 101)
	stamp, stamped, value, trueUp := w25r3MoveAnswers(t, leg, moveID)
	if !stamped {
		t.Fatalf("the true-up receipt's move carries no wac_value_delta_cents stamp")
	}
	if want := value - trueUp; stamp != want {
		t.Fatalf("the true-up receipt's move stamps %d, want the exact subledger delta %d (value %d, true-up %d)",
			stamp, want, value, trueUp)
	}

	// Shape the row the way the pre-stamp writer left it: the true-up keys
	// kept, the wac stamp absent, the instant in the next month.
	w25r3RealignMove(t, leg, moveID, moveAt, true)
	w25r3RealignJournals(t, leg, []string{
		"po.received:" + receiptID,
		"po.received.rounding:" + receiptID,
		"pre_receipt_trueup:" + receiptID + ":" + leg.productID,
	}, moveAt, w25Year, w25Month+1)

	asOf, err := leg.money.inventorySvc.StockValueAsOfCents(ctx, monthEnd, "")
	if err != nil {
		t.Fatalf("the as of read refused a recovered legacy row: %v", err)
	}
	if asOf != leg.stockCents {
		t.Fatalf("the as of read answers %d at the month's end, want %d: the recovered true-up row did not unwind exactly", asOf, leg.stockCents)
	}

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a recovered legacy true-up receipt: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// w25r3TransferLeg is the shared world of the transfer legs: the source
// branch holds two of the leg product at 101, the destination one at 100, the
// month ties at its end (or ends one cent out of tie in the opposite
// direction), and the next month ships and receives the transfer through the
// production service. It answers the leg, the month end, the transfer id and
// the inbound move id.
func w25r3TransferLeg(t *testing.T, suffix string, opposite bool) (leg *w25Leg, monthEnd time.Time, transferID, inboundMoveID string) {
	t.Helper()
	leg = w25Setup(t, "r3"+suffix)
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)

	var srcBranch, dstBranch string
	rows, err := leg.pool.Query(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("two seeded branches: %v", err)
	}
	var branches []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("a seeded branch: %v", err)
		}
		branches = append(branches, b)
	}
	rows.Close()
	if len(branches) < 2 {
		t.Fatalf("the transfer legs need two branches, the seed holds %d", len(branches))
	}
	srcBranch, dstBranch = branches[0], branches[1]

	// The leg product leaves the round 1 harness's branch; the source holds
	// two at 101, the destination one at 100.
	mustExec(t, ctx, leg.pool, `
		DELETE FROM stock_balances WHERE product_id = $1::uuid`, leg.productID)
	mustExec(t, ctx, leg.pool, `
		DELETE FROM product_branch_costs WHERE product_id = $1::uuid`, leg.productID)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, 2)`, leg.productID, srcBranch)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, 101)`, leg.productID, srcBranch)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, 1)`, leg.productID, dstBranch)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, 100)`, leg.productID, dstBranch)

	openAt, moveAt, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)
	if opposite {
		w25r3BookOnlyCent(t, leg, openAt, "w2-5:fixr3:"+suffix+":bookonly")
	}

	var userID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("a seeded user: %v", err)
	}
	tr, err := leg.money.inventorySvc.CreateTransfer(ctx, inventory.CreateTransferInput{
		FromBranchID: srcBranch, ToBranchID: dstBranch,
		Lines:     []inventory.CreateTransferLine{{ProductID: leg.productID, QtyBase: 2}},
		CreatedBy: userID,
	})
	if err != nil {
		t.Fatalf("create the transfer: %v", err)
	}
	if _, err := leg.money.inventorySvc.ShipTransfer(ctx, tr.ID, userID); err != nil {
		t.Fatalf("ship the transfer: %v", err)
	}
	if _, err := leg.money.inventorySvc.ReceiveTransfer(ctx, tr.ID, userID); err != nil {
		t.Fatalf("receive the transfer: %v", err)
	}

	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text FROM stock_moves
		 WHERE source_type = 'stock_transfer' AND to_location_id IS NOT NULL AND source_ref_id = $1::uuid
		 ORDER BY occurred_at DESC LIMIT 1`, tr.ID).Scan(&inboundMoveID); err != nil {
		t.Fatalf("the transfer's inbound move: %v", err)
	}
	w25r3RealignTransferMoves(t, leg, tr.ID, moveAt)
	w25r3RealignJournals(t, leg, []string{
		"inventory.transfer_shipped:" + tr.ID,
		"inventory.transfer_received:" + tr.ID,
		"inventory.transfer_received.rounding:" + tr.ID,
	}, moveAt, w25Year, w25Month+1)
	return leg, monthEnd, tr.ID, inboundMoveID
}

// TestW25FixR3TransferRoundingKeepsATiedMonthTied is the tied direction of
// P1 3: one destination unit at 100 receives two transfer units at 101, the
// blend revalues the unit on hand by one cent, and the close of the month
// that tied at its end still seals it: the inbound move carries the exact
// destination subledger delta and the rounding cent is booked to 1200, so
// both sides of the tie cut the transfer at the month's end.
func TestW25FixR3TransferRoundingKeepsATiedMonthTied(t *testing.T) {
	leg, _, transferID, inboundMoveID := w25r3TransferLeg(t, "trfstamp", false)

	stamp, stamped, value, _ := w25r3MoveAnswers(t, leg, inboundMoveID)
	if !stamped {
		t.Fatalf("the inbound move carries no wac_value_delta_cents stamp")
	}
	if want := value + 1; stamp != want {
		t.Fatalf("the inbound move stamps %d, want %d (the transferred value plus the one cent the blend revalued)", stamp, want)
	}
	if got := w25r3Journal1200(t, leg, "inventory.transfer_received.rounding:"+transferID); got != 1 {
		t.Fatalf("the transfer rounding journal booked %d to 1200, want the one cent the blend added", got)
	}

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a transfer whose blend revalued a unit: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25FixR3TransferRoundingNeverCancelsAnOppositeDiscrepancy is the
// opposite direction: the month ended one cent out of tie with stock below
// 1200, and the transfer's rounding cent added one cent to both sides after
// that. The close of the earlier month must still refuse, naming the month's
// own discrepancy, never the cancelled pass the unrecorded cent used to buy.
func TestW25FixR3TransferRoundingNeverCancelsAnOppositeDiscrepancy(t *testing.T) {
	leg, _, _, _ := w25r3TransferLeg(t, "trfrefuse", true)

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that was out of tie at its end was passed by the transfer's rounding cent: %d: %s", code, body)
	}
	for _, want := range []string{"stock is out of tie with 1200", "$0.01"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the refusal %q does not name %q", body, want)
		}
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}
}
