package billing

// register_writes.go, the register's four writes (DIB-510): we sent something, they opened it, they
// promised, they are arguing.
//
// EVERY ONE OF THEM IS IDEMPOTENT ON A ONE-TIME CLIENT KEY, and in every case the guard is a UNIQUE
// INDEX rather than a check-then-insert. The reasons differ by write and all four are real:
//   · a reminder sent twice is a customer texted twice, the relationship cost this system's whole
//     collections cadence (0110) exists to avoid;
//   · a promise recorded twice is two people who think they own the follow-up;
//   · a dispute raised twice is two arguments about one invoice;
//   · an open recorded twice is a lie about how interested the customer is.
//
// NONE OF THEM TOUCHES MONEY. Not a balance, not a due date, not an aging bucket, not a hold. That is
// the discipline the 0176/0177 headers state and this file is where it would be broken if it were
// going to be: these are facts about the CONVERSATION, and a conversation that could move a balance
// would let a phone call postpone a credit hold.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RecordSend writes one delivery row (0175) and reports whether THIS call wrote it. replayed=true
// means the key had already been honoured and nothing was sent.
func (r *Repository) RecordSend(ctx context.Context, d SendRow) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoDB
	}
	var sentBy any
	if d.SentBy != "" {
		sentBy = d.SentBy
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO ar_invoice_deliveries
		     (invoice_id, customer_id, invoice_no, kind, tone, channel, to_address,
		      balance_cents, idempotency_key, sent_by)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10::uuid)
		 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING
		 RETURNING id::text`,
		d.InvoiceID, d.CustomerID, d.InvoiceNo, d.Kind, d.Tone, d.Channel, d.ToAddress,
		d.BalanceCents, d.IdempotencyKey, sentBy).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing string
		if err := r.pool.QueryRow(ctx,
			`SELECT id::text FROM ar_invoice_deliveries WHERE idempotency_key = $1`,
			d.IdempotencyKey).Scan(&existing); err != nil {
			return "", false, fmt.Errorf("billing: read replayed send: %w", err)
		}
		return existing, true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("billing: record send: %w", err)
	}
	return id, false, nil
}

// SendRow is one row of the delivery log.
type SendRow struct {
	InvoiceID      string
	CustomerID     string
	InvoiceNo      string
	Kind           string // INVOICE | REMINDER | RECEIPT | STATEMENT
	Tone           string // REMINDER only
	Channel        string // EMAIL | SMS | PRINT
	ToAddress      string
	BalanceCents   int64
	IdempotencyKey string
	SentBy         string
}

// MarkOpened records that the customer opened the document we sent, by the DELIVERY row's id.
//
// BY DELIVERY, NOT BY INVOICE, and that is the whole design of the tracking. The id is the
// unguessable primary key of the row we wrote when we sent the message, and it is what rides in the
// link the customer receives. So an open is attributed to the SEND it belongs to, "they opened the
// firm reminder, not the original invoice", rather than to whichever send happened to be newest when
// the request arrived, which is what a by-invoice update would guess.
//
// It stamps the FIRST open and counts every one. The first open is what the register reports, because
// "opened" is a yes/no fact about whether the document reached a human; the count is kept because a
// customer who has opened an invoice nine times and not paid it is telling you something a boolean
// cannot.
//
// found=false means no such delivery row: a link that outlived its send, or a fabricated id. It is
// DISCARDED rather than creating a row to hang it on, an open with no send behind it would make the
// history disagree with itself, and the honest answer to a stranger guessing UUIDs is nothing.
func (r *Repository) MarkOpened(ctx context.Context, deliveryID string, at time.Time) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE ar_invoice_deliveries
		    SET opened_at  = COALESCE(opened_at, $2),
		        open_count = open_count + 1
		  WHERE id = $1::uuid`, deliveryID, at)
	if err != nil {
		return false, fmt.Errorf("billing: mark opened: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// LatestSendID is the id of the most recent thing we sent about a document, what the register hands
// the screen so a staff "resend the link" produces the same tracked url the customer already has.
func (r *Repository) LatestSendID(ctx context.Context, invoiceID string) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text FROM ar_invoice_deliveries
		  WHERE invoice_id = $1::uuid AND kind IN ('INVOICE','REMINDER')
		  ORDER BY sent_at DESC LIMIT 1`, invoiceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("billing: latest send: %w", err)
	}
	return id, true, nil
}

// AddPromise records a commitment (0176). replayed=true on a repeated key; conflict=true when the
// invoice already carries a LIVE promise, which is refused rather than merged, see the migration.
func (r *Repository) AddPromise(ctx context.Context, p PromiseWrite) (PromiseView, bool, bool, error) {
	if r.pool == nil {
		return PromiseView{}, false, false, errNoDB
	}
	var takenBy any
	if p.TakenBy != "" {
		takenBy = p.TakenBy
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO ar_promises_to_pay
		     (invoice_id, customer_id, promised_cents, promised_date, promised_by, note, taken_by, idempotency_key)
		 VALUES ($1::uuid, $2::uuid, $3, $4::date, $5, $6, $7::uuid, $8)
		 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING
		 RETURNING id::text`,
		p.InvoiceID, p.CustomerID, p.PromisedCents, p.PromisedDate, p.PromisedBy, p.Note, takenBy,
		p.IdempotencyKey).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The key was already honoured: answer with what it wrote.
		view, ok, readErr := r.PromiseFor(ctx, p.InvoiceID)
		if readErr != nil {
			return PromiseView{}, false, false, readErr
		}
		return view, ok, false, nil
	case err != nil && uniqueViolationOn(err, "ux_ar_promises_one_open"):
		view, _, readErr := r.PromiseFor(ctx, p.InvoiceID)
		if readErr != nil {
			return PromiseView{}, false, false, readErr
		}
		return view, false, true, nil
	case err != nil:
		return PromiseView{}, false, false, fmt.Errorf("billing: add promise: %w", err)
	}
	view, _, err := r.PromiseFor(ctx, p.InvoiceID)
	return view, false, false, err
}

