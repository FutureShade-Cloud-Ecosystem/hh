//go:build integration

// pick_hold_group_integration_test.go, item 9 F review N4 against the real
// repository: the group write reads the hold inside its own transaction under
// the order row lock, a refusal rolls the move back (the row stays on its
// rung with no stamp), and a clear hold lets the same move through.
package order

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestIntegration_PickHold_GroupMovesReadTheHoldInsideTheWrite(t *testing.T) {
	pool := intPool(t)
	_, ord := hh182SeedOrderAtBranch(t, pool, kingstonBranch)
	gid := w5aPlanWholeOrder(t, pool, ord)
	ctx := identity.ContextWithStaff(context.Background(), hh182StaffAt(kingstonBranch))

	clear := &txRecordingPickHold{facts: PickHoldFacts{}}
	svc := w5aService(t, pool).WithPickHoldReader(clear)
	for _, step := range []string{GroupPicking, GroupReady} {
		if _, err := svc.TransitionGroup(ctx, gid, step); err != nil {
			t.Fatalf("a clear order's group to %s: %v", step, err)
		}
	}
	if clear.txReads != 2 || clear.poolReads != 0 {
		t.Fatalf("the two moves read the hold %d times on the tx and %d on the pool, want 2 and 0", clear.txReads, clear.poolReads)
	}

	// The order owes again: DISPATCHED is refused and the row stays READY, unstamped.
	held := w5aService(t, pool).WithPickHoldReader(&txRecordingPickHold{facts: heldFacts(1)})
	if _, err := held.TransitionGroup(ctx, gid, GroupDispatched); !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("a held order's group dispatch: want ErrHeldFromPicking, got %v", err)
	}
	if status, dispatched, _ := hh182GroupState(t, pool, gid); status != GroupReady || dispatched {
		t.Fatalf("after the refusal the group is %s (dispatched=%v), want READY with no stamp", status, dispatched)
	}

	if _, err := svc.TransitionGroup(ctx, gid, GroupDispatched); err != nil {
		t.Fatalf("once clear the same dispatch goes through: %v", err)
	}
	if status, dispatched, _ := hh182GroupState(t, pool, gid); status != GroupDispatched || !dispatched {
		t.Fatalf("the group is %s (dispatched=%v), want DISPATCHED stamped", status, dispatched)
	}
}
