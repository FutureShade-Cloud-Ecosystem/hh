//go:build integration

package payments

import (
	"context"
	"testing"
)

// TestIntegration_FW1_06a_NSFFeeCanBePaid pins sub-item 3 of FW1-06a:
// when a customer has an unpaid NSF fee recorded on payment_nsf and in
// customer_transactions (source_type = 'payment_nsf'), ApplyReceived allows
// paying the reopened invoice plus the NSF fee in one payment, or paying the
// NSF fee on its own, netting the fee's customer_transactions balance to zero.
func TestIntegration_FW1_06a_NSFFeeCanBePaid(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()

	invID := w.invoice(t, w.customerID, "REOPENED", 20, 0, 50000)

	var bouncedPayID, nsfID string
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES ($1, $2::uuid, 'CHEQUE', 50000, 'BOUNCED', $3::uuid)
		 RETURNING id::text`,
		w.tag+"-BOUNCED-1", w.customerID, dib510Trenton).Scan(&bouncedPayID); err != nil {
		t.Fatalf("seed bounced payment: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, reason, fee_cents)
		 VALUES ($1::uuid, 'NSF test', 4500)
		 RETURNING id::text`,
		bouncedPayID).Scan(&nsfID); err != nil {
		t.Fatalf("seed payment_nsf: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM payment_nsf WHERE id = $1::uuid`, nsfID)
	})
	if _, err := w.pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'ADJUSTMENT', 4500, 54500, 'payment_nsf', $2::uuid, $3::uuid, 'NSF fee')`,
		w.customerID, nsfID, dib510Trenton); err != nil {
		t.Fatalf("seed NSF fee txn: %v", err)
	}

	// Paying the reopened invoice ($500.00) plus the NSF fee ($45.00) in one
	// replacement cheque ($545.00) must succeed and clear both.
	applied, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID:     w.customerID,
		InvoiceIDs:     []string{invID},
		AmountCents:    54500,
		Method:         "CHEQUE",
		Reference:      "REPL-CHQ-1",
		IdempotencyKey: w.tag + "-apply-inv-and-fee",
	})
	if err != nil {
		t.Fatalf("ApplyReceived (invoice + NSF fee): %v", err)
	}
	if applied.AppliedCents != 54500 {
		t.Fatalf("AppliedCents = %d, want 54500", applied.AppliedCents)
	}

	var invBal int64
	if err := w.pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&invBal); err != nil {
		t.Fatalf("read invoice balance: %v", err)
	}
	if invBal != 0 {
		t.Fatalf("invoice balance after payment = %d, want 0", invBal)
	}

	var feeNet int64
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
		w.customerID, nsfID).Scan(&feeNet); err != nil {
		t.Fatalf("read NSF fee net: %v", err)
	}
	if feeNet != 0 {
		t.Fatalf("NSF fee net balance in customer_transactions = %d, want 0", feeNet)
	}

	// Second case: an account whose invoice was already settled and that owes
	// only the $45.00 NSF fee can pay the fee directly by naming the NSF id.
	var bouncedPayID2, nsfID2 string
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES ($1, $2::uuid, 'CHEQUE', 20000, 'BOUNCED', $3::uuid)
		 RETURNING id::text`,
		w.tag+"-BOUNCED-2", w.otherID, dib510Trenton).Scan(&bouncedPayID2); err != nil {
		t.Fatalf("seed bounced payment 2: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, reason, fee_cents)
		 VALUES ($1::uuid, 'NSF test 2', 4500)
		 RETURNING id::text`,
		bouncedPayID2).Scan(&nsfID2); err != nil {
		t.Fatalf("seed payment_nsf 2: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM payment_nsf WHERE id = $1::uuid`, nsfID2)
	})
	if _, err := w.pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'ADJUSTMENT', 4500, 4500, 'payment_nsf', $2::uuid, $3::uuid, 'NSF fee')`,
		w.otherID, nsfID2, dib510Trenton); err != nil {
		t.Fatalf("seed NSF fee txn 2: %v", err)
	}

	applied2, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID:     w.otherID,
		InvoiceIDs:     []string{nsfID2},
		AmountCents:    4500,
		Method:         "CASH",
		Reference:      "FEE-CASH-2",
		IdempotencyKey: w.tag + "-apply-fee-only",
	})
	if err != nil {
		t.Fatalf("ApplyReceived (NSF fee only): %v", err)
	}
	if applied2.AppliedCents != 4500 {
		t.Fatalf("AppliedCents (fee only) = %d, want 4500", applied2.AppliedCents)
	}
	var feeNet2 int64
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid`,
		w.otherID, nsfID2).Scan(&feeNet2); err != nil {
		t.Fatalf("read NSF fee 2 net: %v", err)
	}
	if feeNet2 != 0 {
		t.Fatalf("NSF fee 2 net balance in customer_transactions = %d, want 0", feeNet2)
	}
}
