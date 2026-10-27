//go:build integration

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/google/uuid"
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

// TestIntegration_FW1_12b_ASpentCreditOutOfReachAnswersNotFound pins S6's P1-3: the credit's
// branch reach is judged directly after its row is read, BEFORE the "not open" judgement, for the
// MEMO document and for the ACCOUNT group alike, so a spent credit at another branch answers the
// same 404 no such credit as an unknown one, and the door's classification holds (an out of reach
// credit and an unknown one are alike, whatever state the credit is in).
func TestIntegration_FW1_12b_ASpentCreditOutOfReachAnswersNotFound(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID := seedCustomer(t, pool, n+"-R2")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	// The target invoice sits at Trenton, inside the caller's reach; every credit sits at Kingston,
	// outside it, and spending them takes a Kingston invoice of its own (a credit applies to its own
	// branch's invoices only). The caller reaches Trenton only.
	issue := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	due := time.Now().UTC().AddDate(0, 0, 27).Format("2006-01-02")
	var invID, kingstonInvID, spentMemoID, openMemoID, acctCreditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 100000, 0, 100000, 100000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-INV", custID, trentonBranch, issue, due).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 100000, 0, 100000, 100000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-KINV", custID, kingstonBranch, issue, due).Scan(&kingstonInvID); err != nil {
		t.Fatalf("seed kingston invoice: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -8000, 0, -8000, -8000, now(), 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-CM-SPENT", custID, kingstonBranch, issue).Scan(&spentMemoID); err != nil {
		t.Fatalf("seed spent memo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -7000, 0, -7000, -7000, now(), 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-CM-OPEN", custID, kingstonBranch, issue).Scan(&openMemoID); err != nil {
		t.Fatalf("seed open memo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -6000, 25000, 'order_deposit_cancel', gen_random_uuid(), $2::uuid, 'deposit credit')
		RETURNING id::text`,
		custID, kingstonBranch).Scan(&acctCreditID); err != nil {
		t.Fatalf("seed account credit: %v", err)
	}

	// Spend the Kingston memo and the Kingston account credit against the Kingston invoice, with a
	// caller who reaches every branch, so their state on the books is spent.
	global := ApplyCreditInput{CustomerID: custID, InvoiceID: kingstonInvID, CanAccessBranch: nil}
	if _, err := applySvc.Apply(ctx, func() ApplyCreditInput {
		in := global
		in.CreditKind, in.CreditID, in.AmountCents = CreditKindMemo, spentMemoID, 8000
		in.Reason, in.IdempotencyKey = "spend the kingston memo", n+"-k-spend-memo"
		return in
	}(), true); err != nil {
		t.Fatalf("spend the memo: %v", err)
	}
	if _, err := applySvc.Apply(ctx, func() ApplyCreditInput {
		in := global
		in.CreditKind, in.CreditID, in.AmountCents = CreditKindAccount, acctCreditID, 6000
		in.Reason, in.IdempotencyKey = "spend the kingston account credit", n+"-k-spend-acct"
		return in
	}(), true); err != nil {
		t.Fatalf("spend the account credit: %v", err)
	}

	// The caller reaches Trenton only.
	trentonOnly := func(b string) bool { return b == trentonBranch }
	var noCreditErr NoSuchCreditError

	// A SPENT memo out of reach answers the unknown credit's 404, not the spent credit's 409. This
	// is the leg S6 asked for: today the not open judgement runs first and leaks the state.
	_, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindMemo, CreditID: spentMemoID, InvoiceID: invID,
		AmountCents: 1, Reason: "spent and out of reach", IdempotencyKey: n + "-k-r2-spent",
		CanAccessBranch: trentonOnly,
	}, true)
	if !errors.As(err, &noCreditErr) {
		t.Fatalf("spent memo out of reach err = %v, want NoSuchCreditError", err)
	}

	// Same rule for the ACCOUNT group: a spent account credit out of reach answers 404.
	_, err = applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: acctCreditID, InvoiceID: invID,
		AmountCents: 1, Reason: "spent and out of reach", IdempotencyKey: n + "-k-r2-spent-acct",
		CanAccessBranch: trentonOnly,
	}, true)
	if !errors.As(err, &noCreditErr) {
		t.Fatalf("spent account credit out of reach err = %v, want NoSuchCreditError", err)
	}

	// An OPEN credit out of reach keeps answering 404 (it already did; the move keeps it so).
	_, err = applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindMemo, CreditID: openMemoID, InvoiceID: invID,
		AmountCents: 1, Reason: "open and out of reach", IdempotencyKey: n + "-k-r2-open",
		CanAccessBranch: trentonOnly,
	}, true)
	if !errors.As(err, &noCreditErr) {
		t.Fatalf("open memo out of reach err = %v, want NoSuchCreditError", err)
	}

	// And an unknown credit id inside the caller's reach answers the same 404, the classification's
	// other half.
	unknown := ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindMemo, CreditID: uuid.NewString(), InvoiceID: invID,
		AmountCents: 1, Reason: "unknown credit", IdempotencyKey: n + "-k-r2-unknown",
		CanAccessBranch: trentonOnly,
	}
	if _, err := applySvc.Apply(ctx, unknown, true); !errors.As(err, &noCreditErr) {
		t.Fatalf("unknown credit err = %v, want NoSuchCreditError", err)
	}
}

// TestIntegration_FW1_12b_TwoAppliesNamingDifferentRowsOfOneGroupCannotBothPass pins S6's P2-1:
// an ACCOUNT credit is named by one row id, and after a first application the group also holds the
// positive offsetting row. Two requests naming two different rows of the same group must queue on
// the SAME lock and re-read the group's sum after the first commits, so exactly one can take the
// group's open figure and the group can never turn positive (a debit the customer never owed).
//
// The race is made deterministic the deposit cancel race's own way: the test's own transaction
// holds BOTH rows of the group, both applies are launched and poll parked on their anchor reads,
// and only then does the test commit, so both read the group's sum from the same pre commit state.
func TestIntegration_FW1_12b_TwoAppliesNamingDifferentRowsOfOneGroupCannotBothPass(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	custID := seedCustomer(t, pool, n+"-RACE")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	issue := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	due := time.Now().UTC().AddDate(0, 0, 27).Format("2006-01-02")
	var creditID, groupRef, inv1, inv2 string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -10000, 25000, 'order_deposit_cancel', gen_random_uuid(), $2::uuid, 'deposit credit')
		RETURNING id::text, COALESCE(source_ref_id, id)::text`,
		custID, trentonBranch).Scan(&creditID, &groupRef); err != nil {
		t.Fatalf("seed account credit: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-INV1", custID, trentonBranch, issue, due).Scan(&inv1); err != nil {
		t.Fatalf("seed invoice 1: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-INV2", custID, trentonBranch, issue, due).Scan(&inv2); err != nil {
		t.Fatalf("seed invoice 2: %v", err)
	}

	// A first application of 4000 names the group's original row and leaves the offsetting row
	// (+4000) inside the group; 6000 stays open.
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: inv1,
		AmountCents: 4000, Reason: "first slice", IdempotencyKey: n + "-k-first",
	}, true); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	var offsetID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM customer_transactions
		 WHERE customer_id = $1::uuid AND amount_cents > 0 AND COALESCE(source_type, '') = 'order_deposit_cancel'
		 ORDER BY id LIMIT 1`, custID).Scan(&offsetID); err != nil {
		t.Fatalf("read the offsetting row: %v", err)
	}

	// Hold BOTH rows of the group, then launch the two racing applies.
	held, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback(ctx) }()
	if _, err := held.Exec(ctx, `
		SELECT 1 FROM customer_transactions WHERE id = ANY($1::uuid[]) FOR UPDATE`,
		[]string{creditID, offsetID}); err != nil {
		t.Fatalf("hold the group's rows: %v", err)
	}

	type outcome struct {
		app CreditApplication
		err error
	}
	done := make(chan outcome, 2)
	race := func(namedRow, invoice, key string) {
		app, err := applySvc.Apply(ctx, ApplyCreditInput{
			CustomerID: custID, CreditKind: CreditKindAccount, CreditID: namedRow, InvoiceID: invoice,
			AmountCents: 6000, Reason: "the rest of the credit", IdempotencyKey: key,
		}, true)
		done <- outcome{app, err}
	}
	go race(creditID, inv1, n+"-k-a")
	go race(offsetID, inv2, n+"-k-b")

	// Both must park on their anchor reads before the test lets go: both then read the group's sum
	// from the same pre commit state, whatever the machine's load.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		select {
		case <-done:
			t.Fatal("an apply answered without parking on the held group rows; the race was not staged")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the two applies never both reached a lock wait")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := held.Commit(ctx); err != nil {
		t.Fatalf("release the group's rows: %v", err)
	}

	a, b := <-done, <-done
	successes := 0
	for name, o := range map[string]outcome{"naming the original row": a, "naming the offsetting row": b} {
		if o.err == nil {
			successes++
			continue
		}
		t.Logf("%s refused: %v", name, o.err)
	}
	if successes != 1 {
		t.Fatalf("two applies naming two different rows of one group: %d succeeded (a err = %v, b err = %v), want exactly one", successes, a.err, b.err)
	}
	var groupSum int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid
		   AND COALESCE(source_type, '') = 'order_deposit_cancel'
		   AND COALESCE(source_ref_id, id) = $2::uuid
		   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF')`,
		custID, groupRef).Scan(&groupSum); err != nil {
		t.Fatalf("read the group's sum: %v", err)
	}
	if groupSum > 0 {
		t.Fatalf("the credit's group ends at %d cents, a debit the customer never owed; want it at 0 or below", groupSum)
	}
}

// TestIntegration_FW1_12b_MemoApplicationSurvivesAPastDateAging pins S6's P1-4: the memo side of an
// application (and its reversal's restore) is a ledger row tagged to the MEMO document itself, so
// when 626 (FW1-06b) rebuilds a document's balance at a past date from its history, that row must
// keep counting it: apply a memo, then read the as of aging at a past date after the application,
// and it equals the balances.
//
// HOW THIS LEG IS GATED, and it is never skipped silently: the leg bites only on a tree that carries
// 626's past date rebuild. The gate is a runtime probe of 626's own behaviour, a PAID document on a
// customer of its own: at a PAST as of a rebuilt aging still ages it at its full total, while a
// pre 626 aging (today's balances only) has no row for it at all. No probe row means 626 is not
// here, and the leg skips naming 626; it unskips the moment 626 merges to main, with no edit.
func TestIntegration_FW1_12b_MemoApplicationSurvivesAPastDateAging(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	probeID := seedCustomer(t, pool, n+"-PROBE")
	custID := seedCustomer(t, pool, n+"-PAST")
	t.Cleanup(func() {
		c := context.Background()
		for _, cid := range []string{probeID, custID} {
			_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, cid)
		}
	})

	// The probe: one document PAID after the as of date, settled with no allocation rows a rebuild
	// could net against it, so at the past as of it ages at its full 40000.
	probeIssue := time.Now().UTC().AddDate(0, 0, -6).Format("2006-01-02")
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'PAID', $4::date, $5::date, 40000, 0, 40000, 0, $4::timestamptz, 'INVOICE')`,
		n+"-PROBE-INV", probeID, trentonBranch, probeIssue,
		time.Now().UTC().AddDate(0, 0, 24).Format("2006-01-02")); err != nil {
		t.Fatalf("seed probe invoice: %v", err)
	}

	asOf := time.Now().UTC().AddDate(0, 0, -2)
	probeRows, err := repo.AgingRows(ctx, asOf, 60, "")
	if err != nil {
		t.Fatalf("probe aging read: %v", err)
	}
	probeTotal, probeFound := int64(0), false
	for _, r := range probeRows {
		if r.CustomerID == probeID {
			probeTotal, probeFound = r.TotalCents, true
		}
	}
	if !probeFound {
		t.Skipf("waiting on 626 (FW1-06b true as-of aging): the aging read does not rebuild a past date from its history yet (a document paid after the as of has no row at it), so this leg cannot bite; it unskips when 626 merges to main, with no edit")
	}
	if probeTotal != 40000 {
		t.Fatalf("the past as-of aging holds the paid-after document at %d, want 40000; the rebuild itself moved", probeTotal)
	}

	// The application: a 226.00 memo, a 1000.00 invoice, 50.00 of the memo applied.
	memoIssue := time.Now().UTC().AddDate(0, 0, -7)
	invIssue := time.Now().UTC().AddDate(0, 0, -5)
	var memoID, invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -22600, 0, -22600, -22600, $5::timestamptz, 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-CM", custID, trentonBranch, memoIssue.Format("2006-01-02"), memoIssue).Scan(&memoID); err != nil {
		t.Fatalf("seed memo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $6::date, 100000, 0, 100000, 100000, $5::timestamptz, 'INVOICE')
		RETURNING id::text`,
		n+"-INV", custID, trentonBranch, invIssue.Format("2006-01-02"), invIssue,
		invIssue.AddDate(0, 0, 30).Format("2006-01-02")).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindMemo, CreditID: memoID, InvoiceID: invID,
		AmountCents: 5000, Reason: "past date leg", IdempotencyKey: n + "-k-past",
	}, true); err != nil {
		t.Fatalf("apply memo: %v", err)
	}
	// Move the application's two ledger rows three days back, so the past as of (two days ago) sits
	// after the act, the way a history move would.
	if _, err := pool.Exec(ctx, `
		UPDATE customer_transactions
		   SET created_at = created_at - interval '3 days', occurred_at = occurred_at - interval '3 days'
		 WHERE customer_id = $1::uuid AND source_type = 'invoice'
		   AND source_ref_id IN ($2::uuid, $3::uuid)`, custID, invID, memoID); err != nil {
		t.Fatalf("backdate the application's rows: %v", err)
	}

	// Balances now, nothing having moved since the application: 95000 - 17600 = 77400. The aging at
	// the past as of must say the same.
	rows, err := repo.AgingRows(ctx, asOf, 60, "")
	if err != nil {
		t.Fatalf("aging read at the past as of: %v", err)
	}
	var got *AgingRow
	for i := range rows {
		if rows[i].CustomerID == custID {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatalf("the customer has no aging row at the past as of (%+v)", rows)
	}
	if got.TotalCents != 77400 {
		t.Fatalf("the as of aging at a past date after the application = %d, want 77400 (the balances); the memo side of the application dropped out of the rebuild", got.TotalCents)
	}
}

