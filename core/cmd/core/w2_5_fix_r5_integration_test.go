//go:build integration

package main

// w2_5_fix_r5_integration_test.go, W2-5 round 5 (finance wave 2): S5's P2-1, the
// fractional value writers, proven through the production wiring the same way
// the earlier rounds' legs are: the close pre flight's stock tie reads the
// stock ledger as of the month's end, and every leg lives in a year of its
// own on a fresh migrated and seeded database.
//
// P2-1: a fractional issue, return, count or transfer ship used to book
// value_cents = round(qty x avg), the rounded movement, while the row itself
// moves by round(n x avg) - round((n-q) x avg), which differs by a cent
// whenever the fractional parts straddle the half. 12.38 t on hand at 4510
// cents a tonne is 55,833.8, held as 55,834; a sale of 0.04 t is 180.4,
// booked as 180; the row becomes 12.34 t, 55,653.4, held as 55,653: it moved
// by 181 and the GL moved 180. A later fractional movement therefore refused
// a month that tied at its end by a cent, and passed a month out of tie by
// the opposite cent. The four writers now carry the PO receipt's rule: the
// move stamps wac_value_delta_cents with the row's exact change and the
// difference against value_cents is booked as the same WAC integer cent
// rounding journal the receipt and the transfer receive post, in the same
// transaction, at the same instant, with an idempotency key per move.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='postgres://.../integration_core_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25FixR5 -count=1 -v

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
)

// w25r5LegCost is the leg product's on hand and weighted average at the leg
// branch: a fractional figure at 4510 cents, where the row's rounded value
// and the rounded movement of a 0.04 t move part by a cent.
const (
	w25r5Avg     = 4510
	w25r5FracQty = 0.04
	w25r5Value   = 180 // round(0.04 x 4510), the rounded movement the GL books
	w25r5Stamp   = 181 // round(12.38 x 4510) - round(12.34 x 4510), the row's exact change
)

// w25r5World is the shared frame of the fractional legs: the leg product
// carries onHand at 4510 cents at the leg branch, the closing month ties at
// its end, and opposite ends the month one cent out of tie with stock below
// 1200, the shape K3's example runs.
func w25r5World(t *testing.T, suffix string, onHand float64, opposite bool) (*w25Leg, time.Time) {
	t.Helper()
	leg := w25Setup(t, "r5"+suffix)
	ctx := context.Background()
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, w25Year)
	w25r3LegCost(t, leg, onHand, w25r5Avg)
	openAt, _, monthEnd := w25Instants(t, leg.pool)
	w25TieTheAR(t, leg, openAt)
	w25TieTheMonthAtItsEnd(t, leg, openAt, monthEnd)
	if opposite {
		w25r3BookOnlyCent(t, leg, openAt, "w2-5:fixr5:"+suffix+":bookonly")
	}
	return leg, monthEnd
}

// w25r5AssertAsOfTies proves the as-of read lands the month end on the
// figure the month tied at, through the production seam.
func w25r5AssertAsOfTies(t *testing.T, leg *w25Leg, monthEnd time.Time) {
	t.Helper()
	asOf, err := leg.money.inventorySvc.StockValueAsOfCents(context.Background(), monthEnd, "")
	if err != nil {
		t.Fatalf("the as of read refused the stamped movement: %v", err)
	}
	if asOf != leg.stockCents {
		t.Fatalf("the as of read answers %d at the month's end, want %d: the stamped movement did not unwind exactly", asOf, leg.stockCents)
	}
}

