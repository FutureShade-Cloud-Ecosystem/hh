package order

// activity_test.go, DIB-494. The merged order trail, without a database.
//
// The two properties that matter most here are NEGATIVE ones:
//
//	1. the read shows ONLY what the two shipped panels show, the coarse HTTP rows and the
//	   pre-DIB-175 field-less ORDER_CHANGED rows never surface (TestActivityByNumber_ShowsOnly…);
//	2. the read is fenced by SummaryByNumber's authority, so a cross-branch order, by number OR by
//	   pasted uuid, is found=false and its trail is NEVER READ (…CrossBranch…).
//
// Everything else (ordering by seq, actor names, the empty trail) follows the shape of
// history_test.go and edit_history's reasoning.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// countingTrail wraps fakeTrail (history_test.go) and counts reads, so the fence tests can assert
// the trail was never touched for an order the caller may not see.
type countingTrail struct {
	fakeTrail
	reads int
}

func (f *countingTrail) ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error) {
	f.reads++
	return f.fakeTrail.ReopenTransaction(ctx, entityType, entityID)
}

// namedSummaryFake is summaryFake plus an ActorNames resolver, so NewService binds the names seam
// exactly as it does for the real *Repository.
type namedSummaryFake struct {
	summaryFake
	names map[string]string
}

func (f *namedSummaryFake) ActorNames(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if n, ok := f.names[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

func activityEntry(action string, seq int64, at string, changes map[string]any, actor string) audit.Entry {
	return audit.Entry{Action: action, EntityType: "order", EntityID: "o-1",
		ActorID: actor, Changes: changes, CreatedAt: at, Seq: seq}
}

func aTrail() []audit.Entry {
	return []audit.Entry{
		// deliberately OUT of causal order in the slice, seq is the sort key, not position, and
		// the edit deliberately SHARES the transition's wall-clock (created_at is transaction time
		// and ties within a tx; B2-1), so a timestamp sort cannot reproduce the causal order.
		activityEntry(audit.ActOrderStatusChanged, 30, "2026-08-19T15:00:00Z",
			map[string]any{"old_status": "CONFIRMED", "new_status": "PICKING", "reason": "picker_started"}, histActor),
		activityEntry(audit.ActOrderChanged, 20, "2026-08-19T15:00:00Z",
			map[string]any{"field": "qty_entered", "line_no": float64(2), "before": "4", "after": "6"}, histActor),
		activityEntry(audit.ActOrderStatusChanged, 10, "2026-08-17T09:00:00Z",
			map[string]any{"old_status": "", "new_status": "CONFIRMED"}, ""),
		// the rows the two shipped panels do NOT show, and this read must not either:
		activityEntry("HTTP.POST", 5, "2026-08-17T09:00:00Z", map[string]any{"path": "/api/v1/orders"}, histActor),
		activityEntry(audit.ActOrderChanged, 15, "2026-08-17T10:00:00Z",
			map[string]any{"note": "a pre-DIB-175 row with no field key"}, histActor),
		activityEntry(audit.ActPaymentReceived, 25, "2026-08-18T12:00:00Z", map[string]any{"amount_cents": float64(1)}, histActor),
	}
}

func trailActivitySvc(f *namedSummaryFake, trail AuditTrailReader) *Service {
	f.fakeStore = &fakeStore{}
	svc := NewService(f)
	if trail != nil {
		svc.WithAuditTrail(trail)
	}
	return svc
}

func TestActivityByNumber_MergesTheTwoPanelsInCausalOrder(t *testing.T) {
	f := &namedSummaryFake{
		summaryFake: summaryFake{summary: aSummary(), resolved: "o-1"},
		names:       map[string]string{histActor: "Dana Wolfe"},
	}
	trail := &countingTrail{fakeTrail: fakeTrail{byEntity: map[string][]audit.Entry{"order|o-1": aTrail()}}}
	svc := trailActivitySvc(f, trail)

	sum, events, found, err := svc.ActivityByNumber(context.Background(), ReadScope{BranchIDs: []string{"b-trenton"}}, "SO-4")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if sum.OrderNo != "#SO-4" {
		t.Fatalf("summary = %+v", sum)
	}
	// ONLY the panel-visible rows: two status transitions and one field edit. The coarse HTTP row,
	// the field-less ORDER_CHANGED and the payment action are all skipped.
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (the panels' union): %+v", len(events), events)
	}
	// Causal order by seq (10, 20, 30), not slice order and not wall-clock strings.
	if events[0].Kind != ActivityStatus || events[0].ToStatus != "CONFIRMED" || events[0].FromStatus != "" {
		t.Fatalf("events[0] = %+v, want the creation transition first", events[0])
	}
	if events[1].Kind != ActivityEdit || events[1].Field != "qty_entered" || events[1].LineNo != 2 ||
		events[1].Before != "4" || events[1].After != "6" {
		t.Fatalf("events[1] = %+v, want the qty edit second", events[1])
	}
	if events[2].Kind != ActivityStatus || events[2].FromStatus != "CONFIRMED" || events[2].ToStatus != "PICKING" ||
		events[2].Reason != "picker_started" {
		t.Fatalf("events[2] = %+v, want CONFIRMED→PICKING last", events[2])
	}
	// Actor names resolved best-effort; the unattributed creation stays unattributed.
	if events[1].ActorName != "Dana Wolfe" {
		t.Fatalf("actor name = %q, want the display name", events[1].ActorName)
	}
	if events[0].ActorName != "" || events[0].ActorID != "" {
		t.Fatalf("the system creation row grew an actor: %+v", events[0])
	}
	if events[2].OccurredAt.IsZero() || !events[2].OccurredAt.Equal(time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("occurred_at = %v", events[2].OccurredAt)
	}
}

// The fence, both ways a person can point at another branch's order. It is SummaryByNumber's fence
// (summary_test.go proves its two halves); this test proves the TRAIL sits behind it, found=false
// AND zero trail reads, so a refused order leaks nothing, not even to a log line.
func TestActivityByNumber_ACrossBranchOrdersTrailIsNeverRead(t *testing.T) {
	kingston := aSummary()
	kingston.BranchID, kingston.BranchName = "b-kingston", "Kingston"
	trail := &countingTrail{fakeTrail: fakeTrail{byEntity: map[string][]audit.Entry{"order|o-1": aTrail()}}}
	f := &namedSummaryFake{summaryFake: summaryFake{summary: kingston}}
	svc := trailActivitySvc(f, trail)

	_, events, found, err := svc.ActivityByNumber(context.Background(),
		ReadScope{BranchIDs: []string{"b-trenton"}}, "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatalf("a cross-branch order is an ANSWER (not found), not an error: %v", err)
	}
	if found || events != nil {
		t.Fatalf("a Trenton-scoped caller read a Kingston order's trail: found=%v events=%v", found, events)
	}
	if trail.reads != 0 {
		t.Fatalf("the audit trail was read %d times for an order the caller may not see", trail.reads)
	}
}

func TestActivityByNumber_AnUnwiredTrailIsAnEmptyTrailNotAnError(t *testing.T) {
	f := &namedSummaryFake{summaryFake: summaryFake{summary: aSummary(), resolved: "o-1"}}
	svc := trailActivitySvc(f, nil)

	_, events, found, err := svc.ActivityByNumber(context.Background(), ReadScope{Global: true}, "SO-4")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if events == nil || len(events) != 0 {
		t.Fatalf("an unwired seam must read as an EMPTY trail, got %v", events)
	}
}
