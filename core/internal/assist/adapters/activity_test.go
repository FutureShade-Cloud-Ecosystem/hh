package adapters

// activity_test.go, DIB-494. The order-trail adapter: scope in, vocabulary through, nothing else.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

type fakeTrailSvc struct {
	summary  order.Summary
	events   []order.OrderActivityEvent
	found    bool
	err      error
	gotScope order.ReadScope
	gotRef   string
}

func (f *fakeTrailSvc) ActivityByNumber(_ context.Context, scope order.ReadScope, ref string) (order.Summary, []order.OrderActivityEvent, bool, error) {
	f.gotScope, f.gotRef = scope, ref
	return f.summary, f.events, f.found, f.err
}

func TestOrderActivityAdapterScopesTheReadToTheCaller(t *testing.T) {
	f := &fakeTrailSvc{found: true, summary: order.Summary{OrderID: "o-1", OrderNo: "#SO-4"}}
	rd := NewOrderActivityReader(f)

	scoped := assist.Caller{UserID: "u-1", Tier: 3, BranchIDs: []string{"b-kel"}}
	if _, _, _, err := rd.ActivityFor(context.Background(), scoped, "SO-4"); err != nil {
		t.Fatalf("ActivityFor: %v", err)
	}
	if f.gotScope.Global || len(f.gotScope.BranchIDs) != 1 || f.gotScope.BranchIDs[0] != "b-kel" {
		t.Fatalf("a branch caller's scope arrived as %+v", f.gotScope)
	}
	if f.gotRef != "SO-4" {
		t.Fatalf("ref = %q", f.gotRef)
	}

	global := assist.Caller{UserID: "u-2", Tier: 1, Global: true}
	if _, _, _, err := rd.ActivityFor(context.Background(), global, "SO-4"); err != nil {
		t.Fatalf("ActivityFor: %v", err)
	}
	if !f.gotScope.Global || len(f.gotScope.BranchIDs) != 0 {
		t.Fatalf("a global caller's scope arrived as %+v", f.gotScope)
	}

	// No grants means NO branches, never everything. The inversion that turns the fence inside out.
	none := assist.Caller{UserID: "u-3", Tier: 3}
	if _, _, _, err := rd.ActivityFor(context.Background(), none, "SO-4"); err != nil {
		t.Fatalf("ActivityFor: %v", err)
	}
	if f.gotScope.Global {
		t.Fatal("a caller with no branch grants was projected as GLOBAL")
	}
}

func TestOrderActivityAdapterCarriesTheTrailThroughUntouched(t *testing.T) {
	at := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	f := &fakeTrailSvc{
		found: true,
		summary: order.Summary{OrderID: "o-1", OrderNo: "#SO-4", Status: "PICKING",
			CustomerName: "Northgate Landscaping", BranchName: "Trenton", TotalCents: 512300, LineCount: 3},
		events: []order.OrderActivityEvent{
			{Kind: order.ActivityStatus, FromStatus: "CONFIRMED", ToStatus: "PICKING", Reason: "picker_started",
				ActorID: "a-1", ActorName: "Dana Wolfe", OccurredAt: at},
			{Kind: order.ActivityEdit, Field: "qty_entered", LineNo: 2, Before: "4", After: "6", OccurredAt: at},
		},
	}
	rd := NewOrderActivityReader(f)

	o, events, found, err := rd.ActivityFor(context.Background(), assist.Caller{Tier: 1, Global: true}, "SO-4")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if o.OrderID != "o-1" || o.OrderNo != "#SO-4" || o.CustomerName != "Northgate Landscaping" || o.TotalCents != 512300 {
		t.Fatalf("order = %+v", o)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	s := events[0]
	if s.Kind != assist.AuditKindStatus || s.FromStatus != "CONFIRMED" || s.ToStatus != "PICKING" ||
		s.Reason != "picker_started" || s.ActorName != "Dana Wolfe" || !s.At.Equal(at) {
		t.Fatalf("status event = %+v", s)
	}
	e := events[1]
	if e.Kind != assist.AuditKindEdit || e.Field != "qty_entered" || e.LineNo != 2 || e.Before != "4" || e.After != "6" {
		t.Fatalf("edit event = %+v", e)
	}
}

func TestOrderActivityAdapterNilServiceIsAnUnwiredSeam(t *testing.T) {
	if rd := NewOrderActivityReader(nil); rd != nil {
		t.Fatal("a nil service must yield a NIL seam, not a panic waiting for its first caller")
	}
}
