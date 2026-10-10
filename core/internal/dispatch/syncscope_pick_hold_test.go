package dispatch

// syncscope_pick_hold_test.go, item 9 Opus P1-2: the two SYNC will call doors
// carry the pick hold. The staff mint and the public confirm were guarded
// (willcall_pick_hold_test.go); the board app's rack tag mint over sync
// (WillCallSyncStore.Create) and the contractor's offline confirm
// (PickupsSyncStore.Write) were not, so a held order could be tagged and
// collected with nothing paid.
//
// The doors' own bodies read the org fence and the pickup row through the
// stores' concrete pool, so a unit leg cannot drive Create or Write whole;
// these legs drive the guards the doors call (refuseHeldFromPickingSync and
// refuseHeldFromPickingSyncTx) over a fake gate, the three behaviors the
// staff doors' legs prove, plus each store's wired boot fact. The composition
// root's fail-fast keeps the wired gate from going dark in a deploy.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// The sync mint's guard refuses a held order with the mandated sentence as
// the sync engine's refused reason. The sync door resolves any active staff
// caller with no role check (the will_call scope's predicate reads org
// only), so the refusal carries the words and never the owed figure: a
// figure never reaches an off list role in words either (ruling 91). The
// STAFF mint's refusal (willcall_pick_hold_test.go) keeps the figure: its
// surface is dispatch-board work, held only by roles on the cost list.
func TestSyncWillCallMint_HeldOrderRefusesAsSyncRefusal(t *testing.T) {
	err := refuseHeldFromPickingSync(context.Background(), fakePickHoldGate{held: true, owed: 8800}, refOrderID)
	if err == nil {
		t.Fatal("a held order passed the sync mint's hold guard")
	}
	if !errors.Is(err, sync.ErrOpRefused) {
		t.Fatalf("the refusal is the sync engine's refused reason: %v", err)
	}
	if !strings.Contains(err.Error(), pickHoldWords) {
		t.Fatalf("the refusal's words: %v", err)
	}
	for _, leak := range []string{"is owed", "$", "88"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("the sync refusal names no figure to its any-staff caller (%q): %v", leak, err)
		}
	}
}

// A hold read that cannot be made refuses the sync mint (money fails closed).
func TestSyncWillCallMint_UnreadableHoldRefuses(t *testing.T) {
	err := refuseHeldFromPickingSync(context.Background(), fakePickHoldGate{err: context.DeadlineExceeded}, refOrderID)
	if err == nil || !errors.Is(err, sync.ErrOpRefused) || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("an unreadable hold must refuse the sync mint: %v", err)
	}
}

// A nil gate allows: the additive lane, the boot fail-fast's counterpart.
func TestSyncWillCallMint_NilGateAllows(t *testing.T) {
	if err := refuseHeldFromPickingSync(context.Background(), nil, refOrderID); err != nil {
		t.Fatalf("a nil gate refused the sync mint: %v", err)
	}
}

// The offline confirm's transactional guard refuses a held order the same
// way, on the transaction half of the gate: the words, never the figure
// (the confirm rides the same any-staff sync door as the mint).
func TestSyncPickupConfirm_HeldOrderRefusesAsSyncRefusal(t *testing.T) {
	err := refuseHeldFromPickingSyncTx(context.Background(), fakePickHoldGate{held: true, owed: 8800}, nil, refOrderID)
	if err == nil {
		t.Fatal("a held order passed the sync confirm's hold guard")
	}
	if !errors.Is(err, sync.ErrOpRefused) || !strings.Contains(err.Error(), pickHoldWords) {
		t.Fatalf("the refusal is the sync engine's refused reason with the words: %v", err)
	}
	for _, leak := range []string{"is owed", "$", "88"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("the sync confirm's refusal names no figure (%q): %v", leak, err)
		}
	}
}

func TestSyncPickupConfirm_UnreadableHoldRefuses(t *testing.T) {
	err := refuseHeldFromPickingSyncTx(context.Background(), fakePickHoldGate{err: context.DeadlineExceeded}, nil, refOrderID)
	if err == nil || !errors.Is(err, sync.ErrOpRefused) || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("an unreadable hold must refuse the sync confirm: %v", err)
	}
}

func TestSyncPickupConfirm_NilGateAllows(t *testing.T) {
	if err := refuseHeldFromPickingSyncTx(context.Background(), nil, nil, refOrderID); err != nil {
		t.Fatalf("a nil gate refused the sync confirm: %v", err)
	}
}

// Both sync stores report their gate wired exactly when one was taken (the
// composition root's boot assertion's fact).
func TestSyncStores_PickHoldGateWired(t *testing.T) {
	board := NewWillCallSyncStore(nil, nil, nil, fakePickHoldGate{}, nil)
	if !board.PickHoldGateWired() {
		t.Fatal("a wired will call sync store reports its gate unwired")
	}
	if NewWillCallSyncStore(nil, nil, nil, nil, nil).PickHoldGateWired() {
		t.Fatal("a bare will call sync store reports its gate wired")
	}
	pickups := NewPickupsSyncStore(nil, nil, nil, fakePickHoldGate{}, nil)
	if !pickups.PickHoldGateWired() {
		t.Fatal("a wired pickups sync store reports its gate unwired")
	}
	if NewPickupsSyncStore(nil, nil, nil, nil, nil).PickHoldGateWired() {
		t.Fatal("a bare pickups sync store reports its gate wired")
	}
}
