//go:build integration

package order

// fw1_07_round2_ring_base_integration_test.go, round 2 (the census item): the POS ring's
// fulfilled_base advance belongs to the ORDER MODULE'S OWN CONSUMER.
//
// The ring's COLLECT_NOW sale moves the stock without the order module's help, so the base the
// order's own claim would have advanced never advanced: a ring sale's order sat FULFILLED with its
// lines still reading unfulfilled. The ring may not write order_lines (another module's table, and
// a census adjustment that lets it is not the answer), so the ORDER MODULE reads the ring's own
// event and advances its own counts.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// The ring's event advances the order's line base, and the advance is the order module's own.
func TestFW1_07R2_TheRingsEventAdvancesTheOrdersOwnLineBase(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var branchID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded user: %v", err)
	}
	suffix := fmt.Sprintf("fw107r2ring-%d", time.Now().UnixNano())
	var productID, custID, orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg, is_active)
		VALUES ('SKU-'||$1, 'FW1-07 R2 Ring Product', 'EA', 'EA', 1.0, true) RETURNING id::text`, suffix).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('CUST-'||$1, 'FW1-07 R2 Ring Customer', 'CONTRACTOR', 30, 5000000) RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatal(err)
	}
	// A PICKUP order, the ring's own shape: the customer collects at the counter.
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#R2R-'||$1, $2, $3, 'CONFIRMED', 'PICKUP', 8000, 8000)
		RETURNING id::text`, suffix, custID, branchID).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1, 1, $2, 'R2 Ring Line', 4, 'EA', 4, 2000, 'EA', 8000)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, branch_id, location_id, on_hand_base, qty_reserved)
		VALUES ($1, $2, $2, 10, 0)`, productID, branchID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, 1000)`, productID, branchID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key LIKE 'order.fulfilled:'||$1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key LIKE 'order.fulfilled:'||$1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_ref_id = $1::uuid`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	// The ring's own SHIP move: the stock really left, so the order's own relief is a no op and
	// the base is the order module's to advance.
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_moves (product_id, branch_id, from_location_id, to_location_id, qty_base, qty_entered,
		                         uom_entered, move_type, unit_cost_cents, value_cents, source_type, source_ref_id, reason)
		VALUES ($1, $2, $2, NULL, 4, 4, 'EA', 'SHIP', 1000, 4000, 'pos_sale', $3, 'POS COLLECT_NOW sale')`,
		productID, branchID, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO events_outbox (topic, payload) VALUES ('order.fulfilled', $1)`, map[string]any{
		"order_id": orderID, "branch_id": branchID,
		"lines": []map[string]any{{"product_id": productID, "qty_base": 4.0, "value_cents": 4000}},
	}); err != nil {
		t.Fatal(err)
	}

	invSvc := inventory.NewService(inventory.NewRepository(pool))
	orderSvc := NewService(NewRepository(pool)).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil)).
		WithStockReserver(w5aInvReserver{svc: invSvc}).
		WithStockFulfiller(invSvc)
	billingRepo := billing.NewRepository(pool)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(billingRepo).WithCostBasis(invSvc).RegisterPostingHandlers(dispatcher)
	NewLifecycleConsumer(orderSvc, billingRepo, nil).RegisterHandlers(dispatcher)
	drainOutbox(t, dispatcher)

	var base float64
	if err := pool.QueryRow(ctx, `SELECT fulfilled_base::float8 FROM order_lines WHERE order_id = $1`, orderID).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if base != 4 {
		t.Fatalf("the ring's sale left 4 units and the order's line base reads %v, want 4: the advance is the order module's own", base)
	}
}
