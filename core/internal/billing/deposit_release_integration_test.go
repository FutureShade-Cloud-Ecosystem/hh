//go:build integration

package billing

// deposit_release_integration_test.go, the release seam and the sweep against
// the real schema: ReleaseDeposits emits one deposit.released row per live
// deposit document of the order, and the sweep heals a delivered order whose
// emission was lost, exactly once (a posted journal takes the order out).

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// deliveredFake is the order module's delivered-orders read.
type deliveredFake struct{ delivered map[string]bool }

func (d deliveredFake) DeliveredOrderIDs(_ context.Context, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		if d.delivered[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func seedDepositOrder(t *testing.T, pool *pgxpool.Pool, tag string) (cust, orderID, dep string) {
	t.Helper()
	ctx := context.Background()
	cust = seedCustomer(t, pool, tag)
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		 VALUES ('ORD-DEP-'||$1, $2::uuid, $3::uuid, 'INVOICED', 'DELIVERY') RETURNING id::text`,
		tag, cust, trentonBranch).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	dep = seedSupplierDepositInvoice(t, pool, cust, "#INV-"+tag+"-DEP", 4_000)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid WHERE id = $2::uuid`, orderID, dep); err != nil {
		t.Fatalf("bind the deposit to the order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE (payload->>'invoice_id')::uuid = $1::uuid`, dep)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "deposit.released:"+dep)
	})
	return cust, orderID, dep
}

func releasedRows(t *testing.T, pool *pgxpool.Pool, dep string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events_outbox WHERE topic = 'deposit.released' AND (payload->>'invoice_id')::uuid = $1::uuid`,
		dep).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegration_ReleaseDeposits_EmitsOnePerLiveDocument(t *testing.T) {
	pool := intPool(t)
	tag := ns(t)
	_, orderID, dep := seedDepositOrder(t, pool, tag)
	if err := NewInvoiceWriter(pool).ReleaseDeposits(context.Background(), orderID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := releasedRows(t, pool, dep); got != 1 {
		t.Fatalf("the release wrote %d rows, want the one", got)
	}
}

func TestIntegration_DepositSweep_HealsALostEmissionExactlyOnce(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	_, orderID, dep := seedDepositOrder(t, pool, tag)

	// An undelivered order is never released by the sweep, whatever its status.
	w := NewInvoiceWriter(pool).WithDeliveredOrders(deliveredFake{delivered: map[string]bool{}})
	if _, err := w.ReleaseDeliveredDeposits(ctx, 0); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := releasedRows(t, pool, dep); got != 0 {
		t.Fatalf("the sweep released an undelivered order: %d rows", got)
	}

	// A delivered order with no release journal (the emission was lost) heals.
	w = NewInvoiceWriter(pool).WithDeliveredOrders(deliveredFake{delivered: map[string]bool{orderID: true}})
	res, err := w.ReleaseDeliveredDeposits(ctx, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Released < 1 || releasedRows(t, pool, dep) != 1 {
		t.Fatalf("the sweep did not heal the lost emission: %+v rows %d", res, releasedRows(t, pool, dep))
	}

	// The release journal exists (the consumer posted it; a bare header is enough for the anti-join).
	var periodID string
	var year int
	if err := pool.QueryRow(ctx, `SELECT id::text, year FROM fiscal_periods ORDER BY year, month LIMIT 1`).Scan(&periodID, &year); err != nil {
		t.Skipf("no fiscal period to plant the journal in: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, status, idempotency_key)
		VALUES ($1, 900000000 + (random()*90000000)::int, $2::uuid, 'INVOICE', 'DRAFT', $3)`,
		year, periodID, fmt.Sprintf("deposit.released:%s", dep)); err != nil {
		t.Fatalf("plant the release journal: %v", err)
	}
	if _, err := w.ReleaseDeliveredDeposits(ctx, 0); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if got := releasedRows(t, pool, dep); got != 1 {
		t.Fatalf("after the journal posted the sweep emitted again: %d rows, want the one", got)
	}
}
