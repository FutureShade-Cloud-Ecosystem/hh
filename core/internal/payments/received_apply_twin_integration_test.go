//go:build integration

// received_apply_twin_integration_test.go, THE CONCURRENT DUPLICATE. Two requests, one key, one
// body, racing to record the payment that closes its invoices: both must be answered with the
// first result, because a double click on a Record Payment that settles the account is the common
// case, and answering the second click with a refusal invites a third under a fresh key, which is
// how money gets posted twice.
//
// It lives in the external test package (like the journal leg) because it proves the journal too:
// one payment row, one payment.received event, ONE journal on the deterministic key, and both
// callers holding the same payment id with replayed set on exactly one of them.
package payments_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

func TestApplyReceived_TwoConcurrentDuplicatesOfAClosingPaymentAnswerTheFirstResult(t *testing.T) {
	pool := journalPool(t)
	ctx := context.Background()
	tag := "TWN-" + randomHex()
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 0) RETURNING id::text`,
		tag, "Apply twin "+tag).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (
		   SELECT j.id FROM gl_journals j JOIN payments p ON p.id = j.source_ref_id WHERE p.customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id IN (
		   SELECT p.id FROM payments p WHERE p.customer_id = $1::uuid)`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	plant := func(label string, issueDays, dueDays int, balance int64) string {
		issue := journalNow.AddDate(0, 0, issueDays*-1)
		due := journalNow.AddDate(0, 0, dueDays*-1)
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			 VALUES ($1, $2::uuid, '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 'FINALIZED', $3::date, $4::date, $5, 0, $5, $5, $3, 'INVOICE')
			 RETURNING id::text`,
			tag+"-"+label, customerID, issue.Format("2006-01-02"), due.Format("2006-01-02"), balance).Scan(&id); err != nil {
			t.Fatalf("seed invoice %s: %v", label, err)
		}
		return id
	}
	older := plant("OLD", 90, 60, 10000)
	newer := plant("NEW", 40, 10, 5000)

	// Two callers race with one key and one body; the money closes both invoices, so the twin that
	// arrives second reads balances the first already moved. Half the lists are reversed so the
	// race also exercises the sorted lock walk from both ends.
	repo := payments.NewRepository(pool)
	const callers = 2
	ans := make([]payments.ApplyAnswer, callers)
	errs := make([]error, callers)
	var start sync.WaitGroup
	start.Add(1)
	var race sync.WaitGroup
	for i := 0; i < callers; i++ {
		race.Add(1)
		go func(i int) {
			defer race.Done()
			ids := []string{older, newer}
			if i%2 == 1 {
				ids = []string{newer, older}
			}
			start.Wait()
			ans[i], errs[i] = repo.ApplyReceived(ctx, payments.ApplyRequest{
				CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ TWN",
				IdempotencyKey: tag, InvoiceIDs: ids, AmountCents: 15000,
			})
		}(i)
	}
	start.Done()
	race.Wait()

	// Both answers are the first result: no error, one payment id, exactly one replayed.
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d must be answered, not refused: %v", i, errs[i])
		}
		if ans[i].PaymentID == "" || ans[i].AmountCents != 15000 {
			t.Fatalf("caller %d holds the first payment's answer: %+v", i, ans[i])
		}
	}
	if ans[0].PaymentID != ans[1].PaymentID {
		t.Fatalf("both callers hold the same payment id, got %s and %s", ans[0].PaymentID, ans[1].PaymentID)
	}
	if (ans[0].Replayed && ans[1].Replayed) || (!ans[0].Replayed && !ans[1].Replayed) {
		t.Fatalf("exactly one caller is the replay, got %v and %v", ans[0].Replayed, ans[1].Replayed)
	}

	// The ledger carries the payment once: one row, two allocations, one event.
	for _, q := range []struct {
		sql  string
		arg  string
		want int
		name string
	}{
		{`SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid`, customerID, 1, "payment rows"},
		{`SELECT COUNT(*) FROM payment_allocations a JOIN payments p ON p.id = a.payment_id
		   WHERE p.customer_id = $1::uuid`, customerID, 2, "allocations"},
		{`SELECT COUNT(*) FROM events_outbox WHERE topic = 'payment.received' AND payload->>'payment_id' = $1`,
			ans[0].PaymentID, 1, "events"},
	} {
		var n int
		if err := pool.QueryRow(ctx, q.sql, q.arg).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.name, err)
		}
		if n != q.want {
			t.Fatalf("%s: want %d, got %d", q.name, q.want, n)
		}
	}

	// One journal: drain the real consumer the way the worker does and count on the deterministic
	// key. A twin that answered anything but the first result would have burned a second key path
	// here or refused above; a twin that posted would have made two.
	glSvc := gl.NewService(gl.NewRepository(pool)).WithPaymentReader(till.NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), slog.Default())
	glSvc.RegisterPostingHandlers(d)
	key := "payment.received:" + ans[0].PaymentID
	for i := 0; i < 200; i++ {
		var posted int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&posted); err != nil {
			t.Fatalf("watch the journal: %v", err)
		}
		if posted > 0 {
			break
		}
		d.DrainOnce(ctx)
	}
	var journals int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&journals); err != nil {
		t.Fatalf("count journals: %v", err)
	}
	if journals != 1 {
		t.Fatalf("one journal on the payment's key, found %d", journals)
	}
}
