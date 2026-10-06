package payment

import "time"

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