// w25r5RealignTransferShipMove re-inserts the transfer's outbound move row
// with the instant the leg names. The transfer lines key on the move row, so
// the line detaches first and reattaches after, the way the ship wrote it.
func w25r5RealignTransferShipMove(t *testing.T, leg *w25Leg, transferID, moveID string, at time.Time) {
	t.Helper()
	mustExec(t, context.Background(), leg.pool, `
		UPDATE stock_transfer_lines SET ship_move_id = NULL
		 WHERE transfer_id = $1::uuid`, transferID)
	mustExec(t, context.Background(), leg.pool, `
		WITH gone AS (
			DELETE FROM stock_moves WHERE id = $1::uuid RETURNING *
		)
		INSERT INTO stock_moves (id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, occurred_at, created_at, bin_code)
		SELECT id, product_id, from_location_id, to_location_id, qty_base, qty_entered,
			uom_entered, move_type, unit_cost_cents, value_cents, weight_kg_total, source_type, source_ref_id,
			reason, actor_id, branch_id, metadata, $2, created_at, bin_code
		FROM gone`, moveID, at)
	mustExec(t, context.Background(), leg.pool, `
		UPDATE stock_transfer_lines SET ship_move_id = $2::uuid
		 WHERE transfer_id = $1::uuid`, transferID, moveID)
}

