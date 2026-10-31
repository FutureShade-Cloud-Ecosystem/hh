//go:build integration

package pos

// fw1_07_round2_return_values_integration_test.go, round 2 (S3's P1): the two return events carry
// their OWN figures, so the journal posts the value that moved instead of a re-read average at the
// drain.
//
//   - order.returned carries the RETURN MOVES' own values: the goods that went back on the shelf,
//     valued when they went back.
//   - pos.return.written_off carries the ORIGINAL SALE's own moved value for those lines: the goods
//     were written off, not moved, so they have no move of their own and their cost is the cost they
//     left the shelf with.
//
// The average moves. A receipt lands between the return and the drain, the weighted average moves,
// and a journal that re-reads it says something different next week than it said on the day.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rvRestocker is the real inventory service behind the pos restock seam: the RETURN moves are real
// rows, so their values are real figures.
type rvRestocker struct{ svc *inventory.Service }

func (a rvRestocker) RestockForReturnTx(ctx context.Context, tx pgx.Tx, in RestockInput) error {
	lines := make([]inventory.SaleLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = inventory.SaleLine{ProductID: l.ProductID, QtyBase: l.QtyBase}
	}
	return a.svc.RestockForReturnTx(ctx, tx, inventory.RestockInput{
		ReturnID: in.ReturnID, OrderID: in.OrderID, BranchID: in.BranchID, ReturnNo: in.ReturnNo, Lines: lines,
	})
}

func (a rvRestocker) ReturnMoveValuesTx(ctx context.Context, tx pgx.Tx, returnID string) ([]inventory.ShipLineValue, error) {
	return a.svc.ReturnMoveValuesTx(ctx, tx, returnID)
}

func (a rvRestocker) ShippedValueForOrderLineTx(ctx context.Context, tx pgx.Tx, orderID, productID string, qtyBase float64) (inventory.ShipLineValue, bool, error) {
	return a.svc.ShippedValueForOrderLineTx(ctx, tx, orderID, productID, qtyBase)
}

// rvStock is the real inventory service behind the pos decrement seam: the ring's sale writes a real
// SHIP move, so the sale's own moved value is a real figure.
type rvStock struct{ svc *inventory.Service }

func (a rvStock) DecrementForSaleTx(ctx context.Context, tx pgx.Tx, in StockDecrementInput) error {
	_, err := a.DecrementForSaleValuesTx(ctx, tx, in)
	return err
}

func (a rvStock) DecrementForSaleValuesTx(ctx context.Context, tx pgx.Tx, in StockDecrementInput) ([]inventory.ShipLineValue, error) {
	lines := make([]inventory.SaleLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = inventory.SaleLine{ProductID: l.ProductID, QtyBase: l.QtyBase}
	}
	return a.svc.DecrementForSaleValuesTx(ctx, tx, inventory.DecrementInput{
		OrderID: in.OrderID, BranchID: in.BranchID, SaleType: in.SaleType, Lines: lines,
	})
}

type rvFixture struct {
	t         *testing.T
	pool      *pgxpool.Pool
	ctx       context.Context
	svc       *Service
	productID string
	branchID  string
	custID    string
	orders    []string
}

