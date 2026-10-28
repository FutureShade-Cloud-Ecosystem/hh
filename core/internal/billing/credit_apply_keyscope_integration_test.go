//go:build integration

package billing

import (
	"context"
	"errors"
	"testing"
)

// ROUND 7, THE IDEMPOTENCY KEY'S SCOPE (K3 P3).
//
// The key's record lived only on the TARGET INVOICE's metadata and the only key lookup read that one
// invoice. So a key reused with a DIFFERENT invoice found no prior record and performed another
// application instead of answering the spent key conflict the door's own contract promises. The
// balances stay arithmetically right, but one key moves money twice under two bodies.
//
// The fix makes the key's scope the route: ONE key check that reads the key across the route's
// invoices, so a key already spent on another invoice answers the spent key conflict.
//
// The leg applies a credit to one invoice under a key and then reuses that key against a second
// invoice of the same customer: the second application must be refused and the second invoice must
// not move.

// TestIntegration_FW1_12b_ASpentKeyAgainstAnotherInvoiceIsTheSpentKeyConflict pins K3 P3.
func TestIntegration_FW1_12b_ASpentKeyAgainstAnotherInvoiceIsTheSpentKeyConflict(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID := seedCustomer(t, pool, n+"-CUST")

	// Two open invoices of the same customer at the same branch.
	var invA, invB string
	for i, tag := range []string{"-INVA", "-INVB"} {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE')
			RETURNING id::text`, n+tag, custID, trentonBranch).Scan(&id); err != nil {
			t.Fatalf("seed invoice %d: %v", i, err)
		}
		if tag == "-INVA" {
			invA = id
		} else {
			invB = id
		}
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

	key := n + "-key-1"
	first, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invA,
		AmountCents: 5000, Reason: "the first invoice", IdempotencyKey: key,
	}, true)
	if err != nil {
		t.Fatalf("the first apply: %v", err)
	}
	if first.Replayed {
		t.Fatalf("the first apply answered Replayed=true, want the first result")
	}

	// The same key, another invoice: the key is spent.
	second, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invB,
		AmountCents: 5000, Reason: "the second invoice", IdempotencyKey: key,
	}, true)
	if err == nil {
		t.Fatalf("the door applied a second credit under a spent key: %+v", second)
	}
	var conflict CreditApplicationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("the door answered %v, want the spent key conflict", err)
	}

	// The second invoice never moved and the credit still holds what the first application left.
	var invBBalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invB).Scan(&invBBalance); err != nil {
		t.Fatalf("read the second invoice's balance: %v", err)
	}
	if invBBalance != 10000 {
		t.Fatalf("the refused application moved the second invoice: the balance is %d, want 10000", invBBalance)
	}
	var invABalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invA).Scan(&invABalance); err != nil {
		t.Fatalf("read the first invoice's balance: %v", err)
	}
	if invABalance != 5000 {
		t.Fatalf("the first invoice's balance is %d, want 5000", invABalance)
	}
}
