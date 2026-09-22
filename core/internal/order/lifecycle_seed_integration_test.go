//go:build integration

// lifecycle_seed_integration_test.go carries the seed helper the pin's
// lifecycle integration file owned; that file stays behind with this batch
// (its billing and pos imports land in later batches), while the helper is
// pool-only and other tagged files here still call it. Carried verbatim
// from the pin (module path and punctuation aside).
package order

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// otherBranch is a branch the seeded order does NOT live in.
const otherBranch = "5d1b3f6e-0000-4000-9000-0000000000ff"

// seedOrder inserts a customer + one order at `status` in trentonBranch and returns their ids,
// for assertion-focused lifecycle tests that do not care about their own fixtures.
func seedOrder(t *testing.T, pool *pgxpool.Pool, status string) (cust, ord string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'DIB191 Tenant') RETURNING id::text`,
		fmt.Sprintf("D191-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, $4, 'PICKUP', 1000, 1000) RETURNING id::text`,
		fmt.Sprintf("#D191-%d", time.Now().UnixNano()), cust, trentonBranch, status).Scan(&ord); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return cust, ord
}

// liveStatus reads the order's current status column straight from Postgres.
func liveStatus(t *testing.T, pool *pgxpool.Pool, orderID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}
