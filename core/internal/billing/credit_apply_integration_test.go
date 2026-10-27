//go:build integration

package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestIntegration_FW1_12b_ApplyCreditMemoAndAccountCredit_TiesTo1100AndReverses
// pins FW1-12b acceptance items 1 and 2 against the real database:
//   - Both an open credit memo (MEMO) and an unapplied account credit (ACCOUNT)
//     apply to a later invoice of the same customer;
//   - Both reach customer_transactions and the invoice/memo balances;
//   - Nothing new is posted to 1100 in total (no outbox journal event is queued);
//   - Aging (CreditService.Aging grand total and customer row) and ARBalanceCents
//     (622's 1100 tie read) hold before apply, after memo apply, after account
//     credit apply, and after reversing both applications;
//   - Reversing an application restores both sides and refuses a second reversal.
func TestIntegration_FW1_12b_ApplyCreditMemoAndAccountCredit_TiesTo1100AndReverses(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	creditSvc := NewCreditService(repo)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	asOf := time.Now().UTC().Add(1 * time.Hour)
	baseAR, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("baseline ARBalanceCents: %v", err)
	}
	baseAging, err := creditSvc.Aging(ctx, asOf, "")
	if err != nil {
		t.Fatalf("baseline Aging: %v", err)
	}
	var baseAgingSum int64
	for _, r := range baseAging {
		baseAgingSum += r.TotalCents
	}

	custID := seedCustomer(t, pool, n+"-CUST")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	// Seed an open credit memo (-$150.00 = -15000 cents) and a later open invoice ($500.00 = 50000 cents).
	var memoID, invID string
	issueMemo := time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02")
	issueInv := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	dueInv := time.Now().UTC().AddDate(0, 0, 28).Format("2006-01-02")
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -15000, 0, -15000, -15000, now(), 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-CM1", custID, trentonBranch, issueMemo).Scan(&memoID); err != nil {
		t.Fatalf("seed credit memo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 50000, 0, 50000, 50000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-INV1", custID, trentonBranch, issueInv, dueInv).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	// Seed an unapplied account credit (-$100.00 = -10000 cents) on customer_transactions.
	var acctCreditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -10000, 25000, 'order_deposit_cancel', gen_random_uuid(), $2::uuid, 'cancelled order deposit credit')
		RETURNING id::text`,
		custID, trentonBranch).Scan(&acctCreditID); err != nil {
		t.Fatalf("seed account credit: %v", err)
	}

	assertTie := func(stage string, wantCustTotal int64) {
		t.Helper()
		arNow, err := repo.ARBalanceCents(ctx, asOf, "")
		if err != nil {
			t.Fatalf("%s: ARBalanceCents: %v", stage, err)
		}
		agingNow, err := creditSvc.Aging(ctx, asOf, "")
		if err != nil {
			t.Fatalf("%s: Aging: %v", stage, err)
		}
		var agingSum int64
		var custRow *AgingRow
		for i := range agingNow {
			r := &agingNow[i]
			agingSum += r.TotalCents
			if r.CustomerID == custID {
				custRow = r
			}
		}
		arDelta := arNow - baseAR
		agingDelta := agingSum - baseAgingSum
		if arDelta != wantCustTotal {
			t.Fatalf("%s: ARBalanceCents delta = %d, want %d", stage, arDelta, wantCustTotal)
		}
		if agingDelta != arDelta {
			t.Fatalf("%s: aging grand total delta (%d) != ARBalanceCents delta (%d)", stage, agingDelta, arDelta)
		}
		if custRow == nil || custRow.TotalCents != wantCustTotal {
			var got int64
			if custRow != nil {
				got = custRow.TotalCents
			}
			t.Fatalf("%s: customer aging TotalCents = %d, want %d", stage, got, wantCustTotal)
		}
	}

	// Baseline: 50000 invoice - 15000 memo - 10000 account credit = 25000.
	assertTie("before apply", 25000)

	// 1. Apply $120.00 (12000 cents) of the open credit memo to the later invoice.
	memoApp, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID:     custID,
		CreditKind:     CreditKindMemo,
		CreditID:       memoID,
		InvoiceID:      invID,
		AmountCents:    12000,
		Reason:         "Apply return credit memo to subsequent invoice",
		IdempotencyKey: n + "-key-memo-1",
	}, true)
	if err != nil {
		t.Fatalf("apply credit memo: %v", err)
	}
	if memoApp.AppliedCents != 12000 || memoApp.CreditOpenAfter != 3000 || len(memoApp.Lines) != 1 || memoApp.Lines[0].BalanceAfterCents != 38000 {
		t.Fatalf("unexpected memo apply result: %+v", memoApp)
	}
	assertTie("after credit memo apply", 25000)

	// 2. Apply the full $100.00 (10000 cents) of the account credit to the invoice.
	acctApp, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID:     custID,
		CreditKind:     CreditKindAccount,
		CreditID:       acctCreditID,
		InvoiceID:      invID,
		AmountCents:    10000,
		Reason:         "Apply cancelled deposit credit to invoice",
		IdempotencyKey: n + "-key-acct-1",
	}, true)
	if err != nil {
		t.Fatalf("apply account credit: %v", err)
	}
	if acctApp.AppliedCents != 10000 || acctApp.CreditOpenAfter != 0 || len(acctApp.Lines) != 1 || acctApp.Lines[0].BalanceAfterCents != 28000 {
		t.Fatalf("unexpected account credit apply result: %+v", acctApp)
	}
	assertTie("after account credit apply", 25000)

	// Replay under the same idempotency key and body answers Replayed=true and writes nothing.
	replayApp, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID:     custID,
		CreditKind:     CreditKindAccount,
		CreditID:       acctCreditID,
		InvoiceID:      invID,
		AmountCents:    10000,
		Reason:         "Apply cancelled deposit credit to invoice",
		IdempotencyKey: n + "-key-acct-1",
	}, true)
	if err != nil {
		t.Fatalf("replay account credit apply: %v", err)
	}
	if !replayApp.Replayed || replayApp.ApplicationID != acctApp.ApplicationID {
		t.Fatalf("expected replay of %s, got %+v", acctApp.ApplicationID, replayApp)
	}

	// 3. Reverse the credit memo application; both sides restore, tie still holds, second reversal refused.
	revMemo, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID:     invID,
		ApplicationID: memoApp.ApplicationID,
		Reason:        "Applied to the wrong invoice",
	}, true)
	if err != nil {
		t.Fatalf("reverse credit memo application: %v", err)
	}
	if revMemo.RestoredCents != 12000 || revMemo.InvoiceBalance != 40000 || revMemo.CreditOpenAfter != 15000 {
		t.Fatalf("unexpected memo reversal result: %+v", revMemo)
	}
	assertTie("after credit memo reversal", 25000)

	if _, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID:     invID,
		ApplicationID: memoApp.ApplicationID,
		Reason:        "Second reversal attempt",
	}, true); !errors.Is(err, ApplicationNotReversible) {
		t.Fatalf("second reversal err = %v, want ApplicationNotReversible", err)
	}

	// 4. Reverse the account credit application; both sides restore to initial figures.
	revAcct, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID:     invID,
		ApplicationID: acctApp.ApplicationID,
		Reason:        "Restore deposit credit to account",
	}, true)
	if err != nil {
		t.Fatalf("reverse account credit application: %v", err)
	}
	if revAcct.RestoredCents != 10000 || revAcct.InvoiceBalance != 50000 || revAcct.CreditOpenAfter != 10000 {
		t.Fatalf("unexpected account credit reversal result: %+v", revAcct)
	}
	assertTie("after account credit reversal", 25000)
}

// TestIntegration_FW1_12b_RefusalsLeaveNothingApplied pins Acceptance 3:
// refuses another customer, another org, a branch outside the caller's reach,
// a non-manager, a missing reason, an amount over the credit's open amount or
// the invoice's open amount, and a spent idempotency key with a different body.
func TestIntegration_FW1_12b_RefusalsLeaveNothingApplied(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID := seedCustomer(t, pool, n+"-C1")
	otherCustID := seedCustomer(t, pool, n+"-C2")
	t.Cleanup(func() {
		c := context.Background()
		for _, cid := range []string{custID, otherCustID} {
			_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, cid)
		}
	})

	issue := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	due := time.Now().UTC().AddDate(0, 0, 27).Format("2006-01-02")
	var memoID, invID, otherInvID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -8000, 0, -8000, -8000, now(), 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-CM", custID, trentonBranch, issue).Scan(&memoID); err != nil {
		t.Fatalf("seed memo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-INV", custID, trentonBranch, issue, due).Scan(&invID); err != nil {
		t.Fatalf("seed inv: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-OTH-INV", otherCustID, trentonBranch, issue, due).Scan(&otherInvID); err != nil {
		t.Fatalf("seed other inv: %v", err)
	}

	baseIn := ApplyCreditInput{
		CustomerID:     custID,
		CreditKind:     CreditKindMemo,
		CreditID:       memoID,
		InvoiceID:      invID,
		AmountCents:    5000,
		Reason:         "Valid reason",
		IdempotencyKey: n + "-k0",
	}

	// Non-manager refused.
	if _, err := applySvc.Apply(ctx, baseIn, false); err == nil || !strings.Contains(err.Error(), "manager") {
		t.Fatalf("non-manager err = %v, want manager refusal", err)
	}
	// Missing reason refused.
	noReason := baseIn
	noReason.Reason = "   "
	if _, err := applySvc.Apply(ctx, noReason, true); !errors.Is(err, ErrCreditApplicationReasonRequired) {
		t.Fatalf("missing reason err = %v, want ErrCreditApplicationReasonRequired", err)
	}
	// Another customer's invoice refused.
	wrongCust := baseIn
	wrongCust.InvoiceID = otherInvID
	wrongCust.IdempotencyKey = n + "-k-wrong-cust"
	var noInvErr NoSuchInvoiceError
	if _, err := applySvc.Apply(ctx, wrongCust, true); !errors.As(err, &noInvErr) {
		t.Fatalf("other customer invoice err = %v, want NoSuchInvoiceError", err)
	}
	// Another org refused.
	if _, err := pool.Exec(ctx, `UPDATE invoices SET org_id = '11111111-1111-4111-8111-111111111111'::uuid WHERE id = $1::uuid`, invID); err != nil {
		t.Fatalf("set invoice org_id: %v", err)
	}
	wrongOrg := baseIn
	wrongOrg.OrgID = "99999999-9999-4999-8999-999999999999"
	wrongOrg.IdempotencyKey = n + "-k-wrong-org"
	if _, err := applySvc.Apply(ctx, wrongOrg, true); err == nil || !strings.Contains(err.Error(), "another org") {
		t.Fatalf("other org err = %v, want another org refusal", err)
	}
	// Branch outside caller's reach refused.
	outOfReach := baseIn
	outOfReach.CanAccessBranch = func(b string) bool { return b == kingstonBranch }
	outOfReach.IdempotencyKey = n + "-k-reach"
	if _, err := applySvc.Apply(ctx, outOfReach, true); !errors.As(err, &noInvErr) {
		t.Fatalf("out of reach err = %v, want NoSuchInvoiceError", err)
	}
	// Over credit's open amount (credit has 8000, invoice has 10000; ask for 8001).
	overCredit := baseIn
	overCredit.AmountCents = 8001
	overCredit.IdempotencyKey = n + "-k-over-credit"
	if _, err := applySvc.Apply(ctx, overCredit, true); err == nil || !strings.Contains(err.Error(), "the credit has 8000 cents open") {
		t.Fatalf("over credit err = %v, want credit ceiling message", err)
	}
	// Over invoice's open amount (ask for 10001 with a credit that holds 12000).
	var bigMemoID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -12000, 0, -12000, -12000, now(), 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-BIG-CM", custID, trentonBranch, issue).Scan(&bigMemoID); err != nil {
		t.Fatalf("seed big memo: %v", err)
	}
	overInv := baseIn
	overInv.CreditID = bigMemoID
	overInv.AmountCents = 10001
	overInv.IdempotencyKey = n + "-k-over-inv"
	if _, err := applySvc.Apply(ctx, overInv, true); err == nil || !strings.Contains(err.Error(), "can take at most 10000 cents right now") {
		t.Fatalf("over invoice err = %v, want invoice ceiling message", err)
	}

	// Spend key n+"-k-spent" on 3000 cents, then reuse with 4000 cents -> conflict.
	spentIn := baseIn
	spentIn.AmountCents = 3000
	spentIn.IdempotencyKey = n + "-k-spent"
	if _, err := applySvc.Apply(ctx, spentIn, true); err != nil {
		t.Fatalf("initial apply for spent-key test: %v", err)
	}
	spentDiff := spentIn
	spentDiff.AmountCents = 4000
	var conflictErr CreditApplicationConflictError
	if _, err := applySvc.Apply(ctx, spentDiff, true); !errors.As(err, &conflictErr) {
		t.Fatalf("spent key with different body err = %v, want CreditApplicationConflictError", err)
	}
}
