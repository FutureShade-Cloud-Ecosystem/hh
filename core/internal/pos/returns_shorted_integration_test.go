//go:build integration

package pos

// A return of a line the yard picked short refunds what the customer was charged for it: the line's
// money and its tax both follow the short, so a full return pays back the reduced total plus the
// reduced tax and nothing more.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

func TestIntegration_ReturnOfAShortedLineRefundsWhatWasCharged(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var cust, ord, lineID string
	if err := pool.QueryRow(ctx, `INSERT INTO customers (account_no, name) VALUES ($1, 'Shorted return customer') RETURNING id::text`,
		fmt.Sprintf("SRT-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatalf("customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, tax_cents, total_cents)
SELECT $1, $2, id, 'CONFIRMED', 'DELIVERY', 5000, 650, 5650 FROM locations WHERE name ILIKE '%trenton%' LIMIT 1
RETURNING id::text`, fmt.Sprintf("#SRT-%d", time.Now().UnixNano()), cust).Scan(&ord); err != nil {
		t.Fatalf("order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_shortfall_basis WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, ord)
	})
	if err := pool.QueryRow(ctx, `
INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
                         unit_price_cents, price_uom, line_total_cents, tax_cents)
SELECT $1, 1, p.id, 'shorted line', 5, p.base_uom, 5, 1000, p.base_uom, 5000, 650 FROM products p ORDER BY p.sku LIMIT 1
RETURNING id::text`, ord).Scan(&lineID); err != nil {
		t.Fatalf("line: %v", err)
	}

	osvc := order.NewService(order.NewRepository(pool))
	if _, err := osvc.ApplyYardPicks(ctx, order.YardPickInput{OrderID: ord, ActorID: "", Lines: []order.YardPickLineInput{
		{LineNo: 1, QtyPicked: "3", Reason: "CONDENSED_SKIDS", Note: "test"}}}); err != nil {
		t.Fatalf("short: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rl, ok, err := NewRepository(pool).LockReturnableLine(ctx, tx, ord, lineID)
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	// What a full return pays: the line money plus the line tax. The customer was charged
	// 3000 + 390 for 3, not the 3000 + 650 the line carried before.
	if rl.QtySold != 3 || rl.LineTotalCents != 3000 || rl.TaxCents != 390 {
		t.Fatalf("a full return of the shorted line refunds what was charged: qty %.4f money %d tax %d", rl.QtySold, rl.LineTotalCents, rl.TaxCents)
	}
}
