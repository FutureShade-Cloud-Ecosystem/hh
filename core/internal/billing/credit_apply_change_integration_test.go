//go:build integration

// credit_apply_change_integration_test.go, W3 item 0.10's unapplied cash on the billing side: the
// aging shows the on account credit as a negative item on the customer's row (so the aging still
// ties to GL 1100), and the credit applies later to that customer's open invoices through the
// EXISTING credit application door, once.
//
// Run: go test -tags integration ./internal/billing/ -run W3_UnappliedCash
package billing

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_W3_UnappliedCashAgingShowsItAndTheDoorAppliesItLater(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)
	asOf := time.Now().UTC().Add(1 * time.Hour)
	// The door's key table takes a real org reference (0448), so the act's scope is a planted org
	// row, removed with its keys at test end.
	orgID := "33333333-3333-4333-8333-333333333333"
	if _, err := pool.Exec(ctx, `INSERT INTO org (id, slug, name) VALUES ($1::uuid, $2, $3) ON CONFLICT (id) DO NOTHING`,
		orgID, "w3chg-"+orgID[:8], "W3 Unapplied Cash Org"); err != nil {
		t.Fatalf("plant the org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credit_application_keys WHERE org_id = $1::uuid`, orgID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1::uuid`, orgID)
	})

	inv1 := seedInvoice(t, pool, cust, n, invSpec{
		no: "CHG-1", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -40).Format("2006-01-02"),
		total: 10000, balance: 10000, finalized: true,
	})
	inv2 := seedInvoice(t, pool, cust, n, invSpec{
		no: "CHG-2", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -10).Format("2006-01-02"),
		total: 6000, balance: 6000, finalized: true,
	})

	// The real GL consumer, wired the way the app wires it: billing's invoice reader behind
	// gl.Service, the outbox drained the way the worker's ticks drain it.
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

	// The aging grand total before the story, so the whole act's movement is measured beside it.
	agingBefore := agingSum(t, repo, asOf)

	// This act's own payment rows leave with the test, before seedCustomer's teardown removes the
	// invoices the allocations name (cleanups run last registered first).
	var receiptPaymentID string
	t.Cleanup(func() {
		c := context.Background()
		if receiptPaymentID == "" {
			return
		}
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, receiptPaymentID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, receiptPaymentID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'payment_id' = $1::uuid`, receiptPaymentID)
	})

	// RED LEG 1: the receipt is larger than the invoice it is applied to; the rest is the account's
	// on account credit, and the aging carries it as a negative item on the customer's row.
	ans, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: cust, Method: "CHEQUE", Reference: "CHQ W3",
		IdempotencyKey: "W3CHG-" + n, InvoiceIDs: []string{inv1}, AmountCents: 18000,
	})
	if err != nil {
		t.Fatalf("the over applied receipt lands: %v", err)
	}
	if ans.UnappliedCents != 8000 {
		t.Fatalf("the split leaves 8000 on account, got %d", ans.UnappliedCents)
	}
	receiptPaymentID = ans.PaymentID
	drainUntilPosted("payment.received:" + ans.PaymentID)

	rows, err := repo.AgingRows(ctx, asOf, 60, "")
	if err != nil {
		t.Fatalf("read the aging: %v", err)
	}
	var agingRow *AgingRow
	for i := range rows {
		if rows[i].CustomerID == cust {
			agingRow = &rows[i]
		}
	}
	if agingRow == nil {
		t.Fatal("the account with an invoice and an on account credit must show on the aging")
	}
	// 6000 still owed on the second invoice, -8000 of on account credit: the row total is the
	// negative item the brief describes.
	if agingRow.TotalCents != -2000 {
		t.Fatalf("the aging row carries 6000 owed less 8000 credit = -2000, got %d", agingRow.TotalCents)
	}

	var changeRowID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM customer_transactions
		 WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, ans.PaymentID).Scan(&changeRowID); err != nil {
		t.Fatalf("find the on account credit row: %v", err)
	}

	// RED LEG 2: the credit applies later, through the EXISTING credit application door, ACCOUNT
	// kind, manager gated. The offer read names it for the desk first.
	offers, err := repo.OpenCreditsFor(ctx, cust, trentonBranch)
	if err != nil {
		t.Fatalf("read the open credits: %v", err)
	}
	offered := false
	for _, o := range offers {
		if o.CreditKind == CreditKindAccount && o.CreditID == changeRowID && o.OpenCents == 8000 {
			offered = true
		}
	}
	if !offered {
		t.Fatalf("the desk must be offered the on account credit, got %v", offers)
	}

	applySvc := NewCreditApplyService(repo)
	applied, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: cust, CreditKind: CreditKindAccount, CreditID: changeRowID,
		InvoiceID: inv2, AmountCents: 3000, Reason: "apply the overpayment",
		ActorID: "w3-test", IdempotencyKey: "W3APP-" + n, OrgID: orgID,
	}, true)
	if err != nil {
		t.Fatalf("apply the on account credit to the open invoice: %v", err)
	}
	if applied.AppliedCents != 3000 || applied.CreditOpenAfter != 5000 {
		t.Fatalf("the application takes 3000 and leaves 5000 on account, got %d and %d", applied.AppliedCents, applied.CreditOpenAfter)
	}
	if b, s := w3InvoiceState(t, pool, inv2); b != 3000 || s != "PARTIALLY_PAID" {
		t.Fatalf("the invoice and the credit reduce together: invoice at 3000 PARTIALLY_PAID, got %d %s", b, s)
	}

	// NEVER TWICE: the same key answers its own replay and writes nothing. The act's rows (the
	// group's offset and the invoice's, beside the change row itself) are counted before and after
	// the replay: the number may not move.
	actRows := func() int {
		t.Helper()
		var rows int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM customer_transactions
			 WHERE customer_id = $1::uuid AND txn_type = 'ADJUSTMENT'
			   AND ((source_type = 'receipt_change' AND source_ref_id = $2::uuid)
			     OR (source_type = 'invoice' AND source_ref_id = $3::uuid))`,
			cust, ans.PaymentID, inv2).Scan(&rows); err != nil {
			t.Fatalf("count the application's rows: %v", err)
		}
		return rows
	}
	beforeReplay := actRows()
	if beforeReplay != 3 {
		t.Fatalf("one application writes two rows beside the change row itself (3), found %d", beforeReplay)
	}
	replayed, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: cust, CreditKind: CreditKindAccount, CreditID: changeRowID,
		InvoiceID: inv2, AmountCents: 3000, Reason: "apply the overpayment",
		ActorID: "w3-test", IdempotencyKey: "W3APP-" + n, OrgID: orgID,
	}, true)
	if err != nil {
		t.Fatalf("the spent key replays: %v", err)
	}
	if !replayed.Replayed {
		t.Fatal("the second press under the same key must answer the first result, Replayed")
	}
	if after := actRows(); after != beforeReplay {
		t.Fatalf("the replay writes nothing: %d rows before, %d after", beforeReplay, after)
	}

	// The aging grand total moved with the receipt and never with the application: -18000 for the
	// receipt (the invoice closed on 10000 of it, -8000 of credit stands), 0 for the application
	// (both sides are 1100; the door posts no journal). Measured here, before the story plants one
	// more invoice below.
	if agingSum(t, repo, asOf)-agingBefore != -18000 {
		t.Fatalf("the aging moved -18000 for the receipt and not at all for the application, got %d",
			agingSum(t, repo, asOf)-agingBefore)
	}
	// The GL 1100 tie beside it: the story's only 1100 movement is its own receipt journal's
	// -18000; the application's money moved inside 1100 and booked nothing. Read over the story's
	// own journal key, so no other leg's journals move the figure.
	if glMove := gl1100On(t, pool, "payment.received:"+receiptPaymentID); glMove != -18000 {
		t.Fatalf("GL 1100 moved by the receipt alone (-18000), got %d", glMove)
	}

	// The credit can follow the customer's NEXT invoices too: one more open invoice planted now,
	// and the rest of the credit (5000) closes it whole. The group nets to zero.
	inv3 := seedInvoice(t, pool, cust, n, invSpec{
		no: "CHG-3", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -2).Format("2006-01-02"),
		total: 5000, balance: 5000, finalized: true,
	})
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: cust, CreditKind: CreditKindAccount, CreditID: changeRowID,
		InvoiceID: inv3, AmountCents: 5000, Reason: "apply the rest",
		ActorID: "w3-test", IdempotencyKey: "W3APP2-" + n, OrgID: orgID,
	}, true); err != nil {
		t.Fatalf("apply the rest: %v", err)
	}
	var groupSum int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		 WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, ans.PaymentID).Scan(&groupSum); err != nil {
		t.Fatalf("read the group: %v", err)
	}
	if groupSum != 0 {
		t.Fatalf("the fully applied credit's group nets to zero, got %d", groupSum)
	}
	if b, s := w3InvoiceState(t, pool, inv3); b != 0 || s != "PAID" {
		t.Fatalf("the next invoice closed on the last of the credit, got %d %s", b, s)
	}
	rows, err = repo.AgingRows(ctx, asOf, 60, "")
	if err != nil {
		t.Fatalf("re-read the aging: %v", err)
	}
	// The credit is gone from the books (the group is zero), so the account's row is its part paid
	// invoice alone: 3000, not a credit weighted under it.
	var finalRow *AgingRow
	for i := range rows {
		if rows[i].CustomerID == cust {
			finalRow = &rows[i]
		}
	}
	if finalRow == nil || finalRow.TotalCents != 3000 {
		t.Fatalf("the account's row is its part paid invoice alone (3000), got %v", finalRow)
	}
}

// gl1100On is GL 1100's posted movement under one journal idempotency key: the story's own
// journals, never another leg's.
func gl1100On(t *testing.T, pool *pgxpool.Pool, journalKey string) int64 {
	t.Helper()
	var v int64
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(CASE WHEN a.code = '1100'
		                         THEN COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)
		                         ELSE 0 END), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED' AND j.idempotency_key = $1::text`, journalKey).Scan(&v); err != nil {
		t.Fatalf("read GL 1100: %v", err)
	}
	return v
}

// agingSum is the aging grand total at an as-of.
func agingSum(t *testing.T, repo *Repository, asOf time.Time) int64 {
	t.Helper()
	rows, err := repo.AgingRows(context.Background(), asOf, 60, "")
	if err != nil {
		t.Fatalf("read the aging: %v", err)
	}
	var sum int64
	for _, r := range rows {
		sum += r.TotalCents
	}
	return sum
}

// w3InvoiceState reads one invoice's balance and status.
func w3InvoiceState(t *testing.T, pool *pgxpool.Pool, invoiceID string) (int64, string) {
	t.Helper()
	var balance int64
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT balance_cents, status::text FROM invoices WHERE id = $1::uuid`, invoiceID).
		Scan(&balance, &status); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	return balance, status
}
