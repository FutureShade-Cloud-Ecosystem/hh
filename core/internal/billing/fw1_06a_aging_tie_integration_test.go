//go:build integration

package billing

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_FW1_06a_AgingIncludesNonInvoiceARAndTiesToSubledger pins
// sub-item 1 of FW1-06a: CreditService.Aging (agingSelect) must include
// non-invoice AR rows in customer_transactions (such as NSF fees and order
// deposit adjustments) so the aging grand total and per-customer totals tie to
// ARBalanceCents (and therefore GL 1100 inception to date).
func TestIntegration_FW1_06a_AgingIncludesNonInvoiceARAndTiesToSubledger(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewCreditService(repo)
	ctx := context.Background()
	n := ns(t)

	asOf := time.Now().UTC().Add(1 * time.Hour)
	baseAR, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("baseline ARBalanceCents: %v", err)
	}
	baseAging, err := svc.Aging(ctx, asOf, "")
	if err != nil {
		t.Fatalf("baseline Aging: %v", err)
	}
	var baseAgingSum int64
	for _, r := range baseAging {
		baseAgingSum += r.TotalCents
	}

	// Customer 1 has an open invoice ($500.00), an unpaid NSF fee ($45.00), and
	// a cancelled-order deposit credit (-$100.00), net $445.00 (44500 cents).
	cust1 := seedCustomer(t, pool, n+"-C1")
	seedInvoice(t, pool, cust1, n, invSpec{
		no: "FW106A-INV1", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
		total: 50000, balance: 50000, finalized: true,
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES
		   ($1::uuid, 'ADJUSTMENT', 4500, 54500, 'payment_nsf', gen_random_uuid(), $2::uuid, 'NSF fee'),
		   ($1::uuid, 'ADJUSTMENT', -10000, 44500, 'order_deposit_cancel', gen_random_uuid(), $2::uuid, 'cancelled order deposit')`,
		cust1, trentonBranch); err != nil {
		t.Fatalf("seed cust1 non-invoice AR: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cust1)
	})

	// Customer 2 has paid its invoice and carries only an unpaid $45.00 NSF fee.
	cust2 := seedCustomer(t, pool, n+"-C2")
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'ADJUSTMENT', 4500, 4500, 'payment_nsf', gen_random_uuid(), $2::uuid, 'NSF fee')`,
		cust2, trentonBranch); err != nil {
		t.Fatalf("seed cust2 NSF fee: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cust2)
	})

	afterAR, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("after ARBalanceCents: %v", err)
	}
	afterAging, err := svc.Aging(ctx, asOf, "")
	if err != nil {
		t.Fatalf("after Aging: %v", err)
	}
	var afterAgingSum int64
	var gotCust1, gotCust2 *AgingRow
	for i := range afterAging {
		r := &afterAging[i]
		afterAgingSum += r.TotalCents
		if r.CustomerID == cust1 {
			gotCust1 = r
		}
		if r.CustomerID == cust2 {
			gotCust2 = r
		}
	}

	arDelta := afterAR - baseAR
	agingDelta := afterAgingSum - baseAgingSum
	if arDelta != 49000 {
		t.Fatalf("ARBalanceCents delta = %d, want 49000", arDelta)
	}
	if agingDelta != arDelta {
		t.Fatalf("aging grand total delta (%d) does not tie to ARBalanceCents / GL 1100 delta (%d); diff = %d",
			agingDelta, arDelta, agingDelta-arDelta)
	}
	if gotCust1 == nil || gotCust1.TotalCents != 44500 {
		var c1 int64
		if gotCust1 != nil {
			c1 = gotCust1.TotalCents
		}
		t.Fatalf("cust1 aging TotalCents = %d, want 44500 (50000 invoice + 4500 NSF fee - 10000 deposit credit)", c1)
	}
	if gotCust2 == nil || gotCust2.TotalCents != 4500 {
		var c2 int64
		if gotCust2 != nil {
			c2 = gotCust2.TotalCents
		}
		t.Fatalf("cust2 aging TotalCents = %d, want 4500 (unpaid NSF fee on account with no open invoice)", c2)
	}
}
