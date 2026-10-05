package payments

// reversal.go, DIB-170: payment UNDO / CANCEL.
//
// -
// PRE-RATIFIED RULING, IMPLEMENTED LITERALLY. DO NOT RELITIGATE.
// -
//
//	A payment is NEVER deleted and NEVER edited away.
//	An undo posts a REVERSING entry, a second payment row pointing at the original, and the
//	original stays exactly as it was.
//	"Void" survives as an operator WORD, not a mechanism: a reversal posted on the same day as the
//	original is LABELLED a void in the UI. Same rows underneath.
//
// Why, written once so nobody has to re-derive it under pressure: the AR subledger
// (customer_transactions) and the general ledger are append-only projections of the payment stream.
// Delete a payment and both projections describe money with no source, discoverable only by a
// tie-out that fails months later with no route back to the act that caused it. A reversing entry
// keeps every side self-describing at every instant, and it is the only version of this that an
// auditor can follow.
//
// The same-day-void sugar deserves its own sentence, because it is the thing people ask for and it is
// where the mistake usually enters. An operator who rings a payment onto the wrong account and
// notices immediately wants it to "not have happened". It did happen. What they get is a reversal
// dated the same day, labelled VOID on screen, netting to zero on the statement, and fully visible in
// the audit trail. Nothing about that costs them anything, and it is the difference between a ledger
// you can defend and one you cannot.
//
// - WHAT A REVERSAL DOES, MECHANICALLY -
// In ONE transaction:
//  1. insert a reversing payments row (negative effect, reverses_payment_id → the original);
//  2. stamp the original's reversed_at / reversed_by_payment_id / reversal_reason;
//  3. re-open every invoice the original settled, by the amount it settled, the invoice goes back to
//     FINALIZED or PARTIALLY_PAID with the balance restored;
//  4. write the mirror-image customer_transactions rows so the AR subledger nets to zero;
//  5. emit payment.reversed to the outbox in the SAME transaction, so the GL books the reversing
//     journal exactly once.
//
// Permission-gated (manager) and reason-required. Both are enforced here AND in the database
// (0106's payments_reversal_needs_reason CHECK, ux_payments_one_reversal): the constraint is what a
// second call site cannot forget, this is what a human reads.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// ActPaymentReversed is the audit verb for a reversal.
const ActPaymentReversed = "PAYMENT_REVERSED"

var (
	// ErrAlreadyReversed, a payment may be reversed at most once. Enforced by a unique index too:
	// without it, two operators clicking undo at the same moment both post a reversal and the
	// customer's balance moves the wrong way by the payment amount.
	ErrAlreadyReversed = errors.New("payments: this payment has already been reversed")
	// ErrNotReversible, the row is itself a reversal, or is not in a state that can be reversed.
	ErrNotReversible = errors.New("payments: this payment cannot be reversed")
	// ErrReversalReasonRequired, reason-required, stated as its own error so a UI can prompt for it.
	ErrReversalReasonRequired = errors.New("payments: a reversal reason is required")
)

// Reversal is the result of an undo.
type Reversal struct {
	OriginalPaymentID string    `json:"original_payment_id"`
	ReversalPaymentID string    `json:"reversal_payment_id"`
	ReversalNo        string    `json:"reversal_no"`
	AmountCents       int64     `json:"amount_cents"`
	Reason            string    `json:"reason"`
	SameDay           bool      `json:"same_day"` // the UI labels a same-day reversal a VOID
	ReversedAt        time.Time `json:"reversed_at"`
	ReopenedInvoices  []string  `json:"reopened_invoices"`
}

// Label is the operator-facing word for this reversal. Same-day → "VOID"; otherwise "REVERSAL".
// The mechanism is identical either way, this is presentation, and keeping it a derived function
// rather than a stored column is what stops it from becoming a second kind of record.
func (r Reversal) Label() string {
	if r.SameDay {
		return "VOID"
	}
	return "REVERSAL"
}

