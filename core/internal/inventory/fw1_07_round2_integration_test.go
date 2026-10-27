//go:build integration

// fw1_07_round2_integration_test.go, PR 625 round 2's red first legs (S3's
// probes A and C): the COGS journal posts the STOCK MOVE's own value, carried
// on the order.fulfilled event, never a re-read branch average at the drain;
// and the pre receipt true up corrects a provisional sale exactly once.
package inventory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_07R2Fixture plants one product, one customer, one order and the stock
// and cost rows for a branch, answering the order id.
func fw1_07R2Fixture(t *testing.T, pool *pgxpool.Pool, tag string, onHand int64, unitCost int64) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	productID := mkProduct(t, pool, "FW107R2-"+tag)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, branch_id, location_id, on_hand_base, qty_reserved)
		VALUES ($1, $2, $2, $3, 0)`, productID, branch1, onHand); err != nil {
		t.Fatalf("seed stock_balances: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, $3)`, productID, branch1, unitCost); err != nil {
		t.Fatalf("seed product_branch_costs: %v", err)
	}
	var custID, orderID string
	suffix := fmt.Sprintf("fw107r2-%s-%d", tag, time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ('CUST-'||$1, 'FW1-07 R2 Customer')
		RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#R2-'||$1, $2, $3, 'FULFILLED', 'PICKUP', 5000, 5000)
		RETURNING id::text`, suffix, custID, branch1).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1, 1, $2, 'FW1-07 R2 Line', 2, 'EA', 2, 2500, 'EA', 5000)`, orderID, productID); err != nil {
		t.Fatalf("seed order line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key LIKE 'order.fulfilled:'||$1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key LIKE 'order.fulfilled:'||$1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key LIKE 'pre_receipt_trueup:%'`)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key LIKE 'pre_receipt_trueup:%'`)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic IN ('order.fulfilled','po.received') AND (payload->>'order_id' = $1 OR payload->>'po_id' IN (SELECT id::text FROM purchase_orders WHERE po_no LIKE 'PO-R2-'||$2))`, orderID, tag)
		_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_ref_id = $1::uuid OR product_id = $2`, orderID, productID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})
	return productID, custID, orderID
}

// TestFW1_07R2_ProbeA_TheJournalPostsTheMoveValueNotTheDrainAverage is probe
// A's leg: 10 on hand at $10.00, two fulfilled (the move carries $20.00), and
// a real receipt of 10 at $30.00 landing BEFORE the drain. The COGS journal
// must debit the move's $20.00, never the blended average at the drain.
func TestFW1_07R2_ProbeA_TheJournalPostsTheMoveValueNotTheDrainAverage(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	productID, custID, orderID := fw1_07R2Fixture(t, pool, "PROBEA", 10, 1000)

	poID, _ := mkPO(t, pool, "PO-R2-PROBEA", "CAD", 1.0, []poLineSpec{
		{productID: productID, qtyBase: 10, unitCost: 3000},
	})
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.source_ref_id IN (SELECT id FROM po_receipts WHERE po_id = $1::uuid)`, poID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id IN (SELECT id FROM po_receipts WHERE po_id = $1::uuid)`, poID)
		_, _ = pool.Exec(c, `DELETE FROM po_receipts WHERE po_id = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM po_lines WHERE po_id = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM purchase_orders WHERE id = $1`, poID)
	})

	invSvc := newIntSvc(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithCostBasis(invSvc)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	invSvc.RegisterPostingHandlers(dispatcher)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := invSvc.FulfilOrderLinesTx(ctx, tx, orderID, branch1, []string{productID}, []float64{2}); err != nil {
		t.Fatalf("FulfilOrderLinesTx: %v", err)
	}
	// The receipt lands BEFORE the drain: the average moves while the event waits.
	if err := invSvc.ReceivePOTx(ctx, tx, poID); err != nil {
		t.Fatalf("ReceivePOTx before the drain: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	for i := 0; i < 8; i++ {
		if dispatcher.DrainOnce(ctx) == 0 {
			break
		}
	}

	var cogs int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.debit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code = '5000'`, "order.fulfilled:"+orderID).Scan(&cogs); err != nil {
		t.Fatal(err)
	}
	if cogs != 2000 {
		t.Fatalf("COGS = %d, want the move's own 2000 (two at $10.00), never the blended average at the drain", cogs)
	}
	var drift int64
	if err := pool.QueryRow(ctx, `
		SELECT ABS(COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)),0))
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code IN ('5000','1200')`, "order.fulfilled:"+orderID).Scan(&drift); err != nil {
		t.Fatal(err)
	}
	if drift != 0 {
		t.Fatalf("the COGS journal drifts the stock ledger by %d", drift)
	}
	_ = custID
}

