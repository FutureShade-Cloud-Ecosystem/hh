//go:build integration

// order_no_integration_test.go, DIB-388, ring leg. The counter's order write (Repository.InsertOrder)
// is one of the three order-number writers that had drifted apart; it must now draw from the SAME
// branch-coded mint (migration 0087's next_order_no) and produce a number that resolves back to the
// order through order.Repository.ResolveOrderID.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/pos/... -run OrderNo -count=1
package pos

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	orderNoTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	orderNoKingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

func seedRingCustomer(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var cust string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents)
		 VALUES ($1, 'DIB-388 ring tenant', 'CONTRACTOR', 50000000) RETURNING id::text`,
		fmt.Sprintf("D388R-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust
}

// TestIntegration_OrderNo_RingIsBranchCoded rings the counter's order write at each branch and proves
// the number carries that branch's code AND resolves back to the order it names.
func TestIntegration_OrderNo_RingIsBranchCoded(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	orderRepo := order.NewRepository(pool)
	cust := seedRingCustomer(t, pool)

	for _, tc := range []struct{ branch, code string }{
		{orderNoTrenton, "TRT"},
		{orderNoKingston, "KGN"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var id, no string
			if err := repo.RunInTx(ctx, func(tx pgx.Tx) error {
				var err error
				id, no, err = repo.InsertOrder(ctx, tx, orderRow{
					CustomerID: cust, BranchID: tc.branch, Status: "CONFIRMED", Fulfillment: "PICKUP",
					SubtotalCents: 1000, TaxCents: 130, TotalCents: 1130, Metadata: map[string]any{},
				})
				return err
			}); err != nil {
				t.Fatalf("insert order: %v", err)
			}
			want := "#SO-" + tc.code + "-"
			if !strings.HasPrefix(no, want) {
				t.Fatalf("ring order_no %q does not carry the branch prefix %q", no, want)
			}
			got, err := orderRepo.ResolveOrderID(ctx, no, true, nil)
			if err != nil || got != id {
				t.Fatalf("resolve(%q) = (%s, %v), want (%s, nil)", no, got, err, id)
			}
		})
	}
}
