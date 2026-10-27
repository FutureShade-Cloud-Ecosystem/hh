//go:build integration

package gl_test

// deadletter_retry_integration_test.go, FW1-02 sub item 3: a refused GL
// journal is retried while the worker runs, never only at the next restart.
// The leg plants a finalized invoice whose total does not equal subtotal plus
// tax so the journal builder refuses the unbalanced post, drains the outbox
// through the real posting consumer until the event is DEAD, then corrects the
// invoice total (the refusal is fixed) and runs the worker's dead letter retry
// pass: the event comes back, the journal posts, and the books hold it with no
// restart anywhere.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_DeadLetteredJournal_IsRetriedAfterTheRefusalIsFixed(t *testing.T) {
	url, _ := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()

	// The refused invoice: total_cents (9000) does not equal subtotal_cents +
	// tax_cents (8000 + 1040 = 9040), so the posting consumer refuses the
	// unbalanced journal draft until the invoice is corrected.
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('FW1-02-DL', 'Dead letter proof', 'CONTRACTOR', '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 0)
		 RETURNING id::text`).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	at := time.Date(2026, time.March, 12, 10, 0, 0, 0, time.UTC)
	var invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ('FW1-02-DL-1', $1::uuid, '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 'FINALIZED',
		         $2::date, $2::date, 8000, 1040, 9000, 9000, $2, 'INVOICE')
		 RETURNING id::text`, customerID, at.Format("2006-01-02")).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (
		   SELECT j.id FROM gl_journals j WHERE j.idempotency_key = $1)`, "ar.invoice.finalized:"+invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "ar.invoice.finalized:"+invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'invoice_id' = $1`, invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1::uuid`, invoiceID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	var eventID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO events_outbox (topic, payload, occurred_at)
		 VALUES ('ar.invoice.finalized', jsonb_build_object('invoice_id', $1::uuid), $2)
		 RETURNING event_id::text`, invoiceID, at).Scan(&eventID); err != nil {
		t.Fatalf("emit the event: %v", err)
	}

	glSvc := gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(billing.NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(d)
	for i := 0; i < 16; i++ {
		d.DrainOnce(ctx)
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM events_outbox WHERE event_id = $1::uuid`, eventID).
		Scan(&status, &attempts); err != nil {
		t.Fatalf("read the event back: %v", err)
	}
	if status != "DEAD" || attempts < 5 {
		t.Fatalf("the refused event did not dead letter: status %s after %d attempts", status, attempts)
	}

	// The refusal is fixed: the invoice's total now equals subtotal plus tax.
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET total_cents = 9040, balance_cents = 9040 WHERE id = $1::uuid`, invoiceID); err != nil {
		t.Fatalf("correct the invoice total: %v", err)
	}

	// The worker's dead letter retry pass, the schedule the worker runs: with
	// no restart anywhere, the event comes back and the journal posts.
	retry := notify.NewDeadLetterRetry(notify.NewPgOutboxStore(pool), gl.PostedTopics, nil)
	if n := retry.RunOnce(ctx); n < 1 {
		t.Fatalf("the retry pass requeued nothing (%d rows), the dead lettered event stays dead until a restart", n)
	}
	for i := 0; i < 16; i++ {
		d.DrainOnce(ctx)
	}
	var posted int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`,
		"ar.invoice.finalized:"+invoiceID).Scan(&posted); err != nil {
		t.Fatalf("count the journal: %v", err)
	}
	if posted != 1 {
		t.Fatalf("the fixed refusal never posted its journal: %d journals after the retry pass", posted)
	}
}
