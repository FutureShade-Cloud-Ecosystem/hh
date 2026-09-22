package messagingprovider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// collections.go, DIB-76: the AR collections cadence. THE PRODUCER Wave 2's TopicARNotice never had.
//
// Wave 2 shipped a topic, a template and a consumer for accounts-receivable notices, and nothing that
// ever emitted one. This is that thing: a nightly sweep that reads the aging, decides which overdue
// invoices have crossed a bucket they have not been notified about, and enqueues one notice each.
//
// -------------------------------------------------------------------------------------------------
// THE CADENCE, IN PLAIN LANGUAGE.
//
//	THREE BUCKETS. 30, 60 and 90 days past due, the same three columns as the aging report every AR
//	clerk already reads, so "we texted them at 60" lines up with the report they are looking at.
//
//	ONE NOTICE PER INVOICE PER BUCKET, FOREVER. Crossing 30 days earns exactly one text. The sweep
//	runs again tomorrow, the invoice is still 31 days late, and NOTHING happens, the ledger row from
//	yesterday is the answer. This is enforced by a UNIQUE constraint (0110), not by a query the next
//	maintainer might reorder: the producer INSERTs the ledger row and enqueues the notice in ONE
//	transaction, and a conflicting insert means somebody already sent it.
//
//	ONE NOTICE PER CUSTOMER PER COOLDOWN. A contractor with eleven overdue invoices gets one text,
//	not eleven. Without this, the first night this job runs against a real ledger it sends a customer
//	a dozen messages in a second, and the number is filtered by the carrier before the second one
//	lands. The remaining invoices are not lost, they are simply reconsidered on a later run, and the
//	invoice with the WORST aging is chosen first.
//
//	NEWEST BUCKET WINS. An invoice 95 days late that was never notified gets the 90-day message, and
//	the 30 and 60 buckets are backfilled as SUPPRESSED so tomorrow's run does not walk it up the
//	ladder sending three texts on three consecutive nights for a bill that was already three months
//	late when we started.
//
// -------------------------------------------------------------------------------------------------
// NOTHING HERE SENDS. It enqueues, and it enqueues held until the recipient's quiet hours allow it.
// Delivery is the consumer's job, the provider defaults to the stub, and the schedule that drives
// this ships DISABLED (0110). Three independent off switches, which is the correct number for an
// automation that texts customers about money.

// ARBuckets is the cadence, worst first. The order is load-bearing: the sweep takes the FIRST bucket
// an invoice qualifies for, so "worst first" is what makes a 95-day invoice get the 90-day message.
var ARBuckets = []int{90, 60, 30}

// DefaultARCooldown is how long a customer is left alone after any collections notice.
//
// Seven days is one billing conversation. Shorter and a customer who is already talking to the office
// about a payment plan gets nagged mid-negotiation; longer and a 30-day notice and a 60-day notice
// could not both land in the same month.
const DefaultARCooldown = 7 * 24 * time.Hour

// AgingInvoice is one overdue invoice as the sweep needs it. It is a projection, not the invoice.
type AgingInvoice struct {
	InvoiceID    string
	InvoiceNo    string
	CustomerID   string
	BalanceCents int64
	DueDate      time.Time
	DaysPastDue  int
	CustomerName string
}

// ARAgingSource supplies the overdue population.
//
// It is a SEAM rather than a query in this file because the aging of the receivables ledger is
// billing's fact, not messaging's. The Pg implementation shipped alongside (PgARAging) is a
// deliberately read-only projection with a stated expiry: when internal/billing grows a real aging
// reader, this seam is repointed at it and PgARAging is deleted. That is recorded as a union note
// rather than left as a surprise, because a SELECT over another module's table is a debt whether or
// not anybody writes it down.
type ARAgingSource interface {
	// OverdueInvoices returns unpaid invoices at least minDays past due as of asOf, worst first.
	OverdueInvoices(ctx context.Context, asOf time.Time, minDays int, limit int) ([]AgingInvoice, error)
}

