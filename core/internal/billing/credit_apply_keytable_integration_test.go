//go:build integration

package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ROUND 8, THE CREDIT APPLICATION KEY TABLE (migration 0447).
//
// Round 7 gave the apply door's idempotency key a ROUTE wide scope by scanning the invoices'
// metadata for the key under the key's own advisory slot (findCreditApplicationByKey,
// lockCreditApplicationKey). That read has no index behind it: the key sits inside a jsonb column,
// so every application scans the invoices table, a read that grows with every dealer's history.
// This round replaces the scan with billing's own key table: the idempotency key is the PRIMARY
// KEY, so the check is one index lookup and the database itself refuses a second row under a spent
// key, and the row carries the invoice and the credit the key was spent on, the request's own
// hash, the stored result and the application id.
//
// The three legs, and where each one bites on the tree before the fix:
//
//   - a key reused against a second invoice is refused in the door's own words and moves no
//     money, and the key row names where the key was spent. The refusal and the untouched
//     balances already hold on round 7's tree; the row does not exist there, so the leg is red at
//     its table read.
//   - the same key with the same request replays the STORED result with Replayed=true and writes
//     no second row. The replay already holds on round 7's tree (from the metadata record); the
//     table read is the red.
//   - the same key with a DIFFERENT request on its own invoice is refused 409, not replayed. This
//     one is behaviourally red on round 7's tree: the door there compares only kind, credit and
//     amount, so a retry that changes the reason replays instead of being refused.
//
// The migration carries the table; these legs pin what the door must write into it and read from
// it.

// spentKeyFixture seeds one customer, two open invoices of 10000 and one account credit of 10000
// in a family the door accepts.
func spentKeyFixture(t *testing.T, n string) (custID, invA, invB, creditID string) {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	custID = seedCustomer(t, pool, n+"-CUST")
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
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -10000, 0, 'deposit_cancel', gen_random_uuid(), $2::uuid, 'the credit')
		RETURNING id::text`, custID, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed the credit: %v", err)
	}
	return custID, invA, invB, creditID
}

// TestIntegration_FW1_12b_ASpentKeyRowNamesWhereTheKeyWasSpent pins the table's first half: the
// refusal of a key reused against a second invoice, no money moved, and the one key row carrying
// the invoice, the credit, the application and a request hash.
func TestIntegration_FW1_12b_ASpentKeyRowNamesWhereTheKeyWasSpent(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID, invA, invB, creditID := spentKeyFixture(t, n)

	key := n + "-key-1"
	first, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invA,
		AmountCents: 5000, Reason: "the first invoice", IdempotencyKey: key,
	}, true)
	if err != nil {
		t.Fatalf("the first apply: %v", err)
	}

	// The same key, another invoice: refused as the spent key it is, in the door's own words.
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
	if !strings.Contains(err.Error(), "that idempotency key already applied a credit differently") {
		t.Fatalf("the refusal said %q, want the door's own spent key words", err.Error())
	}

	// No money moved: the second invoice stands at its seeded balance, the first carries the one
	// application.
	var invBBalance, invABalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invB).Scan(&invBBalance); err != nil {
		t.Fatalf("read the second invoice's balance: %v", err)
	}
	if invBBalance != 10000 {
		t.Fatalf("the refused application moved the second invoice: the balance is %d, want 10000", invBBalance)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invA).Scan(&invABalance); err != nil {
		t.Fatalf("read the first invoice's balance: %v", err)
	}
	if invABalance != 5000 {
		t.Fatalf("the first invoice's balance is %d, want 5000", invABalance)
	}

	// The key's row: exactly one, naming the invoice the key was spent on, the credit, the
	// application, and the request's own hash.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_application_keys WHERE idempotency_key = $1`, key).Scan(&count); err != nil {
		t.Fatalf("count the key's rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("the key carries %d rows, want 1", count)
	}
	var rowInvoice, rowKind, rowCredit, rowApp, rowHash string
	if err := pool.QueryRow(ctx, `
		SELECT invoice_id::text, credit_kind, credit_id::text, COALESCE(application_id::text, ''), request_hash
		  FROM credit_application_keys WHERE idempotency_key = $1`, key).
		Scan(&rowInvoice, &rowKind, &rowCredit, &rowApp, &rowHash); err != nil {
		t.Fatalf("read the key's row: %v", err)
	}
	if rowInvoice != invA {
		t.Fatalf("the key row names invoice %s, want the one it was spent on %s", rowInvoice, invA)
	}
	if rowKind != CreditKindAccount || rowCredit != creditID {
		t.Fatalf("the key row names credit %s/%s, want ACCOUNT/%s", rowKind, rowCredit, creditID)
	}
	if rowApp != first.ApplicationID {
		t.Fatalf("the key row names application %s, want the first apply's %s", rowApp, first.ApplicationID)
	}
	if strings.TrimSpace(rowHash) == "" {
		t.Fatalf("the key row carries an empty request hash, want the request's own hash")
	}
}