// ReverseInput is one undo request.
type ReverseInput struct {
	PaymentID string
	Reason    string
	ActorID   string
	// IsManager is resolved by the caller from the identity context. The service refuses without it
	// rather than trusting a role string travelling through this package.
	IsManager bool
}

// ReversePayment posts a reversing entry for a settled payment.
func (r *Repository) ReversePayment(ctx context.Context, in ReverseInput, now time.Time) (Reversal, error) {
	if r.pool == nil {
		return Reversal{}, errNoDB
	}
	if in.PaymentID == "" {
		return Reversal{}, errors.New("payments: a payment id is required")
	}
	if in.Reason == "" {
		return Reversal{}, ErrReversalReasonRequired
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Reversal{}, fmt.Errorf("reverse: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the original and read everything the reversal needs. FOR UPDATE closes the
	// two-operators-clicking-undo race at the row rather than hoping the unique index catches it after
	// both have done work.
	var (
		customerID, method, branchID, status string
		amount                               int64
		receivedAt                           time.Time
		alreadyReversed                      bool
		isItselfAReversal                    bool
	)
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text,''), method, branch_id::text, status, amount_cents, received_at,
		        reversed_at IS NOT NULL, reverses_payment_id IS NOT NULL
		   FROM payments WHERE id = $1::uuid FOR UPDATE`, in.PaymentID).
		Scan(&customerID, &method, &branchID, &status, &amount, &receivedAt, &alreadyReversed, &isItselfAReversal)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reversal{}, ErrNotFound
		}
		return Reversal{}, fmt.Errorf("reverse: lock payment: %w", err)
	}
	if alreadyReversed {
		return Reversal{}, ErrAlreadyReversed
	}
	if isItselfAReversal {
		// Reversing a reversal is not an undo, it is a re-application, and it should be entered as a
		// fresh payment so the reason for taking the money again is recorded as its own act.
		return Reversal{}, fmt.Errorf("%w: it is itself a reversing entry, take the payment again as a new "+
			"payment so the reason for re-charging is its own record", ErrNotReversible)
	}
	if status == "VOID" {
		return Reversal{}, fmt.Errorf("%w: status is VOID", ErrNotReversible)
	}

	// The allocations this payment made are exactly what must be given back.
	rows, err := tx.Query(ctx,
		`SELECT invoice_id::text, amount_cents FROM payment_allocations WHERE payment_id = $1::uuid`, in.PaymentID)
	if err != nil {
		return Reversal{}, fmt.Errorf("reverse: read allocations: %w", err)
	}
	type alloc struct {
		invoiceID string
		cents     int64
	}
	var allocs []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.invoiceID, &a.cents); err != nil {
			rows.Close()
			return Reversal{}, fmt.Errorf("reverse: scan allocation: %w", err)
		}
		allocs = append(allocs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Reversal{}, err
	}

	// 1. The reversing payment row. Status VOID marks it as a contra entry rather than money received;
	// the amount stays POSITIVE because payments.amount_cents has a CHECK (> 0) and the direction is
	// carried by reverses_payment_id and by the negative subledger rows below. Encoding direction in
	// the sign of a column that cannot be negative is how a ledger acquires a lie.
	// The reversing row carries the payments feed seq and the org off
	// the customer beside it (the settle's own stamp posture), so the
	// reversal is visible on the payer's payments feed from the moment
	// it lands.
	var revID, revNo string
	err = tx.QueryRow(ctx, paymentsFeedSeqCTE+`
INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id,
                       reverses_payment_id, reversal_reason, org_id, seq)
		 VALUES ('REV-'||nextval('payment_no_seq')::text, $1::uuid, $2, $3, 'VOID', $4::uuid, $5::uuid, $6,
		         (SELECT org_id FROM customers WHERE id = $1::uuid),
		         (SELECT next_value FROM pseq))
		 RETURNING id::text, payment_no::text`,
		nullableUUID(customerID), method, amount, branchID, in.PaymentID, in.Reason).Scan(&revID, &revNo)
	if err != nil {
		return Reversal{}, fmt.Errorf("reverse: insert reversing payment: %w", err)
	}

	// 2. Stamp the original. The guarded WHERE is the second half of the one-reversal-only rule.
	// The stamp draws a fresh payments feed seq beside it: the
	// original's reversal is the change the payer's cached payment row
	// must see.
	tag, err := tx.Exec(ctx, paymentsFeedSeqCTE+`
		UPDATE payments SET reversed_at = $2, reversed_by_payment_id = $3::uuid, updated_at = now(),
		                    seq = (SELECT next_value FROM pseq)
		  WHERE id = $1::uuid AND reversed_at IS NULL`, in.PaymentID, now, revID)
	if err != nil {
		return Reversal{}, fmt.Errorf("reverse: stamp original: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Reversal{}, ErrAlreadyReversed
	}

	// The re-open's UPDATE draws the invoices counter while it computes the new row, and the contra
	// allocation's foreign key share lock only covers the invoice the loop has already reached: on
	// the second invoice of a multi-invoice reversal the counter would be held with no lock on the
	// row at all, head on into any writer holding that invoice while it waits on the counter (a POS
	// return's account credit, an NSF bounce). Take every allocated invoice row explicitly, in
	// ascending id order (the one money lock order, steps 3 then 4), before the first counter draw.
	// This sits after the payments counter statements above, so the order is steps 2, 3, 4.
	// The rows are taken FOR NO KEY UPDATE for the same reason the settle's are: the reversal writes
	// no key column, and a FOR UPDATE would conflict with the FOR KEY SHARE a POS return's
	// pos_returns foreign key check holds on the returned sale's invoice while the return waits on
	// this transaction's payments counter, closing the same cycle the settle's lock once did.
	lockIDs := make([]string, 0, len(allocs))
	for _, a := range allocs {
		lockIDs = append(lockIDs, a.invoiceID)
	}
	slices.Sort(lockIDs)
	for _, id := range lockIDs {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			return Reversal{}, fmt.Errorf("reverse: lock invoice %s: %w", id, err)
		}
	}

	// 3 + 4. Re-open each invoice and mirror the subledger.
	var runningAR int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		  WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`, customerID).Scan(&runningAR); err != nil {
		return Reversal{}, fmt.Errorf("reverse: read AR balance: %w", err)
	}

	reopened := make([]string, 0, len(allocs))
	for _, a := range allocs {
		// A reversal is a contra ALLOCATION too, so the payment_allocations sum for the invoice still
		// reconciles against what the invoice believes it has been paid.
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents) VALUES ($1::uuid, $2::uuid, $3)`,
			revID, a.invoiceID, a.cents); err != nil {
			return Reversal{}, fmt.Errorf("reverse: insert contra allocation: %w", err)
		}
		// The invoice re-opens to FINALIZED when the whole balance comes back, PARTIALLY_PAID when
		// some other payment still covers part of it. A VOID invoice is NOT re-opened, voiding an
		// invoice is a separate act with its own reasons, and a payment reversal must not undo it.
		// The re-open stamps the invoices feed seq beside the move (the settle's own recorded
		// posture): a reversal is an invoice change the payer's device must see.
		if _, err := tx.Exec(ctx, invoicesFeedSeqCTE+`
