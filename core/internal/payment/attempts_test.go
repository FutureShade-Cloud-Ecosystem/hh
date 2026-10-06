package payment

import (
	"testing"
	"time"
)

// ringSaleTxAllowance is the test's named share of the ring's detached deadline for the SALE
// TRANSACTION. The dispatch (CaptureDispatchDeadline) and the compensation (CompensationDeadline)
// are the code's own detached bounds; the sale transaction between them rides the live request
// and carries no code-set deadline, so the invariant names its allowance here: the widest ring a
// counter's client is expected to wait out once the capture answered, orders of magnitude above
// any transaction the till's statements take, and the number the grace's remaining slack is
// measured against.
const ringSaleTxAllowance = 120 * time.Second

// TestAttemptGraceExceedsRingDeadline is the grace window's invariant (the card charge design
// section 4): AttemptGrace is strictly greater than the ring's detached deadline, dispatch + sale
// transaction + compensation together, the longest path a live request can hold an attempt. The
// sweep reads the deadline off the row's last transition, so a request still inside its ring is
// never past grace and the sweep can never race it: a late bind meets the row the sweep took only
// after the whole ring had to be over already, and its guarded move loses cleanly instead.
func TestAttemptGraceExceedsRingDeadline(t *testing.T) {
	ring := CaptureDispatchDeadline + ringSaleTxAllowance + CompensationDeadline
	if AttemptGrace <= ring {
		t.Fatalf("AttemptGrace = %s, want strictly greater than the ring's detached deadline (%s dispatch + %s sale transaction + %s compensation = %s): the sweep could race a live request still inside its ring",
			AttemptGrace, CaptureDispatchDeadline, ringSaleTxAllowance, CompensationDeadline, ring)
	}
	if AttemptGrace != 5*time.Minute {
		t.Fatalf("AttemptGrace = %s, want the pinned 5m: moving the constant moves the sweep's race bound against every live ring and is a deliberate change, not a drive by", AttemptGrace)
	}
}
