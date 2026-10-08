package order

// pick_hold_group_test.go, item 9 F review N4: the shipment group's moves
// carry the pay before release hold on every goods releasing rung (PICKING,
// READY, DISPATCHED), and the hold is read INSIDE the group write's
// transaction under the order row lock, like the order ladder's own writers.

import (
	"errors"
	"testing"
)

// A held order's group cannot be readied or dispatched, whatever rung it
// reached before the order owed again.
func TestTransitionGroup_ReadyAndDispatchedRefusedWhileHeld(t *testing.T) {
	for _, tc := range []struct{ from, target string }{
		{GroupPicking, GroupReady},
		{GroupReady, GroupDispatched},
	} {
		store := pickHoldShipGroup()
		store.group.Status = tc.from
		svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{facts: heldFacts(1)})
		if _, err := svc.TransitionGroup(pickHoldCtx(), store.group.ID, tc.target); !errors.Is(err, ErrHeldFromPicking) {
			t.Fatalf("%s to %s on a held order: want ErrHeldFromPicking, got %v", tc.from, tc.target, err)
		}
		if store.movedTo != "" || store.group.Status != tc.from {
			t.Fatalf("a held order's group moved %s to %q (status %s)", tc.from, store.movedTo, store.group.Status)
		}
	}
}

// A clear order's group walks the same rungs.
func TestTransitionGroup_ReadyAndDispatchedAllowedWhenClear(t *testing.T) {
	for _, tc := range []struct{ from, target string }{
		{GroupPicking, GroupReady},
		{GroupReady, GroupDispatched},
	} {
		store := pickHoldShipGroup()
		store.group.Status = tc.from
		svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{facts: PickHoldFacts{}})
		if _, err := svc.TransitionGroup(pickHoldCtx(), store.group.ID, tc.target); err != nil {
			t.Fatalf("%s to %s on a clear order: %v", tc.from, tc.target, err)
		}
		if store.movedTo != tc.target {
			t.Fatalf("the clear group moved to %q, want %s", store.movedTo, tc.target)
		}
	}
}

// COMPLETE records a delivery that already happened, and CANCELLED releases
// nothing: neither reads the hold.
func TestTransitionGroup_CompleteAndCancelledDoNotReadTheHold(t *testing.T) {
	for _, tc := range []struct{ from, target string }{
		{GroupDispatched, GroupComplete},
		{GroupPlanned, GroupCancelled},
	} {
		reader := &txRecordingPickHold{facts: heldFacts(1)}
		store := pickHoldShipGroup()
		store.group.Status = tc.from
		svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(reader)
		if _, err := svc.TransitionGroup(pickHoldCtx(), store.group.ID, tc.target); err != nil {
			t.Fatalf("%s to %s on a held order: %v", tc.from, tc.target, err)
		}
		if reader.txReads != 0 || reader.poolReads != 0 {
			t.Fatalf("%s read the hold (%d tx, %d pool)", tc.target, reader.txReads, reader.poolReads)
		}
	}
}

// The guarded rungs read the hold through the Tx half, inside the group
// write's transaction, never the pool beforehand.
func TestTransitionGroup_ReadsTheHoldInsideTheWrite(t *testing.T) {
	for _, tc := range []struct{ from, target string }{
		{GroupPlanned, GroupPicking},
		{GroupPicking, GroupReady},
		{GroupReady, GroupDispatched},
	} {
		reader := &txRecordingPickHold{facts: PickHoldFacts{}}
		store := pickHoldShipGroup()
		store.group.Status = tc.from
		svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(reader)
		if _, err := svc.TransitionGroup(pickHoldCtx(), store.group.ID, tc.target); err != nil {
			t.Fatalf("%s to %s: %v", tc.from, tc.target, err)
		}
		if reader.txReads != 1 || reader.poolReads != 0 {
			t.Fatalf("%s read the hold %d times on the tx and %d on the pool, want 1 and 0", tc.target, reader.txReads, reader.poolReads)
		}
	}
}
