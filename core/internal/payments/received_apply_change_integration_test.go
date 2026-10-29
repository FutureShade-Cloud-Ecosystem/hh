//go:build integration

// received_apply_change_integration_test.go, W3 item 0.10's UNAPPLIED CASH, proved against the real
// schema and the real GL consumer.
//
// A receipt larger than the invoices it is applied to used to be refused on the ceiling. It leaves
// the rest as an ON ACCOUNT CREDIT for that customer and branch instead: the payment row carries the
// whole money that arrived (the receipt journal credits 1100 with all of it), the allocations carry
// what the invoices took, and the remainder is one negative ADJUSTMENT row in the 'receipt_change'
// family keyed to the payment, so the aging shows it as a negative item on the customer's row and
// the subledger still ties to GL 1100. The undo paths give the change back beside the invoices: a
// reversal and a void both mirror the row into the payment's own group, so the group nets to zero
// and 1100's whole receipt movement is what their journals carry.
//
// The legs look at the ledger, the received_apply file's own posture.
//
// Run: go test -tags integration ./internal/payments/ -run ApplyReceived
package payments_test

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// changeGLWorld is the lock order rig's world with the real GL consumer wired beside it.
type changeGLWorld struct {
	r    *loRig
	disp *notify.Dispatcher
}

func newChangeGLWorld(t *testing.T) *changeGLWorld {
	t.Helper()
	r := newLORig(t, 4)
	glSvc := gl.NewService(gl.NewRepository(r.pool)).WithPaymentReader(till.NewRepository(r.pool))
	disp := notify.NewDispatcher(notify.NewPgOutboxStore(r.pool), nil)
	glSvc.RegisterPostingHandlers(disp)
	return &changeGLWorld{r: r, disp: disp}
}

// drainPosted drains the outbox until the named journal key has posted, bounded: a shared database
// may hold other legs' events ahead of ours.
func (w *changeGLWorld) drainPosted(key string) {
	w.r.t.Helper()
	for i := 0; i < 200; i++ {
		w.disp.DrainOnce(context.Background())
		var posted int
		if err := w.r.obs.QueryRow(context.Background(), `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, key).
			Scan(&posted); err != nil {
			w.r.t.Fatalf("watch the journal: %v", err)
		}
		if posted > 0 {
			return
		}
	}
	w.r.t.Fatalf("the journal on key %s never posted", key)
}

// journalLegs reads one posted journal's debit total, credit total and the movement it left on one
// account code.
func (w *changeGLWorld) journalLegs(key, accountCode string) (debits, credits, accountMove int64) {
	w.r.t.Helper()
	err := w.r.obs.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(l.debit_cents), 0), COALESCE(SUM(l.credit_cents), 0),
		       COALESCE(SUM(CASE WHEN a.code = $2::text
		                         THEN COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)
		                         ELSE 0 END), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1::text AND j.status = 'POSTED'`, key, accountCode).
		Scan(&debits, &credits, &accountMove)
	if err != nil {
		w.r.t.Fatalf("read the journal on %s: %v", key, err)
	}
	return debits, credits, accountMove
}

// changeGroupSum reads the 'receipt_change' group one payment keyed: what the account still holds
// from that payment's unapplied remainder.
func (w *changeGLWorld) changeGroupSum(paymentID string) int64 {
	w.r.t.Helper()
	var sum int64
	if err := w.r.obs.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, paymentID).Scan(&sum); err != nil {
		w.r.t.Fatalf("read the receipt_change group: %v", err)
	}
	return sum
}

