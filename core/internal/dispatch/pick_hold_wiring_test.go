package dispatch

// pick_hold_wiring_test.go, item 9 review P3-6: the will call doors' pick
// hold gate reports whether it is wired, the boot fail-fast's fact (the
// order module's PickHoldWired posture). Deleting the composition root's
// WithPickHoldGate line must be loud, not a silent open door.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakePickHoldGate struct {
	held bool
	owed int64
	err  error
}

func (f fakePickHoldGate) HeldFromPicking(context.Context, string) (bool, int64, error) {
	return f.held, f.owed, f.err
}

// HeldFromPickingTx answers the same canned facts on the transaction half
// (the fake never touches the tx).
func (f fakePickHoldGate) HeldFromPickingTx(context.Context, pgx.Tx, string) (bool, int64, error) {
	return f.held, f.owed, f.err
}

func TestPickHoldGateWired(t *testing.T) {
	svc := NewService(nil)
	if svc.PickHoldGateWired() {
		t.Fatal("a bare service reports its pick hold gate wired")
	}
	if svc.WithPickHoldGate(fakePickHoldGate{}).PickHoldGateWired() != true {
		t.Fatal("a wired gate reports itself unwired")
	}
}
