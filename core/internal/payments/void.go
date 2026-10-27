package payments

// void.go, FW1-06b: a TRUE void for a mis-keyed receipt.
//
// The reversal (reversal.go) is the undo for money that really was taken in and must be given a
// home: it books the amount into 2310 Customer Refunds and Unapplied Cash and holds it there for
// the customer, whatever the day. A receipt keyed against the wrong figures and caught on the day
// it was taken needs the other undo: the act that says the receipt never happened. It undoes the
// receipt's allocation (the invoices are re-opened, the allocation rows go) and its journal (the
// mirror of the receipt's own legs goes to the GL through payment.voided, so 1020 or 1010 is
// credited back), it is audited and manager only, and it holds NOTHING in 2310: the money never
// left the building, so the customer is not owed it.
//
// The void is kept deliberately apart from the reversal:
//   - its own repository function and service (this file), never a flag on the reversal's;
//   - the reversal's columns are never written: no reversing payments row, no reversed_at,
//     no reverses_payment_id, so HeldFor keeps reporting zero held money and the re-application
//     path has nothing to see;
//   - its own outbox topic, payment.voided, whose GL builder books the mirror on the RECEIPT's
//     own routing (the payment.received rules: where a receipt lands is where its void credits
//     back), never on the reversal's 2310 rule.
//
// The gates, decided under the payment row lock: manager only (route gate and service check, the
// reversal's own double gate), reason required, RECEIVED only (a DEPOSITED receipt has cleared to
// the bank and its undo is a reversal; a BOUNCED receipt has its own act; VOID is already voided;
// REFUNDED has nothing to give back), never a payment the processor captured (the undo of a
// captured card charge is a refund through the processor, not a void), the same dealer day as the
// clock (afterwards the books of a closed day are at stake and a reversal is the act), never a
// payment that was already reversed (the reversal owns the undo then) and never a reversing entry
// itself.
//
// The money the receipt took is the whole receipt, fee included: a receipt that settled an NSF fee
// carries the fee in its metadata (PR 622's nsf_fee_allocations), and the void gives the fee back
// beside the invoices, so the subledger nets to zero over the receipt and its void (S6's P2-4, the
// sequencing with PR 622).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// ActPaymentVoided is the audit verb for a void.
const ActPaymentVoided = "PAYMENT_VOIDED"

var (
	// ErrVoidReasonRequired, the void's reason is its own record.
	ErrVoidReasonRequired = errors.New("payments: a void reason is required")
	// ErrNotVoidable, the payment is not a receipt that can be undone as never taken.
	ErrNotVoidable = errors.New("payments: this payment cannot be voided")
	// ErrVoidNotWired, the handler answers when no void service is attached to it.
	ErrVoidNotWired = errors.New("payments: the void is not wired on this server")
)

// Void is the result of a void.
type Void struct {
	PaymentID        string    `json:"payment_id"`
	PaymentNo        string    `json:"payment_no"`
	AmountCents      int64     `json:"amount_cents"`
	Reason           string    `json:"reason"`
	VoidedAt         time.Time `json:"voided_at"`
	ReopenedInvoices []string  `json:"reopened_invoices"`
}

// VoidInput is one void request.
type VoidInput struct {
	PaymentID string
	Reason    string
	ActorID   string
	// IsManager is resolved by the caller from the identity context, the reversal's own double gate.
	IsManager bool
	// CanAccessBranch is the caller's branch reach: a payment outside it is not found, the same
	// answer as a missing id. Nil means an internal caller with no branch to be fenced to.
	CanAccessBranch func(branchID string) bool
}

