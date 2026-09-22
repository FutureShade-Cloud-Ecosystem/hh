//go:build integration

// driver_handback_integration_test.go, THE FAILED HANDBACK, pinned.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run DriverHandback ./internal/dispatch/
//
// WHY THIS EXISTS. The driver surface now prints a sentence, after a failed delivery, saying what
// happens next: bring the load back, the yard sees it again, the order is free to be booked. That
// sentence is a CLAIM ABOUT THE SYSTEM, and it was true only by the accident that three separate SQL
// predicates happened to spell `status NOT IN ('CANCELLED','FAILED')` the same way:
//
//	repository_w3.go   PickFeedStopForOrder, a failed stop stops resolving to a delivery ticket
//	repository_w3.go   pickupFeedSelect's anti-join, a pickup-bound order re-enters the pull list
//	repository.go      LiveStopsForOrder, the guard Service.Schedule reads, so a new date can be set
//
// plus the partial unique index from 0090 (`WHERE status NOT IN ('CANCELLED','FAILED')`) that makes
// the third one legal at the database rather than only in Go. Nothing in this module drove a stop to
// FAILED and then asserted any of it. A tidy-up of any one of those WHERE clauses, the kind of edit
// that looks like a no-op, would have broken the handback silently, and the first person to find out
// would have been a driver whose failed delivery vanished.
//
// A UI SENTENCE THAT NO TEST BACKS IS A LIE WITH A DEPLOY DATE. This is that test.
//
// FIXTURES RESTORE WHAT THEY TOUCH: seedOrder's cleanup removes the order and all its stops; the run
// registers its own.

package dispatch

import (
	"context"
	"errors"
	"testing"
)

// TestDriverHandback_AFailedDeliveryPutsTheORDERBackInFrontOfTheYard walks the exact path a driver
// walks, loaded, en route, could not deliver, and then asks the yard's own reads what they see.
func TestDriverHandback_AFailedDeliveryPutsTheORDERBackInFrontOfTheYard(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	feed := pullFeedFor(t, h.pool)
	bg := context.Background()

	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	seedPickLines(t, h.pool, orderID, 2)

	stop, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: today(), AddressLine: "41 Gravel Pit Lane",
		City: "Trenton", PostalCode: "K8V 5R5", ContactName: "Site foreman", TimeWindow: "AM",
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	// - BEFORE: the order is a delivery, and it resolves to THIS stop -
	//
	// The control matters. If the ticket did not resolve here, the ErrNotFound asserted after the
	// failure would prove nothing about the failure.
	before, err := feed.TicketForOrder(bg, orderID)
	if err != nil {
		t.Fatalf("a scheduled delivery must resolve to a pull instruction: %v", err)
	}
	if before.Reference != stop.ID {
		t.Fatalf("a delivery ticket's handle is the STOP id; got %q want %q", before.Reference, stop.ID)
	}
	if len(before.Lines) != 2 {
		t.Fatalf("the ticket must carry the order's material; got %d lines", len(before.Lines))
	}
	// And a second delivery cannot be booked while this one is live, the guard the failure releases.
	if _, err := h.svc.Schedule(ctx, ScheduleInput{OrderID: orderID, ScheduledDate: tomorrow(), City: "Trenton"}); err == nil {
		t.Fatal("a live stop must block a second delivery for the same order, if it does not, the " +
			"release asserted below is not a release, it is the ordinary state")
	}

	// - the driver's day: on the truck, on the road, and then it does not happen -
	run := seedRun(t, h, ctx, branchTRT, today(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}

	// A blank reason is refused BEFORE anything moves. The driver surface collects one in a sheet for
	// exactly this reason: the server will not close a stop that cannot say why.
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopFailed, "   "); err == nil {
		t.Fatal("a failed stop must carry a reason, the surface's sheet exists because the server insists")
	}

	failed, err := h.svc.AdvanceStop(ctx, stop.ID, StopFailed, "Gate locked, nobody on site")
	if err != nil {
		t.Fatalf("could not deliver: %v", err)
	}
	if failed.Status != StopFailed {
		t.Fatalf("the stop should be FAILED, got %s", failed.Status)
	}
	if failed.FailureReason != "Gate locked, nobody on site" {
		t.Fatalf("the reason is kept verbatim, it is the record of what happened; got %q", failed.FailureReason)
	}

	// - AFTER, assertion 1: the failed stop no longer speaks for the order -
	if _, err := feed.TicketForOrder(bg, orderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a FAILED stop must stop resolving to a pull instruction, otherwise the yard is "+
			"still being told to pull against a delivery that is over. Got %v", err)
	}

	// - AFTER, assertion 2: the order is free to be booked again -
	//
	// This is the half the driver's on-screen sentence is really about. The partial unique index
	// (0090) permits the second row only because the first is FAILED, so this is a statement about the
	// DATABASE and not about Go.
	again, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: tomorrow(), AddressLine: "41 Gravel Pit Lane",
		City: "Trenton", PostalCode: "K8V 5R5", ContactName: "Site foreman",
	})
	if err != nil {
		t.Fatalf("a failed delivery must leave the order bookable, this is the sentence the driver "+
			"surface prints, and it has to be true: %v", err)
	}
	if again.Status != StopReady {
		t.Fatalf("the replacement stop is born READY, the state the yard's queue selects; got %s", again.Status)
	}

	// - AFTER, assertion 3: and READY is what puts it back in front of the yard -
	tickets, _, err := feed.PendingTickets(bg, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	got, ok := ticketFor(tickets, orderID)
	if !ok {
		t.Fatal("the rebooked order must be on the yard's pull list, the load came back and somebody " +
			"has to pull it again")
	}
	if got.Reference != again.ID {
		t.Fatalf("the pull instruction must name the NEW stop, not the failed one; got %q want %q",
			got.Reference, again.ID)
	}
}