// TestIntegration_FW1_12b_AReplayAnswersTheStoredResultWithOneKeyRow pins the table's replay
// half: the same key with the same request answers the stored result with Replayed=true, and the
// table still carries exactly one row with that result.
func TestIntegration_FW1_12b_AReplayAnswersTheStoredResultWithOneKeyRow(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID, invA, _, creditID := spentKeyFixture(t, n)

	in := ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invA,
		AmountCents: 10000, Reason: "the whole credit", IdempotencyKey: n + "-key-1",
	}
	first, err := applySvc.Apply(ctx, in, true)
	if err != nil {
		t.Fatalf("the first apply: %v", err)
	}
	if first.Replayed {
		t.Fatalf("the first apply answered Replayed=true, want the first result")
	}

	second, err := applySvc.Apply(ctx, in, true)
	if err != nil {
		t.Fatalf("the retry answered %v, want the stored result replayed", err)
	}
	if !second.Replayed {
		t.Fatalf("the retry answered Replayed=false, want the stored result replayed")
	}
	if second.ApplicationID != first.ApplicationID {
		t.Fatalf("the retry answered the application %s, want the first one %s", second.ApplicationID, first.ApplicationID)
	}
	if second.AppliedCents != first.AppliedCents || second.CreditOpenAfter != first.CreditOpenAfter {
		t.Fatalf("the replay answered %+v, want the stored result %+v", second, first)
	}

	// Nothing was written a second time: the invoice stays closed at the one movement, the
	// invoice's subledger side carries one row.
	var invBalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invA).Scan(&invBalance); err != nil {
		t.Fatalf("read the invoice balance: %v", err)
	}
	if invBalance != 0 {
		t.Fatalf("the retry moved the invoice: the balance is %d, want 0", invBalance)
	}
	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'invoice' AND source_ref_id = $2::uuid`,
		custID, invA).Scan(&rows); err != nil {
		t.Fatalf("read the invoice's subledger rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the invoice carries %d subledger rows, want 1: the retry wrote a second time", rows)
	}

	// The table carries exactly one row for the key, and its stored result is the first answer.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_application_keys WHERE idempotency_key = $1`, in.IdempotencyKey).Scan(&count); err != nil {
		t.Fatalf("count the key's rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("the key carries %d rows, want 1", count)
	}
	var storedApp string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(stored_result->>'application_id', '')
		  FROM credit_application_keys WHERE idempotency_key = $1`, in.IdempotencyKey).Scan(&storedApp); err != nil {
		t.Fatalf("read the key row's stored result: %v", err)
	}
	if storedApp != first.ApplicationID {
		t.Fatalf("the stored result names application %q, want the first apply's %s", storedApp, first.ApplicationID)
	}
}

// TestIntegration_FW1_12b_ADifferentRequestUnderASpentKeyIsRefusedNotReplayed pins the request
// hash's bite: the same key on its own invoice with a DIFFERENT request is refused 409, not
// replayed. Round 7's door compared only kind, credit and amount, so a retry that changed the
// reason replayed the first answer instead of being refused.
func TestIntegration_FW1_12b_ADifferentRequestUnderASpentKeyIsRefusedNotReplayed(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID, invA, _, creditID := spentKeyFixture(t, n)

	key := n + "-key-1"
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invA,
		AmountCents: 4000, Reason: "the first ask", IdempotencyKey: key,
	}, true); err != nil {
		t.Fatalf("the first apply: %v", err)
	}

	// The same key, the same invoice, the same amount, a different reason: a different request.
	_, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invA,
		AmountCents: 4000, Reason: "the second ask", IdempotencyKey: key,
	}, true)
	if err == nil {
		t.Fatalf("the door replayed a different request under a spent key, want the spent key conflict")
	}
	var conflict CreditApplicationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("the door answered %v, want the spent key conflict", err)
	}
	if !strings.Contains(err.Error(), "that idempotency key already applied a credit differently") {
		t.Fatalf("the refusal said %q, want the door's own spent key words", err.Error())
	}

	// A different amount on the same invoice is the same refusal.
	_, err = applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invA,
		AmountCents: 1000, Reason: "the first ask", IdempotencyKey: key,
	}, true)
	if !errors.As(err, &conflict) {
		t.Fatalf("the door answered %v for a different amount, want the spent key conflict", err)
	}

	// The invoice carries only the first application's movement.
	var invBalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invA).Scan(&invBalance); err != nil {
		t.Fatalf("read the invoice balance: %v", err)
	}
	if invBalance != 6000 {
		t.Fatalf("the invoice's balance is %d, want 6000", invBalance)
	}
}
