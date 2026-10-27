package till

// recon_seam.go, FROZEN scaffold seam. The till is the SOLE owner of the payments + till_movements tables,
// so the W5 bank-reconciliation (A-02) and GL (A-01) read seams live HERE (a sanctioned cross-module seam,
// not a bankrec/gl reach into till's tables). These methods on *till.Repository satisfy:
//   - bankrec.PaymentSource   (UnreconciledCardPayments + MarkDeposited)
//   - bankrec.LodgementSource (OpenLodgements + LodgementFacts)
//   - gl.PaymentReader        (PaymentForGL, SELECT the finance facts of a payment)
// Signatures are frozen; the A-02/A-01 workers rely on them exactly as declared.
//
// The seam also answers whether a lodgement carries its OWN journal (carriesJournalSQL), the fact the
// bank match needs (FROM-PRINCIPAL 117 and 120): a lodgement that journaled at lodgement is only confirmed
// by its match, and a lodgement from before this change keeps today's match.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/jackc/pgx/v5"
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
// A reversed CARD is the exception, the same predicate as bankrec's claim (SF-2d round 4 and ruling 55.4): the
// reversal moved no money at the bank, so the processor's settlement of the card still arrives and the card is
// claimable. Every other reversed tender is refused.
//
// The refusal is decided by the UPDATE itself: its row lock and its WHERE (re-checked after a concurrent
// commit, which is how a reversal beside it is seen) carry `reversed_at IS NULL OR method = 'CARD'`. There is no pre-check read.
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
		 WHERE id = ANY($1::uuid[]) AND status = 'RECEIVED' AND (reversed_at IS NULL OR method = 'CARD')
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
			`SELECT id::text FROM payments WHERE id = ANY($1::uuid[]) AND reversed_at IS NOT NULL AND method <> 'CARD' ORDER BY id`, paymentIDs)
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
			numbers, nerr := payments.PaymentNumbersTx(ctx, tx, reversed)
			if nerr != nil {
				return fmt.Errorf("till: mark deposited: read the payment numbers: %w", nerr)
			}
			labels := make([]string, 0, len(reversed))
			for _, id := range reversed {
				labels = append(labels, payments.PaymentLabel(numbers, id))
			}
			return fmt.Errorf("%w: %s", ErrPaymentReversed, strings.Join(labels, ", "))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("till: mark deposited: commit: %w", err)
	}
	return nil
}

// carriesJournalSQL says whether a lodgement carries its OWN journal, the fact the bank match needs
// (FROM-PRINCIPAL 117 and 120): the till posted DR 1000 / CR 1020 AT LODGEMENT, so its bank match may
// only confirm the line and post nothing, or the deposit is booked twice.
//
// A lodgement carries its journal when the till has emitted it, and the till emits it in the movement's
// own transaction: the deposit slip's link rows (the marker the ruling names) or the movement-posted
// emission. The emission covers the lodgement recorded without a slip, which carries its journal just
// the same, and reading the emission rather than the posted journal closes the race: the journal is
// still on its way when the match happens, and the match must not book ahead of it. A lodgement from
// before this change has neither, so it keeps today's match.
const carriesJournalSQL = `(
    EXISTS (SELECT 1 FROM till_lodgement_payments p WHERE p.till_movement_id = m.id)
 OR EXISTS (SELECT 1 FROM events_outbox o WHERE o.topic = 'till.movement.posted' AND o.payload->>'movement_id' = m.id::text)
)`

