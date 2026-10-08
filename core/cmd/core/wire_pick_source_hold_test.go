package main

// wire_pick_source_hold_test.go, item 9 F review N1 and N2: the pickup feed
// rows are marked from billing's one hold read at the composition root, not
// from SQL inside dispatch. The legs run the adapter's marking with a fake
// hold read, so they need no database.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

type fakeHoldRead struct {
	held  map[string]int64
	err   error
	calls []string
}

func (f *fakeHoldRead) OrderHeldFromPicking(_ context.Context, orderID string) (bool, int64, error) {
	f.calls = append(f.calls, orderID)
	if f.err != nil {
		return false, 0, f.err
	}
	owed, ok := f.held[orderID]
	return ok, owed, nil
}

// A held pickup ticket is marked with the words and the owed figure; a clear
// one carries nothing.
func TestDispatchPickSource_MarksAHeldPickupFromTheHoldRead(t *testing.T) {
	read := &fakeHoldRead{held: map[string]int64{"o-held": 8800}}
	src := dispatchPickSource{hold: read}

	held, err := src.markHeld(context.Background(), tasks.PickTicket{OrderID: "o-held", Fulfillment: "WILL_CALL"})
	if err != nil {
		t.Fatal(err)
	}
	if !held.HeldFromPicking || held.HoldWords != "Balance due" || held.StillOwedCents != 8800 {
		t.Fatalf("a held pickup must be marked with the words and the figure: %+v", held)
	}

	clear, err := src.markHeld(context.Background(), tasks.PickTicket{OrderID: "o-paid", Fulfillment: "PICKUP"})
	if err != nil {
		t.Fatal(err)
	}
	if clear.HeldFromPicking || clear.HoldWords != "" || clear.StillOwedCents != 0 {
		t.Fatalf("a clear pickup must carry no mark: %+v", clear)
	}
}

// A delivery ticket costs no read and is never marked (dispatch keeps its own
// gate); an unreadable hold fails the read instead of listing the order clear.
func TestDispatchPickSource_DeliverySkipsTheReadAndAnUnreadableHoldFails(t *testing.T) {
	read := &fakeHoldRead{held: map[string]int64{"o-1": 5}}
	src := dispatchPickSource{hold: read}
	out, err := src.markHeld(context.Background(), tasks.PickTicket{OrderID: "o-1", Fulfillment: "DELIVERY"})
	if err != nil || out.HeldFromPicking || len(read.calls) != 0 {
		t.Fatalf("a delivery ticket must not be read or marked: %+v err=%v calls=%v", out, err, read.calls)
	}

	broken := dispatchPickSource{hold: &fakeHoldRead{err: errors.New("billing down")}}
	if _, err := broken.markHeld(context.Background(), tasks.PickTicket{OrderID: "o-1", Fulfillment: "PICKUP"}); err == nil {
		t.Fatal("an unreadable hold must fail the read, not list a held order clear")
	}
}