// VoidPayment undoes a mis-keyed receipt in ONE transaction: the payment row is stamped VOID with
// the reason in its metadata, its allocations are deleted and their invoices re-opened (the
// balance restored, the status re-read, the invoices feed seq stamped beside each move), the
// subledger gets the mirror-of-cash rows so the customer's history shows the receipt undone, and
// payment.voided goes to the outbox in the SAME transaction so the GL books the undo journal
// exactly once. The reversal's columns are never touched and nothing is held in 2310.
//
// zone is the dealer's business day zone: the same-day gate is the dealer's day, never the server's
// and never UTC midnight (an evening receipt in Ontario is the same dealer day as its void the next
// UTC morning).
func (r *Repository) VoidPayment(ctx context.Context, in VoidInput, now time.Time, zone houseday.Zone) (Void, error) {
	if r.pool == nil {
		return Void{}, errNoDB
	}
	if in.PaymentID == "" {
		return Void{}, errors.New("payments: a payment id is required")
	}
	if in.Reason == "" {
		return Void{}, ErrVoidReasonRequired
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Void{}, fmt.Errorf("void: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the original and read everything the gate and the mirror rows need. FOR UPDATE is the
	// first lock every money act on a payment takes, so a void beside a reversal, a bounce or a
	// re-application serialises here.
	var (
		customerID, method, branchID, status, paymentNo string
		amount                                          int64
		receivedAt                                      time.Time
		alreadyReversed                                 bool
		isItselfAReversal                               bool
		onATillSession                                  bool
		gatewayRef                                      string
		nsfAllocJSON                                    []byte
	)
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text,''), method, branch_id::text, status, amount_cents, received_at,
		        payment_no::text, reversed_at IS NOT NULL, reverses_payment_id IS NOT NULL,
		        till_session_id IS NOT NULL, COALESCE(gateway_ref,''),
		        COALESCE(metadata->'nsf_fee_allocations', '{}'::jsonb)
		   FROM payments WHERE id = $1::uuid FOR UPDATE`, in.PaymentID).
		Scan(&customerID, &method, &branchID, &status, &amount, &receivedAt, &paymentNo, &alreadyReversed, &isItselfAReversal, &onATillSession, &gatewayRef, &nsfAllocJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Void{}, ErrNotFound
		}
		return Void{}, fmt.Errorf("void: lock payment: %w", err)
	}
	// The branch door, before anything about the payment is revealed: not found, as for a missing id.
	if in.CanAccessBranch != nil && !in.CanAccessBranch(branchID) {
		return Void{}, ErrNotFound
	}

	switch {
	case alreadyReversed:
		return Void{}, fmt.Errorf("%w: it was reversed, and the reversal owns the undo", ErrNotVoidable)
	case isItselfAReversal:
		return Void{}, fmt.Errorf("%w: it is itself a reversing entry, not a receipt", ErrNotVoidable)
	case status != "RECEIVED":
		return Void{}, fmt.Errorf("%w: its status is %s, only a received receipt that has not been deposited can be voided", ErrNotVoidable, status)
	case gatewayRef != "":
		// A processor capture (the portal's card charge) has the money at the processor: the undo is a
		// refund through the processor, and a void as never received would owe the customer again
		// while the settlement lands (FW1-06b round 2, S6's P2-3).
		return Void{}, fmt.Errorf("%w: the processor captured this card charge, and its undo is a refund through the processor, not a void", ErrNotVoidable)
	case onATillSession:
		return Void{}, fmt.Errorf("%w: it was taken on a till session, and the drawer count owns that undo; a void is for a desk receipt", ErrNotVoidable)
	case !sameHouseDay(receivedAt, now, zone):
		return Void{}, fmt.Errorf("%w: it was taken on %s, and a receipt can be voided on the dealer day it was taken only; afterwards it is a reversal",
			ErrNotVoidable, receivedAt.In(zone.Location()).Format("2006-01-02"))
	}

	// The allocations this receipt made are exactly what must be given back.
	rows, err := tx.Query(ctx,
		`SELECT invoice_id::text, amount_cents FROM payment_allocations WHERE payment_id = $1::uuid`, in.PaymentID)
	if err != nil {
		return Void{}, fmt.Errorf("void: read allocations: %w", err)
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
			return Void{}, fmt.Errorf("void: scan allocation: %w", err)
		}
		allocs = append(allocs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Void{}, err
	}

	// The one money lock order (payments/LOCK-ORDER.md): the payments sequence row, taken WITHOUT
	// advancing it, then the invoice rows in ascending id, every one before the first balance move
	// draws the invoices counter. The stamp on the original below writes a payments feed row, so
	// the counter lock comes before any invoice row for the same reason the bounce's does.
	if err := r.LockPaymentsSequenceTx(ctx, tx); err != nil {
		return Void{}, fmt.Errorf("void: lock payments sequence: %w", err)
	}
	lockIDs := make([]string, 0, len(allocs))
	for _, a := range allocs {
		lockIDs = append(lockIDs, a.invoiceID)
	}
	slices.Sort(lockIDs)
	for _, id := range lockIDs {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			return Void{}, fmt.Errorf("void: lock invoice %s: %w", id, err)
		}
	}

	// The NSF fees this receipt settled, if any. A receipt that paid a bounce fee carries the fee in
	// its metadata (PR 622's nsf_fee_allocations: the fee id and the cents it took), and the fee is
	// part of the receipt's money: the void has to give it back too, or the fee stays paid in the
	// subledger while 1100 moves by the receipt's whole amount (S6's P2-4, the sequencing with PR
	// 622). The fee rows are locked in ascending id order, the same order the reversal takes them in.
	nsfAllocs := map[string]int64{}
	// The map is written by our own settle (jsonb_build_object over the fee id and the cents it
	// took), so a malformed one cannot arrive from the desk; the reversal reads it the same way.
	_ = json.Unmarshal(nsfAllocJSON, &nsfAllocs)
	nsfIDs := make([]string, 0, len(nsfAllocs))
	for id, amt := range nsfAllocs {
		if amt > 0 {
			nsfIDs = append(nsfIDs, id)
		}
	}
	slices.Sort(nsfIDs)
	for _, id := range nsfIDs {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM payment_nsf WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			return Void{}, fmt.Errorf("void: lock nsf fee %s: %w", id, err)
		}
	}

	// Re-open each invoice and mirror the subledger, in the lock order taken above.
	var runningAR int64
	if customerID != "" {
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
			  WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`, customerID).Scan(&runningAR); err != nil {
			return Void{}, fmt.Errorf("void: read AR balance: %w", err)
		}
	}
	reopened := make([]string, 0, len(allocs))
	for _, a := range allocs {
		// The re-open's status CASE is the reversal's own (a VOID invoice stays VOID, the whole
		// balance coming back re-opens to FINALIZED), and the move stamps the invoices feed seq
		// beside it: a receipt undone is a change the payer's device must see.
		if _, err := tx.Exec(ctx, invoicesFeedSeqCTE+`
UPDATE invoices
			    SET balance_cents = balance_cents + $2,
			        status = CASE
			                   WHEN status = 'VOID' THEN status
			                   WHEN balance_cents + $2 >= total_cents - holdback_cents THEN 'FINALIZED'
			                   ELSE 'PARTIALLY_PAID'
			                 END,
			        updated_at = now(),
			        seq = (SELECT next_value FROM iseq)
			  WHERE id = $1::uuid`, a.invoiceID, a.cents); err != nil {
			return Void{}, fmt.Errorf("void: re-open invoice %s: %w", a.invoiceID, err)
		}
		runningAR += a.cents
		if customerID != "" {
			// The mirror-of-cash subledger row: the receipt's PAYMENT rows stay (a ledger is append
			// only) and this row is the undo beside them. PAYMENT rows are counted by no tie-out arm,
			// so the figure the aging and the 1100 tie read moves only through the re-opened balances.
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
				   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
				 VALUES ($1::uuid, 'PAYMENT', $2, $3, 'payment_void', $4::uuid, $5::uuid, $6)`,
				customerID, a.cents, runningAR, in.PaymentID, branchID, "voided receipt: "+in.Reason); err != nil {
				return Void{}, fmt.Errorf("void: insert mirror subledger txn: %w", err)
			}
		}
		reopened = append(reopened, a.invoiceID)
	}
	// The NSF fees the receipt paid are given back beside the invoices: the fee's own subledger row
	// (PR 622's ADJUSTMENT against the fee) is countered by this one, so the fee is open again and the
	// subledger nets to zero over the receipt and its void, fee included.
	for _, nsfID := range nsfIDs {
		feeAmt := nsfAllocs[nsfID]
		runningAR += feeAmt
		if customerID != "" {
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
				   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
				 VALUES ($1::uuid, 'ADJUSTMENT', $2, $3, 'payment_nsf', $4::uuid, $5::uuid, $6)`,
				customerID, feeAmt, runningAR, nsfID, branchID, "voided receipt: "+in.Reason); err != nil {
				return Void{}, fmt.Errorf("void: insert nsf fee subledger txn: %w", err)
			}
		}
	}
	// The receipt's application goes: the bounce's own undo. Nothing contra is left behind, because
	// nothing is contra: the receipt never happened.
	if _, err := tx.Exec(ctx, `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, in.PaymentID); err != nil {
		return Void{}, fmt.Errorf("void: delete allocations: %w", err)
	}

	// Stamp the original. The reason and the actor ride the metadata, the reversal's columns are
	// not written, and the stamp draws a fresh payments feed seq: a payment the desk lists as
	// VOID is a change the payer's cached row must see.
	stamp, err := json.Marshal(map[string]any{
		"void_reason": in.Reason,
		"voided_by":   in.ActorID,
		"voided_at":   now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return Void{}, fmt.Errorf("void: marshal the stamp: %w", err)
	}
	if _, err := tx.Exec(ctx, paymentsFeedSeqCTE+`
		UPDATE payments SET status = 'VOID',
		                    metadata = metadata || $2::jsonb,
		                    updated_at = now(),
		                    seq = (SELECT next_value FROM pseq)
		  WHERE id = $1::uuid`, in.PaymentID, string(stamp)); err != nil {
		return Void{}, fmt.Errorf("void: stamp the payment: %w", err)
	}

	// Transactional outbox, in the SAME transaction, so the GL books the undo exactly once (the
	// identical discipline the settle, the reversal and the bounce use). The payment facts ride
	// the payload, read under the row lock this transaction holds, so the journal's branch and
	// method are the receipt's own and never a re-read of the now VOID row.
	payload, err := json.Marshal(map[string]any{
		"payment_id":   in.PaymentID,
		"customer_id":  customerID,
		"branch_id":    branchID,
		"method":       method,
		"amount_cents": amount,
		"reason":       in.Reason,
		"invoice_ids":  reopened,
	})
	if err != nil {
		return Void{}, fmt.Errorf("void: marshal payment.voided: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('payment.voided', $1)`, payload); err != nil {
		return Void{}, fmt.Errorf("void: emit payment.voided: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Void{}, fmt.Errorf("void: commit: %w", err)
	}
	return Void{
		PaymentID:        in.PaymentID,
		PaymentNo:        paymentNo,
		AmountCents:      amount,
		Reason:           in.Reason,
		VoidedAt:         now,
		ReopenedInvoices: reopened,
	}, nil
}

