//go:build integration

// qd5_seqorder_integration_test.go, QD-5 review round 4 (fold 1, the money fold): the door and a
// real register settle never deadlock on the two feed sequence rows. The door raised its invoice
// (the invoices sequence row) and then settled its payment (the payments sequence row), while every
// writer that both bills and settles (the AR register settle, the POS ring, the payments settle
// itself) takes the payments row first: two rows taken in opposite orders, which Postgres breaks
// with 40P01, and the aborted side at the counter is an action whose card charge has already
// happened. The door now takes the payments sequence row FIRST, before it raises anything, through
// the payments module's own lock.
//
// The race is forced the way the earlier legs force theirs, never as a bare sleep race: a trigger
// sleeps inside the door's own transaction at the ar.invoice.finalized emit, the instant after the
// raise took the invoices row and before the settle takes the payments row, and the settle is
// issued only once pg_stat_activity shows the door verifiably sleeping there, then held until the
// settle is verifiably queued on a sequence row, so the pair sits in the reviewed position when the
// window closes. Without the lock-first fix the pair deadlocks (40P01); with it the settle queues
// behind the door's transaction and both answer, at pool 4 and at pool 16.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run QD5SeqOrder -count=1 -p 1
package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// qd5SeqSlowOutbox is the trigger function that stretches the door's window: it sleeps inside the
// door's transaction when the raise's finalized event is emitted, so the door holds the invoices
// sequence row and has not yet asked for the payments one while the sleep runs.
const qd5SeqSlowOutbox = `
CREATE OR REPLACE FUNCTION qd5_seq_slow_outbox() RETURNS trigger LANGUAGE plpgsql AS $f$
BEGIN
  IF to_jsonb(NEW)->>'topic' = 'ar.invoice.finalized'
     AND to_jsonb(NEW)->'payload'->>'customer_id' = TG_ARGV[0] THEN
    PERFORM pg_sleep(TG_ARGV[1]::float8);
  END IF;
  RETURN NEW;
END $f$`

// slowFinalizedEvent installs that trigger for the door's customer, dropped with the test.
func (e *qd5Env) slowFinalizedEvent(customerID string, d time.Duration) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, qd5SeqSlowOutbox); err != nil {
		e.t.Fatalf("create the slow outbox function: %v", err)
	}
	if _, err := e.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TRIGGER qd5_slow_finalized AFTER INSERT ON events_outbox FOR EACH ROW
		 EXECUTE FUNCTION qd5_seq_slow_outbox('%s', '%g')`, customerID, d.Seconds())); err != nil {
		e.t.Fatalf("install the slow finalized trigger: %v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS qd5_slow_finalized ON events_outbox`)
	})
}

// waitForSession polls pg_stat_activity until a session matches cond, the deterministic barrier the
// leg enters and leaves the widened window through.
func (e *qd5Env) waitForSession(what, cond string) {
	e.t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := e.pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND `+cond).Scan(&n); err != nil {
			e.t.Fatalf("read pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("no session was seen %s within the deadline", what)
}

// arInvoice seeds a second customer's open invoice, the document a receivables clerk settles from
// the AR register while the counter takes the door's payment beside them.
func (e *qd5Env) arInvoice() (customerID, invoiceID string) {
	e.t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("QD5-AR-%d", time.Now().UnixNano())
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', 500000) RETURNING id::text`, tag, "QD-5 AR "+tag).Scan(&customerID); err != nil {
		e.t.Fatalf("seed the AR customer: %v", err)
	}
	if err := e.pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date, subtotal_cents, tax_cents,
                      total_cents, balance_cents, finalized_at, doc_type)
VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', now() + interval '30 days', 80000, 0, 80000, 80000, now(), 'INVOICE')
RETURNING id::text`, tag+"-OPEN", customerID, qd5Trenton).Scan(&invoiceID); err != nil {
		e.t.Fatalf("seed the AR invoice: %v", err)
	}
	e.t.Cleanup(func() {
		c := context.Background()
		var payIDs []string
		if err := e.pool.QueryRow(c,
			`SELECT COALESCE(array_agg(id::text), '{}') FROM payments WHERE customer_id = $1::uuid`, customerID).Scan(&payIDs); err != nil {
			e.t.Errorf("read the settle's payment ids: %v", err)
		}
		for _, sql := range []string{
			`DELETE FROM events_outbox WHERE payload->>'payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`,
			`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
			`DELETE FROM payments WHERE customer_id = $1::uuid`,
			`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`,
			`DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
			`DELETE FROM invoices WHERE customer_id = $1::uuid`,
			`DELETE FROM customers WHERE id = $1::uuid`,
		} {
			if _, err := e.pool.Exec(c, sql, customerID); err != nil {
				e.t.Errorf("sweep the AR customer: %v", err)
			}
		}
		// Every QD-5 leg leaves the shared gate database as it found it, and later legs run
		// whole-database checks that a stranger's leftovers fail. The settle's payment.received
		// row names only the payment id in its payload, so the rows are counted by the ids the
		// leg captured before its own deletes ran.
		if len(payIDs) > 0 {
			var stray int
			if err := e.pool.QueryRow(c,
				`SELECT count(*) FROM events_outbox WHERE payload->>'payment_id' = ANY($1::text[])`, payIDs).Scan(&stray); err != nil {
				e.t.Errorf("count the settle's outbox rows: %v", err)
			}
			if stray != 0 {
				e.t.Errorf("the leg left %d events_outbox row(s) naming its own payments; the cleanup must take what the settle planted", stray)
			}
		}
	})
	return customerID, invoiceID
}

// TestQD5SeqOrder_TheDoorBesideARealARRegisterSettleNeverDeadlocks: the door's raise is stretched
// open and the register's Record Payment (the real register service over the real payments settle,
// not a double) is issued in the window against another customer's invoice. Without the payments
// sequence row taken first, the door holds the invoices row while the settle holds the payments row
// and each waits for the other: 40P01, and the test fails naming it. With it, the settle queues on
// the payments row the door already holds, the door commits, and both post.
func TestQD5SeqOrder_TheDoorBesideARealARRegisterSettleNeverDeadlocks(t *testing.T) {
	for _, size := range []int32{4, 16} {
		t.Run(fmt.Sprintf("pool %d", size), func(t *testing.T) {
			e := qd5SetupSized(t, size)
			q := e.seedQuote(1_000_000, 0, qd5Trenton)
			e.convert(q)
			e.slowFinalizedEvent(q.CustomerID, 3*time.Second)
			_, arInv := e.arInvoice()
			register := billing.NewRegisterService(billing.NewRepository(e.pool)).
				WithMoney(billing.NewPaymentsReceiptWriter(payments.NewRepository(e.pool)))

			doorErr := make(chan error, 1)
			go func() {
				_, err := e.svc.TakeOrderPayment(e.counter(), q.QuoteID, pay("CARD", 1000, "k-seq"))
				doorErr <- err
			}()

			// Barrier one: the door is verifiably sleeping inside its own transaction, the invoice
			// raised and the payment settle not yet begun.
			e.waitForSession("sleeping inside the door's raise",
				`wait_event = 'PgSleep' AND query LIKE '%events_outbox%'`)

			settleErr := make(chan error, 1)
			go func() {
				_, err := register.RecordPayment(context.Background(), billing.ReceiptInput{
					InvoiceID: arInv, Method: "CHEQUE", AmountCents: 2000,
					Reference: "chq 77", IdempotencyKey: "qd5-ar-settle",
				})
				settleErr <- err
			}()

			// Barrier two: the settle is verifiably queued on a sequence row before the window can
			// close, so the pair is in the reviewed position, not a lucky interleaving.
			e.waitForSession("queued on a sequence row",
				`wait_event_type = 'Lock' AND query LIKE '%scope_sequences%'`)

			derr, serr := <-doorErr, <-settleErr
			if derr != nil || serr != nil {
				t.Fatalf("the door and a real register settle beside each other must both answer, not deadlock: door %v, settle %v", derr, serr)
			}
			if c := e.counts(q); c.Payments != 1 || c.Invoices != 1 {
				t.Fatalf("the door's rows: %+v", c)
			}
			var bal int64
			if err := e.pool.QueryRow(context.Background(),
				`SELECT balance_cents FROM invoices WHERE id = $1::uuid`, arInv).Scan(&bal); err != nil {
				t.Fatal(err)
			}
			if bal != 80000-2000 {
				t.Fatalf("the register's settle did not land: balance %d, want %d", bal, 80000-2000)
			}
		})
	}
}
