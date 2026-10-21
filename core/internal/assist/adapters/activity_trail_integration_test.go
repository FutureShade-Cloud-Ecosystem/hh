//go:build integration

// activity_trail_integration_test.go, UX-93 round 2: the assistant reads the account's ledger through
// the same repository query as the workspace, so a caller without the payment trail gets no payment
// row, no NSF row, no NSF fee and nothing from which a withheld payment can be reconstructed.
package adapters

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

func TestIntegration_RecentActivity_NoPaymentBackWithoutTheTrail(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents, payment_terms_days)
		 VALUES ('UX93R2-A-' || gen_random_uuid(), 'UX-93 r2 assist', 'CONTRACTOR', 500000, 30) RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, id)
	})
	rows := []struct {
		typ, src, memo string
		amt, bal       int64
	}{
		{"INVOICE", "invoice", "", 100000, 100000},
		{"PAYMENT", "payment", "", -41234, 58766},
		{"INVOICE", "invoice", "", 5000, 63766},
		{"NSF", "payment_nsf", "", 41234, 105000},
		{"ADJUSTMENT", "payment_nsf", "NSF fee", 2500, 107500},
		{"INVOICE", "invoice", "", 1000, 108500},
	}
	for i, r := range rows {
		if _, err := pool.Exec(ctx,
			`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, memo, occurred_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, gen_random_uuid(), $6, now() - make_interval(hours => $7))`,
			id, r.typ, r.amt, r.bal, r.src, r.memo, len(rows)-i); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	reader := NewCustomerReader(seededAccounts(), billing.NewCreditService(billing.NewRepository(pool)))

	c := globalCaller()
	got, err := reader.RecentActivity(ctx, c, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("a caller without the trail sees the three invoices only, got %+v", got)
	}
	for _, r := range got {
		if r.Kind != "Invoice" {
			t.Fatalf("only invoice rows remain, got %+v", r)
		}
	}

	c.PaymentTrail = true
	got, err = reader.RecentActivity(ctx, c, id, 10)
	if err != nil || len(got) != 6 {
		t.Fatalf("a caller with the trail sees every row: %d err %v", len(got), err)
	}
}
