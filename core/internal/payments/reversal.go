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
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
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
	// CanAccessBranch is the caller's branch reach, resolved by the caller from the identity context: a
	// payment in a branch it does not reach is not found, the same answer as a missing id. Nil means an
	// internal caller with no branch to be fenced to; every HTTP door sets it.
	CanAccessBranch func(branchID string) bool
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
	// The branch door, before anything about the payment is revealed: not found, as for a missing id.
	if in.CanAccessBranch != nil && !in.CanAccessBranch(branchID) {
		return Reversal{}, ErrNotFound
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
	// Only a RECEIVED or DEPOSITED payment is reversible, decided under the row lock (SF-2d round 7). A payment is
	// reversed or bounced, never both: a bounce already reopened the invoices and deleted the allocations, so a
	// reversal would credit 2310 for money that never arrived. A refund row has nothing to give back either.
	switch status {
	case "RECEIVED", "DEPOSITED":
	case "BOUNCED":
		return Reversal{}, fmt.Errorf("%w: it was bounced, and a payment is reversed or bounced, never both", ErrNotReversible)
	default:
		return Reversal{}, fmt.Errorf("%w: status is %s, only a received or deposited payment can be reversed", ErrNotReversible, status)
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
		// The facts the GL's account choice needs, read under the payment row lock this transaction holds, so the
		// account is fixed when the reversal happens and not when the consumer runs (SF-2d round 2): whether the
		// original had been deposited (cleared to the bank), and its branch.
		"original_status":  status,
		"original_cleared": status == "DEPOSITED",
		"branch_id":        branchID,
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
	store   reversalStore
	reapply reapplyStore // SF-2l: the same repository, when the store carries it (reapply.go)
	payout  payoutStore  // SF-2m: likewise (payout.go)
	audit   audit.Recorder
	clock   func() time.Time
}

// NewReverseService builds the undo service.
func NewReverseService(store reversalStore) *ReverseService {
	svc := &ReverseService{store: store, clock: time.Now}
	if rs, ok := store.(reapplyStore); ok {
		svc.reapply = rs
	}
	if ps, ok := store.(payoutStore); ok {
		svc.payout = ps
	}
	return svc
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

// ReapplyHeld re-applies the money a reversed payment holds in 2310 to invoices of the payment's own customer
// (SF-2l; reapply.go states the rules). ONE transaction, in the one money lock order (LOCK-ORDER.md):
//
//  1. the payment row FOR UPDATE, the first lock a reversal and a bounce take too, so a re-application beside either
//     of them, or beside another re-application of the same payment, serialises here;
//  2. the named invoice rows FOR NO KEY UPDATE in ascending id order, every one before the first balance move draws
//     the invoices counter. No payments counter is drawn: nothing here stamps a payments row.
//
// Every judgement (the replay, the account, the held figure, the invoices) runs under those locks and BEFORE the first
// write, so a refused request writes nothing. The write is the settle's own slice (applySliceTx) per invoice, one
// payment_reapplications row each, and one payment.reapplied outbox row for the GL (Dr 2310 / Cr AR for the total).
func (r *Repository) ReapplyHeld(ctx context.Context, in ReapplyInput, now time.Time) (Reapplication, error) {
	if r.pool == nil {
		return Reapplication{}, errNoDB
	}
	in, err := validateReapply(in)
	if err != nil {
		return Reapplication{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Reapplication{}, fmt.Errorf("reapply: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		customerID, method, branchID string
		amount                       int64
		reversedAt                   *time.Time
		isItselfAReversal            bool
	)
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text,''), method, branch_id::text, amount_cents, reversed_at,
		        reverses_payment_id IS NOT NULL
		   FROM payments WHERE id = $1::uuid FOR UPDATE`, in.PaymentID).
		Scan(&customerID, &method, &branchID, &amount, &reversedAt, &isItselfAReversal)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reapplication{}, ErrNotFound
		}
		return Reapplication{}, fmt.Errorf("reapply: lock payment: %w", err)
	}
	// The branch door, before anything about the payment is revealed: not found, as for a missing id.
	if in.CanAccessBranch != nil && !in.CanAccessBranch(branchID) {
		return Reapplication{}, ErrNotFound
	}

	// What has already left 2310: re-applications and every kind of payout (SF-2m), one figure.
	reappliedCents, paidOutCents, err := outOfHeldTx(ctx, tx, in.PaymentID)
	if err != nil {
		return Reapplication{}, fmt.Errorf("reapply: read applied: %w", err)
	}
	taken := reappliedCents + paidOutCents

	// The replay is judged under the payment row lock, so a twin that committed while this request waited on the row is
	// already visible: the same key and the same body answers the first result and writes nothing.
	if replay, ok, err := r.reapplyReplay(ctx, tx, in, amount, taken); err != nil {
		return Reapplication{}, err
	} else if ok {
		return replay, nil
	}

	switch {
	case isItselfAReversal:
		return Reapplication{}, fmt.Errorf("%w: it is itself a reversing entry, the money is held for the payment it reversed", ErrNotReapplicable)
	case reversedAt == nil:
		return Reapplication{}, fmt.Errorf("%w: it has not been reversed", ErrNotReapplicable)
	case customerID == "":
		return Reapplication{}, fmt.Errorf("%w: it has no account to hold the money for", ErrNotReapplicable)
	}
	held := amount - taken
	var total int64
	for _, a := range in.Allocations {
		total += a.AmountCents
	}
	if total > held {
		return Reapplication{}, fmt.Errorf("%w (%d cents held, %d asked for)", ErrReapplyOverHeld, held, total)
	}

	// The named invoice rows, ascending (the sort is the validation's), taken before any write. An invoice that is
	// missing, another account's or outside the caller's branch reach is the same not found.
	type target struct {
		a       Allocation
		no      string
		balance int64
	}
	targets := make([]target, 0, len(in.Allocations))
	for _, a := range in.Allocations {
		var (
			no, invBranch, status string
			balance, holdback     int64
		)
		err := tx.QueryRow(ctx,
			`SELECT invoice_no::text, balance_cents, holdback_cents, branch_id::text, status
			   FROM invoices WHERE id = $1::uuid AND customer_id = $2::uuid FOR NO KEY UPDATE`,
			a.InvoiceID, customerID).Scan(&no, &balance, &holdback, &invBranch, &status)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Reapplication{}, NoSuchInvoiceError{InvoiceID: a.InvoiceID}
			}
			return Reapplication{}, fmt.Errorf("reapply: lock invoice %s: %w", a.InvoiceID, err)
		}
		if in.CanAccessBranch != nil && !in.CanAccessBranch(invBranch) {
			return Reapplication{}, NoSuchInvoiceError{InvoiceID: a.InvoiceID}
		}
		if status != "FINALIZED" && status != "PARTIALLY_PAID" {
			return Reapplication{}, httpx.BadRequestf("invoice %s is not open, held money can only be applied to an open invoice", no)
		}
		if invBranch != branchID {
			return Reapplication{}, httpx.BadRequestf(
				"invoice %s is in another branch than the payment: the money is held in the payment's branch, apply it to that branch's invoices", no)
		}
		payable := balance - holdback
		if payable < 0 {
			payable = 0
		}
		if a.AmountCents > payable {
			return Reapplication{}, httpx.BadRequestf("invoice %s can take at most %d cents right now; refresh and retry", no, payable)
		}
		targets = append(targets, target{a: a, no: no, balance: balance})
	}

	batchID := uuid.NewString()
	lines := make([]ReapplyLine, 0, len(targets))
	for _, t := range targets {
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_reapplications
			   (batch_id, payment_id, invoice_id, customer_id, branch_id, amount_cents, reason, applied_by, idempotency_key)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8::uuid, $9)`,
			batchID, in.PaymentID, t.a.InvoiceID, customerID, branchID, t.a.AmountCents, in.Reason,
			nullableUUID(in.ActorID), in.IdempotencyKey); err != nil {
			return Reapplication{}, fmt.Errorf("reapply: record the application on %s: %w", t.no, err)
		}
		if err := applySliceTx(ctx, tx, slice{
			CustomerID: customerID, InvoiceID: t.a.InvoiceID, BranchID: branchID, AmountCents: t.a.AmountCents,
			SourceType: "payment_reapply", SourceRefID: in.PaymentID, Memo: "re-applied from a reversed payment: " + in.Reason,
		}); err != nil {
			return Reapplication{}, err
		}
		after := t.balance - t.a.AmountCents
		outcome := ApplyOutcomePart
		if after <= 0 {
			outcome = ApplyOutcomeCloses
		}
		lines = append(lines, ReapplyLine{
			InvoiceID: t.a.InvoiceID, InvoiceNo: t.no, AppliedCents: t.a.AmountCents, Outcome: outcome, BalanceAfterCents: after,
		})
	}

	// Transactional outbox, in the SAME transaction, so the GL books Dr 2310 / Cr AR exactly once (the identical
	// discipline the settle and the reversal use). The branch is the payment's: the invoices share it (checked above).
	invoiceIDs := make([]string, 0, len(targets))
	for _, t := range targets {
		invoiceIDs = append(invoiceIDs, t.a.InvoiceID)
	}
	payload, err := json.Marshal(map[string]any{
		"batch_id":     batchID,
		"payment_id":   in.PaymentID,
		"customer_id":  customerID,
		"branch_id":    branchID,
		"amount_cents": total,
		"method":       method,
		"reason":       in.Reason,
		"invoice_ids":  invoiceIDs,
	})
	if err != nil {
		return Reapplication{}, fmt.Errorf("reapply: marshal payment.reapplied: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('payment.reapplied', $1)`, payload); err != nil {
		return Reapplication{}, fmt.Errorf("reapply: emit payment.reapplied: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Reapplication{}, fmt.Errorf("reapply: commit: %w", err)
	}
	return Reapplication{
		PaymentID: in.PaymentID, BatchID: batchID, AppliedCents: total,
		HeldBeforeCents: held, HeldAfterCents: held - total, Lines: lines,
	}, nil
}

// reapplyReplay judges a request's key against the rows it already wrote, on the caller's transaction (which holds the
// payment row). The same key with the same body answers the first result: its batch, its lines at the invoices'
// balances NOW, and the held figures as they stand now (before = after plus what the request applied). A key that wrote
// anything else is a conflict.
func (r *Repository) reapplyReplay(ctx context.Context, tx pgx.Tx, in ReapplyInput, amount, taken int64) (Reapplication, bool, error) {
	rows, err := tx.Query(ctx,
		`SELECT ra.batch_id::text, ra.payment_id::text, ra.invoice_id::text, ra.amount_cents, i.invoice_no::text, i.balance_cents
		   FROM payment_reapplications ra JOIN invoices i ON i.id = ra.invoice_id
		  WHERE ra.idempotency_key = $1 ORDER BY ra.invoice_id`, in.IdempotencyKey)
	if err != nil {
		return Reapplication{}, false, fmt.Errorf("reapply: read the key: %w", err)
	}
	defer rows.Close()
	var (
		batch, storedPayment string
		stored               = map[string]int64{}
		lines                []ReapplyLine
		total                int64
	)
	for rows.Next() {
		var l ReapplyLine
		var invoiceBalance int64
		var payment string
		if err := rows.Scan(&batch, &payment, &l.InvoiceID, &l.AppliedCents, &l.InvoiceNo, &invoiceBalance); err != nil {
			return Reapplication{}, false, fmt.Errorf("reapply: scan the key: %w", err)
		}
		storedPayment = payment
		stored[l.InvoiceID] = l.AppliedCents
		l.BalanceAfterCents = invoiceBalance
		l.Outcome = ApplyOutcomePart
		if invoiceBalance <= 0 {
			l.Outcome = ApplyOutcomeCloses
		}
		total += l.AppliedCents
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return Reapplication{}, false, err
	}
	if len(lines) == 0 {
		return Reapplication{}, false, nil
	}
	if !sameReapplyBody(in.PaymentID, stored, storedPayment, in) {
		return Reapplication{}, false, replayConflict()
	}
	heldNow := amount - taken
	return Reapplication{
		PaymentID: in.PaymentID, BatchID: batch, AppliedCents: total,
		HeldBeforeCents: heldNow + total, HeldAfterCents: heldNow, Lines: lines, Replayed: true,
	}, true, nil
}

// HeldFor reads what one payment holds in 2310: amount less re-applied, for a reversed payment; zero otherwise. A
// payment outside the caller's branch reach is not found.
func (r *Repository) HeldFor(ctx context.Context, paymentID string, canAccessBranch func(branchID string) bool) (HeldFigure, error) {
	if r.pool == nil {
		return HeldFigure{}, errNoDB
	}
	pid, err := uuid.Parse(paymentID)
	if err != nil {
		return HeldFigure{}, ErrNotFound
	}
	var (
		h          HeldFigure
		branchID   string
		reversedAt *time.Time
		reversing  bool
		hasAccount bool
	)
	err = r.pool.QueryRow(ctx,
		`SELECT p.id::text, p.payment_no::text, p.amount_cents, p.branch_id::text, p.reversed_at,
		        p.reverses_payment_id IS NOT NULL, p.customer_id IS NOT NULL,
		        COALESCE((SELECT SUM(ra.amount_cents) FROM payment_reapplications ra WHERE ra.payment_id = p.id), 0),
		        COALESCE((SELECT SUM(po.amount_cents) FROM payment_payouts po WHERE po.payment_id = p.id), 0)
		   FROM payments p WHERE p.id = $1::uuid`, pid.String()).
		Scan(&h.PaymentID, &h.PaymentNo, &h.AmountCents, &branchID, &reversedAt, &reversing, &hasAccount, &h.AppliedCents, &h.PaidOutCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HeldFigure{}, ErrNotFound
		}
		return HeldFigure{}, fmt.Errorf("held: read payment: %w", err)
	}
	if canAccessBranch != nil && !canAccessBranch(branchID) {
		return HeldFigure{}, ErrNotFound
	}
	h.Reversed = reversedAt != nil
	if h.Reversed && !reversing && hasAccount {
		h.HeldCents = h.AmountCents - h.AppliedCents - h.PaidOutCents
	}
	return h, nil
}

// outOfHeldTx reads, on the caller's transaction (which holds the payment row), what has left a payment's 2310: the
// money re-applied to invoices and the money paid out or written off.
func outOfHeldTx(ctx context.Context, tx pgx.Tx, paymentID string) (reapplied, paidOut int64, err error) {
	err = tx.QueryRow(ctx,
		`SELECT COALESCE((SELECT SUM(amount_cents) FROM payment_reapplications WHERE payment_id = $1::uuid), 0),
		        COALESCE((SELECT SUM(amount_cents) FROM payment_payouts WHERE payment_id = $1::uuid), 0)`,
		paymentID).Scan(&reapplied, &paidOut)
	return reapplied, paidOut, err
}

// WithTill binds the till's side of the drawer pay out and the write off. Without it those two kinds are refused.
func (r *Repository) WithTill(t TillSeam) *Repository { r.till = t; return r }

// PayOutHeld takes held money out of 2310 by the act that moves it (payout.go states the rules). ONE transaction: the
// payment row FOR UPDATE first (the lock a reversal, a bounce and a re-application take), every judgement under it and
// before the first write, then for a drawer pay out the till's PAID_OUT movement on its own session row (FOR SHARE, the
// SF-2j rule), the payout row and one payment.paid_out outbox row for the GL.
func (r *Repository) PayOutHeld(ctx context.Context, in PayoutInput, now time.Time) (Payout, error) {
	if r.pool == nil {
		return Payout{}, errNoDB
	}
	in, err := validatePayout(in)
	if err != nil {
		return Payout{}, err
	}
	pid, err := uuid.Parse(strings.TrimSpace(in.PaymentID))
	if err != nil {
		return Payout{}, httpx.BadRequestf("payment id must be a uuid")
	}
	in.PaymentID = pid.String()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Payout{}, fmt.Errorf("payout: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		customerID, method, branchID, tillSession string
		amount                                    int64
		reversedAt                                *time.Time
		isItselfAReversal                         bool
	)
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text,''), method, branch_id::text, amount_cents, reversed_at,
		        reverses_payment_id IS NOT NULL, COALESCE(till_session_id::text,'')
		   FROM payments WHERE id = $1::uuid FOR UPDATE`, in.PaymentID).
		Scan(&customerID, &method, &branchID, &amount, &reversedAt, &isItselfAReversal, &tillSession)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Payout{}, ErrNotFound
		}
		return Payout{}, fmt.Errorf("payout: lock payment: %w", err)
	}
	if in.CanAccessBranch != nil && !in.CanAccessBranch(branchID) {
		return Payout{}, ErrNotFound
	}
	reapplied, paidOut, err := outOfHeldTx(ctx, tx, in.PaymentID)
	if err != nil {
		return Payout{}, fmt.Errorf("payout: read what has left: %w", err)
	}
	held := amount - reapplied - paidOut

	// A replay is judged under the payment row: the same key and body answers the first result.
	var (
		prevID, prevKind, prevPayment string
		prevAmount                    int64
	)
	switch err := tx.QueryRow(ctx,
		`SELECT id::text, kind, payment_id::text, amount_cents FROM payment_payouts WHERE idempotency_key = $1`, in.IdempotencyKey).
		Scan(&prevID, &prevKind, &prevPayment, &prevAmount); {
	case err == nil:
		if prevPayment != in.PaymentID || prevKind != in.Kind || prevAmount != in.AmountCents {
			return Payout{}, ReplayConflictError{Msg: "that idempotency key already paid out held money differently; use a new key"}
		}
		return Payout{PaymentID: in.PaymentID, PayoutID: prevID, Kind: prevKind, AmountCents: prevAmount,
			HeldBeforeCents: held + prevAmount, HeldAfterCents: held, Replayed: true}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return Payout{}, fmt.Errorf("payout: read the key: %w", err)
	}

	switch {
	case isItselfAReversal:
		return Payout{}, fmt.Errorf("%w: it is itself a reversing entry, the money is held for the payment it reversed", ErrNotReapplicable)
	case reversedAt == nil:
		return Payout{}, fmt.Errorf("%w: it has not been reversed", ErrNotReapplicable)
	case customerID == "":
		return Payout{}, fmt.Errorf("%w: it has no account to hold the money for", ErrNotReapplicable)
	}
	if in.AmountCents > held {
		return Payout{}, fmt.Errorf("%w (%d cents held, %d asked for)", ErrReapplyOverHeld, held, in.AmountCents)
	}

	var sessionForRow any
	switch in.Kind {
	case PayoutDrawer:
		if r.till == nil {
			return Payout{}, errors.New("payments: the drawer pay out is not wired")
		}
		if err := r.till.PayOutHeldMoney(ctx, tx, DrawerPaidOut{
			SessionID: in.TillSessionID, BranchID: branchID, AmountCents: in.AmountCents, Reason: in.Reason, Reference: in.Reference,
		}); err != nil {
			return Payout{}, err
		}
		sessionForRow = in.TillSessionID
	case PayoutWriteOff:
		if r.till == nil {
			return Payout{}, errors.New("payments: the write off is not wired")
		}
		if tillSession == "" {
			return Payout{}, fmt.Errorf("%w: this payment was not rung through a till session, so there is no count to cover a write off", ErrPayoutNotAllowed)
		}
		count, err := r.till.SessionCountTx(ctx, tx, tillSession)
		if err != nil {
			return Payout{}, err
		}
		if count.Status != "CLOSED" {
			return Payout{}, fmt.Errorf("%w: the till count for this payment's session is not complete yet, write it off after the count has posted the shortage", ErrPayoutNotAllowed)
		}
		if count.OverShortCents >= 0 {
			return Payout{}, fmt.Errorf("%w: the count for this payment's session found no shortage to write it off against", ErrPayoutNotAllowed)
		}
		// The shortage is the session's: what its other payments' write offs already took comes off it.
		var written int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(po.amount_cents), 0) FROM payment_payouts po JOIN payments p ON p.id = po.payment_id
			  WHERE po.kind = 'WRITE_OFF' AND p.till_session_id = $1::uuid`, tillSession).Scan(&written); err != nil {
			return Payout{}, fmt.Errorf("payout: read the session's write offs: %w", err)
		}
		if left := -count.OverShortCents - written; in.AmountCents > left {
			return Payout{}, fmt.Errorf("%w: the count's shortage still to be covered is %d cents, %d asked for", ErrPayoutNotAllowed, max(left, 0), in.AmountCents)
		}
		sessionForRow = tillSession
	}

	var payoutID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO payment_payouts (payment_id, customer_id, branch_id, kind, amount_cents, reason, reference, till_session_id, applied_by, idempotency_key)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::uuid, $9::uuid, $10) RETURNING id::text`,
		in.PaymentID, customerID, branchID, in.Kind, in.AmountCents, in.Reason, in.Reference, sessionForRow, nullableUUID(in.ActorID),
		in.IdempotencyKey).Scan(&payoutID); err != nil {
		return Payout{}, fmt.Errorf("payout: record: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"payout_id":    payoutID,
		"payment_id":   in.PaymentID,
		"customer_id":  customerID,
		"branch_id":    branchID,
		"amount_cents": in.AmountCents,
		"kind":         in.Kind,
		"credit_role":  creditRole(in.Kind),
		"method":       method,
		"reason":       in.Reason,
		"reference":    in.Reference,
	})
	if err != nil {
		return Payout{}, fmt.Errorf("payout: marshal payment.paid_out: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events_outbox (topic, payload) VALUES ('payment.paid_out', $1)`, payload); err != nil {
		return Payout{}, fmt.Errorf("payout: emit payment.paid_out: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Payout{}, fmt.Errorf("payout: commit: %w", err)
	}
	return Payout{PaymentID: in.PaymentID, PayoutID: payoutID, Kind: in.Kind, AmountCents: in.AmountCents,
		HeldBeforeCents: held, HeldAfterCents: held - in.AmountCents}, nil
}
