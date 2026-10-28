//go:build integration

package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// ROUND 7, THE ACCOUNT DOOR'S CREDIT FAMILIES (M3 P2-1 and M3 P2-2).
//
// M3 P2-1: the door read its credit group with txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO'),
// while the aging, ARBalanceCents, the statement, the account read and the deposit cancel all read
// the same group with WRITE_OFF in the list as well. So a group the aging reads as ZERO, so that
// the customer holds nothing open in it, read to the door as an open credit: the desk's own open
// credits read listed it, the door applied it, and the group's sum turned POSITIVE, a debit the
// customer never owed. The AR total does not move, so the 1100 tie cannot see it.
//
// M3 P2-2: the door read "a group whose sum is negative" as "a credit the customer holds", but a
// negative row can be money the customer PAID rather than money the customer HOLDS. The NSF fee
// payment row is exactly that: a negative ADJUSTMENT row in the 'payment_nsf' family. The fee's
// charge and its payment share a group, so the group reads zero and the door refuses it. Charged at
// one branch and paid at another, the group splits by branch_id and the paying side stands alone
// at a negative sum: the desk lists it as an open credit and the door applies it, so the invoice's
// balance drops by a fee the customer already paid.
//
// The fix names the credit families the door accepts (pos_return, deposit_cancel) instead of
// inferring credit from a group's negative sum, and it reads the group the way the aging reads it.
//
// The first leg's group is in 'deposit_cancel', one of the families the door accepts, so the leg
// is about the group sum and not about the family's name.