// PromiseWrite is the insert shape.
type PromiseWrite struct {
	InvoiceID      string
	CustomerID     string
	PromisedCents  int64
	PromisedDate   string
	PromisedBy     string
	Note           string
	TakenBy        string
	IdempotencyKey string
}

// SettlePromise closes a live promise with a reason. Used by the register's own "cancel", and by the
// receipt path when money arrives against a document that carried one.
func (r *Repository) SettlePromise(ctx context.Context, invoiceID, status, why string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE ar_promises_to_pay
		    SET status = $2, settled_at = now(), settled_why = $3
		  WHERE invoice_id = $1::uuid AND status = 'OPEN'`, invoiceID, status, why)
	if err != nil {
		return false, fmt.Errorf("billing: settle promise: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// OpenDispute raises a dispute (0177). Same three-way answer as AddPromise.
func (r *Repository) OpenDispute(ctx context.Context, d DisputeWrite) (DisputeView, bool, bool, error) {
	if r.pool == nil {
		return DisputeView{}, false, false, errNoDB
	}
	var raisedBy any
	if d.RaisedBy != "" {
		raisedBy = d.RaisedBy
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO ar_invoice_disputes
		     (invoice_id, customer_id, reason, note, amount_cents, raised_by, idempotency_key)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7)
		 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING
		 RETURNING id::text`,
		d.InvoiceID, d.CustomerID, d.Reason, d.Note, d.AmountCents, raisedBy, d.IdempotencyKey).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		view, ok, readErr := r.DisputeFor(ctx, d.InvoiceID)
		if readErr != nil {
			return DisputeView{}, false, false, readErr
		}
		return view, ok, false, nil
	case err != nil && uniqueViolationOn(err, "ux_ar_disputes_one_open"):
		view, _, readErr := r.DisputeFor(ctx, d.InvoiceID)
		if readErr != nil {
			return DisputeView{}, false, false, readErr
		}
		return view, false, true, nil
	case err != nil:
		return DisputeView{}, false, false, fmt.Errorf("billing: open dispute: %w", err)
	}
	view, _, err := r.DisputeFor(ctx, d.InvoiceID)
	return view, false, false, err
}

// DisputeWrite is the insert shape.
type DisputeWrite struct {
	InvoiceID      string
	CustomerID     string
	Reason         string
	Note           string
	AmountCents    int64
	RaisedBy       string
	IdempotencyKey string
}

// ResolveDispute closes an open dispute with a named outcome.
func (r *Repository) ResolveDispute(ctx context.Context, invoiceID, outcome, resolvedBy string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var by any
	if resolvedBy != "" {
		by = resolvedBy
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE ar_invoice_disputes
		    SET status = 'RESOLVED', outcome = $2, resolved_by = $3::uuid, resolved_at = now()
		  WHERE invoice_id = $1::uuid AND status = 'OPEN'`, invoiceID, outcome, by)
	if err != nil {
		return false, fmt.Errorf("billing: resolve dispute: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// uniqueViolationOn reports whether err is a Postgres unique violation raised by the NAMED
// constraint. The package already has isUniqueViolation (invoice_writer.go), which answers the
// coarser "was this a 23505 anywhere in the chain", right for a writer with one unique index, wrong
// here: 0176 and 0177 each carry TWO (the one-open partial index and the idempotency key), and the
// two mean opposite things to the person reading the refusal. One is "somebody already has this
// conversation open"; the other is "you already did this". Reporting either as the other sends the
// operator looking for the wrong thing.
func uniqueViolationOn(err error, constraint string) bool {
	return isUniqueViolation(err) && strings.Contains(err.Error(), constraint)
}
