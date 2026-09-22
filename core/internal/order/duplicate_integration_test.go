//go:build integration

// DIB-489/490 integration tests: the real pgx duplicate repository against the live Postgres that
// `make reset` provisions (the reorder_integration_test.go approach, same intPool, same seeded TRT
// branch). Proves the SQL-level properties the unit fakes cannot:
//
//   - the advisory-lock idempotent duplicate-draft write under concurrency, with staff attribution
//     (orders.created_by) and the staff-duplicate provenance metadata;
//   - the DIB-490 candidate read: window, status, tenant and PROVENANCE exclusions evaluated by the
//     actual query against real rows.
package order

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedDupCustomer provisions an isolated tenant; rows cascade-cleaned on test end.
func seedDupCustomer(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	ctx := context.Background()
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, $2) RETURNING id::text`,
		fmt.Sprintf("DUP-%s-%d", label, time.Now().UnixNano()), "Duplicate Int "+label).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust
}

// seedDupProduct provisions one catalogue product for overlap lines.
func seedDupProduct(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom, is_active)
		 VALUES ($1, $2, 'EA', 'EA', TRUE) RETURNING id::text`,
		fmt.Sprintf("DUP-%s-%d", label, time.Now().UnixNano()), "Dup product "+label).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// products is ON DELETE RESTRICT from order_lines and t.Cleanup runs LIFO (products before the
	// customers' orders), so drop this product's test lines first.
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM order_lines WHERE product_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = $1`, id)
	})
	return id
}

// seedDupOrder provisions one order with one line per product id, and pushes created_at by offset.
func seedDupOrder(t *testing.T, pool *pgxpool.Pool, cust, status string, offset time.Duration, products ...string) string {
	t.Helper()
	ctx := context.Background()
	var ord string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents,
		                     created_at)
		 VALUES ($1, $2, $3, $4, 'PICKUP', 1000, 1000, now() + $5::interval) RETURNING id::text`,
		fmt.Sprintf("#DI-%d", time.Now().UnixNano()), cust, trentonBranch, status,
		fmt.Sprintf("%f seconds", offset.Seconds())).Scan(&ord); err != nil {
		t.Fatal(err)
	}
	for i, p := range products {
		if _, err := pool.Exec(ctx,
			`INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
			                          qty_base, unit_price_cents, price_uom, line_total_cents)
			 VALUES ($1, $2, $3, 'overlap line', 1, 'EA', 1, 100, 'EA', 100)`, ord, i+1, p); err != nil {
			t.Fatal(err)
		}
	}
	return ord
}

func stampSource(t *testing.T, pool *pgxpool.Pool, ord, sourceID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE orders SET metadata = metadata || jsonb_build_object('source_order_id', $2::text) WHERE id = $1`,
		ord, sourceID); err != nil {
		t.Fatal(err)
	}
}

// N concurrent submits with ONE idempotency key must create EXACTLY ONE draft, all returning the
// same id, and that draft carries the staff-duplicate provenance + the creating user.
func TestIntegration_CreateDuplicateDraft_IdempotentUnderConcurrency(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	cust := seedDupCustomer(t, pool, "IDEM")
	src := seedDupOrder(t, pool, cust, "FULFILLED", 0)

	var staff string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'Dup Int Staffer', 'ACTIVE') RETURNING id::text`,
		fmt.Sprintf("dup-int-%d@example.test", time.Now().UnixNano())).Scan(&staff); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE created_by = $1`, staff)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, staff)
	})

	key := fmt.Sprintf("dup-it-%d", time.Now().UnixNano())
	draft := ReorderDraft{
		CustomerID: cust, BranchID: trentonBranch, Fulfillment: "PICKUP",
		SourceOrderID: src, IdempotencyKey: key, SubtotalCents: 4400,
		Lines: []ReorderDraftLine{{
			LineNo: 1, Description: "Duplicated line", QtyEntered: "4", UOMEntered: "EA",
			QtyBase: "4", UnitPriceCents: 1100, PriceUOM: "EA", LineTotalCents: 4400,
		}},
	}

	const n = 8
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := repo.CreateDuplicateDraft(ctx, draft, staff)
			if err != nil {
				t.Errorf("concurrent create %d: %v", i, err)
				return
			}
			ids[i] = res.OrderID
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("submit %d created a different order (%s vs %s), idempotency broken", i, ids[i], ids[0])
		}
	}

	var drafts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE customer_id = $1 AND metadata->>'duplicate_idempotency_key' = $2`,
		cust, key).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 1 {
		t.Fatalf("want exactly 1 draft for the key, got %d", drafts)
	}

	var status, source, sourceOrderID, createdBy string
	var subtotal, tax, total int64
	if err := pool.QueryRow(ctx,
		`SELECT status, subtotal_cents, tax_cents, total_cents,
		        metadata->>'source', metadata->>'source_order_id', COALESCE(created_by::text, '')
		 FROM orders WHERE id = $1`, ids[0]).
		Scan(&status, &subtotal, &tax, &total, &source, &sourceOrderID, &createdBy); err != nil {
		t.Fatal(err)
	}
	if status != "DRAFT" || subtotal != 4400 || tax != 0 || total != 4400 {
		t.Fatalf("draft header = %s %d/%d/%d, want DRAFT 4400/0/4400 (pre-tax)", status, subtotal, tax, total)
	}
	if source != "staff_duplicate" || sourceOrderID != src {
		t.Fatalf("provenance = %s/%s, want staff_duplicate pointing at the source order", source, sourceOrderID)
	}
	if createdBy != staff {
		t.Fatalf("created_by = %q, want the staff user %q", createdBy, staff)
	}
}