// ARSweep is the job body. A nil pool, a nil source or a nil outbox make it a no-op that reports
// SKIPPED, an unwired automation must be visibly inert, not quietly half-running.
type ARSweep struct {
	pool     *pgxpool.Pool
	source   ARAgingSource
	outbox   *Outbox
	resolver *ConsentResolver
	log      *slog.Logger

	cooldown time.Duration
	now      Clock
	limit    int
}

// NewARSweep builds the sweep with the house cadence.
func NewARSweep(pool *pgxpool.Pool, outbox *Outbox, resolver *ConsentResolver, log *slog.Logger) *ARSweep {
	return &ARSweep{
		pool: pool, outbox: outbox, resolver: resolver, log: log,
		cooldown: DefaultARCooldown, now: time.Now, limit: 500,
	}
}

// WithSource wires the aging projection.
func (s *ARSweep) WithSource(src ARAgingSource) *ARSweep { s.source = src; return s }

// WithClock injects the clock (tests only).
func (s *ARSweep) WithClock(c Clock) *ARSweep {
	if c != nil {
		s.now = c
	}
	return s
}

// WithCooldown overrides the per-customer cooldown (<=0 is ignored, an uncapped collections cadence
// is not a configuration this package offers).
func (s *ARSweep) WithCooldown(d time.Duration) *ARSweep {
	if d > 0 {
		s.cooldown = d
	}
	return s
}

// WithLimit caps how many invoices one run considers.
func (s *ARSweep) WithLimit(n int) *ARSweep {
	if n > 0 {
		s.limit = n
	}
	return s
}

// SweepResult is what one run did. Considered/Enqueued/Suppressed add up to the decisions made, which
// is what an operator reading job_runs wants to see.
type SweepResult struct {
	Considered int
	Enqueued   int
	Suppressed int // a bucket recorded as deliberately not sent (cooldown, no consent, backfill)
	Skipped    int // an invoice whose buckets were all already decided, the idempotent no-op
}

// ErrARNotWired, the sweep has no aging source. Reported as a SKIPPED run, never as a failure.
var ErrARNotWired = errors.New("messagingprovider: AR collections sweep has no aging source wired")

// Run evaluates the cadence once. It is safe to call repeatedly: a second run over an unchanged ledger
// enqueues nothing, and that property is the point of the whole file.
func (s *ARSweep) Run(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	if s.pool == nil || s.outbox == nil || s.source == nil {
		return res, ErrARNotWired
	}
	now := s.now().UTC()

	// The smallest bucket bounds the query: anything less than 30 days late is not this job's business.
	invoices, err := s.source.OverdueInvoices(ctx, now, ARBuckets[len(ARBuckets)-1], s.limit)
	if err != nil {
		return res, fmt.Errorf("messagingprovider: aging read: %w", err)
	}

	// Which customers have already been texted in THIS run. The database cooldown covers previous runs;
	// this set covers the current one, because two invoices for the same customer in one pass would
	// otherwise both pass a cooldown check taken before either insert committed.
	textedThisRun := map[string]bool{}

	for _, inv := range invoices {
		res.Considered++
		bucket, ok := bucketFor(inv.DaysPastDue)
		if !ok {
			continue
		}
		enq, sup, err := s.considerOne(ctx, inv, bucket, now, textedThisRun)
		if err != nil {
			return res, err
		}
		switch {
		case enq:
			res.Enqueued++
			textedThisRun[inv.CustomerID] = true
		case sup:
			res.Suppressed++
		default:
			res.Skipped++
		}
	}
	return res, nil
}

