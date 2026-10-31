//go:build integration

package billing

import (
	"context"
	"testing"
)

// TM-2 fix round 2, P3-1: the credit reverse reopens an invoice by the one FINALIZED rule every undo
// door shares (>= total_cents). A balance that lands between total less holdback and total reads
// PARTIALLY_PAID here exactly as it does at the void, the reversal and the bounce.
func TestIntegration_UndoStatusRule_CreditReverseReadsPartiallyPaidBelowTotal(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)
	custID := seedCustomer(t, pool, n+"-USR")
	applyOrg := plantApplyOrg(t, pool, n)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID); err != nil {
			t.Errorf("cleanup failed on customer_transactions: %v", err)
		}
	})
	// Total 100000, holdback 10000, balance 95000: between total less holdback (90000) and total.
	invID := seedInvoice(t, pool, custID, n, invSpec{
		no: "USR1", status: "PARTIALLY_PAID", subtotal: 100000, holdback: 10000, total: 100000, balance: 95000, finalized: true,
	})
	var creditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -7500, -7500, 'deposit_cancel', gen_random_uuid(), $2::uuid, 'the credit')
		RETURNING id::text`, custID, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed the credit: %v", err)
	}
	app, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invID, OrgID: applyOrg,
		AmountCents: 2000, Reason: "part of the credit", IdempotencyKey: n + "-usr-1",
	}, true)
	if err != nil {
		t.Fatalf("the apply: %v", err)
	}
	if _, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID: invID, ApplicationID: app.ApplicationID, Reason: "status rule",
	}, true); err != nil {
		t.Fatalf("the reverse: %v", err)
	}
	var bal int64
	var st string
	if err := pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, invID).Scan(&bal, &st); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if bal != 95000 || st != "PARTIALLY_PAID" {
		t.Errorf("after the credit reverse the invoice reads %d %s, want 95000 PARTIALLY_PAID", bal, st)
	}
}
