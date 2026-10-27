//go:build integration

package pos

// pay_balance_gl_integration_test.go, FW1-02 sub item 1: the receipt a Pay
// balance take writes must reach the GL. The take settles through
// AllocateToInvoiceTx, which emits no ledger event by design (the caller's
// tender owns the receipt event), and the take itself emitted none either, so
// a Pay balance left GL 1100 holding the receivable the subledger had already
// cleared. This leg rings the deposit sale, takes the balance in full by
// cash, drains the outbox through the REAL GL posting consumer, and reads the
// ledger back: the receipt journal exists on the deterministic key, 1100 nets
// to zero against the invoice's own accrual, and 1020 holds the whole take.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

func TestIntegration_PayBalanceTake_PostsItsReceiptJournal(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	_, _, orderID, _, session, _, balance := seedOwingWorld(t, pool, stubCharger{}, "glr")
	svc := realPosSvc(t, pool, stubCharger{})

	out, err := svc.PayBalanceTake(ctx, PayBalanceTakeInput{
		OrderID: orderID, Method: TenderCash, AmountCents: balance,
		TillSessionID: session, IdempotencyKey: key(),
	})
	if err != nil {
		t.Fatalf("take: %v", err)
	}

	// The consumer the worker runs: the GL service wired with the real
	// invoice and payment readers, registered on the real dispatcher.
	glSvc := gl.NewService(gl.NewRepository(pool)).
		WithInvoiceReader(billing.NewRepository(pool)).
		WithPaymentReader(till.NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(d)
	receiptKey := "payment.received:" + out.PaymentID
	for i := 0; i < 200; i++ {
		d.DrainOnce(ctx)
		var posted int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`,
			receiptKey).Scan(&posted); err != nil {
			t.Fatalf("watch the receipt journal: %v", err)
		}
		if posted > 0 {
			break
		}
	}
	var posted int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`,
		receiptKey).Scan(&posted); err != nil {
		t.Fatalf("count the receipt journal: %v", err)
	}
	if posted != 1 {
		t.Fatalf("the Pay balance take posted no receipt journal: %d journals on key %s", posted, receiptKey)
	}

	// The receipt journal's own legs: the cash debit to 1020 and the AR
	// credit to 1100, each exactly the take (the posting rules' own choice of
	// legs for a CASH receipt).
	leg := func(code string) int64 {
		var n int64
		if err := pool.QueryRow(ctx, `
SELECT COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)
  FROM gl_journal_lines l
  JOIN gl_journals j ON j.id = l.journal_id
  JOIN gl_accounts a ON a.id = l.account_id
 WHERE j.idempotency_key = $1 AND a.code = $2`, receiptKey, code).Scan(&n); err != nil {
			t.Fatalf("leg %s: %v", code, err)
		}
		return n
	}
	if n := leg("1100"); n != -out.AmountCents {
		t.Fatalf("GL 1100 leg of the receipt is %d, want -%d (AR falls by the take)", n, out.AmountCents)
	}
	if n := leg("1020"); n != out.AmountCents {
		t.Fatalf("GL 1020 leg of the receipt is %d, want %d (undeposited funds rise by the take)", n, out.AmountCents)
	}
}