UPDATE invoices
			    SET balance_cents = balance_cents + $2,
			        status = CASE
			                   WHEN balance_cents + $2 >= total_cents - holdback_cents THEN 'FINALIZED'
			                   ELSE 'PARTIALLY_PAID'
			                 END,
			        updated_at = now(),
			        seq = (SELECT next_value FROM iseq)
			  WHERE id = $1::uuid AND status <> 'VOID'`, a.invoiceID, a.cents); err != nil {
			return Reversal{}, fmt.Errorf("reverse: re-open invoice %s: %w", a.invoiceID, err)
		}
		runningAR += a.cents
		if _, err := tx.Exec(ctx,
			`INSERT INTO customer_transactions
			   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
			 VALUES ($1::uuid, 'PAYMENT', $2, $3, 'payment_reversal', $4::uuid, $5::uuid, $6)`,
			customerID, a.cents, runningAR, revID, branchID, "reversal: "+in.Reason); err != nil {
			return Reversal{}, fmt.Errorf("reverse: insert contra subledger txn: %w", err)
		}
		reopened = append(reopened, a.invoiceID)
	}

	// 5. Transactional outbox, in the SAME transaction as the reversal, so the GL books the reversing
	// journal exactly once, the identical discipline the settle path uses for payment.received.
	payload, err := json.Marshal(map[string]any{
		"payment_id":          revID,
		"reverses_payment_id": in.PaymentID,
		"customer_id":         customerID,
		"amount_cents":        amount,
		"method":              method,
		"reason":              in.Reason,
		"invoice_ids":         reopened,
	})
	if err != nil {
		return Reversal{}, fmt.Errorf("reverse: marshal payment.reversed: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('payment.reversed', $1)`, payload); err != nil {
		return Reversal{}, fmt.Errorf("reverse: emit payment.reversed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Reversal{}, fmt.Errorf("reverse: commit: %w", err)
	}

	return Reversal{
		OriginalPaymentID: in.PaymentID,
		ReversalPaymentID: revID,
		ReversalNo:        revNo,
		AmountCents:       amount,
		Reason:            in.Reason,
		SameDay:           sameDay(receivedAt, now),
		ReversedAt:        now,
		ReopenedInvoices:  reopened,
	}, nil
}

