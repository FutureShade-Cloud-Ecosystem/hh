//go:build integration

// CP-04 integration tests: the real pgx reorder repository against the live Postgres 16
// that `make reset` provisions (mirrors internal/portal/integration_test.go). Proves the
// two SQL-level properties the unit fakes cannot: the tenant-scoped source read (IDOR)
// and the advisory-lock idempotent draft write under concurrency.
package order

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const trentonBranch = "5d1b3f6e-0000-4000-9000-000000000001" // seeded TRT branch (0003)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...) // CI has a DB configured → a failure must fail, not skip
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedCustomerAndOrder provisions an isolated tenant with one two-line order and
// returns (customerID, orderID). Rows are cleaned up on test end.
func seedCustomerAndOrder(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'Reorder Int Tenant') RETURNING id::text`,
		fmt.Sprintf("REO-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	var ord string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'FULFILLED', 'PICKUP', 5550, 5550) RETURNING id::text`,
		fmt.Sprintf("#IT-%d", time.Now().UnixNano()), cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO order_lines (order_id, line_no, description, qty_entered, uom_entered, qty_base,
		                          unit_price_cents, price_uom, line_total_cents)
		 VALUES ($1, 1, 'Historical line', 10, 'EA', 10, 555, 'EA', 5550)`, ord); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, ord
}

// The source read is tenant-scoped: the owner sees the lines; any other customer id
// gets ErrNotFound (US-RO-04 at the SQL layer).
func TestIntegration_ReorderSource_TenantScoped(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	cust, ord := seedCustomerAndOrder(t, pool)

	src, err := repo.ReorderSource(ctx, cust, ord)
	if err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if len(src.Lines) != 1 || src.BranchID != trentonBranch {
		t.Fatalf("source = %+v, want 1 line at TRT", src)
	}

	var stranger string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'Stranger') RETURNING id::text`,
		fmt.Sprintf("STR-%d", time.Now().UnixNano())).Scan(&stranger); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, stranger) })
	if _, err := repo.ReorderSource(ctx, stranger, ord); err != ErrNotFound {
		t.Fatalf("cross-tenant read err = %v, want ErrNotFound", err)
	}
}

// N concurrent submits with ONE idempotency key must create EXACTLY ONE draft and all
// return the same order id (the advisory-lock + metadata-key replay path).
func TestIntegration_CreateReorderDraft_IdempotentUnderConcurrency(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	cust, ord := seedCustomerAndOrder(t, pool)

	key := fmt.Sprintf("it-key-%d", time.Now().UnixNano())
	draft := ReorderDraft{
		CustomerID: cust, BranchID: trentonBranch, Fulfillment: "PICKUP",
		SourceOrderID: ord, IdempotencyKey: key, SubtotalCents: 9990,
		Lines: []ReorderDraftLine{{
			LineNo: 1, Description: "Repriced line", QtyEntered: "10", UOMEntered: "EA",
			QtyBase: "10", UnitPriceCents: 999, PriceUOM: "EA", LineTotalCents: 9990,
		}},
	}

	const n = 8
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := repo.CreateReorderDraft(ctx, draft)
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
		`SELECT count(*) FROM orders WHERE customer_id = $1 AND metadata->>'reorder_idempotency_key' = $2`,
		cust, key).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 1 {
		t.Fatalf("want exactly 1 draft for the key, got %d", drafts)
	}

	// The draft is a DRAFT for the same customer with the repriced line, pre-tax.
	var status string
	var subtotal, tax, total int64
	if err := pool.QueryRow(ctx,
		`SELECT status, subtotal_cents, tax_cents, total_cents FROM orders WHERE id = $1`, ids[0]).
		Scan(&status, &subtotal, &tax, &total); err != nil {
		t.Fatal(err)
	}
	if status != "DRAFT" || subtotal != 9990 || tax != 0 || total != 9990 {
		t.Fatalf("draft header = %s %d/%d/%d, want DRAFT 9990/0/9990", status, subtotal, tax, total)
	}
	var unit int64
	if err := pool.QueryRow(ctx,
		`SELECT unit_price_cents FROM order_lines WHERE order_id = $1 AND line_no = 1`, ids[0]).Scan(&unit); err != nil {
		t.Fatal(err)
	}
	if unit != 999 {
		t.Fatalf("draft line price = %d, want today's 999 (never the historical 555)", unit)
	}
}
