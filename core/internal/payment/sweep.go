package payment

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// The card attempt sweep (the card charge design section 4): the body behind the jobs runner's
// payment.card_attempt_sweep registration. The crash windows are the cases where no sale
// transaction committed, so no outbox event exists and only a sweep can close them. Every action
// is a guarded transition on one row (attempts.go), so overlapping runs, or a second worker,
// cannot act twice.

// AttemptGrace is the sweep's grace window: how long an in-flight attempt (CHARGING, CAPTURED or
// VOIDING) sits past its last transition before the sweep owns it. Its invariant, enforced by
// TestAttemptGraceExceedsRingDeadline, is that it is strictly greater than the ring's detached
// deadline, dispatch + sale transaction + compensation together, the longest path a live request
// can hold an attempt: a request still inside its ring is never past grace, so the sweep can
// never race it and a late bind's guarded move is the one that loses.
const AttemptGrace = 5 * time.Minute

// AlertFunc is the card attempt sweep's escalation seam (the card charge design section 4): the
// sweep calls it once per operator-item state it moves a row to (UNKNOWN, VOID_UNKNOWN or
// VOID_DECLINED, never VOIDED and never an in-flight status), with the event key
// card-attempt:<id>:<status> and a body naming the money facts a person reconciling a settlement
// report needs: the amount, the branch, the till session, the time, the sale key and the charge
// ref. Never a token, a credential, a PAN or a customer name. cmd/core binds it to messaging's
// escalations channel once at process start, the warranty sweep's precedent; the default is the
// no-op below, so an unwired seam keeps the sweep's guarded moves and drops only the visibility
// post, and a test pins the posts precisely by swapping the var. The error answer is the
// adapter's: the sweep logs a failed post and moves on, the row is the record.
var AlertFunc func(ctx context.Context, key, body string) error = func(context.Context, string, string) error { return nil }

// SweepResult is one pass's report: what the pass found, what it moved, and what it left for the
// next pass or a person. The jobs registration maps it onto the run's row.
type SweepResult struct {
	Examined       int // the in-flight rows past grace the pass looked at
	ToUnknown      int // CHARGING to UNKNOWN, no processor call
	Voided         int // CAPTURED the sweep reversed clean (the void, or the refund fallback)
	ToVoidUnknown  int // a reversal whose answer is lost, or a stale VOIDING closed: an operator item
	ToVoidDeclined int // a reversal the processor refused: an operator item
	Lost           int // rows another writer moved first (the bind won the race, a manager closed)
}

// sweepListLimit bounds one pass's work: a pass that finds more rows than this leaves the rest
// for the next claim, the schedule's cadence is a minute, and one pass's rows each take one
// guarded statement.
const sweepListLimit = 200

