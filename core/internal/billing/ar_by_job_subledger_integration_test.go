//go:build integration

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// jaPay plants a receipt of amount cents with allocations (invoice id to cents) and its PAYMENT
// ledger row, and removes them (allocations, payment, ledger row) loudly before the customer goes.
func jaPay(t *testing.T, pool *pgxpool.Pool, customerID string, amount int64, allocs map[string]int64) string {
	t.Helper()
	ctx := context.Background()
	var payID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', $2, 'RECEIVED', $3::uuid)
		 RETURNING id::text`, customerID, amount, trentonBranch).Scan(&payID); err != nil {
		t.Fatalf("seed the receipt: %v", err)
	}
	for inv, c := range allocs {
		if _, err := pool.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid, $2::uuid, $3)`,
			payID, inv, c); err != nil {
			t.Fatalf("seed the allocation: %v", err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'PAYMENT', $2, 0, 'payment', $3::uuid, $4::uuid, 'Receipt')`,
		customerID, -amount, payID, trentonBranch); err != nil {
		t.Fatalf("seed the receipt's ledger row: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []string{
			`DELETE FROM customer_transactions WHERE source_ref_id = $1::uuid`,
			`DELETE FROM payment_allocations WHERE payment_id = $1::uuid`,
			`DELETE FROM payments WHERE id = $1::uuid`,
		} {
			if _, err := pool.Exec(c, q, payID); err != nil {
				t.Errorf("remove the receipt (%s): %v", q, err)
			}
		}
	})
	return payID
}

// TestIntegration_JA4_SubledgerByJob_PaymentSplitsByAllocations pins JA-4 item 2: the customer's
// ledger carries its job, a payment applied across jobs splits by its allocations (the unapplied
// part stays account level), and the by job subtotals sum to the statement's closing balance.
func TestIntegration_JA4_SubledgerByJob_PaymentSplitsByAllocations(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := jaSeed(t, pool)
	jaPay(t, pool, f.customer, 25000, map[string]int64{f.invA: 15000, f.invB: 5000})
	asOf := time.Now().Add(time.Hour)

	plain, err := repo.StatementLines(ctx, f.customer, asOf, AllBranches)
	if err != nil {
		t.Fatalf("StatementLines: %v", err)
	}
	closing := plain[len(plain)-1].BalanceRun

	lines, err := repo.StatementLinesByJob(ctx, f.customer, asOf, AllBranches)
	if err != nil {
		t.Fatalf("StatementLinesByJob: %v", err)
	}
	per := map[string]int64{}
	var sum int64
	for _, ln := range lines {
		per[ln.JobID] += ln.ChargeCents - ln.CreditCents
		sum += ln.ChargeCents - ln.CreditCents
	}
	if sum != closing {
		t.Fatalf("by job lines sum to %d, the statement closes at %d", sum, closing)
	}
	if per[f.jobA] != 15000 || per[f.jobB] != 15000 || per[""] != 9500 {
		t.Fatalf("per job = A %d, B %d, account %d; want 15000, 15000, 9500", per[f.jobA], per[f.jobB], per[""])
	}
}