// The DIB-490 candidate read, against real rows: a same-customer twin inside the window is found
// with its shared-product count; out-of-window, CANCELLED, other-tenant and provenance-linked orders
// (the checked order's source, its copies, and same-source siblings) are all excluded by the query.
func TestIntegration_SimilarRecentOrders_WindowStatusTenantAndProvenance(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	cust := seedDupCustomer(t, pool, "HEUR")
	other := seedDupCustomer(t, pool, "OTHER")
	prodA := seedDupProduct(t, pool, "A")
	prodB := seedDupProduct(t, pool, "B")

	me := seedDupOrder(t, pool, cust, "DRAFT", 0, prodA, prodB)

	twin := seedDupOrder(t, pool, cust, "CONFIRMED", -2*time.Hour, prodA, prodB) // IN: full overlap
	part := seedDupOrder(t, pool, cust, "DRAFT", -3*time.Hour, prodA)            // IN: partial overlap (raw read)
	_ = seedDupOrder(t, pool, cust, "DRAFT", -30*time.Hour, prodA, prodB)        // OUT: outside ±24h
	_ = seedDupOrder(t, pool, cust, "CANCELLED", -time.Hour, prodA, prodB)       // OUT: cancelled
	_ = seedDupOrder(t, pool, other, "DRAFT", -time.Hour, prodA, prodB)          // OUT: another tenant

	cands, mine, err := repo.SimilarRecentOrders(ctx, me, 24*time.Hour, 5)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if mine != 2 {
		t.Fatalf("draftProducts = %d, want 2", mine)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %+v, want exactly the twin and the partial", cands)
	}
	if cands[0].OrderID != twin || cands[0].SharedProducts != 2 {
		t.Fatalf("worst-first = %+v, want the full-overlap twin (shared=2) ranked first", cands[0])
	}
	if cands[1].OrderID != part || cands[1].SharedProducts != 1 {
		t.Fatalf("second = %+v, want the partial (shared=1)", cands[1])
	}

	// A twin keyed AFTER the checked order is just as suspicious, the window is symmetric.
	after := seedDupOrder(t, pool, cust, "DRAFT", 2*time.Hour, prodA, prodB)
	cands, _, err = repo.SimilarRecentOrders(ctx, me, 24*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cands {
		if c.OrderID == after {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidates = %+v, want the later-keyed twin included (symmetric window)", cands)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE id = $1`, after)

	// Provenance: stamp me as duplicated FROM the twin → the twin stops warning (intentional copy)…
	stampSource(t, pool, me, twin)
	cands, _, err = repo.SimilarRecentOrders(ctx, me, 24*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.OrderID == twin {
			t.Fatalf("candidates still name the checked order's own SOURCE (%s), provenance exclusion broken", twin)
		}
	}

	// …a SIBLING duplicated from the same source is excluded (the four-trucks flow must not warn
	// about itself)…
	sib := seedDupOrder(t, pool, cust, "DRAFT", -time.Hour, prodA, prodB)
	stampSource(t, pool, sib, twin)
	cands, _, err = repo.SimilarRecentOrders(ctx, me, 24*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.OrderID == sib {
			t.Fatalf("candidates name a same-source sibling (%s), the intentional multi-truck flow would warn", sib)
		}
	}

	// …and an order duplicated FROM the checked order (its child) is excluded too.
	child := seedDupOrder(t, pool, cust, "DRAFT", time.Hour, prodA, prodB)
	stampSource(t, pool, child, me)
	cands, _, err = repo.SimilarRecentOrders(ctx, me, 24*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.OrderID == child {
			t.Fatalf("candidates name the checked order's own copy (%s), provenance exclusion broken", child)
		}
	}
}

// A free-text-only order (no catalogue products) never has candidates, the read short-circuits with
// draftProducts = 0 and the service's materialOverlap keeps it warning-free.
func TestIntegration_SimilarRecentOrders_FreeTextOnlyNeverWarns(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	cust := seedDupCustomer(t, pool, "FREE")
	me := seedDupOrder(t, pool, cust, "DRAFT", 0) // no product lines
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO order_lines (order_id, line_no, description, qty_entered, uom_entered, qty_base,
		                          unit_price_cents, price_uom, line_total_cents)
		 VALUES ($1, 1, 'Custom delivery fee', 1, 'EA', 1, 5000, 'EA', 5000)`, me); err != nil {
		t.Fatal(err)
	}
	cands, mine, err := repo.SimilarRecentOrders(context.Background(), me, 24*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	if mine != 0 || len(cands) != 0 {
		t.Fatalf("free-text order answered products=%d cands=%+v, want 0 and none", mine, cands)
	}
}
