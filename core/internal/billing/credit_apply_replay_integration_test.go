//go:build integration

package billing

import (
	"context"
	"testing"
)

// ROUND 7, THE IDEMPOTENCY REPLAY BEFORE THE OPEN CHECK (K3 P2-2).
//
// The target's open status check ran before the idempotency record was consulted, so a retry of an
// application that took its invoice to PAID was refused with "invoice ... is not open" instead of
// answering the first result. That made a lost response indistinguishable from a validation refusal,
// and it is not what the door's own contract promises: the same key with the same body answers the
// first result and writes nothing.
//
// The fix reads the replay under the target invoice's row lock BEFORE the open and ceiling checks: the
// reach checks first (the account, the branch reach and the org), then the replay, then the open status
// and the ceilings.
//
// The leg applies a credit that takes the invoice to zero and then retries the same key and the same
// body: the retry must answer the first result with Replayed = true and write nothing a second time.

// TestIntegration_FW1_12b_ASpentKeyOnAClosedInvoiceReplaysItsFirstResult pins K3 P2-2.
func TestIntegration_FW1_12b_ASpentKeyOnAClosedInvoiceReplaysItsFirstResult(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID := seedCustomer(t, pool, n+"-CUST")

	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`, n+"-INV", custID, trentonBranch).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	// One account credit of 10000 in a credit family the door accepts.
	var creditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -10000, 0, 'deposit_cancel', gen_random_uuid(), $2::uuid, 'the credit')
		RETURNING id::text`, custID, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed the credit: %v", err)
	}

	in := ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invID,
		AmountCents: 10000, Reason: "the whole credit", IdempotencyKey: n + "-key-1",
	}
	first, err := applySvc.Apply(ctx, in, true)
	if err != nil {
		t.Fatalf("the first apply: %v", err)
	}
	if first.Replayed {
		t.Fatalf("the first apply answered Replayed=true, want the first result")
	}

	// The invoice is closed by that application, so the retry walks the door's open status check
	// first on the unfixed tree.
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1::uuid`, invID).Scan(&status); err != nil {
		t.Fatalf("read the invoice status: %v", err)
	}
	if status != "PAID" {
		t.Fatalf("the fixture is wrong: the invoice is %s, want PAID", status)
	}

	second, err := applySvc.Apply(ctx, in, true)
	if err != nil {
		t.Fatalf("the retry answered %v, want the first result replayed", err)
	}
	if !second.Replayed {
		t.Fatalf("the retry answered Replayed=false, want the first result replayed")
	}
	if second.ApplicationID != first.ApplicationID {
		t.Fatalf("the retry answered the application %s, want the first one %s", second.ApplicationID, first.ApplicationID)
	}

	// Nothing was written a second time: the invoice still carries the one movement and the
	// metadata still carries the one application.
	var invBalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&invBalance); err != nil {
		t.Fatalf("read the invoice balance: %v", err)
	}
	if invBalance != 0 {
		t.Fatalf("the retry moved the invoice: the balance is %d, want 0", invBalance)
	}
	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'invoice' AND source_ref_id = $2::uuid`,
		custID, invID).Scan(&rows); err != nil {
		t.Fatalf("read the invoice's subledger rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the invoice carries %d subledger rows, want 1: the retry wrote a second time", rows)
	}
}
