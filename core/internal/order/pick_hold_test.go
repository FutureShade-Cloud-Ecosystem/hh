package order

// pick_hold_test.go, item 9.3 (the row 1047 deposits work): an order that owes
// money under a pay before release shape cannot reach picking, at any of the
// paths that release goods. These are the unit legs over a fake facts reader:
// the predicate itself (read from the money, never a stamp) is proven against
// the real database in pick_hold_integration_test.go.

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// pickHoldCtx stamps the caller the guard reads (history_test.go's key).
func pickHoldCtx() context.Context {
	return context.WithValue(context.Background(), ctxKeyOrder{},
		identity.StaffContext{UserID: "u-pick", RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
			Scope: identity.ScopeGlobal, Status: "ACTIVE"})
}

// fakePickHold answers the pick hold seam with canned facts.
type fakePickHold struct {
	facts PickHoldFacts
	err   error
}

func (f fakePickHold) PickHoldFacts(context.Context, string) (PickHoldFacts, error) {
	return f.facts, f.err
}

func heldFacts(owed int64) PickHoldFacts {
	return PickHoldFacts{Held: true, Words: PickHoldWords, OwedCents: owed, OrderTotalCents: owed}
}

// pickHoldStore is one CONFIRMED order the picking moves act on (the fake
// store's seed shape, service_test.go's mkDetail).
func pickHoldStore() *fakeStore {
	return newFakeStore(mkDetail("ord-1", "cust-1", "CONFIRMED"))
}

// The manual transition to PICKING is refused on a held order.
func TestTransitionManual_PickingRefusedWhileHeld(t *testing.T) {
	store := pickHoldStore()
	svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{facts: heldFacts(11300)})
	_, err := svc.TransitionManual(pickHoldCtx(), "ord-1", ActionPicking, "")
	if !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("want ErrHeldFromPicking, got %v", err)
	}
	status, _, _ := store.OrderHeader(pickHoldCtx(), "ord-1")
	if status != "CONFIRMED" {
		t.Fatalf("a held order moved to %s", status)
	}
}

// The choke point itself refuses the move, whatever caller reached it.
func TestSetStatus_PickingRefusedWhileHeld(t *testing.T) {
	store := pickHoldStore()
	svc := NewService(store).WithPickHoldReader(fakePickHold{facts: heldFacts(1)})
	if err := svc.SetStatus(pickHoldCtx(), "", "u-1", "ord-1", "PICKING", ""); !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("want ErrHeldFromPicking, got %v", err)
	}
	// A held order still moves to statuses that release nothing.
	if err := svc.SetStatus(pickHoldCtx(), "", "u-1", "ord-1", "READY", ""); errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("the hold refused a non picking move: %v", err)
	}
}

// The yard pick is refused on a held order, before any line is touched.
func TestApplyYardPicks_RefusedWhileHeld(t *testing.T) {
	store := pickHoldStore()
	// The yard door keys by UUID (its own shape check), so this leg's fixture
	// id is one.
	store.details["7c191000-0000-4000-9000-00000000000a"] = store.details["ord-1"]
	svc := NewService(store).WithPickHoldReader(fakePickHold{facts: heldFacts(1)})
	_, err := svc.ApplyYardPicks(pickHoldCtx(), YardPickInput{
		OrderID: "7c191000-0000-4000-9000-00000000000a", Lines: []YardPickLineInput{{LineNo: 1, QtyPicked: "1"}},
	})
	if !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("want ErrHeldFromPicking, got %v", err)
	}
}

// fakeShipStore arms the service's shipment-group surface over the unit
// fake store: one canned group (PLANNED, on the held order) whose moves are
// recorded rather than written, so a test can prove a refused call moved
// nothing.
type fakeShipStore struct {
	*fakeStore
	group    ShipmentGroup
	branchID string
	movedTo  string
}

func (f *fakeShipStore) ShipmentPlan(context.Context, string) (ShipmentPlan, error) {
	return ShipmentPlan{}, nil
}

func (f *fakeShipStore) ShipmentGroup(_ context.Context, _ string) (ShipmentGroup, string, error) {
	return f.group, f.branchID, nil
}

func (f *fakeShipStore) ReplaceShipmentPlan(context.Context, string, string, []ShipmentGroupInput) error {
	return nil
}

func (f *fakeShipStore) TransitionShipmentGroup(_ context.Context, _, target string, _ []string) (ShipmentGroup, error) {
	f.movedTo = target
	f.group.Status = target
	return f.group, nil
}

func (f *fakeShipStore) SetInvoicePolicy(context.Context, string, string) error { return nil }

func (f *fakeShipStore) OrderHasInvoice(context.Context, string) (bool, error) { return false, nil }

// pickHoldShipGroup is one PLANNED group on the held order, the ladder's own
// rung below PICKING and READY.
func pickHoldShipGroup() *fakeShipStore {
	f := &fakeShipStore{fakeStore: pickHoldStore(), branchID: "branch-1"}
	f.group = ShipmentGroup{ID: "7c191000-0000-4000-9000-00000000000b", OrderID: "ord-1", Status: GroupPlanned}
	return f
}

// A held order's shipment group cannot move into PICKING, the rung its READY
// and DISPATCHED are reached through, beside the yard pick's own guard.
func TestTransitionGroup_PickingRefusedWhileHeld(t *testing.T) {
	store := pickHoldShipGroup()
	svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{facts: heldFacts(1)})
	_, err := svc.TransitionGroup(pickHoldCtx(), store.group.ID, GroupPicking)
	if !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("want ErrHeldFromPicking, got %v", err)
	}
	if store.movedTo != "" {
		t.Fatalf("a held order's group moved to %s", store.movedTo)
	}
	if store.group.Status != GroupPlanned {
		t.Fatalf("the group is %s, want PLANNED untouched", store.group.Status)
	}
}

