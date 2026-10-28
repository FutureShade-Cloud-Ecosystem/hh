//go:build integration

package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ROUND 7, THE REVERSAL'S STATUS HANDLING (M3 P3-2), ROUND 9: THE LIVE REACH.
//
// The reversal restored the invoice's balance AND its status without judging the invoice's status, so a
// VOID invoice came back FINALIZED: a document the desk had voided came back as an open receivable. One
// production door voids an invoice: the deposit cancel's own settlement (deposit_cancel.go) closes an
// unpaid supplier deposit document with the status VOID, and the order cancel reaches it
// (order/deposit_cancel_seam.go), so the void case is live, not dormant, and this file's first leg drives
// that close rather than seeding a VOID row by hand, so the gate's reach is pinned to the writer that
// makes it.
//
// The reversal also set the memo's status to FINALIZED rather than restoring the status the memo carried
// before the application, so a memo that was not FINALIZED came back FINALIZED.
//
// The fix: the reversal judges the invoice's status (a VOID invoice stays VOID, refused in words), and the
// application record carries the memo's prior status so the reversal restores that one.

// TestIntegration_FW1_12b_AReversalRefusesAVoidInvoiceAndLeavesItVoid pins M3 P3-2's invoice side through
// the deposit cancel's own close.
func TestIntegration_FW1_12b_AReversalRefusesAVoidInvoiceAndLeavesItVoid(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	w := NewInvoiceWriter(pool)
	ctx := context.Background()
	n := ns(t)
	applyOrg := plantApplyOrg(t, pool, n)

	// An unpaid supplier deposit document on its order, the shape the deposit cancel's
	// settlement voids (settleDepositDocument: a document still carrying balance leaves the
	// live set VOID under CREDIT_BACK).
	custID, orderID, depID := seedDepositOrderAt(t, pool, n, 4_000)

	var creditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -7500, -7500, 'deposit_cancel', $2::uuid, $3::uuid, 'the credit')
		RETURNING id::text`, custID, depID, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed the credit: %v", err)
	}

	// A PART application: 2500 of the credit onto the document, so the document still owes
	// 1500 when the cancel lands. A document the application had taken to balance 0 is PAID
	// and the settlement leaves it untouched, so the VOID case needs the balance to survive
	// the application.
	app, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: depID,
		OrgID:       applyOrg,
		AmountCents: 2500, Reason: "part of the credit", IdempotencyKey: n + "-key-1",
	}, true)
	if err != nil {
		t.Fatalf("the apply: %v", err)
	}

	// THE LIVE REACH: the deposit cancel's own close voids the document. The order cancel's
	// own path reaches this settlement; the leg drives it directly, so the gate below is
	// pinned to the one production writer that voids an invoice.
	settled, err := w.CancelOrderDeposits(ctx, nil, orderID, DepositCancelCreditBack, "the order's cancel")
	if err != nil {
		t.Fatalf("the deposit cancel: %v", err)
	}
	if settled != 1 {
		t.Fatalf("the deposit cancel settled %d documents, want 1", settled)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, depID).Scan(&status, &balance); err != nil {
		t.Fatalf("read the document: %v", err)
	}
	if status != "VOID" || balance != 0 {
		t.Fatalf("the fixture is wrong: the deposit cancel left the document %s/%d, want VOID/0", status, balance)
	}

	res, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID: depID, ApplicationID: app.ApplicationID, Reason: "undo the application",
	}, true)
	if err == nil {
		t.Fatalf("the reversal answered %+v, want it refused on the voided deposit document", res)
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the reversal answered %v, want it refused in words", err)
	}
	if !strings.Contains(err.Error(), "void") {
		t.Fatalf("the reversal answered %v, want the refusal to name the void document", err)
	}

	// The document stays VOID at the balance the application and the close left: nothing was
	// restored, and a cancelled deposit document was not written back onto the books as an
	// open receivable.
	if err := pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, depID).Scan(&status, &balance); err != nil {
		t.Fatalf("read the document: %v", err)
	}
	if status != "VOID" {
		t.Fatalf("the refused reversal left the document %s, want VOID", status)
	}
	if balance != 0 {
		t.Fatalf("the refused reversal moved the document: the balance is %d, want 0", balance)
	}

	// The customer still holds the credit: the group (the credit, the application's offsetting
	// row, the settlement's credit back of the paid part) sums to -7500 and nothing moved it.
	var groupSum int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND COALESCE(source_type, '') = 'deposit_cancel'
		   AND COALESCE(source_ref_id, id) = $2::uuid`, custID, depID).Scan(&groupSum); err != nil {
		t.Fatalf("read the credit's group: %v", err)
	}
	if groupSum != -7500 {
		t.Fatalf("the credit's group sums to %d after the refused reversal, want -7500: the customer's credit moved", groupSum)
	}
}

// TestIntegration_FW1_12b_AReversalRestoresTheMemosPriorStatus pins M3 P3-2's memo side: the reversal
// restores the status the memo carried before the application, not FINALIZED.
func TestIntegration_FW1_12b_AReversalRestoresTheMemosPriorStatus(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	applySvc := NewCreditApplyService(repo)
	ctx := context.Background()
	n := ns(t)
	applyOrg := plantApplyOrg(t, pool, n)

	custID := seedCustomer(t, pool, n+"-CUST")

	// One credit memo that is not FINALIZED: the status the memo carried before its application is
	// the one the reversal must restore.
	var memoID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'DRAFT', CURRENT_DATE, CURRENT_DATE, -10000, 0, -10000, -10000, 'CREDIT_MEMO')
		RETURNING id::text`, n+"-CM", custID, trentonBranch).Scan(&memoID); err != nil {
		t.Fatalf("seed the credit memo: %v", err)
	}
	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`, n+"-INV", custID, trentonBranch).Scan(&invID); err != nil {
		t.Fatalf("seed the invoice: %v", err)
	}

	app, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindMemo, CreditID: memoID, InvoiceID: invID,
		OrgID:       applyOrg,
		AmountCents: 1000, Reason: "a slice of the memo", IdempotencyKey: n + "-key-1",
	}, true)
	if err != nil {
		t.Fatalf("the apply: %v", err)
	}

	// The application left the memo's status alone: it only flips it when the memo is used up.
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1::uuid`, memoID).Scan(&status); err != nil {
		t.Fatalf("read the memo's status: %v", err)
	}
	if status != "DRAFT" {
		t.Fatalf("the fixture is wrong: the memo is %s after its application, want DRAFT", status)
	}

	if _, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID: invID, ApplicationID: app.ApplicationID, Reason: "undo the application",
	}, true); err != nil {
		t.Fatalf("the reversal: %v", err)
	}

	// The memo carries the status it carried before the application.
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1::uuid`, memoID).Scan(&status); err != nil {
		t.Fatalf("read the memo's status: %v", err)
	}
	if status != "DRAFT" {
		t.Fatalf("the reversal left the memo %s, want the status it carried before the application (DRAFT)", status)
	}

	// And the money: the memo's balance is back where it was and the invoice's too.
	var memoBalance, invBalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, memoID).Scan(&memoBalance); err != nil {
		t.Fatalf("read the memo's balance: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&invBalance); err != nil {
		t.Fatalf("read the invoice's balance: %v", err)
	}
	if memoBalance != -10000 {
		t.Fatalf("the memo's balance after the reversal is %d, want -10000", memoBalance)
	}
	if invBalance != 10000 {
		t.Fatalf("the invoice's balance after the reversal is %d, want 10000", invBalance)
	}
}