// OpenLodgements returns the cash/cheque banking movements (till_movements of type LODGEMENT) in [from, to),
// branch-stamped via their session. "Open" means eligible for matching; the A-02 matcher dedupes against its
// own live bank_matches before booking a deposit journal. Each lodgement carries whether it has a journal of
// its own (carriesJournalSQL), so the matcher knows which match only confirms.
func (r *Repository) OpenLodgements(ctx context.Context, from, to time.Time) ([]bankrec.Lodgement, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT m.id::text, s.branch_id::text, m.till_session_id::text, m.method, m.amount_cents, m.reference, m.created_at, `+carriesJournalSQL+`
		 FROM till_movements m
		 JOIN till_sessions s ON s.id = m.till_session_id
		 WHERE m.movement_type = 'LODGEMENT' AND m.created_at >= $1 AND m.created_at < $2
		 ORDER BY m.created_at`, from, to)
	if err != nil {
		return nil, fmt.Errorf("till: open lodgements: %w", err)
	}
	type rawLodgement struct {
		bankrec.Lodgement
		sessionID string
		method    string
	}
	var raw []rawLodgement
	for rows.Next() {
		var rl rawLodgement
		if err := rows.Scan(&rl.MovementID, &rl.BranchID, &rl.sessionID, &rl.method, &rl.AmountCents, &rl.Reference, &rl.OccurredAt, &rl.CarriesJournal); err != nil {
			rows.Close()
			return nil, fmt.Errorf("till: scan lodgement: %w", err)
		}
		raw = append(raw, rl)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	used := map[string]bool{}
	out := make([]bankrec.Lodgement, 0, len(raw))
	for _, rl := range raw {
		if !rl.CarriesJournal {
			pids, unresolved, perr := r.lodgementPaymentIDs(ctx, rl.BranchID, rl.sessionID, rl.method, rl.AmountCents, used)
			if perr != nil {
				return nil, perr
			}
			rl.PaymentIDs = pids
			rl.PaymentsUnresolved = unresolved
		}
		out = append(out, rl.Lodgement)
	}
	return out, nil
}

// LodgementFacts answers the branch, the amount, the covered RECEIVED payment ids, and whether it carries
// its own journal (carriesJournalSQL) of one lodgement (a till_movements row of type LODGEMENT), found false
// when the id is no lodgement or not a uuid. A movement row is never updated or deleted, its branch is its
// session's and its amount its own, so the facts are immutable.
func (r *Repository) LodgementFacts(ctx context.Context, movementID string) (bankrec.LodgementFact, bool, error) {
	if r.pool == nil {
		return bankrec.LodgementFact{}, false, errNoDB
	}
	var f bankrec.LodgementFact
	var sessionID, method string
	err := r.pool.QueryRow(ctx,
		`SELECT s.branch_id::text, m.till_session_id::text, m.method, m.amount_cents, `+carriesJournalSQL+`
		   FROM till_movements m JOIN till_sessions s ON s.id = m.till_session_id
		  WHERE m.id::text = $1 AND m.movement_type = 'LODGEMENT'`, movementID).
		Scan(&f.BranchID, &sessionID, &method, &f.AmountCents, &f.CarriesJournal)
	if errors.Is(err, pgx.ErrNoRows) {
		return bankrec.LodgementFact{}, false, nil
	}
	if err != nil {
		return bankrec.LodgementFact{}, false, fmt.Errorf("till: lodgement facts: %w", err)
	}
	if !f.CarriesJournal {
		pids, unresolved, perr := r.lodgementPaymentIDs(ctx, f.BranchID, sessionID, method, f.AmountCents, nil)
		if perr != nil {
			return bankrec.LodgementFact{}, false, perr
		}
		f.PaymentIDs = pids
		f.PaymentsUnresolved = unresolved
	}
	return f, true, nil
}

// lodgementPaymentIDs resolves the RECEIVED non-card, non-reversed payment row(s) covered by a lodgement
// movement so bankrec's PersistMatchReach flips them to DEPOSITED when the lodgement clears at the bank.
// Ruling 128 (P1-2): claim only payments from the lodgement's own till_session_id and method, and only
// when exactly one subset of those payments sums to amountCents; never claim a partial set, another
// session's payment, or another method's payment, and report unresolved when no unique set matches.
//
// Ruling 141 (round 3): only CHEQUE lodgements are resolved. A cash lodgement claims nothing and is
// not called unresolved, because nothing downstream acts on its resolution: cash does not bounce, and
// a DEPOSITED flag only changes which account a bounce credits. A unique-set search over every cash
// taking of a busy session is also the exponential walk R2-P1-2 removed.
//
// maxLodgementCandidates caps the unique-set search at 20 candidates (about a millisecond at the
// cap). More than that is marked unresolved rather than searched: 2^n subsets over a session's
// takings never finishes in the request that asks for it.
const maxLodgementCandidates = 20

func (r *Repository) lodgementPaymentIDs(ctx context.Context, branchID, sessionID, method string, amountCents int64, used map[string]bool) ([]string, bool, error) {
	if amountCents <= 0 {
		return nil, false, nil
	}
	if branchID == "" || sessionID == "" || method == "" {
		return nil, true, nil
	}
	if method != "CHEQUE" {
		return nil, false, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT p.id::text, p.amount_cents
		   FROM payments p
		  WHERE p.branch_id = $1::uuid
		    AND p.till_session_id = $2::uuid
		    AND p.method = $3
		    AND p.status = 'RECEIVED'
		    AND p.reversed_at IS NULL
		    AND p.method <> 'CARD'
		  ORDER BY p.received_at ASC, p.id ASC`,
		branchID, sessionID, method)
	if err != nil {
		return nil, false, fmt.Errorf("till: lodgement payments: %w", err)
	}
	defer rows.Close()
	type cand struct {
		id  string
		amt int64
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.amt); err != nil {
			return nil, false, fmt.Errorf("till: scan lodgement payment: %w", err)
		}
		if used != nil && used[c.id] {
			continue
		}
		if c.amt <= 0 || c.amt > amountCents {
			continue
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(cands) > maxLodgementCandidates {
		return nil, true, nil
	}

	suffixSum := make([]int64, len(cands)+1)
	for i := len(cands) - 1; i >= 0; i-- {
		suffixSum[i] = suffixSum[i+1] + cands[i].amt
	}
	var uniqueMatch []string
	var cur []string
	matches := 0
	var dfs func(idx int, rem int64)
	dfs = func(idx int, rem int64) {
		if matches > 1 {
			return
		}
		if rem == 0 {
			matches++
			if matches == 1 {
				uniqueMatch = append([]string(nil), cur...)
			}
			return
		}
		if idx >= len(cands) || suffixSum[idx] < rem {
			return
		}
		if cands[idx].amt <= rem {
			cur = append(cur, cands[idx].id)
			dfs(idx+1, rem-cands[idx].amt)
			cur = cur[:len(cur)-1]
			if matches > 1 {
				return
			}
		}
		dfs(idx+1, rem)
	}
	dfs(0, amountCents)
	if matches != 1 {
		return nil, true, nil
	}
	if used != nil {
		for _, id := range uniqueMatch {
			used[id] = true
		}
	}
	return uniqueMatch, false, nil
}

// PaymentForGL resolves the finance facts of a payment so the GL posting consumer can branch-stamp the
// journal and choose the 1010 (card) / 1020 (cash/cheque undeposited) / 1000 (bank) debit leg. The
// payment.received outbox payload carries no branch/invoice, so this SELECT is the source of truth.
func (r *Repository) PaymentForGL(ctx context.Context, paymentID string) (gl.PaymentFacts, error) {
	if r.pool == nil {
		return gl.PaymentFacts{}, errNoDB
	}
	var f gl.PaymentFacts
	var banked bool
	err := r.pool.QueryRow(ctx,
		`SELECT branch_id::text, COALESCE(customer_id::text, ''), method, status, amount_cents,
		        COALESCE(metadata->>'bank_statement_line_id', '') <> ''
		 FROM payments WHERE id = $1`, paymentID,
	).Scan(&f.BranchID, &f.CustomerID, &f.Method, &f.Status, &f.AmountCents, &banked)
	if err != nil {
		return gl.PaymentFacts{}, fmt.Errorf("till: payment for gl %q: %w", paymentID, err)
	}
	if f.Status == "BOUNCED" && banked {
		f.Status = "DEPOSITED"
	}
	return f, nil
}
