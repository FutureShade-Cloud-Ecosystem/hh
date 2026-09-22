//go:build integration

// statement_notice_integration_test.go, W5-C item 3's acceptance evidence.
//
// The DIB-166 tests proved the SEAM (a fake outbox receives a notice). These prove the WIRING: real
// rows land in the real sms_outbox table, in the statement's own transaction, carrying no address -
// and the shipped default still writes none of them.
package billing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// sqlNoticeQueue is the production-shaped enqueue: the SAME INSERT *messagingprovider.Outbox.EnqueueTx
// performs, against the same table, inside the caller's transaction.
//
// IT IS A LOCAL COPY RATHER THAN AN IMPORT because internal/billing must not import
// internal/messagingprovider, that is the consumer-declared-seam rule this whole item is built on,
// and a test import would make the fence a fiction. What the copy risks (drifting from the real
// producer) is bounded: the columns it writes are the outbox's public schema, pinned by 0097's CHECK
// and by the dispatcher that drains it, and the topic string is asserted below against the one the
// composition root passes.
type sqlNoticeQueue struct{ rows int }

func (q *sqlNoticeQueue) EnqueueTx(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sms_outbox (topic, payload) VALUES ($1, $2::jsonb)`, topic, string(b)); err != nil {
		return err
	}
	q.rows++
	return nil
}

func cleanupSMSOutbox(t *testing.T, customerID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = intPool(t).Exec(context.Background(),
			`DELETE FROM sms_outbox WHERE payload->>'customer_id' = $1`, customerID)
	})
}

// TestIntegration_StatementNotices_EnqueuedRowsAndZeroSends is the W5-C item 3 named test.
//
// It asserts, in order: the row lands; it lands on the EXISTING AR topic (no new topic, no
// dead-lettering); it is PENDING (queued, not sent); it names the customer so consent can be resolved
// at delivery; and it carries NO address, which is the UNION-W4 rule the payload must not be able to
// break.
func TestIntegration_StatementNotices_EnqueuedRowsAndZeroSends(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)
	cleanupSMSOutbox(t, rig.customerID)
	rig.invoiceDueDaysAgo(t, "STMTQ", 137000, 40, false)

	q := &sqlNoticeQueue{}
	svc := NewStatementService(rig.repo).WithOutbox(NewStatementNoticeOutbox(q, StatementNoticeTopic))

	asOf := time.Now()
	run, statements, err := svc.GenerateRun(ctx, RunModeInterim, "", asOf, "", rig.customerID)
	if err != nil {
		t.Fatalf("statement run: %v", err)
	}
	if run.QueuedCount != 1 || len(statements) != 1 {
		t.Fatalf("run = %d generated / %d queued, want one of each", run.GeneratedCount, run.QueuedCount)
	}
	if statements[0].DeliveryStatus != DeliveryQueued {
		t.Fatalf("delivery status = %q, want QUEUED", statements[0].DeliveryStatus)
	}

	// -- the row landed, and it landed committed ------------------------------------------------
	var topic, status, payload string
	if err := pool.QueryRow(ctx, `
		SELECT topic, status, payload::text FROM sms_outbox
		 WHERE payload->>'customer_id' = $1 ORDER BY created_at DESC LIMIT 1`,
		rig.customerID).Scan(&topic, &status, &payload); err != nil {
		t.Fatalf("no statement notice in sms_outbox: %v", err)
	}
	if q.rows != 1 {
		t.Fatalf("the producer enqueued %d rows, want exactly 1 per queued statement", q.rows)
	}
	// THE LITERAL, not the constant. Asserting against StatementNoticeTopic would be self-referential:
	// renaming the constant would rename both sides and the test would keep passing while every notice
	// landed on a topic the SMS dispatcher has no handler for, claimed, retried four times, then
	// DEAD. The whole ruling is that this must be the EXISTING sms.ar.notice topic, so the test says
	// the string out loud. (A survivor found this: mutation M15.)
	if topic != "sms.ar.notice" {
		t.Fatalf("topic = %q, want the EXISTING \"sms.ar.notice\", a topic with no registered handler "+
			"is claimed and DEAD-LETTERED (0097)", topic)
	}
	if StatementNoticeTopic != "sms.ar.notice" {
		t.Fatalf("StatementNoticeTopic = %q; it must stay the string form of "+
			"messagingprovider.TopicARNotice", StatementNoticeTopic)
	}
	// PENDING is the proof of ZERO REAL SENDS at this layer: the producer queues and stops. Delivery
	// is the dispatcher's job, and the dispatcher's notifier is the stub unless the environment names
	// Twilio, two independent switches, both off.
	if status != "PENDING" {
		t.Fatalf("status = %q, want PENDING, enqueue must not deliver", status)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload is not readable json: %v", err)
	}
	if got["customer_id"] != rig.customerID {
		t.Fatalf("payload customer_id = %v, want %s, the consent resolver has nothing to look up without it",
			got["customer_id"], rig.customerID)
	}
	if got["amount_cents"] != float64(137000) {
		t.Fatalf("payload amount_cents = %v, want 137000", got["amount_cents"])
	}
	if got["invoice_no"] != StatementRef(asOf.Format("2006-01-02")) {
		t.Fatalf("payload invoice_no = %v, want %q", got["invoice_no"], StatementRef(asOf.Format("2006-01-02")))
	}
	// -- THE CONSENT RULE. UNION-W4 dropped the pre-resolved `to` fast path because a number captured
	// at enqueue time skipped the consent check at delivery time. This producer must give a future
	// regression nothing to trust: the key is ABSENT, not empty.
	if _, present := got["to"]; present {
		t.Fatalf("a statement notice must carry NO recipient address (consent is resolved at delivery); payload = %s", payload)
	}
}

// TestIntegration_StatementNotices_FlagOffWritesNoQueueRows is the other half of "wired flag-off":
// with no outbox opted in, which is what cmd/server ships, a month-end run writes ZERO sms_outbox
// rows while still marking exactly the set that WOULD have gone out.
func TestIntegration_StatementNotices_FlagOffWritesNoQueueRows(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)
	cleanupSMSOutbox(t, rig.customerID)
	rig.invoiceDueDaysAgo(t, "STMTOFF", 90000, 40, false)

	// NewStatementNoticeOutbox(nil, …) returns nil, so this is literally the shipped default: an
	// unwired dependency degrades to generate-and-queue rather than panicking at month end.
	svc := NewStatementService(rig.repo).WithOutbox(NewStatementNoticeOutbox(nil, StatementNoticeTopic))

	run, statements, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID)
	if err != nil {
		t.Fatalf("flag-off run: %v", err)
	}
	if run.QueuedCount != 1 || statements[0].DeliveryStatus != DeliveryQueued {
		t.Fatalf("the set that WOULD go out must still be marked QUEUED, got %d queued / %q",
			run.QueuedCount, statements[0].DeliveryStatus)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sms_outbox WHERE payload->>'customer_id' = $1`, rig.customerID).Scan(&n); err != nil {
		t.Fatalf("count queue rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("flag-off must write ZERO sms_outbox rows, got %d", n)
	}
}

