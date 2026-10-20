package billing

// statement_outbox.go, W5-C item 3. DIB-166 left the statement notification seam declared and
// deliberately unwired ("the shipped default is nil, a run enqueues its statements in ar_statements
// and hands nothing to any provider"). This wires it, FLAG-OFF, and states every ruling it rests on.
//
// -- WHAT "WIRED FLAG-OFF" MEANS HERE, EXACTLY ----------------------------------------------------
// There are two independent switches and BOTH are off in the shipped configuration:
//
//	1. the outbox is nil unless the composition root opts in (HOS_STATEMENT_NOTICES). Off → a
//	   month-end run behaves EXACTLY as it does today: statements generated, the sendable ones marked
//	   QUEUED in ar_statements, zero rows written to sms_outbox.
//	2. the notifier behind the SMS dispatcher is the STUB unless the environment names Twilio. Off →
//	   rows are enqueued, drained, rendered, and delivered to a stub that sends nothing.
//
// So turning (1) on in a staging environment produces real, inspectable queue rows and still sends
// nothing. That is the point of separating them: "does the wiring work" and "do customers receive
// text messages" are different questions and must be answerable independently.
//
// -- NO NEW TOPIC. THIS IS A RULING, NOT A SHORTCUT ----------------------------------------------
// A statement notice is enqueued on the EXISTING sms.ar.notice topic. A new topic would need a
// handler registered on the SMS dispatcher, and that dispatcher freezes its handler set before it
// starts running, a topic with no handler is claimed and DEAD-LETTERED, which is 0097's stated
// reason for the whole two-table design. The handler for that topic also happens to be exactly right:
// it resolves the recipient through ARRecipient → CustomerRecipient, which is "the one place consent
// is evaluated". A statement is an accounts-receivable notice. It is not a new kind of thing.
//
// The cost is the rendered wording: the AR template says "invoice <ref> for <amount> is outstanding",
// so a statement reads as an invoice with a statement-shaped reference (STMT Jul 31, 2026). That is a
// template change in a package outside this fence, and it is named in the report rather than hacked
// around from here.
//
// -- THE PAYLOAD CARRIES NO ADDRESS. THIS IS THE W4 RULE -----------------------------------------
// UNION-W4 dropped the pre-resolved `to` fast path because a number captured at ENQUEUE time skipped
// the consent check at DELIVERY time, an AR notice queued before a STOP shipped anyway. This
// producer therefore emits no `to` at all, so there is nothing for a future regression to trust.
// StatementNotice itself has carried no address since DIB-166 for the same reason; this keeps the
// property all the way onto the wire.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// StatementNoticeTopic is the topic statement notices are enqueued on. It is the string form of
// messagingprovider.TopicARNotice, written out rather than imported: billing must not import the
// messaging module (consumer-declared seams, see the module precedents), and a topic is a wire
// contract, not a type. The composition root passes it in, so a rename is caught there.
const StatementNoticeTopic = "sms.ar.notice"

// StatementNoticeQueue is the narrow durable enqueue this producer needs: put a payload on a topic,
// inside the caller's transaction. Satisfied in main.go by *messagingprovider.Outbox.EnqueueTx.
//
// IN THE CALLER'S TRANSACTION is the whole contract. The notice and the ar_statements row commit or
// roll back together, so a customer is never told about a statement that does not exist.
type StatementNoticeQueue interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, topic string, payload any) error
}

// statementNoticePayload is the wire shape, matching messagingprovider.ARNotice field for field.
//
// NO `to` FIELD EXISTS. Not empty, absent. A field that can hold an address is a field somebody can
// fill in, and the consent rule is that delivery resolves the recipient itself, every time.
type statementNoticePayload struct {
	InvoiceID   string `json:"invoice_id"`   // always empty: a statement is not an invoice
	InvoiceNo   string `json:"invoice_no"`   // the human reference, e.g. "STMT Jul 31, 2026"
	CustomerID  string `json:"customer_id"`  // what the consent resolver looks the number up by
	AmountCents int64  `json:"amount_cents"` // the closing balance the statement claims
	DueDate     string `json:"due_date"`     // empty: a statement is a position, not a due date
	DaysPastDue int    `json:"days_past_due"`
}

// statementNoticeOutbox is the flag-on implementation of StatementOutbox.
type statementNoticeOutbox struct {
	queue StatementNoticeQueue
	topic string
}

// NewStatementNoticeOutbox builds the StatementOutbox that enqueues onto the SMS outbox. A nil queue
// returns nil, so `WithOutbox(NewStatementNoticeOutbox(nil, t))` is the shipped default rather than a
// panic waiting for the first month end, an unwired dependency must degrade to "generate and queue,
// send nothing", which is the behaviour DIB-166 chose on purpose.
func NewStatementNoticeOutbox(q StatementNoticeQueue, topic string) StatementOutbox {
	if q == nil {
		return nil
	}
	if topic == "" {
		topic = StatementNoticeTopic
	}
	return statementNoticeOutbox{queue: q, topic: topic}
}

// EnqueueStatementTx writes one notice into the queue, in the statement's own transaction.
//
// AN ERROR HERE FAILS THE STATEMENT, and that is deliberate. SaveStatement propagates it and
// GenerateRun marks the run FAILED. The alternative, log and continue, produces a run that reports
// success while an unknown subset of customers was never notified, and the only way to find out is a
// customer phoning about a statement they did not get. A month-end run that fails loudly is re-runnable.
func (o statementNoticeOutbox) EnqueueStatementTx(ctx context.Context, tx pgx.Tx, n StatementNotice) error {
	if n.CustomerID == "" {
		// Nothing to resolve a recipient from. Refusing here (rather than queueing a row that can only
		// ever skip) keeps the queue's contents meaningful: every row in it is a notice that could be
		// delivered to somebody if they have consented.
		return fmt.Errorf("billing: statement notice for statement %s has no customer", n.StatementID)
	}
	return o.queue.EnqueueTx(ctx, tx, o.topic, statementNoticePayload{
		InvoiceNo:   StatementRef(n.AsOfDate),
		CustomerID:  n.CustomerID,
		AmountCents: n.BalanceCents,
	})
}

// StatementRef is the human reference a statement notice carries. Exported so the walkthrough and the
// composition root can name the same string the customer will read.
func StatementRef(asOfDate string) string {
	if asOfDate == "" {
		return "STMT"
	}
	return "STMT " + asOfDate
}