// w25r5RealignMove re-inserts one move row with the instant the leg names,
// the way w25Move does: a DELETE the trigger allows and an INSERT of the
// same row, occurred_at apart, metadata preserved.
func w25r5RealignMove(t *testing.T, leg *w25Leg, moveID string, at time.Time) {
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
			reason, actor_id, branch_id, metadata, $2, created_at, bin_code
		FROM gone`, moveID, at)
}

// w25r5MoveOf answers the leg product's one move of one source.
func w25r5MoveOf(t *testing.T, leg *w25Leg, sourceRefID string) (moveID string, value int64) {
	t.Helper()
	if err := leg.pool.QueryRow(context.Background(), `
		SELECT id::text, value_cents FROM stock_moves
		 WHERE source_ref_id = $1::uuid AND product_id = $2::uuid`, sourceRefID, leg.productID).Scan(&moveID, &value); err != nil {
		t.Fatalf("the movement's move: %v", err)
	}
	return moveID, value
}

// TestW25FixR5FractionalSaleKeepsATiedMonthTied is S5's probe B as a leg: the
// month ties at its end, the next month's fractional sale moves the row by
// 181 cents while its GL COGS books the movement's 180, and the close of the
// earlier month still seals it: the SHIP move stamps the row's exact change
// and the rounding cent rides its own journal in the same transaction.
func TestW25FixR5FractionalSaleKeepsATiedMonthTied(t *testing.T) {
	leg, monthEnd := w25r5World(t, "sale", 12.38, false)
	ctx := context.Background()
	_, moveAt, _ := w25Instants(t, leg.pool)

	var customerID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer: %v", err)
	}
	var orderID string
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('W2-5-R5-SALE-' || substr(md5(random()::text), 1, 8), $1, $2, 'FULFILLED', 'PICKUP', $3, $3)
		RETURNING id::text`, customerID, leg.branchID, w25r5Value).Scan(&orderID); err != nil {
		t.Fatalf("the sale: %v", err)
	}
	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the fulfilment: %v", err)
	}
	if err := leg.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, orderID, leg.branchID, []string{leg.productID}, []float64{w25r5FracQty}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the fulfilment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fulfilment: %v", err)
	}
	moveID, value := w25r5MoveOf(t, leg, orderID)
	if value != w25r5Value {
		t.Fatalf("the sale's move values %d cents, want %d", value, w25r5Value)
	}
	w25r5RealignMove(t, leg, moveID, moveAt)
	// The COGS journal the order.fulfilled consumer posts, through the GL's
	// own provider path, under the consumer's own key: the drain then acks
	// the emitted event as the duplicate it is.
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "order.fulfilled:" + orderID,
		Memo: "COGS on fulfillment", BranchID: leg.branchID, DocumentDate: moveAt,
		Lines: []gl.ExternalLine{
			{AccountCode: "5000", DebitCents: w25r5Value, Memo: "COGS"},
			{AccountCode: "1200", CreditCents: w25r5Value, Memo: "inventory relief"},
		},
	})
	w25Drain(t, leg)
	w25r3RealignJournals(t, leg, []string{
		"order.fulfilled:" + orderID,
		"inventory.ship.rounding:" + moveID,
	}, moveAt, w25Year, w25Month+1)

	w25r5AssertAsOfTies(t, leg, monthEnd)

	stamp, stamped, _, _ := w25r3MoveAnswers(t, leg, moveID)
	if !stamped {
		t.Fatalf("the sale's move carries no wac_value_delta_cents stamp")
	}
	if stamp != w25r5Stamp {
		t.Fatalf("the sale's move stamps %d, want the row's exact change %d (value %d)", stamp, w25r5Stamp, value)
	}
	if got := w25r3Journal1200(t, leg, "inventory.ship.rounding:"+moveID); got != -1 {
		t.Fatalf("the sale's rounding journal booked %d to 1200, want -1 (the cent the row moved beyond the movement)", got)
	}

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a fractional sale that moved the row and 1200 together: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25FixR5FractionalSaleNeverCancelsAnOppositeDiscrepancy is the
// opposite direction: the month ended one cent out of tie with stock below
// 1200, and the fractional sale's rounding cent left both sides together
// after that. The close of the earlier month must still refuse, naming the
// month's own discrepancy, never the cancelled pass the unrecorded cent used
// to buy.
func TestW25FixR5FractionalSaleNeverCancelsAnOppositeDiscrepancy(t *testing.T) {
	leg, _ := w25r5World(t, "salerefuse", 12.38, true)
	ctx := context.Background()
	_, moveAt, _ := w25Instants(t, leg.pool)

	var customerID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatalf("a seeded customer: %v", err)
	}
	var orderID string
	if err := leg.pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('W2-5-R5-SREF-' || substr(md5(random()::text), 1, 8), $1, $2, 'FULFILLED', 'PICKUP', $3, $3)
		RETURNING id::text`, customerID, leg.branchID, w25r5Value).Scan(&orderID); err != nil {
		t.Fatalf("the sale: %v", err)
	}
	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the fulfilment: %v", err)
	}
	if err := leg.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, orderID, leg.branchID, []string{leg.productID}, []float64{w25r5FracQty}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the fulfilment: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fulfilment: %v", err)
	}
	moveID, _ := w25r5MoveOf(t, leg, orderID)
	w25r5RealignMove(t, leg, moveID, moveAt)
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "order.fulfilled:" + orderID,
		Memo: "COGS on fulfillment", BranchID: leg.branchID, DocumentDate: moveAt,
		Lines: []gl.ExternalLine{
			{AccountCode: "5000", DebitCents: w25r5Value, Memo: "COGS"},
			{AccountCode: "1200", CreditCents: w25r5Value, Memo: "inventory relief"},
		},
	})
	w25Drain(t, leg)
	w25r3RealignJournals(t, leg, []string{
		"order.fulfilled:" + orderID,
		"inventory.ship.rounding:" + moveID,
	}, moveAt, w25Year, w25Month+1)

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusConflict {
		t.Fatalf("the close of the month that was out of tie at its end was passed by the fractional sale's rounding cent: %d: %s", code, body)
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

// TestW25FixR5FractionalReturnKeepsATiedMonthTied is the return writer: the
// month ties at its end, the next month takes 0.04 t back onto 12.34 t on
// hand, the row moves by 181 while the COGS reversal books the movement's
// 180, and the close of the earlier month still seals it.
func TestW25FixR5FractionalReturnKeepsATiedMonthTied(t *testing.T) {
	leg, monthEnd := w25r5World(t, "return", 12.34, false)
	ctx := context.Background()
	_, moveAt, _ := w25Instants(t, leg.pool)

	returnID := "b2000000-0000-4000-8000-0000000000e1"
	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the return: %v", err)
	}
	if err := leg.money.inventorySvc.RestockForReturnTx(ctx, tx, inventory.RestockInput{
		ReturnID: returnID, BranchID: leg.branchID, ReturnNo: "W2-5-R5-RET",
		Lines: []inventory.SaleLine{{ProductID: leg.productID, QtyBase: w25r5FracQty}},
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the return: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the return: %v", err)
	}
	moveID, value := w25r5MoveOf(t, leg, returnID)
	if value != w25r5Value {
		t.Fatalf("the return's move values %d cents, want %d", value, w25r5Value)
	}
	w25r5RealignMove(t, leg, moveID, moveAt)
	// The COGS reversal the order.returned consumer posts, through the GL's
	// own provider path, under the consumer's own key.
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "order.returned:" + returnID,
		Memo: "COGS reversal on return", BranchID: leg.branchID, DocumentDate: moveAt,
		Lines: []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: w25r5Value, Memo: "inventory back in stock"},
			{AccountCode: "5000", CreditCents: w25r5Value, Memo: "COGS reversal"},
		},
	})
	w25r3RealignJournals(t, leg, []string{
		"order.returned:" + returnID,
		"inventory.return.rounding:" + moveID,
	}, moveAt, w25Year, w25Month+1)

	w25r5AssertAsOfTies(t, leg, monthEnd)

	stamp, stamped, _, _ := w25r3MoveAnswers(t, leg, moveID)
	if !stamped {
		t.Fatalf("the return's move carries no wac_value_delta_cents stamp")
	}
	if stamp != w25r5Stamp {
		t.Fatalf("the return's move stamps %d, want the row's exact change %d (value %d)", stamp, w25r5Stamp, value)
	}
	if got := w25r3Journal1200(t, leg, "inventory.return.rounding:"+moveID); got != 1 {
		t.Fatalf("the return's rounding journal booked %d to 1200, want 1 (the cent the row gained beyond the movement)", got)
	}

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a fractional return that moved the row and 1200 together: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25FixR5FractionalCountKeepsATiedMonthTied is the count writer: the
// month ties at its end, the next month's count finds 0.04 t short of 12.38
// t on hand, the row moves by -181 while the count's adjustment journal
// books the movement's 180, and the close of the earlier month still seals
// it.
func TestW25FixR5FractionalCountKeepsATiedMonthTied(t *testing.T) {
	leg, monthEnd := w25r5World(t, "count", 12.38, false)
	ctx := context.Background()
	_, moveAt, _ := w25Instants(t, leg.pool)

	tx, err := leg.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the count: %v", err)
	}
	moveID, err := leg.money.inventorySvc.PostCountAdjustmentTx(ctx, tx, leg.productID, leg.branchID, leg.branchID, -w25r5FracQty, w25r5Avg, "SHRINK")
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("the count: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the count: %v", err)
	}
	if moveID == "" {
		t.Fatalf("the count wrote no move")
	}
	var value int64
	if err := leg.pool.QueryRow(ctx, `SELECT value_cents FROM stock_moves WHERE id = $1::uuid`, moveID).Scan(&value); err != nil {
		t.Fatalf("the count's move: %v", err)
	}
	if value != w25r5Value {
		t.Fatalf("the count's move values %d cents, want %d", value, w25r5Value)
	}
	w25r5RealignMove(t, leg, moveID, moveAt)
	// The reason-coded journal the counted sheet posts (invcount's own
	// shape: the reason leg against 1200, valued at the movement), through
	// the GL's own provider path.
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "w2-5:fixr5:count:" + leg.suffix,
		Memo: "count sheet shrink", BranchID: leg.branchID, DocumentDate: moveAt,
		Lines: []gl.ExternalLine{
			{AccountCode: "5200", DebitCents: w25r5Value, Memo: "count shrink"},
			{AccountCode: "1200", CreditCents: w25r5Value, Memo: "inventory loss"},
		},
	})
	w25r3RealignJournals(t, leg, []string{
		"w2-5:fixr5:count:" + leg.suffix,
		"inventory.count.rounding:" + moveID,
	}, moveAt, w25Year, w25Month+1)

	w25r5AssertAsOfTies(t, leg, monthEnd)

	stamp, stamped, _, _ := w25r3MoveAnswers(t, leg, moveID)
	if !stamped {
		t.Fatalf("the count's move carries no wac_value_delta_cents stamp")
	}
	if stamp != w25r5Stamp {
		t.Fatalf("the count's move stamps %d, want the row's exact change %d (value %d)", stamp, w25r5Stamp, value)
	}
	if got := w25r3Journal1200(t, leg, "inventory.count.rounding:"+moveID); got != -1 {
		t.Fatalf("the count's rounding journal booked %d to 1200, want -1 (the cent the row lost beyond the movement)", got)
	}

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a fractional count that moved the row and 1200 together: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}

// TestW25FixR5FractionalTransferShipKeepsATiedMonthTied is the transfer
// ship writer: the month ties at its end, the next month loads 0.04 t onto a
// truck from 12.38 t on hand at the source, the source row moves by -181
// while the in-transit leg books the movement's 180, and the close of the
// earlier month still seals it.
func TestW25FixR5FractionalTransferShipKeepsATiedMonthTied(t *testing.T) {
	leg, monthEnd := w25r5World(t, "trfship", 12.38, false)
	ctx := context.Background()
	_, moveAt, _ := w25Instants(t, leg.pool)

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
		t.Fatalf("the transfer leg needs two branches, the seed holds %d", len(branches))
	}
	srcBranch, dstBranch = branches[0], branches[1]

	// The leg product leaves the tied branch; the source holds 12.38 t at
	// 4510 cents, the destination holds nothing.
	mustExec(t, ctx, leg.pool, `
		DELETE FROM stock_balances WHERE product_id = $1::uuid`, leg.productID)
	mustExec(t, ctx, leg.pool, `
		DELETE FROM product_branch_costs WHERE product_id = $1::uuid`, leg.productID)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, 12.38)`, leg.productID, srcBranch)
	mustExec(t, ctx, leg.pool, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, $3)`, leg.productID, srcBranch, w25r5Avg)
	// The company's stock value did not change: the row moved branches, so
	// the month still ties at its end.
	stock, err := leg.money.inventorySvc.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("the company's stock value: %v", err)
	}
	if stock != leg.stockCents {
		t.Fatalf("the company's stock value moved from %d to %d before the leg's movement", leg.stockCents, stock)
	}

	var userID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("a seeded user: %v", err)
	}
	tr, err := leg.money.inventorySvc.CreateTransfer(ctx, inventory.CreateTransferInput{
		FromBranchID: srcBranch, ToBranchID: dstBranch,
		Lines:     []inventory.CreateTransferLine{{ProductID: leg.productID, QtyBase: w25r5FracQty}},
		CreatedBy: userID,
	})
	if err != nil {
		t.Fatalf("create the transfer: %v", err)
	}
	if _, err := leg.money.inventorySvc.ShipTransfer(ctx, tr.ID, userID); err != nil {
		t.Fatalf("ship the transfer: %v", err)
	}

	var moveID string
	var value int64
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text, value_cents FROM stock_moves
		 WHERE source_ref_id = $1::uuid AND source_type = 'stock_transfer' AND to_location_id IS NULL`, tr.ID).Scan(&moveID, &value); err != nil {
		t.Fatalf("the transfer's outbound move: %v", err)
	}
	if value != w25r5Value {
		t.Fatalf("the outbound move values %d cents, want %d", value, w25r5Value)
	}
	w25r5RealignTransferShipMove(t, leg, tr.ID, moveID, moveAt)
	w25r3RealignJournals(t, leg, []string{
		"inventory.transfer_shipped:" + tr.ID,
		"inventory.transfer_shipped.rounding:" + moveID,
	}, moveAt, w25Year, w25Month+1)

	w25r5AssertAsOfTies(t, leg, monthEnd)

	stamp, stamped, _, _ := w25r3MoveAnswers(t, leg, moveID)
	if !stamped {
		t.Fatalf("the outbound move carries no wac_value_delta_cents stamp")
	}
	if stamp != w25r5Stamp {
		t.Fatalf("the outbound move stamps %d, want the row's exact change %d (value %d)", stamp, w25r5Stamp, value)
	}
	if got := w25r3Journal1200(t, leg, "inventory.transfer_shipped.rounding:"+moveID); got != -1 {
		t.Fatalf("the transfer ship's rounding journal booked %d to 1200, want -1 (the cent the source row lost beyond the movement)", got)
	}

	code, body := w25Close(t, leg, w25Year, w25Month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a fractional transfer ship that moved the row and 1200 together: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, w25Year, w25Month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}
}
