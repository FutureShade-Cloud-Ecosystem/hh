//go:build integration

// received_apply_seen_integration_test.go, AN EVENT THAT NEVER BOOKED IS SEEN (item 102,
// condition c). The GL consumer is the only thing that books a received payment's journal, so a
// stuck consumer would leave the money applied and the journal unposted: a silent short, unless
// something reads the difference. The AR tie out is that reader: it compares the AR subledger (the
// invoices' live balances) against GL 1100 as-of, and the money desk's books screen reads it
// through GET /api/v1/gl/tie-out (operation glTieOut), one row per subledger and account with
// subledger_cents, gl_cents, delta_cents, in_tie and the persisted out_of_tie_since and age_days.
//
// The leg is self relative (the gate database is shared and seeded): it reads the tie out's
// baseline, applies a payment whose event it does NOT drain, and shows the delta move by exactly
// the payment while the GL arm stands still; then it drains and shows the delta return. Whatever
// else the shared world holds, only this payment moves between the three reads.
package payments_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

func TestApplyReceived_AnEventThatNeverBookedShowsInTheARTieOut(t *testing.T) {
	pool := journalPool(t)
	ctx := context.Background()
	tag := "SEEN-" + randomHex()
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 0) RETURNING id::text`,
		tag, "Apply seen "+tag).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	issue := journalNow.AddDate(0, 0, -90)
	var inv string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 'FINALIZED', $3::date, $3::date, 10000, 0, 10000, 10000, $3, 'INVOICE')
		 RETURNING id::text`,
		tag+"-OLD", customerID, issue.Format("2006-01-02")).Scan(&inv); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, inv)
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (
		   SELECT id FROM gl_journals WHERE idempotency_key IN ('ar.invoice.finalized:' || $1, 'payment.received:' || ANY(
		     SELECT p.id::text FROM payments p WHERE p.customer_id = $2::uuid)))`, inv, customerID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key IN (
		   'ar.invoice.finalized:' || $1, 'payment.received:' || ANY(
		     SELECT p.id::text FROM payments p WHERE p.customer_id = $2::uuid))`, inv, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	// The real consumer, wired the way the worker wires it, with the billing seams both arms read.
	billingRepo := billing.NewRepository(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).
		WithInvoiceReader(billingRepo).
		WithPaymentReader(till.NewRepository(pool)).
		WithARSubledger(billingRepo)
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), slog.Default())
	glSvc.RegisterPostingHandlers(d)
	// The invoice's own journal must be behind it before the baseline: emit its event and drain
	// until its journal exists, then drain anything else still pending so no other deferred post
	// can move the GL arm between this leg's three reads.
	if _, err := pool.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('ar.invoice.finalized', jsonb_build_object('invoice_id', $1::text))`, inv); err != nil {
		t.Fatalf("emit the invoice event: %v", err)
	}
	for i := 0; i < 400; i++ {
		var posted int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`,
			"ar.invoice.finalized:"+inv).Scan(&posted); err != nil {
			t.Fatalf("watch the invoice journal: %v", err)
		}
		if posted > 0 {
			break
		}
		d.DrainOnce(ctx)
	}
	for i := 0; i < 400; i++ {
		if n := d.DrainOnce(ctx); n == 0 {
			break
		}
	}

	tie := func() (sub, glBal, delta int64) {
		t.Helper()
		asOf := time.Now().Add(time.Hour)
		states, err := glSvc.TieOut(ctx, asOf, "")
		if err != nil {
			t.Fatalf("tie out: %v", err)
		}
		for _, st := range states {
			if st.Subledger == "AR" && st.AccountCode == "1100" && st.BranchID == "" {
				return st.SubledgerCents, st.GLCents, st.DeltaCents
			}
		}
		t.Fatal("the AR tie out row is missing")
		return
	}
	sub0, gl0, delta0 := tie()

	// The payment lands, its event sits in the outbox, and the consumer is NOT drained: the stuck
	// consumer's shape. The subledger arm drops by the payment; the GL arm cannot move.
	ans, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ SEEN",
		IdempotencyKey: tag, InvoiceIDs: []string{inv}, AmountCents: 10000,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var pending int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM events_outbox WHERE topic = 'payment.received' AND payload->>'payment_id' = $1`,
		ans.PaymentID).Scan(&pending); err != nil {
		t.Fatalf("count the pending event: %v", err)
	}
	if pending != 1 {
		t.Fatalf("the stuck consumer's shape needs the event pending, found %d", pending)
	}
	sub1, gl1, delta1 := tie()
	if sub1 != sub0-10000 {
		t.Fatalf("the subledger arm dropped by the payment: %d then %d", sub0, sub1)
	}
	if gl1 != gl0 {
		t.Fatalf("the GL arm cannot move while the journal is unbooked: %d then %d", gl0, gl1)
	}
	if delta1 != delta0-10000 {
		t.Fatalf("the tie out shows the unbooked payment as a difference: delta %d then %d", delta0, delta1)
	}

	// The consumer wakes: the journal books, both arms move by the payment together (the account
	// owes less AND the ledger credits AR), and the difference the stuck consumer showed is gone:
	// the delta returns to whatever the shared world's baseline was.
	for i := 0; i < 200; i++ {
		var posted int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`,
			"payment.received:"+ans.PaymentID).Scan(&posted); err != nil {
			t.Fatalf("watch the payment journal: %v", err)
		}
		if posted > 0 {
			break
		}
		d.DrainOnce(ctx)
	}
	sub2, gl2, delta2 := tie()
	if sub2 != sub0-10000 || gl2 != gl0-10000 {
		t.Fatalf("once the journal books both arms move by the payment: sub %d->%d gl %d->%d",
			sub0, sub2, gl0, gl2)
	}
	if delta2 != delta0 {
		t.Fatalf("the difference the stuck consumer showed is gone: delta %d then %d, now %d", delta0, delta1, delta2)
	}
}
