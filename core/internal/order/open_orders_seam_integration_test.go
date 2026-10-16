//go:build integration

// open_orders_seam_integration_test.go, the order module's numbers-for-orders read (change 5,
// slice S3), end to end against a live Postgres.
//
// The inventory shortfall worklist names the open sales orders a shortfall holds up, through this
// module: it is the sole reader of orders + order_lines, so the "which orders wait on this
// product" question is answered here and handed over as numbers, not as a second module reaching
// into the 0008 tables. The legs pin the whole predicate: open statuses only, lines not yet fully
// fulfilled or released, the named branch only, the named products only, oldest first.
//
// Run: DATABASE_URL=... go test -tags=integration ./internal/order/... -run OpenSaleOrders -count=1

package order

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedShortOrdersFixture plants one customer, one product off the seeded catalogue, and five orders
// around it: three open ones at Trenton with staggered created_at, one CLOSED and one CANCELLED at
// Trenton, one open at Kingston, and one open Trenton order whose line is already fully fulfilled.
// It hands back the open Trenton order ids oldest first and cleans everything up.
func seedShortOrdersFixture(t *testing.T, pool *pgxpool.Pool) (cust, product string, openOrderNos []string) {
	t.Helper()
	ctx := context.Background()
	nano := time.Now().UnixNano()

	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'S3 Short Orders Tenant') RETURNING id::text`,
		fmt.Sprintf("D-S3SO-%d", nano)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// Registered before anything else is planted, so a failed plant still cleans up after itself
	// (the shared gate database must not keep a dead fixture's orders).
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM products ORDER BY id LIMIT 1`).Scan(&product); err != nil {
		t.Fatalf("pick a seeded product: %v", err)
	}

	plant := func(no, status, branch string, age time.Duration, fulfilled float64) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents, created_at)
			 VALUES ($1, $2, $3::uuid, $4, 'DELIVERY', 1000, 1000, $5) RETURNING id::text`,
			no, cust, branch, status, time.Now().Add(-age)).Scan(&id); err != nil {
			t.Fatalf("seed order %s: %v", no, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
			                          qty_base, fulfilled_base, unit_price_cents, price_uom, line_total_cents)
			 SELECT $1::uuid, 1, $2::uuid, 'S3 seeded line', 5, p.base_uom, 5, $3, 100, p.base_uom, 500
			   FROM products p WHERE p.id = $2::uuid`, id, product, fulfilled); err != nil {
			t.Fatalf("seed line for %s: %v", no, err)
		}
		return id
	}

	// The three open Trenton orders, oldest first.
	nos := []string{
		fmt.Sprintf("#S3SO-%d-1", nano),
		fmt.Sprintf("#S3SO-%d-2", nano),
		fmt.Sprintf("#S3SO-%d-3", nano),
	}
	plant(nos[0], "CONFIRMED", trentonBranch, 72*time.Hour, 0)
	plant(nos[1], "PARTIALLY_FULFILLED", trentonBranch, 48*time.Hour, 0)
	plant(nos[2], "CONFIRMED", trentonBranch, 24*time.Hour, 0)
	// The ones the read must never name.
	plant(fmt.Sprintf("#S3SO-%d-closed", nano), "CLOSED", trentonBranch, 60*time.Hour, 0)
	plant(fmt.Sprintf("#S3SO-%d-cancelled", nano), "CANCELLED", trentonBranch, 50*time.Hour, 0)
	plant(fmt.Sprintf("#S3SO-%d-fulfilled", nano), "CONFIRMED", trentonBranch, 40*time.Hour, 5)
	plant(fmt.Sprintf("#S3SO-%d-kgn", nano), "CONFIRMED", kingstonBranch, 10*time.Hour, 0)

	return cust, product, nos
}

// TestIntegration_OpenSaleOrdersFor_NamesTheOpenOnesOldestFirst is the seam's own acceptance: the
// numbers come back oldest first, and the closed, cancelled, fulfilled and other-branch orders stay
// out.
func TestIntegration_OpenSaleOrdersFor_NamesTheOpenOnesOldestFirst(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	_, product, wantNos := seedShortOrdersFixture(t, pool)

	rows, err := repo.OpenSaleOrdersFor(context.Background(), trentonBranch, []string{product})
	if err != nil {
		t.Fatalf("OpenSaleOrdersFor: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ProductID != product {
			t.Errorf("the read named a line for product %q, asked about %q", r.ProductID, product)
		}
		if r.BranchID != trentonBranch {
			t.Errorf("the read named an order at branch %q, asked about Trenton", r.BranchID)
		}
		got = append(got, r.OrderNo)
	}
	if len(got) != len(wantNos) {
		t.Fatalf("the read named %v, want exactly the three open Trenton orders %v", got, wantNos)
	}
	for i := range wantNos {
		if got[i] != wantNos[i] {
			t.Fatalf("the numbers are %v, want the oldest first %v", got, wantNos)
		}
	}

	// A nil product list asks about every product with something outstanding; the fixture's orders
	// are still the only ones this customer holds, and the branch fence still holds.
	every, err := repo.OpenSaleOrdersFor(context.Background(), trentonBranch, nil)
	if err != nil {
		t.Fatalf("OpenSaleOrdersFor every product: %v", err)
	}
	if len(every) < len(wantNos) {
		t.Fatalf("an every-product ask answered %d rows, want at least the fixture's %d", len(every), len(wantNos))
	}
	for _, r := range every {
		if r.BranchID != trentonBranch {
			t.Fatalf("the every-product ask named branch %q into a Trenton question", r.BranchID)
		}
	}
}

// TestIntegration_OpenSaleOrdersFor_AnEmptyProductListIsNotAWildcard pins the seam's narrow read:
// an EMPTY (non-nil) product list names no product, so it answers nothing rather than everything.
func TestIntegration_OpenSaleOrdersFor_AnEmptyProductListIsNotAWildcard(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	_, product, _ := seedShortOrdersFixture(t, pool)

	rows, err := repo.OpenSaleOrdersFor(context.Background(), trentonBranch, []string{product})
	if err != nil || len(rows) == 0 {
		t.Fatalf("the named-product ask answered %d rows, err %v", len(rows), err)
	}
	empty, err := repo.OpenSaleOrdersFor(context.Background(), trentonBranch, []string{})
	if err != nil {
		t.Fatalf("the empty-list ask failed: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("an empty product list answered %d rows, want none (it names no product)", len(empty))
	}
}

// TestIntegration_OpenSaleOrdersFor_ServicePassthrough, the service method is the seam's door and
// answers exactly what the repository answers.
func TestIntegration_OpenSaleOrdersFor_ServicePassthrough(t *testing.T) {
	pool := intPool(t)
	svc := NewService(NewRepository(pool))
	_, product, wantNos := seedShortOrdersFixture(t, pool)

	rows, err := svc.OpenSaleOrdersFor(context.Background(), trentonBranch, []string{product})
	if err != nil {
		t.Fatalf("OpenSaleOrdersFor: %v", err)
	}
	if len(rows) != len(wantNos) {
		t.Fatalf("the service named %d orders, want %d", len(rows), len(wantNos))
	}
	if rows[0].OrderNo != wantNos[0] {
		t.Fatalf("the service's first number is %q, want the oldest %q", rows[0].OrderNo, wantNos[0])
	}
}