// TestFW1_07R2_ProbeC_TheTrueUpCorrectsAProvisionalSaleOnce is probe C's leg:
// two sold at the catalogue cost $8.00 with no stock on hand, the first receipt
// of five at $10.50, then the drain. The sale's COGS posts the move's
// provisional $16.00 and the true up adds the $5.00 once: $21.00 for goods
// that cost $21.00, never $26.00.
func TestFW1_07R2_ProbeC_TheTrueUpCorrectsAProvisionalSaleOnce(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	productID, _, orderID := fw1_07R2Fixture(t, pool, "PROBEC", 0, 0)
	if _, err := pool.Exec(ctx, `
		UPDATE products SET metadata = '{"catalogue_cost_cents": 800}'::jsonb WHERE id = $1`, productID); err != nil {
		t.Fatal(err)
	}
	poID, _ := mkPO(t, pool, "PO-R2-PROBEC", "CAD", 1.0, []poLineSpec{
		{productID: productID, qtyBase: 5, unitCost: 1050},
	})
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.source_ref_id IN (SELECT id FROM po_receipts WHERE po_id = $1::uuid)`, poID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id IN (SELECT id FROM po_receipts WHERE po_id = $1::uuid)`, poID)
		_, _ = pool.Exec(c, `DELETE FROM po_receipts WHERE po_id = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM po_lines WHERE po_id = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM purchase_orders WHERE id = $1`, poID)
	})

	invSvc := newIntSvc(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithCostBasis(invSvc)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	invSvc.RegisterPostingHandlers(dispatcher)

	// The sale and the receipt both land BEFORE the drain (S3's order): the
	// sale's journal must post the move's provisional value, and the true up
	// corrects it once.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := invSvc.FulfilOrderLinesTx(ctx, tx, orderID, branch1, []string{productID}, []float64{2}); err != nil {
		t.Fatalf("FulfilOrderLinesTx: %v", err)
	}
	if err := invSvc.ReceivePOTx(ctx, tx, poID); err != nil {
		t.Fatalf("ReceivePOTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if dispatcher.DrainOnce(ctx) == 0 {
			break
		}
	}

	var saleCOGS int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.debit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code = '5000'`, "order.fulfilled:"+orderID).Scan(&saleCOGS); err != nil {
		t.Fatal(err)
	}
	var rcptID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM po_receipts WHERE po_id = $1 LIMIT 1`, poID).Scan(&rcptID); err != nil {
		t.Fatal(err)
	}
	var trueUp int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.debit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code = '5000'`, "pre_receipt_trueup:"+rcptID+":"+productID).Scan(&trueUp); err != nil {
		t.Fatal(err)
	}
	if saleCOGS != 1600 {
		t.Fatalf("the sale's COGS = %d, want the move's provisional 1600 (two at the catalogue $8.00)", saleCOGS)
	}
	if trueUp != 500 {
		t.Fatalf("the true up = %d, want 500 (two at $10.50 less two at $8.00), once", trueUp)
	}
	var totalCOGS int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE (j.idempotency_key = $1 OR j.idempotency_key = $2) AND a.code = '5000'`,
		"order.fulfilled:"+orderID, "pre_receipt_trueup:"+rcptID+":"+productID).Scan(&totalCOGS); err != nil {
		t.Fatal(err)
	}
	if totalCOGS != 2100 {
		t.Fatalf("total COGS = %d, want 2100 for goods that cost $21.00 (never doubled)", totalCOGS)
	}
}
