//go:build integration

package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ROUND 7, THE REVERSAL'S STATUS HANDLING (M3 P3-2).
//
// The reversal restored the invoice's balance AND its status without judging the invoice's status, so a
// VOID invoice came back FINALIZED: a document the desk had voided came back as an open receivable. No
// production door voids an invoice today, so the case is dormant in the books as they stand, but the
// reversal's status handling is a rule of its own and it is worth stating: an invoice that is not open is
// not restored by this door.
//
// The reversal also set the memo's status to FINALIZED rather than restoring the status the memo carried
// before the application, so a memo that was not FINALIZED came back FINALIZED.
//
// The fix: the reversal judges the invoice's status (a VOID invoice stays VOID, refused in words), and the
// application record carries the memo's prior status so the reversal restores that one.

// TestIntegration_FW1_12b_AReversalRefusesAVoidInvoiceAndLeavesItVoid pins M3 P3-2's invoice side.
func TestIntegration_FW1_12b_AReversalRefusesAVoidInvoiceAndLeavesItVoid(t *testing.T) {
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
	var creditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -10000, 0, 'deposit_cancel', gen_random_uuid(), $2::uuid, 'the credit')
		RETURNING id::text`, custID, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed the credit: %v", err)
	}

	app, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invID,
		AmountCents: 10000, Reason: "the whole credit", IdempotencyKey: n + "-key-1",
	}, true)
	if err != nil {
		t.Fatalf("the apply: %v", err)
	}

	// The invoice is voided after its application. No production door voids an invoice today, so the
	// case is seeded here: it is the rule, not the reach, that the leg holds.
	if _, err := pool.Exec(ctx, `UPDATE invoices SET status = 'VOID' WHERE id = $1::uuid`, invID); err != nil {
		t.Fatalf("void the invoice: %v", err)
	}

	res, err := applySvc.Reverse(ctx, ReverseCreditApplicationInput{
		InvoiceID: invID, ApplicationID: app.ApplicationID, Reason: "undo the application",
	}, true)
	if err == nil {
		t.Fatalf("the reversal answered %+v, want it refused on the void invoice", res)
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the reversal answered %v, want it refused in words", err)
	}

	// The invoice stays VOID and keeps the balance the application left: nothing was restored.
	var status string
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&status, &balance); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if status != "VOID" {
		t.Fatalf("the refused reversal left the invoice %s, want VOID", status)
	}
	if balance != 0 {
		t.Fatalf("the refused reversal moved the invoice: the balance is %d, want 0", balance)
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