// considerOne decides and records ONE invoice's bucket, in one transaction.
//
// The transaction is the whole design. The ledger insert and the outbox enqueue commit together, so
// there is no interval in which we have recorded a notice we did not queue (a customer never told) or
// queued a notice we did not record (a customer told twice tomorrow).
func (s *ARSweep) considerOne(ctx context.Context, inv AgingInvoice, bucket int, now time.Time, textedThisRun map[string]bool) (enqueued, suppressed bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the (invoice, bucket) slot. ON CONFLICT DO NOTHING + RETURNING means an empty result is
	// "somebody already decided this bucket", the idempotent path, and the only thing standing between
	// this job and a customer receiving the same text every night.
	claimed, err := claimBucket(ctx, tx, inv, bucket, now)
	if err != nil {
		return false, false, err
	}
	if !claimed {
		return false, false, tx.Commit(ctx)
	}

	// Suppression reasons, in the order they are decided. Each one still leaves the ledger row behind,
	// so tomorrow's run does not reconsider a bucket today's run already ruled on.
	if why := s.suppressionFor(ctx, tx, inv, bucket, now, textedThisRun); why != "" {
		if err := markSuppressed(ctx, tx, inv.InvoiceID, bucket, why); err != nil {
			return false, false, err
		}
		return false, true, tx.Commit(ctx)
	}

	// Consent + quiet hours. MaySendAt answers both: may we text this customer at all, and when.
	may, notBefore, err := s.resolver.MaySendAt(ctx, inv.CustomerID, now)
	if err != nil {
		return false, false, err
	}
	if !may {
		if err := markSuppressed(ctx, tx, inv.InvoiceID, bucket, "no SMS consent"); err != nil {
			return false, false, err
		}
		return false, true, tx.Commit(ctx)
	}

	// THE PRE-RESOLVED RECIPIENT, AND WHY IT IS ON THE PAYLOAD.
	//
	// Every other topic in this package leaves To empty and lets the CONSUMER resolve the recipient at
	// delivery time, which re-checks consent at the last possible moment. sms.ar.notice cannot: its
	// handler was registered by the Wave 2 wiring against the empty fixture resolver, that consumer
	// instance is not reachable from the Wave 3 composition block, and re-registering the topic would
	// hit the dispatcher's duplicate-registration panic. So this producer uses the affordance Wave 2
	// built into ARNotice for exactly this case (see the To field's doc) and resolves the number here.
	//
	// WHAT THAT COSTS, PRECISELY: consent is evaluated when the notice is QUEUED rather than when it is
	// SENT, so a customer who opts out during the quiet-hours hold still receives this one message.
	// The hold is seconds when the sweep runs inside the send window and at most one night otherwise.
	// It is closed entirely by the one-line union change named in REPORT-W3-D.md. It is NOT closed by
	// omitting the number, which would instead mean the notice is queued and never delivered at all.
	//
	// The number is redacted from the row the moment it is published (the outbox's existing
	// published-row redaction), so it does not live in the queue table after it has been used.
	recipient, err := s.resolver.CustomerRecipient(ctx, inv.CustomerID)
	if err != nil {
		return false, false, err
	}
	eventID, err := s.outbox.EnqueueAtTx(ctx, tx, TopicARNotice, ARNotice{
		InvoiceID:   inv.InvoiceID,
		InvoiceNo:   inv.InvoiceNo,
		CustomerID:  inv.CustomerID,
		AmountCents: inv.BalanceCents,
		DueDate:     inv.DueDate.Format("2006-01-02"),
		DaysPastDue: inv.DaysPastDue,
		To:          recipient.Phone,
	}, notBefore)
	if err != nil {
		return false, false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE ar_notice_log SET event_id = $2 WHERE invoice_id = $1 AND bucket_days = $3`,
		inv.InvoiceID, eventID, bucket); err != nil {
		return false, false, fmt.Errorf("messagingprovider: link notice to ledger: %w", err)
	}

	// Backfill the buckets this invoice sailed past unnotified, so tomorrow does not walk the ladder.
	if err := s.backfillLowerBuckets(ctx, tx, inv, bucket, now); err != nil {
		return false, false, err
	}

	if s.log != nil {
		s.log.Info("ar: collections notice queued (nothing sent yet, the consumer delivers)",
			"invoice_no", inv.InvoiceNo, "bucket_days", bucket, "days_past_due", inv.DaysPastDue,
			"not_before", notBefore.Format(time.RFC3339))
	}
	return true, false, tx.Commit(ctx)
}

// suppressionFor returns a reason to record-but-not-send, or "" to send.
//
// THE ROW THIS TRANSACTION JUST CLAIMED IS EXCLUDED, AND IT HAS TO BE. claimBucket has already
// INSERTed the ledger row for (invoice, bucket), that is what makes the claim atomic, so a cooldown
// query that did not exclude it would find the claim itself, conclude the customer was notified
// moments ago, and suppress every single notice this job would ever send. It is excluded by its exact
// key rather than by invoice, because a DIFFERENT bucket of the SAME invoice is a real prior notice
// and must still count against the cooldown.
func (s *ARSweep) suppressionFor(ctx context.Context, tx pgx.Tx, inv AgingInvoice, bucket int, now time.Time, textedThisRun map[string]bool) string {
	if textedThisRun[inv.CustomerID] {
		return "customer already notified in this run"
	}
	var recent bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM ar_notice_log
			WHERE customer_id = $1 AND NOT suppressed AND decided_at > $2
			  AND NOT (invoice_id = $3 AND bucket_days = $4)
		)`, inv.CustomerID, now.Add(-s.cooldown), inv.InvoiceID, bucket).Scan(&recent); err != nil {
		// A failed cooldown read must not become a send. Treating it as "recently notified" errs
		// toward silence, which is the only safe direction for an outbound message.
		if s.log != nil {
			s.log.Warn("ar: cooldown check failed, suppressing to stay quiet", "err", err)
		}
		return "cooldown check unavailable"
	}
	if recent {
		return fmt.Sprintf("cooldown (%s since the last notice to this customer)", s.cooldown)
	}
	return ""
}