// invoiceState reads one invoice's balance and status.
func (w *changeGLWorld) invoiceState(invoiceID string) (int64, string) {
	w.r.t.Helper()
	var balance int64
	var status string
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT balance_cents, status::text FROM invoices WHERE id = $1::uuid`, invoiceID).
		Scan(&balance, &status); err != nil {
		w.r.t.Fatalf("read invoice: %v", err)
	}
	return balance, status
}

// The red leg: a receipt larger than the invoices it is applied to leaves the rest as an on account
// credit. The payment row carries the WHOLE money that arrived, the invoice closes on its share, and
// the remainder sits as one negative receipt_change row on the payment, branch stamped, so the
// account holds exactly what the customer overpaid.
func TestApplyReceived_OverAppliedCashLeavesOnAccountCredit(t *testing.T) {
	w := newChangeGLWorld(t)
	inv := w.r.invoice("CHG", 10000)

	ans, err := w.r.repo.ApplyReceived(context.Background(), payments.ApplyRequest{
		CustomerID: w.r.custID, Method: "CHEQUE", Reference: "CHQ OVER",
		IdempotencyKey: w.r.suffix + "-chg", InvoiceIDs: []string{inv}, AmountCents: 12500,
	})
	if err != nil {
		t.Fatalf("the over applied receipt lands: %v", err)
	}
	if ans.AppliedCents != 10000 || ans.UnappliedCents != 2500 || ans.OnAccountCents != 2500 {
		t.Fatalf("the split says 10000 applied and 2500 on account, got applied %d unapplied %d on_account %d",
			ans.AppliedCents, ans.UnappliedCents, ans.OnAccountCents)
	}
	if b, s := w.invoiceState(inv); b != 0 || s != "PAID" {
		t.Fatalf("the invoice closed on its share, got %d %s", b, s)
	}

	// The payment row carries the whole receipt: the money that arrived is the money the books took.
	var rowAmount, changeCents int64
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT amount_cents, COALESCE((metadata->>'change_cents')::bigint, 0) FROM payments WHERE id = $1::uuid`,
		ans.PaymentID).Scan(&rowAmount, &changeCents); err != nil {
		t.Fatalf("read the payment row: %v", err)
	}
	if rowAmount != 12500 || changeCents != 2500 || changeCents != ans.OnAccountCents {
		t.Fatalf("the payment row carries the whole receipt and names its change, got amount %d change %d on_account %d",
			rowAmount, changeCents, ans.OnAccountCents)
	}

	// The on account credit is one negative receipt_change row keyed to the payment, on the payment's
	// own branch, matching the answer's on_account_cents.
	if got := w.changeGroupSum(ans.PaymentID); got != -2500 || -got != ans.OnAccountCents {
		t.Fatalf("the account holds the overpayment as -2500 matching on_account_cents %d, got %d", ans.OnAccountCents, got)
	}
	var branch string
	if err := w.r.obs.QueryRow(context.Background(), `
		SELECT COALESCE(branch_id::text, '') FROM customer_transactions
		 WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, ans.PaymentID).Scan(&branch); err != nil {
		t.Fatalf("read the change row's branch: %v", err)
	}
	if branch != w.r.branchID {
		t.Fatalf("the change row is stamped with the payment's branch %s, got %q", w.r.branchID, branch)
	}

	// The receipt journal books the WHOLE receipt: cash debited 12500, 1100 credited 12500.
	w.drainPosted("payment.received:" + ans.PaymentID)
	debits, credits, arMove := w.journalLegs("payment.received:"+ans.PaymentID, "1100")
	if debits != 12500 || credits != 12500 || arMove != -12500 {
		t.Fatalf("the receipt journal books the whole receipt (D 12500 C 12500, 1100 -12500), got D %d C %d 1100 %d", debits, credits, arMove)
	}
}

// The same money, proved at the tie: after the over applied receipt, the movement the AR subledger
// made for this account (the invoices open plus the receipt_change row) equals the movement GL 1100
// made, whatever the account's start. The deltas are measured beside each other so the planted
// invoices' own history cancels.
func TestApplyReceived_OverAppliedCashTiesTheSubledgerTo1100(t *testing.T) {
	w := newChangeGLWorld(t)
	inv := w.r.invoice("TIE", 10000)
	w.r.invoice("TIE2", 4000)

	subledger := func() int64 {
		var v int64
		if err := w.r.obs.QueryRow(context.Background(), `
			SELECT (SELECT COALESCE(SUM(balance_cents), 0) FROM invoices WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID'))
			     + (SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
			         WHERE customer_id = $1::uuid AND source_type <> 'invoice'
			           AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF'))`,
			w.r.custID).Scan(&v); err != nil {
			t.Fatalf("read the subledger: %v", err)
		}
		return v
	}
	gl1100 := func() int64 {
		var v int64
		if err := w.r.obs.QueryRow(context.Background(), `
			SELECT COALESCE(SUM(CASE WHEN a.code = '1100'
			                         THEN COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)
			                         ELSE 0 END), 0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.status = 'POSTED' AND j.branch_id = $1::uuid`, w.r.branchID).Scan(&v); err != nil {
			t.Fatalf("read GL 1100: %v", err)
		}
		return v
	}

	beforeSub, beforeGL := subledger(), gl1100()
	ans, err := w.r.repo.ApplyReceived(context.Background(), payments.ApplyRequest{
		CustomerID: w.r.custID, Method: "CASH", Reference: "CHQ TIE",
		IdempotencyKey: w.r.suffix + "-tie", InvoiceIDs: []string{inv}, AmountCents: 15000,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	w.drainPosted("payment.received:" + ans.PaymentID)

	// The receipt took 15000 of cash: the invoice closed on 10000 of it and the account holds
	// -5000 of change, so both figures moved by -15000 together.
	if dSub, dGL := subledger()-beforeSub, gl1100()-beforeGL; dSub != -15000 || dGL != -15000 {
		t.Fatalf("the over applied receipt moves the subledger and GL 1100 by -15000 together, got subledger %d and GL %d", dSub, dGL)
	}
}

// The reversal of an over applied payment gives the change back beside the invoices: the
// receipt_change group nets to zero over the receipt and its reversal, the invoices re-open, and the
// reversing journal moves 1100 by the whole receipt into 2310, where the money waits for its owner.
func TestApplyReceived_ReversalMirrorsTheChangeRow(t *testing.T) {
	w := newChangeGLWorld(t)
	inv := w.r.invoice("RVS", 10000)

	ans, err := w.r.repo.ApplyReceived(context.Background(), payments.ApplyRequest{
		CustomerID: w.r.custID, Method: "CHEQUE", Reference: "CHQ RVS",
		IdempotencyKey: w.r.suffix + "-rvs", InvoiceIDs: []string{inv}, AmountCents: 12500,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	rev, err := w.r.repo.ReversePayment(context.Background(), payments.ReverseInput{
		PaymentID: ans.PaymentID, Reason: "cheque returned", IsManager: true,
	}, time.Now())
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}

	if got := w.changeGroupSum(ans.PaymentID); got != 0 {
		t.Fatalf("the change group nets to zero over the receipt and its reversal, got %d", got)
	}
	if b, s := w.invoiceState(inv); b != 10000 || s != "FINALIZED" {
		t.Fatalf("the invoice re-opened whole, got %d %s", b, s)
	}

	w.drainPosted("payment.reversed:" + rev.ReversalPaymentID)
	debits, credits, arMove := w.journalLegs("payment.reversed:"+rev.ReversalPaymentID, "1100")
	if debits != 12500 || credits != 12500 || arMove != 12500 {
		t.Fatalf("the reversing journal moves 1100 by the whole receipt (D 12500 C 12500, 1100 +12500), got D %d C %d 1100 %d", debits, credits, arMove)
	}
}

// The void of an over applied receipt (a mis-keyed overpayment caught the same dealer day) gives the
// change back beside the invoices too: the group nets to zero and the mirror journal credits back
// the whole receipt on the receipt's own routing.
func TestApplyReceived_VoidMirrorsTheChangeRow(t *testing.T) {
	w := newChangeGLWorld(t)
	inv := w.r.invoice("VOD", 10000)

	ans, err := w.r.repo.ApplyReceived(context.Background(), payments.ApplyRequest{
		CustomerID: w.r.custID, Method: "CHEQUE", Reference: "CHQ VOD",
		IdempotencyKey: w.r.suffix + "-vod", InvoiceIDs: []string{inv}, AmountCents: 12500,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := w.r.repo.VoidPayment(context.Background(), payments.VoidInput{
		PaymentID: ans.PaymentID, Reason: "wrong account", IsManager: true,
	}, time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("void: %v", err)
	}

	if got := w.changeGroupSum(ans.PaymentID); got != 0 {
		t.Fatalf("the change group nets to zero over the receipt and its void, got %d", got)
	}
	if b, s := w.invoiceState(inv); b != 10000 || s != "FINALIZED" {
		t.Fatalf("the invoice re-opened whole, got %d %s", b, s)
	}

	w.drainPosted("payment.voided:" + ans.PaymentID)
	debits, credits, arMove := w.journalLegs("payment.voided:"+ans.PaymentID, "1100")
	if debits != 12500 || credits != 12500 || arMove != 12500 {
		t.Fatalf("the void journal moves 1100 back by the whole receipt (D 12500 C 12500, 1100 +12500), got D %d C %d 1100 %d", debits, credits, arMove)
	}
}
