//go:build integration

// credit_helpers_integration_test.go carries the cleanup helper the pin's
// credit integration file owned; that file stays behind with this batch
// (its till import lands in a later batch), while the helper is pool-only
// and other tagged files here still call it. Carried verbatim from the pin
// (module path and punctuation aside).
package quote

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func cleanupConvertedOrders(t *testing.T, pool *pgxpool.Pool, quoteID string) {
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE quote_id = $1)`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE quote_id = $1`, quoteID)
	})
}

// setCreditLimit moves a customer's credit limit, the credit gate's input.
func setCreditLimit(t *testing.T, pool *pgxpool.Pool, customerID string, cents int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE customers SET credit_limit_cents = $2 WHERE id = $1`, customerID, cents); err != nil {
		t.Fatalf("set credit limit: %v", err)
	}
}
