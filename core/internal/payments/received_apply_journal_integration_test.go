//go:build integration

// received_apply_journal_integration_test.go, THE JOURNAL A RECEIVED PAYMENT POSTS, and the AR tie
// out of its own slice. It lives in the external test package on purpose: it wires the REAL GL
// posting consumer (gl imports billing, billing imports payments, so the internal test files of
// this package cannot import gl without a cycle; the external package can, and nothing here needs
// this package's test fixtures).
//
// What the leg proves, against the migrated and seeded database:
//   - the one payment.received row the apply emits, drained through the real dispatcher and the
//     real posting rules, books ONE journal, on the deterministic key payment.received:<payment id>;
//   - the journal is balanced, its AR leg credits exactly the payment;
//   - the AR tie out of the payment's own slice: the invoices' balance moves sum to the payment,
//     which is the AR subledger's word against the ledger's word;
//   - a redelivered copy of the same event (the retry the outbox exists to make safe) posts
//     nothing: the key holds and the journal stays one.
package payments_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit/legsweep"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

var journalNow = time.Date(2026, time.August, 26, 9, 0, 0, 0, time.UTC)

func journalPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
		_ = set
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
	legsweep.Register(t, pool)
	return pool
}

// randomHex keeps two concurrent runs off each other's rows without a clock in a file that pins
// instants (the DIB-427 rule).
func randomHex() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fixed"
	}
	return hex.EncodeToString(b[:])
}

func TestApplyReceived_TheJournalPostsOnceAndTheARSliceTiesOut(t *testing.T) {
	pool := journalPool(t)
	ctx := context.Background()
	tag := "APJ-" + randomHex()
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 0) RETURNING id::text`,
		tag, "Apply journal "+tag).Scan(&customerID); err != nil {
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
	older := plant("OLD", 90, 60, 30000)
	newer := plant("NEW", 40, 10, 15000)

	ans, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ J1",
		IdempotencyKey: tag, InvoiceIDs: []string{older, newer}, AmountCents: 35000,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The real consumer: the dispatcher the worker runs, the GL service wired the way the worker
	// wires it, one drain of the outbox.
	glSvc := gl.NewService(gl.NewRepository(pool)).WithPaymentReader(till.NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), slog.Default())
	glSvc.RegisterPostingHandlers(d)
	// A shared database may hold other legs' pending events ahead of this one: drain until this
	// payment's journal exists (bounded), the way the worker's ticks would.
	for i := 0; i < 200; i++ {
		d.DrainOnce(ctx)
		var posted int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`,
			"payment.received:"+ans.PaymentID).Scan(&posted); err != nil {
			t.Fatalf("watch the journal: %v", err)
		}
		if posted > 0 {
			break
		}
	}

	// ONE journal on the deterministic key, posted, balanced, crediting AR by the payment.
	var journalID, status string
	if err := pool.QueryRow(ctx,
		`SELECT id::text, status FROM gl_journals WHERE idempotency_key = $1`,
		"payment.received:"+ans.PaymentID).Scan(&journalID, &status); err != nil {
		t.Fatalf("the journal on the payment's key: %v", err)
	}
	if status != "POSTED" {
		t.Fatalf("the journal is POSTED, got %s", status)
	}
	var debits, credits int64
	var arCredit int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(debit_cents),0), COALESCE(SUM(credit_cents),0),
		       COALESCE(SUM(credit_cents) FILTER (WHERE memo = 'AR settled'),0)
		  FROM gl_journal_lines WHERE journal_id = $1::uuid`, journalID).Scan(&debits, &credits, &arCredit); err != nil {
		t.Fatalf("read journal lines: %v", err)
	}
	if debits != 35000 || credits != 35000 {
		t.Fatalf("the journal is balanced at the payment amount, got D %d C %d", debits, credits)
	}
	if arCredit != 35000 {
		t.Fatalf("the AR leg credits the whole payment, got %d", arCredit)
	}

	// The AR tie out of this payment's slice: the invoices' balance moves (what the AR subledger
	// now says the account owes less of) equal what the ledger credited AR.
	var moved int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_cents - balance_cents),0) FROM invoices WHERE id = ANY($1::uuid[])`,
		[]string{older, newer}).Scan(&moved); err != nil {
		t.Fatalf("read the balance moves: %v", err)
	}
	if moved != 35000 {
		t.Fatalf("the invoices moved %d against the journal's 35000: the slice does not tie out", moved)
	}

	// The retry: a redelivered copy of the same event, exactly the row the outbox would hand the
	// consumer again, posts nothing. The key holds; the journal stays one. The final count is BY
	// THE PAYMENT (source_ref_id), not by the idempotency key: a consumer that booked a
	// redelivery under any other key would pass a key scoped count while doubling the payment's
	// journals (the delta reviews' finding); what the books carry is the payment's journals.
	if _, err := pool.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('payment.received', jsonb_build_object(
		   'payment_id', $1::text, 'customer_id', $2::text, 'amount_cents', 35000, 'method', 'CHEQUE',
		   'gateway_ref', '', 'invoice_ids', to_jsonb($3::text[])))`,
		ans.PaymentID, customerID, []string{older, newer}); err != nil {
		t.Fatalf("plant the redelivered event: %v", err)
	}
	d.DrainOnce(ctx)
	var journals int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM gl_journals WHERE source_ref_id = $1::uuid`,
		ans.PaymentID).Scan(&journals); err != nil {
		t.Fatalf("count the payment's journals: %v", err)
	}
	if journals != 1 {
		t.Fatalf("a redelivered payment.received posts nothing: the payment holds one journal, found %d", journals)
	}
}
