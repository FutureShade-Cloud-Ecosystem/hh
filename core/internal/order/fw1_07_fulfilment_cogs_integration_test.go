//go:build integration

package order

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFW1_07_FulfilmentRelievesStockAndPostsCOGS(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	var branchID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded user: %v", err)
	}

	suffix := fmt.Sprintf("fw107-%d", time.Now().UnixNano())
	var productID, custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg, is_active)
		VALUES ('SKU-'||$1, 'FW1-07 Fulfilment Stock Product', 'EA', 'EA', 2.0, true)
		RETURNING id::text`, suffix).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('CUST-'||$1, 'FW1-07 Customer', 'CONTRACTOR', 30, 5000000)
		RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}

	// Seed 10 EA on hand at 1200 cents ($12.00) branch average cost.
	const unitCostCents int64 = 1200
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, branch_id, location_id, on_hand_base, qty_reserved)
		VALUES ($1, $2, $2, 10, 0)`, productID, branchID); err != nil {
		t.Fatalf("insert stock_balances: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, $3)`, productID, branchID, unitCostCents); err != nil {
		t.Fatalf("insert product_branch_costs: %v", err)
	}

	var orderIDs []string
	plantOrder := func(tag, status, fulfillment string, qty float64) string {
		var ordID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			RETURNING id::text`,
			fmt.Sprintf("#ORD-%s-%s", tag, suffix), custID, branchID, status, fulfillment, int64(qty)*2500,
		).Scan(&ordID); err != nil {
			t.Fatalf("insert order %s: %v", tag, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
			VALUES ($1, 1, $2, 'FW1-07 Line', $3, 'EA', $3, 2500, 'EA', $4)`,
			ordID, productID, qty, int64(qty)*2500); err != nil {
			t.Fatalf("insert order_line %s: %v", tag, err)
		}
		orderIDs = append(orderIDs, ordID)
		return ordID
	}

	// 1. Delivered order (sits at INVOICED because the invoice finalized before POD).
	deliveredOrderID := plantOrder("DELIV", "INVOICED", "DELIVERY", 3)
	// 2. Collect-later order (sits at READY, then fulfilled at counter pickup).
	collectLaterOrderID := plantOrder("COLLECT", "READY", "PICKUP", 2)
	// 3. Cash On Demand (COD) order fulfilled via ship group completion.
	codOrderID := plantOrder("COD", "CONFIRMED", "DELIVERY", 1)

	t.Cleanup(func() {
		c := context.Background()
		for _, id := range orderIDs {
			key := "order.fulfilled:" + id
			_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key = $1`, key)
			_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_ref_id = $1::uuid`, id)
			_, _ = pool.Exec(c, `DELETE FROM shipment_groups WHERE order_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, id)
		}
		_, _ = pool.Exec(c, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM products WHERE id = $1`, productID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	invSvc := inventory.NewService(inventory.NewRepository(pool))
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	orderSvc := NewService(NewRepository(pool)).
		WithAudit(auditSvc).
		WithAuditTrail(auditSvc).
		WithDepositReleaser(billing.NewInvoiceWriter(pool))
	if withFulfiller, ok := any(orderSvc).(interface {
		WithStockFulfiller(*inventory.Service) *Service
	}); ok {
		orderSvc = withFulfiller.WithStockFulfiller(invSvc)
	}

	billingRepo := billing.NewRepository(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).
		WithInvoiceReader(billingRepo).
		WithCostBasis(invSvc)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	NewLifecycleConsumer(orderSvc, billingRepo, nil).RegisterHandlers(dispatcher)

	staffCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE", Branches: map[string]bool{branchID: true},
	})

	// Fulfil 1: Delivered order via AdvanceFromDelivery (invoice beat delivery, so ErrIllegalTransition is expected on status).
	if err := orderSvc.AdvanceFromDelivery(ctx, deliveredOrderID, "dispatch:pod-deliv"); err != nil && !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("AdvanceFromDelivery delivered order: %v", err)
	}

	// Fulfil 2: Collect-later order via SetStatus to FULFILLED.
	if err := orderSvc.SetStatus(staffCtx, "", userID, collectLaterOrderID, "FULFILLED", "counter pickup complete"); err != nil {
		t.Fatalf("SetStatus collect-later order: %v", err)
	}

	// Fulfil 3: Cash On Demand order via shipment group completion (GroupComplete).
	plan, err := orderSvc.PlanShipment(staffCtx, codOrderID, []ShipmentGroupInput{{
		Label: "COD Truck",
		Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 1}},
	}})
	if err != nil {
		t.Fatalf("PlanShipment COD: %v", err)
	}
	grpID := plan.Groups[0].ID
	for _, st := range []string{GroupPicking, GroupReady, GroupDispatched, GroupComplete} {
		if _, err := orderSvc.TransitionGroup(staffCtx, grpID, st); err != nil {
			t.Fatalf("TransitionGroup COD to %s: %v", st, err)
		}
	}

	drainOutbox(t, dispatcher)

	var onHand float64
	if err := pool.QueryRow(ctx, `SELECT on_hand_base::float8 FROM stock_balances WHERE product_id = $1 AND branch_id = $2`, productID, branchID).Scan(&onHand); err != nil {
		t.Fatalf("read on_hand_base: %v", err)
	}

	glNet := func(accountCode string) int64 {
		var net int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.idempotency_key IN ($1, $2, $3) AND a.code = $4`,
			"order.fulfilled:"+deliveredOrderID,
			"order.fulfilled:"+collectLaterOrderID,
			"order.fulfilled:"+codOrderID,
			accountCode,
		).Scan(&net); err != nil {
			t.Fatalf("glNet %s: %v", accountCode, err)
		}
		return net
	}

	got1200 := glNet("1200")
	got5000 := glNet("5000")
	const wantRelieved = 6.0
	const wantOnHand = 10.0 - wantRelieved
	const wantCOGSCents = int64(wantRelieved) * unitCostCents // 7200 cents

	if onHand != wantOnHand || got1200 != -wantCOGSCents || got5000 != wantCOGSCents {
		t.Fatalf("fulfilment stock & COGS mismatch: on_hand=%.2f (want %.2f), GL 1200 net=%d (want %d), GL 5000 net=%d (want %d)",
			onHand, wantOnHand, got1200, -wantCOGSCents, got5000, wantCOGSCents)
	}

	// Idempotency: re-running AdvanceFromDelivery on all three orders must not relieve stock or post COGS a second time.
	for _, id := range orderIDs {
		_ = orderSvc.AdvanceFromDelivery(ctx, id, "dispatch:redelivery")
	}
	drainOutbox(t, dispatcher)
	if err := pool.QueryRow(ctx, `SELECT on_hand_base::float8 FROM stock_balances WHERE product_id = $1 AND branch_id = $2`, productID, branchID).Scan(&onHand); err != nil {
		t.Fatalf("re-read on_hand_base: %v", err)
	}
	if onHand != wantOnHand || glNet("1200") != -wantCOGSCents {
		t.Fatalf("idempotency failed after redelivery: on_hand=%.2f (want %.2f), GL 1200=%d (want %d)",
			onHand, wantOnHand, glNet("1200"), -wantCOGSCents)
	}
}

func assertOrderFulfilmentHelper(_ *pgxpool.Pool) {}
