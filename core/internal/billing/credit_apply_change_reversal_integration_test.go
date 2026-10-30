//go:build integration

// credit_apply_change_reversal_integration_test.go, W3 item 0.15's second leg (S5 on 707): part of
// a receipt's change applied to ANOTHER invoice, and then the receipt reversed. The sequence had
// no leg of its own: the reversal counters the whole change inside its own 'receipt_change' group
// (reversal.go's changeCents block), including the part that already paid the other invoice, so
// the aging and GL 1100 move by the same cents at every act and the group ends honestly positive,
// a receivable the customer owes back while the money waits in 2310.
//
// Run: go test -tags integration ./internal/billing/ -run W3P0A_ReceiptChange
package billing

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

func TestIntegration_W3P0A_ReceiptChangeAppliedThenReversedTies(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)
	asOf := time.Now().UTC().Add(1 * time.Hour)
	orgID := "33333333-3333-4333-8333-3333333333aa"
	if _, err := pool.Exec(ctx, `INSERT INTO org (id, slug, name) VALUES ($1::uuid, $2, $3) ON CONFLICT (id) DO NOTHING`,
		orgID, "w3p0achg-"+orgID[:8], "W3 P0A Change Reversal Org"); err != nil {
		t.Fatalf("plant the org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credit_application_keys WHERE org_id = $1::uuid`, orgID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1::uuid`, orgID)
	})

	inv1 := seedInvoice(t, pool, cust, n, invSpec{
		no: "P0A-CHG-1", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -40).Format("2006-01-02"),
		total: 10000, balance: 10000, finalized: true,
	})
	inv2 := seedInvoice(t, pool, cust, n, invSpec{
		no: "P0A-CHG-2", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -10).Format("2006-01-02"),
		total: 6000, balance: 6000, finalized: true,
	})

	// The story's own payment rows (the receipt and its reversal) leave with the test, before
	// seedCustomer's teardown removes the invoices the allocations name. The pair references
	// itself in BOTH directions (the original's reversed_by_payment_id, the reversal's
	// reverses_payment_id), so both columns clear on both rows before either row goes.
	var receiptPaymentID, reversalPaymentID string
	t.Cleanup(func() {
		c := context.Background()
		if receiptPaymentID == "" {
			return
		}
		both := []string{receiptPaymentID}
		if reversalPaymentID != "" {
			both = append(both, reversalPaymentID)
		}
		_, _ = pool.Exec(c, `UPDATE payments SET reversed_by_payment_id = NULL, reverses_payment_id = NULL
		 WHERE id = ANY($1::uuid[])`, both)
		_, _ = pool.Exec(c, `UPDATE payments SET reversed_by_payment_id = NULL, reverses_payment_id = NULL
		 WHERE reversed_by_payment_id = ANY($1::uuid[]) OR reverses_payment_id = ANY($1::uuid[])`, both)
		for _, pid := range both {
			_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, pid)
			_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE source_ref_id = $1::uuid`, pid)
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'payment_id' = $1::uuid`, pid)
			_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1::uuid)`, pid)
			_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id = $1::uuid`, pid)
		}
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = ANY($1::uuid[])`, both)
	})

	// The real GL consumer, wired the way the app wires it.
	glSvc := gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(repo).WithPaymentReader(till.NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(d)
	drainUntilPosted := func(key string) {
		t.Helper()
		for i := 0; i < 200; i++ {
			d.DrainOnce(ctx)
			var posted int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, key).
				Scan(&posted); err != nil {
				t.Fatalf("watch the journal: %v", err)
			}
			if posted > 0 {
				return
			}
		}
		t.Fatalf("the journal on key %s never posted", key)
	}

	changeGroup := func() int64 {
		t.Helper()
		var sum int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
			 WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, receiptPaymentID).Scan(&sum); err != nil {
			t.Fatalf("read the change group: %v", err)
		}
		return sum
	}

	agingBefore := agingSum(t, repo, asOf)

	// ACT 1: a receipt of 18000 against a 10000 invoice; the 8000 change is the account's on
	// account credit, and the aging and GL 1100 both move by the receipt's whole 18000.
	ans, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: cust, Method: "CHEQUE", Reference: "CHQ W3P0A",
		IdempotencyKey: "W3P0ACHG-" + n, InvoiceIDs: []string{inv1}, AmountCents: 18000,
	})
	if err != nil {
		t.Fatalf("the over applied receipt lands: %v", err)
	}
	if ans.UnappliedCents != 8000 {
		t.Fatalf("the split leaves 8000 on account, got %d", ans.UnappliedCents)
	}
	receiptPaymentID = ans.PaymentID
	drainUntilPosted("payment.received:" + ans.PaymentID)
	if agingSum(t, repo, asOf)-agingBefore != -18000 {
		t.Fatalf("W3-P0A red (act 1): the aging moved %d for the receipt, want -18000",
			agingSum(t, repo, asOf)-agingBefore)
	}
	if glMove := gl1100On(t, pool, "payment.received:"+ans.PaymentID); glMove != -18000 {
		t.Fatalf("W3-P0A red (act 1): GL 1100 moved %d on the receipt journal, want -18000", glMove)
	}

	// ACT 2: 3000 of the change applies to the second invoice through the credit door. The aging
	// does not move (both sides are 1100) and no journal books: the group drops to -5000.
	var changeRowID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM customer_transactions
		 WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, ans.PaymentID).Scan(&changeRowID); err != nil {
		t.Fatalf("find the on account credit row: %v", err)
	}
	if _, err := (NewCreditApplyService(repo)).Apply(ctx, ApplyCreditInput{
		CustomerID: cust, CreditKind: CreditKindAccount, CreditID: changeRowID,
		InvoiceID: inv2, AmountCents: 3000, Reason: "apply part of the overpayment",
		ActorID: "w3-test", IdempotencyKey: "W3P0AAPP-" + n, OrgID: orgID,
	}, true); err != nil {
		t.Fatalf("apply part of the change to the other invoice: %v", err)
	}
	if b, s := w3InvoiceState(t, pool, inv2); b != 3000 || s != "PARTIALLY_PAID" {
		t.Fatalf("W3-P0A red (act 2): invoice two reads %d %s, want 3000 PARTIALLY_PAID", b, s)
	}
	if agingSum(t, repo, asOf)-agingBefore != -18000 {
		t.Fatalf("W3-P0A red (act 2): the application moved the aging by %d, want 0 (both sides are 1100)",
			agingSum(t, repo, asOf)-agingBefore)
	}
	if g := changeGroup(); g != -5000 {
		t.Fatalf("W3-P0A red (act 2): the change group is %d after the application, want -5000", g)
	}

	// ACT 3: the receipt is reversed. The reversal's journal moves 1100 by the receipt's whole
	// 18000 back, invoice one re-opens whole, and the change group is countered to +3000: the
	// part of a reversed receipt that already paid another invoice becomes a receivable the
	// customer owes back, honestly shown, while the money waits in 2310.
	rev, err := payments.NewReverseService(payments.NewRepository(pool)).Reverse(ctx, payments.ReverseInput{
		PaymentID: ans.PaymentID, Reason: "the cheque was meant for another account", ActorID: "w3-test", IsManager: true,
	})
	if err != nil {
		t.Fatalf("reverse the receipt: %v", err)
	}
	reversalPaymentID = rev.ReversalPaymentID
	drainUntilPosted("payment.reversed:" + rev.ReversalPaymentID)
	if glMove := gl1100On(t, pool, "payment.reversed:"+rev.ReversalPaymentID); glMove != 18000 {
		t.Fatalf("W3-P0A red (act 3): GL 1100 moved %d on the reversal journal, want +18000", glMove)
	}
	if b, s := w3InvoiceState(t, pool, inv1); b != 10000 || s != "FINALIZED" {
		t.Fatalf("W3-P0A red (act 3): invoice one reads %d %s after the reversal, want 10000 FINALIZED", b, s)
	}
	if b, s := w3InvoiceState(t, pool, inv2); b != 3000 || s != "PARTIALLY_PAID" {
		t.Fatalf("W3-P0A red (act 3): invoice two reads %d %s after the reversal, want 3000 PARTIALLY_PAID", b, s)
	}
	if g := changeGroup(); g != 3000 {
		t.Fatalf("W3-P0A red (act 3): the change group nets %d after the reversal, want +3000 (the applied part owed back)", g)
	}
	if delta := agingSum(t, repo, asOf) - agingBefore; delta != 0 {
		t.Fatalf("W3-P0A red (act 3): the story nets %d on the aging, want 0 (the receipt and its reversal cancel; the owed back 3000 shows on both sides)", delta)
	}
}