// backfillLowerBuckets records the smaller buckets an invoice passed without a notice as SUPPRESSED.
//
// Without this, an invoice that is 95 days old the first time this job ever runs gets the 90-day text
// tonight, the 60-day text tomorrow and the 30-day text the night after, three messages, in
// descending order of severity, which reads as a system that has lost track of the account.
func (s *ARSweep) backfillLowerBuckets(ctx context.Context, tx pgx.Tx, inv AgingInvoice, sent int, now time.Time) error {
	for _, b := range ARBuckets {
		if b >= sent {
			continue
		}
		claimed, err := claimBucket(ctx, tx, inv, b, now)
		if err != nil {
			return err
		}
		if claimed {
			if err := markSuppressed(ctx, tx, inv.InvoiceID, b, fmt.Sprintf("superseded by the %d-day notice", sent)); err != nil {
				return err
			}
		}
	}
	return nil
}

// claimBucket inserts the ledger row for one (invoice, bucket) and reports whether THIS call created
// it. The UNIQUE constraint from 0110 is what makes the answer trustworthy under concurrency.
func claimBucket(ctx context.Context, tx pgx.Tx, inv AgingInvoice, bucket int, now time.Time) (bool, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO ar_notice_log
		  (invoice_id, customer_id, invoice_no, bucket_days, balance_cents, days_past_due, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (invoice_id, bucket_days) DO NOTHING
		RETURNING id::text`,
		inv.InvoiceID, inv.CustomerID, inv.InvoiceNo, bucket, inv.BalanceCents, inv.DaysPastDue, now).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // already decided, the idempotent path
		}
		return false, fmt.Errorf("messagingprovider: claim AR bucket: %w", err)
	}
	return true, nil
}

// markSuppressed stamps a claimed ledger row as a deliberate non-send with a reason.
func markSuppressed(ctx context.Context, tx pgx.Tx, invoiceID string, bucket int, why string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE ar_notice_log SET suppressed = TRUE, suppress_why = $3
		 WHERE invoice_id = $1 AND bucket_days = $2`, invoiceID, bucket, why); err != nil {
		return fmt.Errorf("messagingprovider: record suppression: %w", err)
	}
	return nil
}

// bucketFor picks the worst bucket an invoice qualifies for. Fewer than 30 days late is not overdue
// enough for a text, that is a statement, not a rounding: a customer whose invoice went past due
// yesterday gets a statement, not a collections message.
func bucketFor(daysPastDue int) (int, bool) {
	for _, b := range ARBuckets {
		if daysPastDue >= b {
			return b, true
		}
	}
	return 0, false
}