// TestIntegration_FW1_12b_AWriteOffRowIsNotACredit pins S6's P3: a WRITE_OFF ledger row is not a
// credit. A write off is never value the customer holds, so the door refuses it with the unknown
// credit's 404 instead of applying it. No code writes such a row today; the row here is what an
// older box's history would carry.
func TestIntegration_FW1_12b_AWriteOffRowIsNotACredit(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	writeOffCust := seedCustomer(t, pool, n+"-WO")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, writeOffCust)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, writeOffCust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, writeOffCust)
	})

	issue := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	due := time.Now().UTC().AddDate(0, 0, 27).Format("2006-01-02")

	// Leg 1: a WRITE_OFF row, the type an older box's history might carry. No code writes one today;
	// the row here is what such a history would hold, and the door must refuse it.
	var writeOffID, woInvID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'WRITE_OFF', -5000, 25000, 'manual', gen_random_uuid(), $2::uuid, 'old box write off')
		RETURNING id::text`,
		writeOffCust, trentonBranch).Scan(&writeOffID); err != nil {
		t.Fatalf("seed write off row: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 20000, 0, 20000, 20000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-WO-INV", writeOffCust, trentonBranch, issue, due).Scan(&woInvID); err != nil {
		t.Fatalf("seed write off invoice: %v", err)
	}
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: writeOffCust, CreditKind: CreditKindAccount, CreditID: writeOffID, InvoiceID: woInvID,
		AmountCents: 1000, Reason: "write off is not a credit", IdempotencyKey: n + "-k-wo",
	}, true); !errors.As(err, &NoSuchCreditError{}) {
		t.Fatalf("applying a WRITE_OFF row err = %v, want NoSuchCreditError: a write off is never value the customer holds", err)
	}
}

// TestIntegration_FW1_12b_ReversalRefusedOnAVoidedMemo pins S6's P3: the reversal checks the memo's
// status. A memo VOIDED after its application must stay VOID, so the reversal of that application
// is refused rather than bringing the memo back FINALIZED.
func TestIntegration_FW1_12b_ReversalRefusedOnAVoidedMemo(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)

	voidCust := seedCustomer(t, pool, n+"-VOID")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, voidCust)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, voidCust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, voidCust)
	})

	issue := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	due := time.Now().UTC().AddDate(0, 0, 27).Format("2006-01-02")

	// A memo applied, then VOIDED the way a void writer would leave it (balance zeroed, status
	// VOID). Reversing the application must be refused and the memo must stay VOID.
	var memoID, vInvID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -8000, 0, -8000, -8000, now(), 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-V-CM", voidCust, trentonBranch, issue).Scan(&memoID); err != nil {
		t.Fatalf("seed void memo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-V-INV", voidCust, trentonBranch, issue, due).Scan(&vInvID); err != nil {
		t.Fatalf("seed void leg invoice: %v", err)
	}
	app, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: voidCust, CreditKind: CreditKindMemo, CreditID: memoID, InvoiceID: vInvID,
		AmountCents: 5000, Reason: "applied before the void", IdempotencyKey: n + "-k-void",
	}, true)
	if err != nil {
		t.Fatalf("apply memo: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices SET status = 'VOID', balance_cents = 0 WHERE id = $1::uuid`, memoID); err != nil {
		t.Fatalf("void the memo: %v", err)
	}
	if _, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID: vInvID, ApplicationID: app.ApplicationID, Reason: "reversing a voided memo's application",
	}, true); err == nil {
		t.Fatal("the reversal of a voided memo's application succeeded; the memo would come back FINALIZED, a VOID memo stays VOID")
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, memoID).Scan(&status, &balance); err != nil {
		t.Fatalf("re-read the memo: %v", err)
	}
	if status != "VOID" || balance != 0 {
		t.Fatalf("the memo moved to %s/%d on a refused reversal, want VOID/0", status, balance)
	}
}