// sameDay reports whether two instants fall on the same calendar day in UTC. It is what decides
// whether the UI says VOID or REVERSAL, and nothing else depends on it.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}

// nullableUUID turns "" into a nil any so a NULL customer_id round-trips (payments.customer_id is
// nullable, a counter cash sale has no account).
func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// - service surface -

// reversalStore is the persistence ReverseService needs.
type reversalStore interface {
	ReversePayment(ctx context.Context, in ReverseInput, now time.Time) (Reversal, error)
}

// ReverseService is the permission-gated, audited undo. It is a separate small service rather than
// another method on Service because the pay flow's dependencies (a gateway) have nothing to do with a
// reversal, and a reversal must remain possible when no processor is reachable at all, the money has
// already moved; this is bookkeeping.
type ReverseService struct {
	store reversalStore
	audit audit.Recorder
	clock func() time.Time
}

// NewReverseService builds the undo service.
func NewReverseService(store reversalStore) *ReverseService {
	return &ReverseService{store: store, clock: time.Now}
}

// WithAudit attaches the audit recorder.
func (s *ReverseService) WithAudit(r audit.Recorder) *ReverseService { s.audit = r; return s }

// WithClock overrides the clock (tests).
func (s *ReverseService) WithClock(now func() time.Time) *ReverseService {
	if now != nil {
		s.clock = now
	}
	return s
}

// Reverse posts a reversing entry. Manager-only, reason required, audited.
func (s *ReverseService) Reverse(ctx context.Context, in ReverseInput) (Reversal, error) {
	if !in.IsManager {
		return Reversal{}, errors.New("payments: reversing a payment is a manager action")
	}
	if in.Reason == "" {
		return Reversal{}, ErrReversalReasonRequired
	}
	rev, err := s.store.ReversePayment(ctx, in, s.clock())
	if err != nil {
		return Reversal{}, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActPaymentReversed, EntityType: "payment", EntityID: in.PaymentID,
			ActorID: in.ActorID, Purpose: rev.Label(), Reason: in.Reason,
			Extra: map[string]any{
				"reversal_payment_id": rev.ReversalPaymentID,
				"amount_cents":        rev.AmountCents,
				"reopened_invoices":   rev.ReopenedInvoices,
			},
		})
	}
	return rev, nil
}