// TestIntegration_FW1_12b_AnAccountGroupTheAgingReadsAsZeroIsNotAnOpenCredit
// pins M3 P2-1: a credit group the aging reads as zero is not a credit the customer holds, so the
// desk must not offer it and the door must not apply it.
func TestIntegration_FW1_12b_AnAccountGroupTheAgingReadsAsZeroIsNotAnOpenCredit(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)
	applyOrg := plantApplyOrg(t, pool, n)

	custID := seedCustomer(t, pool, n+"-CUST")

	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 50000, 0, 50000, 50000, now(), 'INVOICE')
		RETURNING id::text`, n+"-INV", custID, trentonBranch).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	// One group in a credit family the door accepts: a credit of 25000 and a write off of 25000
	// against it. The group nets to zero, which is what the aging reads and what the AR balance
	// counts, so the customer holds nothing open in it.
	groupRef := uuid.NewString()
	var creditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -25000, 25000, 'deposit_cancel', $2::uuid, $3::uuid, 'the credit')
		RETURNING id::text`, custID, groupRef, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed the credit row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'WRITE_OFF', 25000, 25000, 'deposit_cancel', $2::uuid, $3::uuid, 'written off')`,
		custID, groupRef, trentonBranch); err != nil {
		t.Fatalf("seed the write off row: %v", err)
	}

	// The aging's group sum: the same group, read the way the aging reads it.
	var agingSum int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND COALESCE(source_type, '') <> 'invoice'
		   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')
		   AND COALESCE(source_type, '') = 'deposit_cancel'
		   AND COALESCE(source_ref_id, id) = $2::uuid
		   AND COALESCE(branch_id::text, '') = $3`, custID, groupRef, trentonBranch).Scan(&agingSum); err != nil {
		t.Fatalf("read the aging's group sum: %v", err)
	}
	if agingSum != 0 {
		t.Fatalf("the fixture is wrong: the aging reads the group as %d, want 0", agingSum)
	}

	// The desk's own open credits read must not offer it.
	open, err := repo.OpenCreditsFor(ctx, custID, trentonBranch)
	if err != nil {
		t.Fatalf("read the open credits: %v", err)
	}
	for _, c := range open {
		if c.CreditKind == CreditKindAccount && c.CreditID == creditID {
			t.Fatalf("the desk offers a group the aging reads as zero: kind=%s id=%s open=%d", c.CreditKind, c.CreditID, c.OpenCents)
		}
	}

	// The door must refuse it.
	app, aerr := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID:     custID,
		CreditKind:     CreditKindAccount,
		CreditID:       creditID,
		InvoiceID:      invID,
		AmountCents:    25000,
		Reason:         "the written off credit",
		OrgID:          applyOrg,
		IdempotencyKey: n + "-key-1",
	}, true)
	if aerr == nil {
		t.Fatalf("the door applied a group the aging reads as zero: %+v", app)
	}
	if !errors.Is(aerr, CreditApplicationNotOpen) {
		t.Fatalf("the door refused the written off group with %v, want %v", aerr, CreditApplicationNotOpen)
	}

	// Nothing moved: the invoice's balance and the group's sum are where they were.
	var invBalance, groupAfter int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&invBalance); err != nil {
		t.Fatalf("read the invoice balance: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND COALESCE(source_type, '') <> 'invoice'
		   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')
		   AND COALESCE(source_type, '') = 'deposit_cancel'
		   AND COALESCE(source_ref_id, id) = $2::uuid
		   AND COALESCE(branch_id::text, '') = $3`, custID, groupRef, trentonBranch).Scan(&groupAfter); err != nil {
		t.Fatalf("read the group's sum after: %v", err)
	}
	if invBalance != 50000 {
		t.Fatalf("the refused application moved the invoice: the balance is %d, want 50000", invBalance)
	}
	if groupAfter != 0 {
		t.Fatalf("the group's sum is %d, want 0: the customer never owed a positive group", groupAfter)
	}
}

// TestIntegration_FW1_12b_APaidNSFIsNotACreditTheDoorMayApply
// pins M3 P2-2: the NSF fee payment row is money the customer PAID, not money the customer HOLDS,
// so the desk must not offer it and the door must not apply it.
func TestIntegration_FW1_12b_APaidNSFIsNotACreditTheDoorMayApply(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)
	applyOrg := plantApplyOrg(t, pool, n)

	custID := seedCustomer(t, pool, n+"-CUST")

	// A later invoice at Kingston.
	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`, n+"-INV", custID, kingstonBranch).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	// The NSF fee: charged at Trenton (the till's NSF charge row), paid at Kingston (the payments
	// module's NSF fee payment row). The group key carries branch_id, so the two rows stand in two
	// groups and the paying side stands alone.
	nsfID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', 2500, 2500, 'payment_nsf', $2::uuid, $3::uuid, 'NSF fee')`,
		custID, nsfID, trentonBranch); err != nil {
		t.Fatalf("seed the NSF fee charge: %v", err)
	}
	var feePayID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -2500, 0, 'payment_nsf', $2::uuid, $3::uuid, 'NSF fee payment')
		RETURNING id::text`, custID, nsfID, kingstonBranch).Scan(&feePayID); err != nil {
		t.Fatalf("seed the NSF fee payment: %v", err)
	}

	// The desk's own open credits read must not offer the fee the customer already paid.
	open, err := repo.OpenCreditsFor(ctx, custID, kingstonBranch)
	if err != nil {
		t.Fatalf("read the open credits: %v", err)
	}
	for _, c := range open {
		if c.CreditID == feePayID {
			t.Fatalf("the desk offers a fee the customer already paid: kind=%s id=%s open=%d", c.CreditKind, c.CreditID, c.OpenCents)
		}
	}

	// The door must refuse it: a payment_nsf row is a debt, never a credit.
	app, aerr := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID:     custID,
		CreditKind:     CreditKindAccount,
		CreditID:       feePayID,
		InvoiceID:      invID,
		AmountCents:    2500,
		Reason:         "the paid NSF fee",
		OrgID:          applyOrg,
		IdempotencyKey: n + "-key-1",
	}, true)
	if aerr == nil {
		t.Fatalf("the door applied a fee the customer already paid: %+v", app)
	}
	var nsfErr NoSuchCreditError
	if !errors.As(aerr, &nsfErr) {
		t.Fatalf("the door refused the paid fee with %v, want no such credit", aerr)
	}

	// Nothing moved: the invoice still carries its whole balance and the fee still stands at the
	// branch it was charged at.
	var invBalance, trentonFee int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&invBalance); err != nil {
		t.Fatalf("read the invoice balance: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'payment_nsf' AND source_ref_id = $2::uuid
		   AND branch_id = $3::uuid`, custID, nsfID, trentonBranch).Scan(&trentonFee); err != nil {
		t.Fatalf("read the Trenton fee group: %v", err)
	}
	if invBalance != 10000 {
		t.Fatalf("the refused application moved the invoice: the balance is %d, want 10000", invBalance)
	}
	if trentonFee != 2500 {
		t.Fatalf("the fee at the branch it was charged at is %d, want 2500", trentonFee)
	}
}