// sameHouseDay reports whether two instants fall on the same dealer day in the zone.
func sameHouseDay(a, b time.Time, zone houseday.Zone) bool {
	return a.In(zone.Location()).Format("2006-01-02") == b.In(zone.Location()).Format("2006-01-02")
}

// - service surface -

// voidStore is the persistence VoidService needs.
type voidStore interface {
	VoidPayment(ctx context.Context, in VoidInput, now time.Time, zone houseday.Zone) (Void, error)
}

// VoidService is the permission gated, audited void of a mis-keyed receipt. It is its own service,
// kept apart from the reversal's: an undo that holds money and an undo that undoes the receipt are
// two different acts, and a flag joining them is how one day books 2310 for a receipt that never
// happened.
type VoidService struct {
	store   voidStore
	audit   audit.Recorder
	clock   func() time.Time
	zoneSrc houseday.Source
}

// NewVoidService builds the void service. Without a zone source the dealer's day is the Ontario
// default, the same default every unhoused service runs on.
func NewVoidService(store voidStore) *VoidService {
	return &VoidService{store: store, clock: time.Now}
}

// WithAudit attaches the audit recorder.
func (s *VoidService) WithAudit(r audit.Recorder) *VoidService { s.audit = r; return s }

// WithClock overrides the clock (tests).
func (s *VoidService) WithClock(now func() time.Time) *VoidService {
	if now != nil {
		s.clock = now
	}
	return s
}

// WithZone attaches the dealer's business day source, so the same-day gate is the dealer's day.
func (s *VoidService) WithZone(src houseday.Source) *VoidService { s.zoneSrc = src; return s }

// Void posts the void. Manager only, reason required, audited.
func (s *VoidService) Void(ctx context.Context, in VoidInput) (Void, error) {
	if !in.IsManager {
		return Void{}, errors.New("payments: voiding a receipt is a manager action")
	}
	if in.Reason == "" {
		return Void{}, ErrVoidReasonRequired
	}
	zone, err := houseday.Resolve(ctx, s.zoneSrc)
	if err != nil {
		return Void{}, err
	}
	v, err := s.store.VoidPayment(ctx, in, s.clock(), zone)
	if err != nil {
		return Void{}, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActPaymentVoided, EntityType: "payment", EntityID: in.PaymentID,
			ActorID: in.ActorID, Purpose: "VOID", Reason: in.Reason,
			Extra: map[string]any{
				"amount_cents":      v.AmountCents,
				"reopened_invoices": v.ReopenedInvoices,
			},
		})
	}
	return v, nil
}