// TestDriverHandback_APickupBoundOrderReturnsToThePullListImmediately is the other half of the same
// predicate, and it needs no reschedule at all.
//
// A pickup has no second stop to book: the anti-join in pickupFeedSelect excludes an order that has a
// live delivery stop, so the moment the stop is FAILED the order is back in the pickup source on its
// own. The two cases are asserted separately because they are two different WHERE clauses in two
// different queries that only happen to agree, and a lane that "simplified" one of them would leave
// the other passing.
func TestDriverHandback_APickupBoundOrderReturnsToThePullListImmediately(t *testing.T) {
	h := newHarness(t)
	ctx := globalCtx(t, h.pool)
	feed := pullFeedFor(t, h.pool)
	bg := context.Background()

	// A will-call order that somebody nonetheless put on a truck, the real case this covers is a
	// customer who asked for it to be dropped off after all, and then was not there.
	orderID := seedPickupOrder(t, h.pool, branchTRT, "WILL_CALL", "CONFIRMED", today())
	seedPickLines(t, h.pool, orderID, 1)

	stop, err := h.svc.Schedule(ctx, ScheduleInput{
		OrderID: orderID, ScheduledDate: today(), City: "Trenton", ContactName: "Customer",
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	// While the stop is live the order is OUT of the pickup source, the union de-duplicates at the
	// database, which is what stops one order making two tickets.
	//
	// FULFILLMENT IS NOT THE DISCRIMINATOR, and it is worth saying why: the delivery source stamps the
	// ORDER's own fulfilment (that was the shortcut the pull-instruction lane retired, it used to
	// hardcode "DELIVERY"), so a will-call order on a truck yields a WILL_CALL ticket from the
	// DELIVERY read. What tells the two sources apart is the HANDLE: a delivery ticket's Reference is
	// the stop id, a pickup ticket's is the order number. That, and the count.
	tickets, _, err := feed.PendingTickets(bg, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	if n := ticketsForCount(tickets, orderID); n != 1 {
		t.Fatalf("one order, one pull instruction, got %d while a live delivery stop exists", n)
	}
	live, _ := ticketFor(tickets, orderID)
	if live.Reference != stop.ID {
		t.Fatalf("while the stop is live the instruction comes from the DELIVERY source and is handled "+
			"by the stop id; got %q", live.Reference)
	}

	run := seedRun(t, h, ctx, branchTRT, today(), true)
	if _, err := h.svc.AssignStop(ctx, stop.ID, AssignInput{RunID: run.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := h.svc.DispatchRun(ctx, run.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopEnRoute, ""); err != nil {
		t.Fatalf("en route: %v", err)
	}
	if _, err := h.svc.AdvanceStop(ctx, stop.ID, StopFailed, "Nobody on site"); err != nil {
		t.Fatalf("could not deliver: %v", err)
	}

	// - and it is back, as a PICKUP, with its own lines, with nobody having rescheduled anything -
	after, _, err := feed.PendingTickets(bg, branchTRT, 200, 0)
	if err != nil {
		t.Fatalf("pending tickets: %v", err)
	}
	got, ok := ticketFor(after, orderID)
	if !ok {
		t.Fatal("a failed delivery on a pickup-bound order must put it straight back on the yard's " +
			"pull list, the material is coming back and the customer is still going to collect it")
	}
	if n := ticketsForCount(after, orderID); n != 1 {
		t.Fatalf("still one order, still one pull instruction; got %d", n)
	}
	if got.Fulfillment != "WILL_CALL" {
		t.Fatalf("it returns as what it IS, a will-call collection, not a delivery; got %s", got.Fulfillment)
	}
	if len(got.Lines) != 1 {
		t.Fatalf("it returns with its material; got %d lines", len(got.Lines))
	}
	if got.Reference != got.OrderNo {
		t.Fatalf("a pickup's handle is the order NUMBER (there is no stop to name); got %q, if this is "+
			"the failed stop's id, the anti-join is still counting a FAILED stop as live", got.Reference)
	}
}

// ticketsForCount is the union's de-duplication expressed as a number: one order, one instruction.
func ticketsForCount(tickets []PickFeedTicket, orderID string) int {
	n := 0
	for _, t := range tickets {
		if t.OrderID == orderID {
			n++
		}
	}
	return n
}
