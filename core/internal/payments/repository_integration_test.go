//go:build integration

// repository_integration_test.go, W5 A-05 AC8. The portal Pay write must emit payment.received in the SAME
// tx as the settle, so the GL posting consumer books the receipt exactly once and the AR tie-out stays
// trustworthy. Run: make reset && go test -tags integration ./internal/payments/...
package payments

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit/legsweep"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
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
	legsweep.Register(t, pool)
	return pool
}

// AC8, Repository.Pay settles a portal receipt AND emits payment.received in the same tx (exactly one
// event for the new payment id), and the resulting AR is consistent (invoice balance reduced).
func TestIntegration_Pay_EmitsPaymentReceived(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	suffix := t.Name()
	var customerID, invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('PAY-'||$1, 'Pay Test '||$1, 'CONTRACTOR', $2::uuid, 1000000) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date, subtotal_cents, tax_cents, total_cents, balance_cents)
		 VALUES ('INV-PAY-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30, 40000, 0, 40000, 40000) RETURNING id::text`,
		suffix, customerID, branchID).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1::uuid`, invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	repo := NewRepository(pool)
	p, err := repo.Pay(ctx, customerID, "EFT", "gw-pay-"+suffix, []Allocation{{InvoiceID: invoiceID, AmountCents: 40000}})
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}

	var nEvent int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE topic='payment.received' AND payload->>'payment_id' = $1`, p.ID).Scan(&nEvent)
	if nEvent != 1 {
		t.Fatalf("payment.received events = %d, want 1 (same-tx emission)", nEvent)
	}
	// The invoice is now settled (balance 0, PAID), AR reduced by the receipt.
	var balance int64
	var status string
	_ = pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1`, invoiceID).Scan(&balance, &status)
	if balance != 0 || status != "PAID" {
		t.Fatalf("invoice not settled: balance=%d status=%s", balance, status)
	}

	// Q-044: the allocation read carries attribution. A portal payment has no staff allocator
	// (allocated_by NULL → empty by/name) but ALWAYS a when (created_at).
	listed, err := repo.ListPayments(ctx, customerID)
	if err != nil {
		t.Fatalf("ListPayments: %v", err)
	}
	if len(listed) != 1 || len(listed[0].Allocations) != 1 {
		t.Fatalf("want 1 payment with 1 allocation, got %+v", listed)
	}
	got := listed[0].Allocations[0]
	if got.InvoiceID != invoiceID || got.AmountCents != 40000 {
		t.Fatalf("unexpected allocation: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("allocation read must carry created_at (Q-044): %+v", got)
	}
	if got.AllocatedBy != "" || got.AllocatedByName != "" {
		t.Fatalf("portal allocation must have no staff allocator: %+v", got)
	}
	// The Pay response itself echoes the persisted allocation timestamp.
	if p.Allocations[0].CreatedAt.IsZero() {
		t.Fatalf("Pay response allocation must carry created_at: %+v", p.Allocations[0])
	}
}