// SweepCardAttempts is the jobs runner's payment.card_attempt_sweep body (the card charge design
// section 4's crash windows). It walks the in-flight attempts whose last transition is past
// grace and closes each window: a CHARGING row (died between the claim and the stamp) to UNKNOWN
// with no processor call, a CAPTURED unbound row (died before the sale committed) through the
// same reversal the in-request compensator runs, a VOIDING row (died mid compensation) to
// VOID_UNKNOWN, never re-voiding blindly. Every move is the row's own guarded transition, so
// overlapping runs cannot act twice, and a live request's row is never past grace, so the sweep
// never races one: a bind that arrives after the sweep took the row loses its guarded move and
// the sale's transaction fails, by design. now is the clock, injected: production passes
// time.Now, a test passes a controllable one.
func (s *Service) SweepCardAttempts(ctx context.Context, now func() time.Time) (SweepResult, error) {
	if s.attempts == nil {
		return SweepResult{}, errAttemptsNoDB
	}
	if now == nil {
		now = time.Now
	}
	rows, err := s.attempts.listPastGrace(ctx, now().Add(-AttemptGrace), sweepListLimit)
	if err != nil {
		return SweepResult{}, err
	}
	res := SweepResult{Examined: len(rows)}
	for _, a := range rows {
		switch a.Status {
		case AttemptCharging:
			row, moved, err := s.attempts.StampUnknown(ctx, a.ID,
				"the sweep moved the attempt: the charge outlived the ring's detached deadline with no answer recorded")
			if err != nil {
				slog.Warn("payment: the sweep could not move a CHARGING attempt to UNKNOWN", "attempt", a.ID, "err", err)
				continue
			}
			if !moved {
				res.Lost++ // another writer moved it first; the guard is the arbiter
				continue
			}
			res.ToUnknown++
			s.postAttemptAlert(ctx, row, now())
		case AttemptCaptured:
			row, moved, err := s.attempts.BeginVoiding(ctx, a.ID)
			if err != nil {
				slog.Warn("payment: the sweep could not claim a CAPTURED attempt for reversal", "attempt", a.ID, "err", err)
				continue
			}
			if !moved {
				res.Lost++ // the bind won the race (the row is BOUND), or a manager closed it
				continue
			}
			// The reversal is the compensator's own, refund fallback included, on a context
			// detached from the runner's shutdown and bounded like every reversal.
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CompensationDeadline)
			cres, rerr := s.reverse(rctx, row, true)
			cancel()
			if rerr != nil {
				// The reversal's outcome could not be recorded: the row sits VOIDING for the next
				// pass, which closes it to VOID_UNKNOWN for a person. Logged, never fatal.
				slog.Warn("payment: the sweep's reversal of a captured attempt did not finish clean",
					"attempt", a.ID, "charge_ref", a.ProcessorRef, "err", rerr)
			}
			switch cres.Kind {
			case CompensateKindVoided:
				res.Voided++ // no escalation: the money is back
			case CompensateKindNeedsManager:
				switch cres.Attempt.Status {
				case AttemptVoidUnknown:
					res.ToVoidUnknown++
					s.postAttemptAlert(ctx, cres.Attempt, now())
				case AttemptVoidDeclined:
					res.ToVoidDeclined++
					s.postAttemptAlert(ctx, cres.Attempt, now())
				}
			}
		case AttemptVoiding:
			// The process died mid compensation. The reversal is never retried blindly (the
			// double reversal ruling): the row goes to a person.
			row, moved, err := s.attempts.StampVoidUnknown(ctx, a.ID,
				"the sweep moved the attempt: the reversal outlived the ring's detached deadline with no answer recorded; it is not retried blindly")
			if err != nil {
				slog.Warn("payment: the sweep could not close a stale VOIDING attempt", "attempt", a.ID, "err", err)
				continue
			}
			if !moved {
				res.Lost++
				continue
			}
			res.ToVoidUnknown++
			s.postAttemptAlert(ctx, row, now())
		}
	}
	return res, nil
}

// listPastGrace reads the in-flight attempts the sweep owns: the rows whose status is one of the
// machine's in-flight statuses and whose last transition is older than the cutoff. The partial
// index idx_cca_sweep carries exactly this scan, which is what keeps a pass between the
// schedule's claims cheap.
func (s *Store) listPastGrace(ctx context.Context, cutoff time.Time, limit int) ([]Attempt, error) {
	if s.pool == nil {
		return nil, errAttemptsNoDB
	}
	rows, err := s.pool.Query(ctx, `SELECT `+attemptColumns+` FROM card_charge_attempts
		 WHERE status IN ('CHARGING','CAPTURED','VOIDING') AND updated_at < $1::timestamptz
		 ORDER BY updated_at LIMIT `+strconv.Itoa(limit), cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// postAttemptAlert posts one escalation for the operator-item state the sweep just moved a row
// to (UNKNOWN, VOID_UNKNOWN, VOID_DECLINED; never VOIDED and never an in-flight status). The
// body names the money facts a person reconciling a settlement report needs, the amount, the
// branch, the till session, the time, the sale key and, once captured, the charge ref, and never
// a token, a credential, a PAN or a customer name. A failed post is logged, never fatal: the
// attempt row is the system of record, the post is visibility.
func (s *Service) postAttemptAlert(ctx context.Context, a Attempt, at time.Time) {
	key := "card-attempt:" + a.ID + ":" + a.Status
	body := fmt.Sprintf("card charge attempt %s is %s at %s: amount %d cents, branch %s, till session %s, sale key %s",
		sweepShortRef(a.ID), a.Status, at.UTC().Format(time.RFC3339), a.AmountCents,
		sweepShortRef(a.BranchID), sweepShortRef(a.TillSessionID), sweepShortRef(a.SaleKey))
	if a.ProcessorRef != "" {
		body += ", charge ref " + a.ProcessorRef
	}
	if err := AlertFunc(ctx, key, body); err != nil {
		slog.Warn("payment: the sweep's escalation post failed; the attempt row is the record",
			"attempt", a.ID, "status", a.Status, "err", err)
	}
}

// sweepShortRef names an id the way a person reads it: the head of the uuid.
func sweepShortRef(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
