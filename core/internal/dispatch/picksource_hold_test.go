package dispatch

// picksource_hold_test.go, item 9 Opus P2-1: the pickup feed rows carry the
// pay before release hold, so the yard queue shows a held order as held with
// "Balance due" instead of listing it like any other waiting order. The held
// order STAYS in the feed (the yard knows why it waits); the doors (Stage,
// Claim) are what refuse.

import (
	"testing"
)

// A held pickup row maps onto a ticket marked held, with the words and the
// owed figure.
func TestPickupTicket_CarriesTheHold(t *testing.T) {
	feed := NewPickFeed(nil)
	row := pickFeedPickup{
		OrderID: "o-1", OrderNo: "#SO-1", BranchID: "b-1", CustomerName: "A Buyer",
		Fulfillment: "WILL_CALL", PromisedDate: "2026-10-07", OrderStatus: "CONFIRMED",
		HeldFromPicking: true, OwedCents: 8800,
	}
	ticket := feed.pickupTicket(row)
	if !ticket.HeldFromPicking {
		t.Fatal("a held pickup row mapped to an unmarked ticket")
	}
	if ticket.HoldWords != "Balance due" {
		t.Fatalf("the hold words: %q", ticket.HoldWords)
	}
	if ticket.StillOwedCents != 8800 {
		t.Fatalf("the owed figure: %d", ticket.StillOwedCents)
	}
}

// A clear row carries no mark and no words (the fields are omitempty on the
// wire, so a clear ticket's shape is unchanged).
func TestPickupTicket_ClearRowCarriesNoMark(t *testing.T) {
	feed := NewPickFeed(nil)
	ticket := feed.pickupTicket(pickFeedPickup{OrderID: "o-1", Fulfillment: "PICKUP"})
	if ticket.HeldFromPicking || ticket.HoldWords != "" || ticket.StillOwedCents != 0 {
		t.Fatalf("a clear row must carry no hold mark: %+v", ticket)
	}
}