// TestIntegration_FW1_12b_TheDoorAndTheAgingReadOneGroupTheSameWay
// pins the group's own arithmetic: the door's group figure and the aging's group figure are ONE figure
// for the same group. A group of a credit of 25000 and a write off of 10000 nets to 15000, so the desk
// offers 15000 open (not the 25000 the credit alone carries) and the door applies 15000 of it, which
// leaves the group at zero.
func TestIntegration_FW1_12b_TheDoorAndTheAgingReadOneGroupTheSameWay(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)
	applyOrg := plantApplyOrg(t, pool, n)

	custID := seedCustomer(t, pool, n+"-CUST")

	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 50000, 0, 50000, 50000, now(), 'INVOICE')
		RETURNING id::text`, n+"-INV", custID, trentonBranch).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	// One group in a credit family the door accepts: a credit of 25000 and a write off of 10000
	// against it, the write off a moment later so the group's first row is the credit.
	groupRef := uuid.NewString()
	var creditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		VALUES ($1::uuid, 'ADJUSTMENT', -25000, 25000, 'deposit_cancel', $2::uuid, $3::uuid, 'the credit', now() - interval '1 hour')
		RETURNING id::text`, custID, groupRef, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed the credit row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		VALUES ($1::uuid, 'WRITE_OFF', 10000, 25000, 'deposit_cancel', $2::uuid, $3::uuid, 'written off', now())`,
		custID, groupRef, trentonBranch); err != nil {
		t.Fatalf("seed the write off row: %v", err)
	}

	// The aging's group figure.
	var agingSum int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND COALESCE(source_type, '') <> 'invoice'
		   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')
		   AND COALESCE(source_type, '') = 'deposit_cancel'
		   AND COALESCE(source_ref_id, id) = $2::uuid
		   AND COALESCE(branch_id::text, '') = $3`, custID, groupRef, trentonBranch).Scan(&agingSum); err != nil {
		t.Fatalf("read the aging's group sum: %v", err)
	}
	if agingSum != -15000 {
		t.Fatalf("the fixture is wrong: the aging reads the group as %d, want -15000", agingSum)
	}

	// The desk offers the group at the aging's figure.
	open, err := repo.OpenCreditsFor(ctx, custID, trentonBranch)
	if err != nil {
		t.Fatalf("read the open credits: %v", err)
	}
	offered := false
	for _, c := range open {
		if c.CreditKind == CreditKindAccount && c.CreditID == creditID {
			offered = true
			if c.OpenCents != 15000 {
				t.Fatalf("the desk offers the group as %d open, want the aging's figure 15000", c.OpenCents)
			}
		}
	}
	if !offered {
		t.Fatalf("the desk does not offer the group at all")
	}

	// And the door applies that figure, which leaves the group at zero.
	app, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID:     custID,
		CreditKind:     CreditKindAccount,
		CreditID:       creditID,
		InvoiceID:      invID,
		AmountCents:    15000,
		Reason:         "the group as the aging reads it",
		OrgID:          applyOrg,
		IdempotencyKey: n + "-key-1",
	}, true)
	if err != nil {
		t.Fatalf("the apply: %v", err)
	}
	if app.CreditOpenAfter != 0 {
		t.Fatalf("the door reports %d open after the application, want 0: the group nets to zero", app.CreditOpenAfter)
	}
	var groupAfter int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND COALESCE(source_type, '') <> 'invoice'
		   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')
		   AND COALESCE(source_type, '') = 'deposit_cancel'
		   AND COALESCE(source_ref_id, id) = $2::uuid
		   AND COALESCE(branch_id::text, '') = $3`, custID, groupRef, trentonBranch).Scan(&groupAfter); err != nil {
		t.Fatalf("read the group's sum after: %v", err)
	}
	if groupAfter != 0 {
		t.Fatalf("the group's sum after the application is %d, want 0", groupAfter)
	}
}