// A clear or unwired account's group still walks its ladder.
func TestTransitionGroup_PickingAllowedWhenClearOrUnwired(t *testing.T) {
	clear := pickHoldShipGroup()
	svc := NewService(clear).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{facts: PickHoldFacts{Held: false}})
	if _, err := svc.TransitionGroup(pickHoldCtx(), clear.group.ID, GroupPicking); err != nil {
		t.Fatalf("a clear account's group pick: %v", err)
	}
	if clear.movedTo != GroupPicking {
		t.Fatalf("the clear group moved to %q, want PICKING", clear.movedTo)
	}
	unwired := pickHoldShipGroup()
	if _, err := NewService(unwired).withCaller(testCallerFrom).TransitionGroup(pickHoldCtx(), unwired.group.ID, GroupPicking); err != nil {
		t.Fatalf("an unwired lane's group pick: %v", err)
	}
}

// The refusal's words are the item's mandated sentence, verbatim.
func TestPickHoldWords_AreTheMandatedSentence(t *testing.T) {
	if PickHoldWords != "This order cannot reach picking until the balance is paid." {
		t.Fatalf("the hold's words are %q", PickHoldWords)
	}
}

// A facts read that cannot be made refuses the move (fail closed on the wired
// lane), distinct from the unwired lane, which is off.
func TestTransitionManual_PickingRefusedWhenFactsUnreadable(t *testing.T) {
	store := pickHoldStore()
	svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{err: errors.New("boom")})
	if _, err := svc.TransitionManual(pickHoldCtx(), "ord-1", ActionPicking, ""); !errors.Is(err, ErrPickHoldUnverifiable) {
		t.Fatalf("want ErrPickHoldUnverifiable, got %v", err)
	}
}

// An unwired seam leaves the lane off (the additive posture, asserted at boot
// in the composition root), and a clear account still moves to picking.
func TestTransitionManual_PickingAllowedWhenClearOrUnwired(t *testing.T) {
	store := pickHoldStore()
	clear := NewService(store).withCaller(testCallerFrom).
		WithPickHoldReader(fakePickHold{facts: PickHoldFacts{Held: false, OwedCents: 0}})
	if _, err := clear.TransitionManual(pickHoldCtx(), "ord-1", ActionPicking, ""); err != nil {
		t.Fatalf("a clear account's pick: %v", err)
	}
	unwired := NewService(newFakeStore(mkDetail("ord-2", "cust-1", "CONFIRMED"))).withCaller(testCallerFrom)
	if _, err := unwired.TransitionManual(pickHoldCtx(), "ord-2", ActionPicking, ""); err != nil {
		t.Fatalf("an unwired lane's pick: %v", err)
	}
}

// The order read carries the hold: held or not, the words, the total, paid so
// far, still owed, and the in process words when a deposit has landed.
func TestOrderRead_CarriesThePayHold(t *testing.T) {
	store := pickHoldStore()
	svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{facts: PickHoldFacts{
		Held: true, Words: PickHoldWords, InProcessWords: PickInProcessWords,
		OrderTotalCents: 11300, PaidCents: 2000, OwedCents: 9300,
	}})
	view, err := svc.OrderRead(pickHoldCtx(), "ord-1")
	if err != nil {
		t.Fatalf("order read: %v", err)
	}
	if view.PayHold == nil {
		t.Fatalf("the order read carries no pay hold block")
	}
	if !view.PayHold.HeldFromPicking || view.PayHold.Words != PickHoldWords ||
		view.PayHold.InProcessWords != PickInProcessWords ||
		view.PayHold.OrderTotalCents != 11300 || view.PayHold.PaidCents != 2000 || view.PayHold.OwedCents != 9300 {
		t.Fatalf("the pay hold block: %+v", view.PayHold)
	}
	// An unwired seam omits the block (the read degrades, never invents).
	bare := NewService(store).withCaller(testCallerFrom)
	view2, err := bare.OrderRead(pickHoldCtx(), "ord-1")
	if err != nil {
		t.Fatalf("order read: %v", err)
	}
	if view2.PayHold != nil {
		t.Fatalf("an unwired seam answered a pay hold block: %+v", view2.PayHold)
	}
}

// The event driver's move into PICKING carries the guard too (AdvanceFromEvent
// routes through setStatusGuarded; this leg is the direct proof the review
// asked for, beside the manual one).
func TestAdvanceFromEvent_PickingRefusedWhileHeld(t *testing.T) {
	store := pickHoldStore()
	svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(fakePickHold{facts: heldFacts(1)})
	if err := svc.AdvanceFromEvent(pickHoldCtx(), "ord-1", "PICKING", "evt-1"); !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("want ErrHeldFromPicking, got %v", err)
	}
	status, _, _ := store.OrderHeader(pickHoldCtx(), "ord-1")
	if status != "CONFIRMED" {
		t.Fatalf("a held order moved to %s by event", status)
	}
	clear := NewService(store).withCaller(testCallerFrom).
		WithPickHoldReader(fakePickHold{facts: PickHoldFacts{Held: false}})
	if err := clear.AdvanceFromEvent(pickHoldCtx(), "ord-1", "PICKING", "evt-2"); err != nil {
		t.Fatalf("a clear account's event driven pick: %v", err)
	}
}
