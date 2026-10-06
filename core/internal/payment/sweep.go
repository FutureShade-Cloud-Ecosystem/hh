package payment

import (
	"context"
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

// SweepCardAttempts is the jobs runner's payment.card_attempt_sweep body (the card charge design
// section 4's crash windows). now is the clock, injected: production passes time.Now, a test
// passes a controllable one.
func (s *Service) SweepCardAttempts(ctx context.Context, now func() time.Time) (SweepResult, error) {
	return SweepResult{}, nil
}