// TestIntegration_StatementNotices_EnqueueFailureRollsTheStatementBack pins the transaction claim,
// which is the reason the seam takes a pgx.Tx at all: a customer must never be told about a statement
// by a transaction that rolled back, and the run must FAIL rather than report a success in which an
// unknown subset was silently not notified.
func TestIntegration_StatementNotices_EnqueueFailureRollsTheStatementBack(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)
	rig.invoiceDueDaysAgo(t, "STMTFAIL", 50000, 40, false)

	svc := NewStatementService(rig.repo).WithOutbox(failingStatementOutbox{})
	if _, _, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID); err == nil {
		t.Fatal("a failing enqueue must fail the run, not be logged and continued past")
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ar_statements WHERE customer_id = $1::uuid`, rig.customerID).Scan(&n); err != nil {
		t.Fatalf("count statements: %v", err)
	}
	if n != 0 {
		t.Fatalf("the statement must roll back with its notice, got %d rows", n)
	}
}

type failingStatementOutbox struct{}

func (failingStatementOutbox) EnqueueStatementTx(context.Context, pgx.Tx, StatementNotice) error {
	return errStatementQueueDown
}

var errStatementQueueDown = &queueDownError{}

type queueDownError struct{}

func (*queueDownError) Error() string { return "sms outbox unavailable" }
