//go:build integration

package gl_test

// untaxed_invoice_gl_integration_test.go, FW1-02 sub item 2: an invoice with
// no tax posts its journal. The invoice.finalized builder always appended the
// tax leg, and a zero amount leg violates gl_journal_lines' one sided
// positivity CHECK (0007), so the journal insert failed, the event retried
// five times and dead lettered: every untaxed invoice left the books with no
// accrual at all. This leg plants an invoice with tax 0, emits its
// ar.invoice.finalized event, drains the outbox through the REAL posting
// consumer, and reads the journal back: posted, balanced, with the tax leg
// simply absent.

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

func TestIntegration_UntaxedInvoice_FinalizedPostsItsJournal(t *testing.T) {
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

	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('FW1-02-UT', 'Untaxed invoice proof', 'CONTRACTOR', '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 0)
		 RETURNING id::text`).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	at := time.Date(2026, time.March, 12, 10, 0, 0, 0, time.UTC)
	var invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ('FW1-02-UT-1', $1::uuid, '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 'FINALIZED',
		         $2::date, $2::date, 12500, 0, 12500, 12500, $2, 'INVOICE')
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

	var posted int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`,
		"ar.invoice.finalized:"+invoiceID).Scan(&posted); err != nil {
		t.Fatalf("count the journal: %v", err)
	}
	if posted != 1 {
		var status string
		var attempts int
		_ = pool.QueryRow(ctx, `SELECT status, attempts FROM events_outbox WHERE event_id = $1::uuid`, eventID).
			Scan(&status, &attempts)
		t.Fatalf("the untaxed invoice posted no journal: %d journals, the event sits %s after %d attempts",
			posted, status, attempts)
	}

	// The journal carries the AR debit and the revenue credit, and no tax
	// leg at all: a zero tax line is not a line.
	type line struct {
		code        string
		debitCents  int64
		creditCents int64
	}
	rows, err := pool.Query(ctx, `
SELECT a.code, COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0)
  FROM gl_journal_lines l
  JOIN gl_journals j ON j.id = l.journal_id
  JOIN gl_accounts a ON a.id = l.account_id
 WHERE j.idempotency_key = $1 ORDER BY l.line_no`, "ar.invoice.finalized:"+invoiceID)
	if err != nil {
		t.Fatalf("read the journal lines: %v", err)
	}
	var lines []line
	for rows.Next() {
		var ln line
		if err := rows.Scan(&ln.code, &ln.debitCents, &ln.creditCents); err != nil {
			t.Fatalf("scan a line: %v", err)
		}
		lines = append(lines, ln)
	}
	rows.Close()
	if len(lines) != 2 {
		t.Fatalf("the untaxed invoice's journal carries %d lines, want 2 (AR and revenue, no tax leg): %+v",
			len(lines), lines)
	}
	if lines[0].code != "1100" || lines[0].debitCents != 12500 {
		t.Fatalf("the AR leg: %+v", lines[0])
	}
	if lines[1].code != "4000" || lines[1].creditCents != 12500 {
		t.Fatalf("the revenue leg: %+v", lines[1])
	}
}
