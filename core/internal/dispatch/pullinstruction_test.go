package dispatch

// pullinstruction_test.go, the pull-instruction generalisation, pinned.
//
// picksource.go shipped with a premise stated in its own header ("a stop scheduled on the dispatch
// board IS the instruction to pull material") and enforced twice. A customer pickup has no delivery
// stop, so a will-call order could be racked and given a pickup code while never once appearing in
// the yard's pick queue. These tests pin the generalisation and, just as importantly, pin that it
// changed nothing for a delivery.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func pullFeed() *PickFeed {
	at := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	return NewPickFeed(NewRepository(nil)).WithClock(func() time.Time { return at })
}

// - point 1 · the fulfilment, and the proof that it moved nothing -

// TestDeliveryTicket_IsByteIdenticalToWhatThisCodeProducedBefore is the INERTNESS PROOF, and it is a
// recording rather than an assertion.
//
// The want strings below were produced by running the PRE-CHANGE ticket() (at 56d07fc, the branch
// point) over exactly these rows and marshalling the result. They are not what this author thinks
// the function should return, they are what the shipped function did return, byte for byte. A
// regression in ANY field of a delivery ticket, not just its fulfilment, fails here.
//
// The row set is chosen to cover what the delivery query can actually produce: an unassigned stop, a
// stop on a planned run with a window and an MTO class, a stop whose LEFT JOIN to orders found
// nothing (fulfilment "), and a stop with an unparseable date.
func TestDeliveryTicket_IsByteIdenticalToWhatThisCodeProducedBefore(t *testing.T) {
	f := pullFeed()
	cases := []struct {
		name string
		row  pickFeedStop
		want string
	}{
		{
			name: "unassigned stop, delivered today",
			row: pickFeedStop{StopID: "s-1", BranchID: "b-1", OrderID: "o-1", OrderNo: "#SO-TRT-1041",
				CustomerName: "Bostock Landscaping", Fulfillment: "DELIVERY", ScheduledDate: "2026-08-06",
				StopStatus: "READY", MTOClass: "STANDARD"},
			want: `{"OrderID":"o-1","OrderNo":"#SO-TRT-1041","BranchID":"b-1","CustomerName":"Bostock Landscaping","Fulfillment":"DELIVERY","Priority":"HIGH","Reference":"s-1","ScheduledDate":"2026-08-06","RequestedAt":"2026-08-06T00:00:00Z","StopID":"s-1","StopStatus":"READY","RunID":"","RunNo":"","RunStatus":"","RunDepartsAt":null,"LoadPosition":0,"MTOClass":"STANDARD","TimeWindow":"","Lines":[]}`,
		},
		{
			name: "on a planned run, already late, made to order",
			row: pickFeedStop{StopID: "s-2", BranchID: "b-1", OrderID: "o-2", OrderNo: "#SO-TRT-1042",
				CustomerName: "Rood Contracting", Fulfillment: "DELIVERY", ScheduledDate: "2026-08-05",
				StopStatus: "READY", RunID: "r-1", RunNo: "RUN-7", RunStatus: "PLANNED", MTOClass: "MTO",
				TimeWindow: "08:00-12:00"},
			want: `{"OrderID":"o-2","OrderNo":"#SO-TRT-1042","BranchID":"b-1","CustomerName":"Rood Contracting","Fulfillment":"DELIVERY","Priority":"URGENT","Reference":"s-2","ScheduledDate":"2026-08-05","RequestedAt":"2026-08-05T00:00:00Z","StopID":"s-2","StopStatus":"READY","RunID":"r-1","RunNo":"RUN-7","RunStatus":"PLANNED","RunDepartsAt":null,"LoadPosition":0,"MTOClass":"MTO","TimeWindow":"08:00-12:00","Lines":[]}`,
		},
		{
			// THE CASE THE FALLBACK EXISTS FOR. pickFeedSelect LEFT JOINs orders and COALESCEs a
			// missing one to the empty string. Without the fallback this ticket would have stopped
			// saying DELIVERY, which would have been a regression dressed as a fix.
			name: "order row unreadable, fulfilment arrives empty",
			row: pickFeedStop{StopID: "s-3", BranchID: "b-2", OrderID: "o-3", Fulfillment: "",
				ScheduledDate: "2026-08-09", StopStatus: "READY", MTOClass: "MIXED"},
			want: `{"OrderID":"o-3","OrderNo":"","BranchID":"b-2","CustomerName":"","Fulfillment":"DELIVERY","Priority":"NORMAL","Reference":"s-3","ScheduledDate":"2026-08-09","RequestedAt":"2026-08-09T00:00:00Z","StopID":"s-3","StopStatus":"READY","RunID":"","RunNo":"","RunStatus":"","RunDepartsAt":null,"LoadPosition":0,"MTOClass":"MIXED","TimeWindow":"","Lines":[]}`,
		},
		{
			name: "unparseable date stays NORMAL and carries no requested-at",
			row: pickFeedStop{StopID: "s-4", BranchID: "b-1", OrderID: "o-4", OrderNo: "#SO-TRT-1044",
				CustomerName: "Colton", Fulfillment: "DELIVERY", ScheduledDate: "not-a-date",
				StopStatus: "READY", MTOClass: "STANDARD"},
			want: `{"OrderID":"o-4","OrderNo":"#SO-TRT-1044","BranchID":"b-1","CustomerName":"Colton","Fulfillment":"DELIVERY","Priority":"NORMAL","Reference":"s-4","ScheduledDate":"not-a-date","RequestedAt":null,"StopID":"s-4","StopStatus":"READY","RunID":"","RunNo":"","RunStatus":"","RunDepartsAt":null,"LoadPosition":0,"MTOClass":"STANDARD","TimeWindow":"","Lines":[]}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(f.ticket(c.row))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != c.want {
				t.Fatalf("a delivery ticket changed shape.\n before (56d07fc): %s\n after           : %s", c.want, got)
			}
		})
	}
}

// TestStopOnANonDeliveryOrder_StopsLyingAboutTheMethod is the ONE ticket this change moves, named so
// nobody has to discover it.
//
// Nothing refuses a stop for a WILL_CALL order, dispatch.Service.Schedule gates on order STATUS, not
// fulfilment, so this row is reachable. Before, its ticket said DELIVERY, which was the hardcode
// asserting something the order contradicted. It now says what the order says. The stop fields are
// still populated, because the truck is still real: the yard learns both that a load is involved and
// that the commitment behind it is a customer collection.
func TestStopOnANonDeliveryOrder_StopsLyingAboutTheMethod(t *testing.T) {
	tk := pullFeed().ticket(pickFeedStop{
		StopID: "s-5", BranchID: "b-1", OrderID: "o-5", OrderNo: "#SO-TRT-1045",
		CustomerName: "Walk-in", Fulfillment: "WILL_CALL", ScheduledDate: "2026-08-07",
		StopStatus: "READY", MTOClass: MTOStandard,
	})
	if tk.Fulfillment != "WILL_CALL" {
		t.Fatalf("fulfillment = %q, want WILL_CALL, the ticket carries the ORDER's method, not the hardcode", tk.Fulfillment)
	}
	if tk.StopID != "s-5" || tk.Reference != "s-5" {
		t.Fatal("a stop that exists keeps its stop id and its dispatch handle, the truck is still real")
	}
}

// - point 6 · the pickup ticket's Reference, and the rest of its shape -

func TestPickupTicket_CarriesItsOwnMethodDateAndHandle(t *testing.T) {
	tk := pullFeed().pickupTicket(pickFeedPickup{
		OrderID: "o-9", OrderNo: "#SO-TRT-1099", BranchID: "b-1", CustomerName: "Bostock Landscaping",
		Fulfillment: "WILL_CALL", PromisedDate: "2026-08-05", OrderStatus: "READY",
	})
	if tk.Fulfillment != "WILL_CALL" {
		t.Fatalf("fulfillment = %q, a pickup is not a delivery and must never claim to be", tk.Fulfillment)
	}
	// Point 6. The stop id is the delivery handle because a dispatcher can answer for a stop. A
	// pickup has no stop; the order number is what is printed on the paperwork and what the counter
	// looks orders up by, so it is the handle a picker can read aloud and get an answer.
	if tk.Reference != "#SO-TRT-1099" {
		t.Fatalf("reference = %q, want the order number, a picker must be able to read it aloud", tk.Reference)
	}
	// Point 3. Yesterday's promise is already broken, so it is URGENT, graded off the PROMISED
	// COLLECTION date, which is the only urgency signal a pickup carries.
	if tk.Priority != "URGENT" {
		t.Fatalf("priority = %q, want URGENT for a promise made for yesterday", tk.Priority)
	}
	if tk.ScheduledDate != "2026-08-05" || tk.RequestedAt == nil {
		t.Fatal("the promised collection date is the date on the ticket")
	}
	// A pickup is on no truck. Every stop-shaped field is empty rather than plausible.
	if tk.StopID != "" || tk.StopStatus != "" || tk.RunID != "" || tk.RunNo != "" || tk.RunStatus != "" || tk.TimeWindow != "" {
		t.Fatalf("a pickup carries no stop, run or window; got %+v", tk)
	}
	if tk.Lines == nil {
		t.Fatal("lines are an empty slice, never nil, the surface iterates them")
	}
}

func TestPickupTicket_NoPromisedDateIsNormalAndNotUrgent(t *testing.T) {
	tk := pullFeed().pickupTicket(pickFeedPickup{
		OrderID: "o-10", OrderNo: "#SO-TRT-1100", BranchID: "b-1", Fulfillment: "PICKUP",
		PromisedDate: "", OrderStatus: "CONFIRMED",
	})
	if tk.Priority != "NORMAL" {
		t.Fatalf("priority = %q, an order nobody promised a day for must not flood the top of the queue", tk.Priority)
	}
	if tk.RequestedAt != nil {
		t.Fatal("no promised date means no requested-at; inventing one would be inventing a promise")
	}
}

// - point 2 · the union's merge -

// TestMergeByDate_WithNoPickupsIsTheDeliveryListExactly is the union's half of the inertness proof:
// the reason a branch that has never taken a pickup sees no change at all.
func TestMergeByDate_WithNoPickupsIsTheDeliveryListExactly(t *testing.T) {
	deliveries := []PickFeedTicket{
		{OrderID: "d1", ScheduledDate: "2026-08-05"},
		{OrderID: "d2", ScheduledDate: "2026-08-05"},
		{OrderID: "d3", ScheduledDate: "2026-08-09"},
	}
	got := mergeByDate(deliveries, nil)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i := range deliveries {
		before, _ := json.Marshal(deliveries[i])
		after, _ := json.Marshal(got[i])
		if string(before) != string(after) {
			t.Fatalf("position %d changed: %s vs %s, the delivery list must survive the merge untouched", i, before, after)
		}
	}
}

func TestMergeByDate_InterleavesByDateAndBreaksTiesForTheDelivery(t *testing.T) {
	deliveries := []PickFeedTicket{
		{OrderID: "d-late", ScheduledDate: "2026-08-05"},
		{OrderID: "d-tie", ScheduledDate: "2026-08-06"},
		{OrderID: "d-far", ScheduledDate: "2026-08-09"},
	}
	pickups := []PickFeedTicket{
		{OrderID: "p-earliest", ScheduledDate: "2026-08-04"},
		{OrderID: "p-tie", ScheduledDate: "2026-08-06"},
		{OrderID: "p-undated", ScheduledDate: ""},
	}
	want := []string{"p-earliest", "d-late", "d-tie", "p-tie", "d-far", "p-undated"}
	got := mergeByDate(deliveries, pickups)
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].OrderID != id {
			t.Fatalf("position %d = %q, want %q, full order %v", i, got[i].OrderID, id, ids(got))
		}
	}
}

func ids(ts []PickFeedTicket) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.OrderID)
	}
	return out
}

// sortsBefore is the merge's whole comparison, and an undated ticket sorting FIRST would be the
// failure mode that matters: a blank field outranking a customer who is actually waiting.
func TestSortsBefore_AnAbsentDateIsLaterThanEveryRealOne(t *testing.T) {
	if sortsBefore("", "2026-08-05") {
		t.Fatal("an undated ticket must not outrank a dated one")
	}
	if !sortsBefore("2026-08-05", "") {
		t.Fatal("a dated ticket outranks an undated one")
	}
	if sortsBefore("2026-08-05", "2026-08-05") {
		t.Fatal("equal dates are not strictly before, that is what keeps the merge stable")
	}
	if sortsBefore("", "") {
		t.Fatal("two undated tickets keep their given order")
	}
}

// - point 5 · the rack tag follows the pick -

// fakePickState stands in for the yard's own read. It is deliberately dumb: the judgement about what
// counts as "pulled" belongs to the adapter in the composition root, and dispatch must hold none of it.
type fakePickState struct {
	state PickCompletion
	// states, when set, overrides `state` per order id, the shape a population test needs when some
	// pickups are staged and some are not.
	states map[string]PickCompletion
	err    error
	calls  int
}

func (f *fakePickState) OrderPickState(context.Context, string) (PickCompletion, error) {
	f.calls++
	return f.state, f.err
}

// The batched twin answers the same one state for every id it is asked about, dumb in the same way
// and for the same reason. A test that wants a MIXED population sets `states` instead.
func (f *fakePickState) OrderPickStates(_ context.Context, orderIDs []string) (map[string]PickCompletion, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]PickCompletion{}
	for _, id := range orderIDs {
		if f.states != nil {
			if st, ok := f.states[id]; ok {
				out[id] = st
			}
			continue
		}
		if f.state.Exists {
			out[id] = f.state
		}
	}
	return out, nil
}

func willCallOrders() *spyOrders {
	o := newSpyOrders()
	o.resolved[refOrderID] = refOrderID
	o.briefs[refOrderID] = OrderBrief{ID: refOrderID, BranchID: refBranch, Status: "CONFIRMED", Fulfillment: "WILL_CALL"}
	return o
}

// TestMintWillCall_UnwiredBehavesExactlyAsItShipped is the additive proof for point 5. Nothing about
// this change is a flag day: a deployment that has not wired the yard has not lost the ability to
// print a tag, and every suite that built a Service without the seam still means what it meant.
//
// The nil repository is what proves the gate was passed rather than skipped: MintWillCall reaches the
// insert and fails THERE, on the database, which is only possible if requirePicked allowed it.
func TestMintWillCall_UnwiredBehavesExactlyAsItShipped(t *testing.T) {
	_, err := refSvc(t, willCallOrders(), refCaller(identity.ScopeGlobal)).
		MintWillCall(context.Background(), refOrderID, 0)
	if err == nil {
		t.Fatal("the nil repository should have failed the insert")
	}
	if strings.Contains(err.Error(), "picked") || strings.Contains(err.Error(), "staged") {
		t.Fatalf("an un-wired service refused on the PICK gate: %v", err)
	}
}

// TestMintWillCall_TheTagFollowsThePick is the reversal itself. 0092 minted a code "when the order
// goes on the rack", which presupposed a pull nothing had recorded. Only a STAGED pick mints now.
func TestMintWillCall_TheTagFollowsThePick(t *testing.T) {
	cases := []struct {
		name    string
		state   PickCompletion
		err     error
		refuses bool
		says    string
	}{
		{
			name:    "nothing has been picked",
			state:   PickCompletion{},
			refuses: true,
			says:    "yard's queue",
		},
		{
			name:    "somebody is halfway through it",
			state:   PickCompletion{Exists: true, Status: "IN_PROGRESS"},
			refuses: true,
			says:    "being picked now",
		},
		{
			name:    "claimed but not pulled",
			state:   PickCompletion{Exists: true, Status: "ACCEPTED"},
			refuses: true,
			says:    "claimed but not pulled yet",
		},
		{
			// A FAILED READ REFUSES. A tag minted because a query timed out is evidence of nothing,
			// and on a rack it is indistinguishable from one that means something.
			name:    "the yard could not be asked",
			err:     errors.New("connection refused"),
			refuses: true,
			says:    "could not be asked",
		},
		{
			name:  "staged, the material is on the ground",
			state: PickCompletion{Exists: true, Staged: true, Status: "STAGED"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			picks := &fakePickState{state: c.state, err: c.err}
			svc := refSvc(t, willCallOrders(), refCaller(identity.ScopeGlobal)).WithPickState(picks)
			_, err := svc.MintWillCall(context.Background(), refOrderID, 0)
			if picks.calls != 1 {
				t.Fatalf("the yard was asked %d times, want once", picks.calls)
			}
			if !c.refuses {
				// Allowed through: it then fails on the nil repository, which is the only evidence
				// available here that the gate did not stop it.
				if err != nil && strings.Contains(err.Error(), "pick") {
					t.Fatalf("a staged pick was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("the mint was allowed without a staged pick")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Fatalf("the refusal must name the way forward; got %q, wanted it to mention %q", err, c.says)
			}
		})
	}
}

// TestMintWillCall_TheRefusalNamesWhereThePickActuallyIs, "no" sends somebody walking out to the
// yard to find out which "no" it was. The sentence carries the yard's own word for the state, in
// words rather than the enum.
func TestMintWillCall_TheRefusalNamesWhereThePickActuallyIs(t *testing.T) {
	picks := &fakePickState{state: PickCompletion{Exists: true, Status: "IN_PROGRESS"}}
	svc := refSvc(t, willCallOrders(), refCaller(identity.ScopeGlobal)).WithPickState(picks)
	_, err := svc.MintWillCall(context.Background(), refOrderID, 0)
	if err == nil {
		t.Fatal("an unstaged pick must refuse")
	}
	if strings.Contains(err.Error(), "IN_PROGRESS") || strings.Contains(err.Error(), "in progress") {
		t.Fatalf("the refusal shouts an enum at a counter person: %v", err)
	}
	if !strings.Contains(err.Error(), "being picked now") {
		t.Fatalf("the refusal must say where the pick is in words a counter person uses: %v", err)
	}
	if !strings.Contains(err.Error(), "staged") {
		t.Fatalf("the refusal must say what unblocks it: %v", err)
	}
}

// - item 1 (session A) · the queue pages instead of truncating -

// TestPageOf_TheWindowIsTheWindowAndPastTheEndIsEmpty pins the slice that replaced the truncation.
//
// WHAT WAS THERE BEFORE. `if len(out) > limit { out = out[:limit] }`, the merged list cut at the cap,
// so at the cap a pickup that sorted earlier DISPLACED a delivery off the end and the board silently
// stopped being the board. There was no offset and no total: a caller could not tell a full page from
// a truncated queue.
func TestPageOf_TheWindowIsTheWindowAndPastTheEndIsEmpty(t *testing.T) {
	all := []PickFeedTicket{{OrderNo: "a"}, {OrderNo: "b"}, {OrderNo: "c"}, {OrderNo: "d"}, {OrderNo: "e"}}
	nos := func(ts []PickFeedTicket) string {
		out := ""
		for _, t := range ts {
			out += t.OrderNo
		}
		return out
	}

	if got := nos(pageOf(all, 2, 0)); got != "ab" {
		t.Fatalf("first page = %q, want \"ab\"", got)
	}
	if got := nos(pageOf(all, 2, 2)); got != "cd" {
		t.Fatalf("second page = %q, want \"cd\"", got)
	}
	// A LAST PAGE IS SHORT, NOT PADDED and not refused.
	if got := nos(pageOf(all, 2, 4)); got != "e" {
		t.Fatalf("last page = %q, want \"e\"", got)
	}
	// PAST THE END IS EMPTY. Clicking "next" once too often is a thing people do; it deserves an
	// empty list under an honest total, not an error.
	if got := pageOf(all, 2, 99); len(got) != 0 {
		t.Fatalf("past the end must be empty, got %+v", got)
	}
	if got := pageOf(all, 2, 5); len(got) != 0 {
		t.Fatalf("exactly at the end must be empty, got %+v", got)
	}
	// A window WIDER than the queue is the whole queue, not an error.
	if got := nos(pageOf(all, 50, 0)); got != "abcde" {
		t.Fatalf("an over-wide window = %q, want the whole queue", got)
	}
}

// TestPendingTickets_RefusesADepthThatWouldBecomeAScan pins the cap, in words.
//
// A merge cannot be offset at the database, the k-th merged row is not the k-th row of either source
// , so a deep page has to over-read BOTH sources to (limit+offset). That is fine at any depth a yard
// board reaches and is a two-table scan at a depth it never does. The refusal names what to do
// instead, which is the loadboard ValidatePolicy posture rather than a silent clamp that would return
// a page the caller did not ask for and could not detect.
func TestPendingTickets_RefusesADepthThatWouldBecomeAScan(t *testing.T) {
	f := NewPickFeed(nil)
	_, _, err := f.PendingTickets(context.Background(), "b-1", 200, 5000)
	if err == nil {
		t.Fatal("a page 5000 rows deep must be refused rather than scanned")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be a 400, not a 500: %#v", err)
	}
	for _, want := range []string{"too far", "narrow"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name the way forward (%q missing): %v", want, err)
		}
	}
	// And a depth INSIDE the cap is not refused by this guard, it fails later, on the nil repo,
	// which is what proves the guard is a depth check and not a blanket refusal.
	_, _, err = f.PendingTickets(context.Background(), "b-1", 50, 100)
	if err != nil && errors.As(err, &bad) {
		t.Fatalf("a reasonable depth must pass the depth guard: %v", err)
	}
}