func newRVFixture(t *testing.T) *rvFixture {
	t.Helper()
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("rv-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('RV-'||$1, 'Return values proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	inv := inventory.NewService(inventory.NewRepository(pool))
	svc := realPosSvc(t, pool, stubCharger{}).
		WithRoleConfig(modifiedDefault(t, roleconfig.SettingRefundApprovalLimitEnabled, false)).
		WithRestocker(rvRestocker{svc: inv}).
		WithStock(rvStock{svc: inv}).
		WithOpenQuantityReleaser(order.NewRepository(pool))
	f := &rvFixture{t: t, pool: pool, ctx: ctx, svc: svc, productID: productID, branchID: branchID, custID: custID}
	// The sale and the return move the seeded product's stock balance rows. Capture them whole
	// and put them back last (row 0.39: the leg sweep fails a leg that leaves a seeded row changed).
	balanceImages := map[string]string{}
	brows, err := pool.Query(ctx, `
		SELECT location_id::text, row_to_json(s)::text FROM stock_balances s
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`, productID, branchID)
	if err != nil {
		t.Fatalf("capture the stock balances: %v", err)
	}
	for brows.Next() {
		var loc, img string
		if err := brows.Scan(&loc, &img); err != nil {
			brows.Close()
			t.Fatalf("scan the stock balances: %v", err)
		}
		balanceImages[loc] = img
	}
	brows.Close()
	if err := brows.Err(); err != nil {
		t.Fatalf("read the stock balances: %v", err)
	}
	// cost() upserts the seeded branch cost row for the product, so that row is captured too.
	var costImage string
	if err := pool.QueryRow(ctx, `
		SELECT row_to_json(c)::text FROM product_branch_costs c
		 WHERE product_id = $1::uuid AND branch_id = $2::uuid`, productID, branchID).Scan(&costImage); err != nil && err != pgx.ErrNoRows {
		t.Fatalf("capture the branch cost: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		if costImage != "" {
			if _, err := pool.Exec(c, `
				UPDATE product_branch_costs t SET avg_unit_cost_cents = r.avg_unit_cost_cents,
				       updated_at = r.updated_at, org_id = r.org_id
				  FROM json_populate_record(NULL::product_branch_costs, $3::json) r
				 WHERE t.product_id = $1::uuid AND t.branch_id = $2::uuid`,
				productID, branchID, costImage); err != nil {
				t.Errorf("restore the branch cost: %v", err)
			}
		}
		for loc, img := range balanceImages {
			if _, err := pool.Exec(c, `
				UPDATE stock_balances t SET on_hand_base = r.on_hand_base, allocated_base = r.allocated_base,
				       updated_at = r.updated_at, bin_code = r.bin_code, qty_reserved = r.qty_reserved,
				       org_id = r.org_id, seq = r.seq, deleted_at = r.deleted_at, idempotency_key = r.idempotency_key
				  FROM json_populate_record(NULL::stock_balances, $3::json) r
				 WHERE t.product_id = $1::uuid AND t.location_id = $2::uuid`,
				productID, loc, img); err != nil {
				t.Errorf("restore the stock balance at %s: %v", loc, err)
			}
		}
	})
	t.Cleanup(func() {
		c := context.Background()
		for _, oid := range f.orders {
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'order_id' = $1`, oid)
			_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_ref_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_return_lines WHERE return_id IN (SELECT id FROM pos_returns WHERE order_id = $1::uuid)`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_returns WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, oid)
		}
		_, _ = pool.Exec(c, `DELETE FROM return_notes WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
	})
	return f
}

// cost sets the branch's weighted average for the product, so the figures this leg reads are
// knowable to the cent.
func (f *rvFixture) cost(cents int64) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, $3)
		ON CONFLICT (product_id, branch_id) DO UPDATE SET avg_unit_cost_cents = EXCLUDED.avg_unit_cost_cents`,
		f.productID, f.branchID, cents); err != nil {
		f.t.Fatalf("set the branch cost: %v", err)
	}
}

func (f *rvFixture) ring(qty float64) (orderID, lineID string) {
	f.t.Helper()
	sale, err := f.svc.Sale(f.ctx, SaleInput{
		IdempotencyKey: key(), BranchID: f.branchID, CustomerID: f.custID,
		SaleType: SaleCollectNow, Tender: TenderAccount,
		Lines: []LineInput{{ProductID: f.productID, QtyEntered: qty, UOMEntered: "SF"}},
	})
	if err != nil {
		f.t.Fatalf("ring the sale: %v", err)
	}
	f.orders = append(f.orders, sale.OrderID)
	if err := f.pool.QueryRow(f.ctx, `SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`, sale.OrderID).Scan(&lineID); err != nil {
		f.t.Fatalf("the sale's line: %v", err)
	}
	return sale.OrderID, lineID
}

// eventValue reads one event line's own value_cents, and whether the line carries one at all.
func (f *rvFixture) eventValue(topic, returnID string) (int64, bool) {
	f.t.Helper()
	var raw *int64
	if err := f.pool.QueryRow(f.ctx, `
		SELECT (payload->'lines'->0->>'value_cents')::bigint
		  FROM events_outbox WHERE topic = $1 AND payload->>'return_id' = $2 LIMIT 1`, topic, returnID).Scan(&raw); err != nil {
		f.t.Fatalf("read the %s event: %v", topic, err)
	}
	if raw == nil {
		return 0, false
	}
	return *raw, true
}

// The restocked goods carry the RETURN MOVES' own values, and the written off goods carry the
// ORIGINAL SALE's own moved value for the lines they came off.
//
// THE RULED LEG (FROM-PRINCIPAL 146): sell at one average, move the average with a receipt, then
// write the return off. The write off equals the SALE's moved value, not the new average.
func TestFW1_07R2_TheReturnEventsCarryTheirOwnValues(t *testing.T) {
	f := newRVFixture(t)
	f.cost(700)

	// One sale, two units at 700: the sale moved 1400 worth of the goods.
	orderID, lineID := f.ring(2)

	// The average moves: a receipt lands at 900 a unit, the way a purchase at a new price does.
	// The sale's goods are still worth what the sale paid for them.
	f.cost(900)

	// Half come back damaged: written off, no move of their own.
	note, err := f.svc.ReturnAcross(f.ctx, ReturnAcrossInput{
		IdempotencyKey: key(), CustomerID: f.custID, RefundMethod: RefundAccountCredit, Reason: "damaged goods",
		Orders: []ReturnAcrossOrderInput{{OrderID: orderID, Lines: []ReturnLineInput{
			{OrderLineID: lineID, QtyBase: 1, ReasonCode: "DAMAGED", Disposition: DispositionDamaged},
		}}},
	})
	if err != nil {
		t.Fatalf("the damaged return: %v", err)
	}
	if len(note.Returns) != 1 {
		t.Fatalf("the note made %d returns, want 1", len(note.Returns))
	}
	got, found := f.eventValue("pos.return.written_off", note.Returns[0].ReturnID)
	if !found {
		t.Fatal("the write off event carries no value: the journal would re-read the average at the drain")
	}
	if got != 700 {
		t.Fatalf("the write off carries %d, want the SALE's own moved value for one unit (700), not the average now (%d)",
			got, 900)
	}
	// The sale's own move is the figure, read back from the books.
	var saleValue int64
	if err := f.pool.QueryRow(f.ctx, `
		SELECT value_cents FROM stock_moves
		 WHERE source_ref_id = $1::uuid AND move_type = 'SHIP' AND product_id = $2::uuid LIMIT 1`,
		orderID, f.productID).Scan(&saleValue); err != nil {
		t.Fatalf("read the sale's move: %v", err)
	}
	if saleValue != 1400 {
		t.Fatalf("the sale moved %d, want 1400 (two units at 700)", saleValue)
	}

	// The other unit comes back on the shelf: the RETURN move's own value rides the event.
	orderID2, lineID2 := f.ring(2)
	note2, err := f.svc.ReturnAcross(f.ctx, ReturnAcrossInput{
		IdempotencyKey: key(), CustomerID: f.custID, RefundMethod: RefundAccountCredit, Reason: "restock",
		Orders: []ReturnAcrossOrderInput{{OrderID: orderID2, Lines: []ReturnLineInput{
			{OrderLineID: lineID2, QtyBase: 1, ReasonCode: "OTHER", Disposition: DispositionRestock},
		}}},
	})
	if err != nil {
		t.Fatalf("the restock return: %v", err)
	}
	if len(note2.Returns) != 1 {
		t.Fatalf("the note made %d returns, want 1", len(note2.Returns))
	}
	got, found = f.eventValue("order.returned", note2.Returns[0].ReturnID)
	if !found {
		t.Fatal("the returned event carries no value: the journal would re-read the average at the drain")
	}
	// The move is the figure: the RETURN move's own value_cents is what the event carries.
	var moveValue int64
	if err := f.pool.QueryRow(f.ctx, `
		SELECT value_cents FROM stock_moves
		 WHERE source_type = 'pos_return' AND source_ref_id = $1::uuid AND move_type = 'RETURN' LIMIT 1`,
		note2.Returns[0].ReturnID).Scan(&moveValue); err != nil {
		t.Fatalf("read the RETURN move: %v", err)
	}
	if moveValue != got {
		t.Fatalf("the event carries %d but the RETURN move is %d: the event is not the move's own value", got, moveValue)
	}
}
