package till

// recon_seam.go, FROZEN scaffold seam. The till is the SOLE owner of the payments + till_movements tables,
// so the W5 bank-reconciliation (A-02) and GL (A-01) read seams live HERE (a sanctioned cross-module seam,
// not a bankrec/gl reach into till's tables). These methods on *till.Repository satisfy:
//   - bankrec.PaymentSource   (UnreconciledCardPayments + MarkDeposited)
//   - bankrec.LodgementSource (OpenLodgements)
//   - gl.PaymentReader        (PaymentForGL, SELECT the finance facts of a payment)
// Signatures are frozen; the A-02/A-01 workers rely on them exactly as declared.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// UnreconciledCardPayments returns the CARD payments received in [from, to) that are still RECEIVED (not yet
// matched to a bank settlement line → not yet DEPOSITED). The A-02 matcher pairs these to statement lines.
func (r *Repository) UnreconciledCardPayments(ctx context.Context, from, to time.Time) ([]bankrec.CandidatePayment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, branch_id::text, amount_cents, COALESCE(gateway_ref, ''), received_at
		 FROM payments
		 WHERE method = 'CARD' AND status = 'RECEIVED' AND received_at >= $1 AND received_at < $2
		 ORDER BY received_at`, from, to)
	if err != nil {
		return nil, fmt.Errorf("till: unreconciled card payments: %w", err)
	}
	defer rows.Close()
	var out []bankrec.CandidatePayment
	for rows.Next() {
		var c bankrec.CandidatePayment
		if err := rows.Scan(&c.PaymentID, &c.BranchID, &c.AmountCents, &c.GatewayRef, &c.ReceivedAt); err != nil {
			return nil, fmt.Errorf("till: scan candidate payment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ErrPaymentReversed is MarkDeposited's refusal of a payment that has been reversed. A reversal stamps
// reversed_at on the original and leaves its status RECEIVED, so the status alone cannot say the money
// went back; a reversed payment is never banked.
var ErrPaymentReversed = clientError{status: http.StatusConflict, msg: "till: a reversed payment cannot be marked deposited"}

// MarkDeposited flips the given RECEIVED payments to DEPOSITED and stamps the matching bank statement line id
// into metadata (traceability). Idempotent: only RECEIVED rows transition, so a replay of an already DEPOSITED
// payment is a no-op. A REVERSED payment is refused (ErrPaymentReversed), and the whole call rolls back, so a
// caller that gets the refusal finds no row moved.
//
// The refusal is decided by the UPDATE itself: its row lock and its WHERE (re-checked after a concurrent
// commit, which is how a reversal beside it is seen) carry `reversed_at IS NULL`. There is no pre-check read.
// Rows the UPDATE did not move are then read, only to say why: a reversed one refuses; a payment that is
// already DEPOSITED (or not there) is the no-op it has always been.
func (r *Repository) MarkDeposited(ctx context.Context, paymentIDs []string, statementLineID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if len(paymentIDs) == 0 {
		return nil
	}
	// One transaction: the named payment rows first, ascending (LOCK-ORDER.md), then the UPDATE, which takes
	// the payments counter on its first row and must not hold it while it waits for another row a reversal
	// holds.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("till: mark deposited: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := payments.LockPaymentRowsTx(ctx, tx, paymentIDs); err != nil {
		return fmt.Errorf("till: mark deposited: %w", err)
	}
	rows, err := tx.Query(ctx,
		`UPDATE payments
		 SET status = 'DEPOSITED',
		     metadata = metadata || jsonb_build_object('bank_statement_line_id', $2::text),
		     updated_at = now()
		 WHERE id = ANY($1::uuid[]) AND status = 'RECEIVED' AND reversed_at IS NULL
		 RETURNING id::text`,
		paymentIDs, statementLineID)
	if err != nil {
		return fmt.Errorf("till: mark deposited: %w", err)
	}
	moved := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("till: mark deposited: scan: %w", err)
		}
		moved[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("till: mark deposited: %w", err)
	}
	if len(moved) < len(paymentIDs) {
		var reversed []string
		rrows, err := tx.Query(ctx,
			`SELECT id::text FROM payments WHERE id = ANY($1::uuid[]) AND reversed_at IS NOT NULL ORDER BY id`, paymentIDs)
		if err != nil {
			return fmt.Errorf("till: mark deposited: read the unmoved: %w", err)
		}
		for rrows.Next() {
			var id string
			if err := rrows.Scan(&id); err != nil {
				rrows.Close()
				return fmt.Errorf("till: mark deposited: scan the unmoved: %w", err)
			}
			if !moved[id] {
				reversed = append(reversed, id)
			}
		}
		rrows.Close()
		if err := rrows.Err(); err != nil {
			return fmt.Errorf("till: mark deposited: %w", err)
		}
		if len(reversed) > 0 {
			return fmt.Errorf("%w: %s", ErrPaymentReversed, strings.Join(reversed, ", "))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("till: mark deposited: commit: %w", err)
	}
	return nil
}

// OpenLodgements returns the cash/cheque banking movements (till_movements of type LODGEMENT) in [from, to),
// branch-stamped via their session. "Open" means eligible for matching; the A-02 matcher dedupes against its
// own live bank_matches before booking a deposit journal.
func (r *Repository) OpenLodgements(ctx context.Context, from, to time.Time) ([]bankrec.Lodgement, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT m.id::text, s.branch_id::text, m.amount_cents, m.reference, m.created_at
		 FROM till_movements m
		 JOIN till_sessions s ON s.id = m.till_session_id
		 WHERE m.movement_type = 'LODGEMENT' AND m.created_at >= $1 AND m.created_at < $2
		 ORDER BY m.created_at`, from, to)
	if err != nil {
		return nil, fmt.Errorf("till: open lodgements: %w", err)
	}
	defer rows.Close()
	var out []bankrec.Lodgement
	for rows.Next() {
		var l bankrec.Lodgement
		if err := rows.Scan(&l.MovementID, &l.BranchID, &l.AmountCents, &l.Reference, &l.OccurredAt); err != nil {
			return nil, fmt.Errorf("till: scan lodgement: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PaymentForGL resolves the finance facts of a payment so the GL posting consumer can branch-stamp the
// journal and choose the 1010 (card) / 1020 (cash/cheque undeposited) / 1000 (bank) debit leg. The
// payment.received outbox payload carries no branch/invoice, so this SELECT is the source of truth.
func (r *Repository) PaymentForGL(ctx context.Context, paymentID string) (gl.PaymentFacts, error) {
	if r.pool == nil {
		return gl.PaymentFacts{}, errNoDB
	}
	var f gl.PaymentFacts
	err := r.pool.QueryRow(ctx,
		`SELECT branch_id::text, COALESCE(customer_id::text, ''), method, status, amount_cents
		 FROM payments WHERE id = $1`, paymentID,
	).Scan(&f.BranchID, &f.CustomerID, &f.Method, &f.Status, &f.AmountCents)
	if err != nil {
		return gl.PaymentFacts{}, fmt.Errorf("till: payment for gl %q: %w", paymentID, err)
	}
	return f, nil
}
