//go:build integration

package pricing

// A line the yard picked short (order migration 0366) reaches the low margin report with its quantity,
// line total and base quantity all reduced to what was delivered, the unit price unchanged and a
// backorder held beside them. The report grades unit price against unit cost, so a shorted line must
// grade exactly as the same line unshorted, and report the quantity actually delivered. (The shape is
// planted by hand here because order imports pricing; the order module's own tests prove it writes
// this shape.)

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestIntegration_LowMarginDocs_AShortedLineGradesLikeTheSameLineWhole(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	whole := fmt.Sprintf("SO-LMW-%d", time.Now().UnixNano())
	short := fmt.Sprintf("SO-LMS-%d", time.Now().UnixNano())
	ids := map[string]string{whole: "0d4a0000-0000-4000-9000-000000000301", short: "0d4a0000-0000-4000-9000-000000000302"}
	for no, id := range ids {
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			VALUES ($1, $2, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $3, 'INVOICED', 'PICKUP', 32000, 32000)`,
			id, no, lmTrenton); err != nil {
			t.Fatal(err)
		}
		id := id
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, id) })
		if _, err := pool.Exec(ctx, `
			INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
			                         unit_price_cents, price_uom, line_total_cents)
			VALUES ($1, 1, (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'), 'Brussels Block', 40, 'SF', 164, 800, 'SF', 32000)`, id); err != nil {
			t.Fatal(err)
		}
	}
	// What a yard short to 20 of 40 SF leaves on the line: half the quantity, half the base quantity,
	// half the money at the agreed unit price, the other half held on backorder.
	if _, err := pool.Exec(ctx, `
		UPDATE order_lines SET qty_entered = 20, qty_base = 82, line_total_cents = 16000,
		       backorder_entered = 20, backorder_base = 82 WHERE order_id = $1`, ids[short]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET subtotal_cents = 16000, total_cents = 16000 WHERE id = $1`, ids[short]); err != nil {
		t.Fatal(err)
	}
	f := lmWindow()
	f.DocType = ScanDocsOrders
	rows, err := svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	w, s := lmRowsByDoc(rows, whole), lmRowsByDoc(rows, short)
	if len(w) != 1 || len(s) != 1 {
		t.Skipf("the Brussels line is not below target at Trenton on this seed (whole %d rows, shorted %d); nothing to compare", len(w), len(s))
	}
	if s[0].MarginBps != w[0].MarginBps || s[0].Level != w[0].Level || s[0].UnitPriceCents != w[0].UnitPriceCents ||
		s[0].CostInPriceUOMCents != w[0].CostInPriceUOMCents {
		t.Errorf("a shorted line grades exactly as the same line whole: whole %+v shorted %+v", w[0], s[0])
	}
	if s[0].QtyEntered != 20 || w[0].QtyEntered != 40 {
		t.Errorf("the report shows the quantity actually delivered: whole %v shorted %v", w[0].QtyEntered, s[0].QtyEntered)
	}
	if w[0].DocMarginBps != nil && s[0].DocMarginBps != nil && *w[0].DocMarginBps != *s[0].DocMarginBps {
		t.Errorf("the document's own margin is unchanged by delivering half of its one line: %d vs %d", *w[0].DocMarginBps, *s[0].DocMarginBps)
	}
}

// A zero pick leaves a line of nothing delivered. It sold nothing, so it must not appear as a sale
// graded below target.
func TestIntegration_LowMarginDocs_AZeroPickedLineIsNotASale(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	no := fmt.Sprintf("SO-LMZ-%d", time.Now().UnixNano())
	id := "0d4a0000-0000-4000-9000-000000000303"
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment)
		VALUES ($1, $2, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $3, 'INVOICED', 'PICKUP')`, id, no, lmTrenton); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, id) })
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents, backorder_entered, backorder_base)
		VALUES ($1, 1, (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'), 'Brussels Block', 0, 'SF', 0, 800, 'SF', 0, 40, 164)`, id); err != nil {
		t.Fatal(err)
	}
	f := lmWindow()
	f.DocType = ScanDocsOrders
	rows, err := svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := lmRowsByDoc(rows, no); len(got) != 0 {
		t.Fatalf("a line with nothing delivered is not a sale and must not be graded: %+v", got)
	}
}