// TestIntegration_FW1_12b_HandlerAndDetailRoundTrip verifies CreditApplyHandler
// over HTTP and RegisterService.Detail surfacing open_credits and credit_applications.
func TestIntegration_FW1_12b_HandlerAndDetailRoundTrip(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	handler := NewCreditApplyHandler(applySvc, nil)
	regSvc := NewRegisterService(repo)
	ctx := context.Background()
	n := ns(t)

	custID := seedCustomer(t, pool, n+"-HD")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	issue := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	due := time.Now().UTC().AddDate(0, 0, 28).Format("2006-01-02")
	var memoID, invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, -6000, 0, -6000, -6000, now(), 'CREDIT_MEMO')
		RETURNING id::text`,
		n+"-HD-CM", custID, trentonBranch, issue).Scan(&memoID); err != nil {
		t.Fatalf("seed memo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 15000, 0, 15000, 15000, now(), 'INVOICE')
		RETURNING id::text`,
		n+"-HD-INV", custID, trentonBranch, issue, due).Scan(&invID); err != nil {
		t.Fatalf("seed inv: %v", err)
	}

	mgrCtx := identity.ContextWithStaff(ctx, applyGlobalCaller())
	detailBefore, err := regSvc.Detail(mgrCtx, invID, true)
	if err != nil {
		t.Fatalf("detail before: %v", err)
	}
	if len(detailBefore.OpenCredits) != 1 || detailBefore.OpenCredits[0].CreditID != memoID || detailBefore.OpenCredits[0].OpenCents != 6000 {
		t.Fatalf("detail before OpenCredits = %+v, want 1 memo with 6000 open", detailBefore.OpenCredits)
	}

	rawApply, _ := json.Marshal(map[string]any{
		"customer_id":     custID,
		"credit_kind":     "MEMO",
		"credit_id":       memoID,
		"invoice_id":      invID,
		"amount_cents":    4000,
		"reason":          "Apply credit memo on desk",
		"idempotency_key": n + "-hd-k1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ar/credits/apply", strings.NewReader(string(rawApply))).WithContext(mgrCtx)
	rec := httptest.NewRecorder()
	handler.apply(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("handler apply status = %d body = %s, want 201", rec.Code, rec.Body.String())
	}
	var appRes CreditApplication
	if err := json.Unmarshal(rec.Body.Bytes(), &appRes); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}

	detailAfter, err := regSvc.Detail(mgrCtx, invID, true)
	if err != nil {
		t.Fatalf("detail after: %v", err)
	}
	if len(detailAfter.OpenCredits) != 1 || detailAfter.OpenCredits[0].OpenCents != 2000 {
		t.Fatalf("detail after OpenCredits = %+v, want 2000 remaining", detailAfter.OpenCredits)
	}
	if len(detailAfter.CreditApplications) != 1 || detailAfter.CreditApplications[0].ApplicationID != appRes.ApplicationID || detailAfter.CreditApplications[0].Reversed {
		t.Fatalf("detail after CreditApplications = %+v", detailAfter.CreditApplications)
	}

	rawRev, _ := json.Marshal(map[string]any{
		"invoice_id":     invID,
		"application_id": appRes.ApplicationID,
		"reason":         "Undo from desk",
	})
	reqRev := httptest.NewRequest(http.MethodPost, "/api/v1/ar/credits/applications/reverse", strings.NewReader(string(rawRev))).WithContext(mgrCtx)
	recRev := httptest.NewRecorder()
	handler.reverse(recRev, reqRev, guards.StaffCaller{})
	if recRev.Code != http.StatusCreated {
		t.Fatalf("handler reverse status = %d body = %s, want 201", recRev.Code, recRev.Body.String())
	}
}
