//go:build integration

// fw1_07_round2_integration_test.go, PR 625 round 2's order side legs (S3's
// probes B and the section 4 holds): a ship group's completion relieves the
// group's own lines only, and an INVOICED order consumes its holds and
// relieves what left only when the goods have left.
package order

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
)

// TestFW1_07R2_ProbeB_TheGroupsCompletionRelievesTheGroupsOwnLines is probe
// B's leg: five planned on two trucks (2, then 3). Completing truck 1 relieves
// exactly 2 (today it relieves the whole order's 5 and every later group is a
// no op); truck 2's completion takes the rest.
func TestFW1_07R2_ProbeB_TheGroupsCompletionRelievesTheGroupsOwnLines(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var branchID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded user: %v", err)
	}
	suffix := fmt.Sprintf("fw107r2b-%d", time.Now().UnixNano())
	var productID, custID, orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg, is_active)
		VALUES ('SKU-'||$1, 'FW1-07 R2 Product', 'EA', 'EA', 1.0, true) RETURNING id::text`, suffix).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('CUST-'||$1, 'FW1-07 R2 Customer', 'CONTRACTOR', 30, 5000000) RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#R2B-'||$1, $2, $3, 'CONFIRMED', 'DELIVERY', 12500, 12500)
		RETURNING id::text`, suffix, custID, branchID).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1, 1, $2, 'R2 Line', 5, 'EA', 5, 2500, 'EA', 12500)`, orderID, productID); err != nil {
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
		_, _ = pool.Exec(c, `DELETE FROM shipment_groups WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	invSvc := inventory.NewService(inventory.NewRepository(pool))
	orderSvc := NewService(NewRepository(pool)).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil)).
		WithStockReserver(w5aInvReserver{svc: invSvc}).
		WithStockFulfiller(invSvc)
	staffCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE", Branches: map[string]bool{branchID: true},
	})

	plan, err := orderSvc.PlanShipment(staffCtx, orderID, []ShipmentGroupInput{
		{Label: "Truck 1", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 2}}},
		{Label: "Truck 2", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 3}}},
	})
	if err != nil {
		t.Fatalf("PlanShipment: %v", err)
	}
	onHand := func() float64 {
		t.Helper()
		var v float64
		if err := pool.QueryRow(ctx, `SELECT on_hand_base FROM stock_balances WHERE product_id=$1 AND branch_id=$2`, productID, branchID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if onHand() != 10 {
		t.Fatalf("on hand %v, want 10 before any completion", onHand())
	}
	for _, st := range []string{GroupPicking, GroupReady, GroupDispatched, GroupComplete} {
		if _, err := orderSvc.TransitionGroup(staffCtx, plan.Groups[0].ID, st); err != nil {
			t.Fatalf("truck 1 to %s: %v", st, err)
		}
	}
	if got := onHand(); got != 8 {
		t.Fatalf("after truck 1's completion on hand = %v, want 8 (only the group's own 2 relieved, never the order's 5)", got)
	}
	for _, st := range []string{GroupPicking, GroupReady, GroupDispatched, GroupComplete} {
		if _, err := orderSvc.TransitionGroup(staffCtx, plan.Groups[1].ID, st); err != nil {
			t.Fatalf("truck 2 to %s: %v", st, err)
		}
	}
	if got := onHand(); got != 5 {
		t.Fatalf("after truck 2's completion on hand = %v, want 5 (truck 2's own 3; the first group never twice)", got)
	}
}

// TestFW1_07R2_InvoicedConsumesHoldsOnlyWhenTheGoodsHaveLeft is section 4's
// leg: an order whose goods left (its SHIP moves exist) consumes its holds and
// relieves what left at INVOICED; an order still waiting on the yard keeps
// both.
func TestFW1_07R2_InvoicedConsumesHoldsOnlyWhenTheGoodsHaveLeft(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var branchID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded user: %v", err)
	}
	suffix := fmt.Sprintf("fw107r2i-%d", time.Now().UnixNano())
	var productID, custID, waitedOrderID, leftOrderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg, is_active)
		VALUES ('SKU-'||$1, 'FW1-07 R2 Hold Product', 'EA', 'EA', 1.0, true) RETURNING id::text`, suffix).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('CUST-'||$1, 'FW1-07 R2 Holds Customer', 'CONTRACTOR', 30, 5000000) RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatal(err)
	}
	plant := func(tag, status string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			VALUES ('#R2I-'||$1, $2, $3, $4, 'DELIVERY', 5000, 5000)
			RETURNING id::text`, fmt.Sprintf("%s-%s", suffix, tag), custID, branchID, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
			VALUES ($1, 1, $2, 'R2 Holds Line', 2, 'EA', 2, 2500, 'EA', 5000)`, id, productID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO stock_reservations (product_id, branch_id, location_id, qty_base, res_type, hold_mode, source_type, source_ref_id, status)
			VALUES ($2, $3, $3, 2, 'ORDER', 'HARD', 'order', $1, 'ACTIVE')`,
			id, productID, branchID); err != nil {
			t.Fatalf("plant the hold: %v", err)
		}
		return id
	}
	waitedOrderID = plant("WAITED", "CONFIRMED")
	leftOrderID = plant("LEFT", "CONFIRMED")
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, branch_id, location_id, on_hand_base, qty_reserved)
		VALUES ($1, $2, $2, 10, 4)`, productID, branchID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range []string{waitedOrderID, leftOrderID} {
			_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key LIKE 'order.fulfilled:'||$1`, id)
			_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key LIKE 'order.fulfilled:'||$1`, id)
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_ref_id = $1::uuid`, id)
			_, _ = pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, id)
		}
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	invSvc := inventory.NewService(inventory.NewRepository(pool))
	orderSvc := NewService(NewRepository(pool)).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil)).
		WithStockReserver(w5aInvReserver{svc: invSvc}).
		WithStockFulfiller(invSvc)
	staffCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE", Branches: map[string]bool{branchID: true},
	})

	// The waited order's goods never left: INVOICED keeps the holds.
	if err := orderSvc.SetStatus(staffCtx, "", userID, waitedOrderID, "INVOICED", "round 2 invoiced, goods waiting"); err != nil {
		t.Fatalf("invoice the waited order: %v", err)
	}
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_reservations WHERE source_ref_id=$1::uuid AND status='ACTIVE'`, waitedOrderID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("the waited order's holds = %d ACTIVE, want 1 kept (the goods have not left)", active)
	}

	// The left order's goods left (its ship move exists): INVOICED consumes the
	// holds and relieves what left.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := invSvc.FulfilOrderLinesTx(ctx, tx, leftOrderID, branchID, []string{productID}, []float64{2}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := orderSvc.SetStatus(staffCtx, "", userID, leftOrderID, "INVOICED", "round 2 invoiced, goods gone"); err != nil {
		t.Fatalf("invoice the left order: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_reservations WHERE source_ref_id=$1::uuid AND status='ACTIVE'`, leftOrderID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("the left order's holds = %d ACTIVE, want 0 consumed (the goods have left)", active)
	}
}
