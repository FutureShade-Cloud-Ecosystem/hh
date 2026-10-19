//go:build integration

// order_change_applied_integration_test.go, HH-681: the REAL composition root wires the change order apply.
//
// The order module's unit and module legs inject their own seams. This one builds the served graph the way
// production does (registerRoutes), signs and applies an approved change on a taxed order through the order
// service the graph holds, and asserts what only the root can get wrong: the tax engine seam and the dealer
// zone are bound (the tax is re-assessed, not refused), exactly one order.change.applied row is written on the
// apply's transaction, and the worker's owner registration settles it instead of dead lettering it.
//
//	Run: HH_CORE_TEST_DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run OrderChangeApplied -count=1 -p 1
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_OrderChangeApplied_TheRootWiresTaxEventAndOwner(t *testing.T) {
	env := newCompEnv(t)
	_, graph := registeredSurface(t, env)
	pool := graph.pool
	ctx := context.Background()
	tag := fmt.Sprintf("HH681-%d", time.Now().UnixNano())

	qd5KeepStockRow(t, pool, testkit.ProductID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, allocated_base)
		VALUES ($1, $2, $2, 1000, 0)
		ON CONFLICT (product_id, location_id) DO UPDATE SET on_hand_base = GREATEST(stock_balances.on_hand_base, 1000)`,
		testkit.ProductID, testkit.BranchNorthAID); err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	var cust, ord, changeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1::text,$1::text,'CONTRACTOR') RETURNING id::text`,
		tag).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// 13 percent standard HST on 10000: 1300, the tax a quote conversion would have copied.
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, org_id, status, fulfillment,
		                    subtotal_cents, tax_cents, total_cents)
		VALUES ('#'||$1::text, $2::uuid, $3::uuid, $4::uuid, 'PICKING', 'PICKUP', 10000, 1300, 11300)
		RETURNING id::text`, tag, cust, testkit.BranchNorthAID, testkit.OrgNorthID).Scan(&ord); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'HH-681 line', 10, 'EA', 10, 1000, 'EA', 10000)`, ord, testkit.ProductID); err != nil {
		t.Fatalf("seed line: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO change_orders (order_id, branch_id, change_no, status, reason, delta_cents,
		                           finance_required, order_status_at_request, pick_started_at_request, requested_by)
		VALUES ($1::uuid, $2::uuid, '#CO-' || nextval('change_order_no_seq'), 'APPROVED', 'HH-681', 2000,
		        false, 'PICKING', true, $3::uuid) RETURNING id::text`,
		ord, testkit.BranchNorthAID, testkit.UserSalesMgrID).Scan(&changeID); err != nil {
		t.Fatalf("seed change: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO change_order_lines (change_order_id, seq, op, line_no, product_id, qty_entered_new, uom_entered,
		                                qty_base_before, qty_base_after, qty_delta_base, unit_price_cents, price_uom,
		                                line_delta_cents)
		VALUES ($1::uuid, 1, 'QTY', 1, $2::uuid, 12, 'EA', 10, 12, 2, 1000, 'EA', 2000)`, changeID, testkit.ProductID); err != nil {
		t.Fatalf("seed change line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'order_id' = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM change_orders WHERE order_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, ord)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, cust)
	})

	mgr := identity.StaffContext{
		UserID: testkit.UserSalesMgrID, RoleCode: "SALES_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{testkit.BranchNorthAID: true},
	}
	key := "33333333-3333-4333-8333-333333333333"
	svcCtx := identity.ContextWithStaff(ctx, mgr)
	out, err := graph.money.orderSvc.SignApplyChange(svcCtx, changeID, key, "")
	if err != nil {
		t.Fatalf("sign and apply through the root's order service = %v: the tax seam, the zone or the apply is not wired", err)
	}
	if out.Status != order.ChangeStatusApplied {
		t.Fatalf("status = %q, want APPLIED", out.Status)
	}
	var sub, tax, total int64
	if err := pool.QueryRow(ctx, `SELECT subtotal_cents, tax_cents, total_cents FROM orders WHERE id = $1::uuid`, ord).
		Scan(&sub, &tax, &total); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if sub != 12000 || tax != 1560 || total != 13560 {
		t.Fatalf("order money = %d / %d / %d, want 12000 / 1560 / 13560 (13 percent from the real tax engine)", sub, tax, total)
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic = $1 AND payload->>'change_id' = $2`,
		order.ChangeAppliedTopic, changeID).Scan(&rows); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if rows != 1 {
		t.Fatalf("%d %s rows after the apply, want exactly 1: the root did not wire the event seam", rows, order.ChangeAppliedTopic)
	}

	// The worker's owner registration settles the row rather than dead lettering it.
	d := notify.NewDispatcher(graph.money.outboxStore, graph.log)
	registerOrderEventOwners(d, graph.log)
	for i := 0; i < 20 && d.DrainOnce(ctx) > 0; i++ {
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM events_outbox WHERE topic = $1 AND payload->>'change_id' = $2`,
		order.ChangeAppliedTopic, changeID).Scan(&status); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	if status != "PUBLISHED" {
		t.Fatalf("event status = %q after the worker's owner drained it, want PUBLISHED", status)
	}
}

// TestOrderChangeApplied_TheWorkerRegistersTheOwner pins the one line the composition test above cannot
// reach: the worker itself calls registerOrderEventOwners before it runs the dispatcher.
func TestOrderChangeApplied_TheWorkerRegistersTheOwner(t *testing.T) {
	src, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatalf("read worker.go: %v", err)
	}
	if !strings.Contains(string(src), "registerOrderEventOwners(dispatcher, log)") {
		t.Fatal("worker.go does not register the order event owner: order.change.applied rows would be dead lettered")
	}
}
