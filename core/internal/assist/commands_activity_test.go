package assist

// commands_activity_test.go, DIB-494. The order.activity command over an in-memory trail.
//
// Same rules as every command test in this package: no database, no network, the fake RECORDS THE
// CALLER IT WAS HANDED (the pass-through assertion is the only way to check "runs as the asking
// user" from outside), and the empty states are asserted as ANSWERS, never as faults.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeActivity is the OrderActivityReader double.
type fakeActivity struct {
	order  OrderRecord
	events []OrderAuditEvent
	found  bool
	err    error

	gotCaller Caller
	gotRef    string
	calls     int
}

func (f *fakeActivity) ActivityFor(_ context.Context, c Caller, ref string) (OrderRecord, []OrderAuditEvent, bool, error) {
	f.gotCaller, f.gotRef, f.calls = c, ref, f.calls+1
	return f.order, f.events, f.found, f.err
}

func activityAt(day int) time.Time { return time.Date(2026, 8, day, 10, 0, 0, 0, time.UTC) }

func seededActivity() *fakeActivity {
	return &fakeActivity{
		order: OrderRecord{OrderID: "8f14e45f-ceea-467a-9b1e-1f0e2d4a77c1", OrderNo: "SO-4",
			Status: "PICKING", CustomerName: "Northgate Landscaping"},
		found: true,
		events: []OrderAuditEvent{ // oldest → newest, as the seam delivers them
			{Kind: AuditKindStatus, FromStatus: "", ToStatus: "CONFIRMED", At: activityAt(17)},
			{Kind: AuditKindEdit, Field: "unit_price_cents", LineNo: 2, Before: "125000", After: "118000",
				ActorID: "33333333-3333-4333-8333-333333333333", ActorName: "Dana Wolfe", At: activityAt(18)},
			{Kind: AuditKindStatus, FromStatus: "CONFIRMED", ToStatus: "PICKING", Reason: "picker_started",
				ActorName: "Sam Chu", At: activityAt(19)},
		},
	}
}

func activityRegistry(f *fakeActivity) *Registry {
	return NewRegistry(Deps{Activity: f}, nil)
}

func TestOrderActivityAnswersNewestFirstWithWhoAndWhen(t *testing.T) {
	f := seededActivity()
	reg := activityRegistry(f)

	res := mustRun(t, reg, frontline(), "order.activity", Args{"order_no": "SO-4"})
	if res.Empty {
		t.Fatal("a real trail answered as empty")
	}
	// The headline answers "what was the last change" without opening a single card.
	for _, want := range []string{"3 recorded changes on SO-4", "most recent", "status moving to Picking", "Sam Chu", "19 Aug 2026"} {
		if !strings.Contains(res.Headline, want) {
			t.Fatalf("headline %q missing %q", res.Headline, want)
		}
	}
	if len(res.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(res.Items))
	}
	// NEWEST FIRST, the question under every phrasing of this question is "what happened LAST".
	if !strings.Contains(res.Items[0].Title, "Confirmed → Picking") {
		t.Fatalf("items[0] = %q, want the newest change first", res.Items[0].Title)
	}
	// The creation transition reads as what it is, not as a broken "- → CONFIRMED" cell.
	if res.Items[2].Title != "Status set to Confirmed" {
		t.Fatalf("items[2] = %q", res.Items[2].Title)
	}
	// The money edit renders as MONEY, with its line, from → to, the same figure the order screen
	// shows, through the one formatter.
	edit := res.Items[1]
	if edit.Title != "Unit price (line 2)" {
		t.Fatalf("edit title = %q", edit.Title)
	}
	wantFields := map[string]string{"From": "$1,250.00", "To": "$1,180.00", "By": "Dana Wolfe", "When": "18 Aug 2026"}
	for _, fl := range edit.Fields {
		if want, ok := wantFields[fl.Label]; ok && fl.Value != want {
			t.Fatalf("edit field %s = %q, want %q", fl.Label, fl.Value, want)
		}
	}
	// The unattributed creation is "system", exactly as the order screens render it.
	if v := fieldValue(res.Items[2], "By"); v != "system" {
		t.Fatalf("creation By = %q, want system", v)
	}
	// DIB-465: the answer links to THE ORDER, named as the order.
	if res.Ref == nil || res.Ref.Kind != RefOrder || res.Ref.ID != f.order.OrderID || res.Ref.Label != "SO-4" {
		t.Fatalf("ref = %+v", res.Ref)
	}
}

func TestOrderActivityEmptyTrailIsAnAnswerNotAFault(t *testing.T) {
	f := seededActivity()
	f.events = nil
	reg := activityRegistry(f)

	res := mustRun(t, reg, frontline(), "order.activity", Args{"order_no": "SO-4"})
	if !res.Empty {
		t.Fatal("an order that has never moved is an ordinary EMPTY answer")
	}
	if !strings.Contains(res.Headline, "Nothing has been recorded on SO-4") {
		t.Fatalf("headline = %q", res.Headline)
	}
	if res.Note == "" {
		t.Fatal("the empty answer must say what would change it, that sentence is the whole DIB-443 lesson")
	}
	// Empty, and still linked: the order resolved, and "well, what IS it" is the next question.
	if res.Ref == nil || res.Ref.Kind != RefOrder {
		t.Fatalf("ref = %+v", res.Ref)
	}
}

func TestOrderActivityUnknownOrderSpeaksOrderLookupsWords(t *testing.T) {
	f := &fakeActivity{found: false}
	reg := activityRegistry(f)

	res := mustRun(t, reg, frontline(), "order.activity", Args{"order_no": "SO-9999"})
	if !res.Empty {
		t.Fatal("an unknown number is an answer, not a fault")
	}
	// The SAME sentence order.lookup says for the same situation, one wording per situation.
	if !strings.Contains(res.Headline, "I can't find an order numbered SO-9999") {
		t.Fatalf("headline = %q", res.Headline)
	}
	if !strings.Contains(res.Note, "branch you don't have access to") {
		t.Fatalf("note = %q, the honest half of the sentence is missing", res.Note)
	}
	if res.Ref != nil {
		t.Fatal("an unresolved order must not carry a ref")
	}
}

func TestOrderActivityRunsAsTheAskingUserAndRefusesTheUnassigned(t *testing.T) {
	f := seededActivity()
	reg := activityRegistry(f)

	who := frontline()
	mustRun(t, reg, who, "order.activity", Args{"order_no": "SO-4"})
	if f.gotCaller.UserID != who.UserID || f.gotCaller.Tier != who.Tier {
		t.Fatalf("the read ran as %+v, not as the asking user", f.gotCaller)
	}
	if len(f.gotCaller.BranchIDs) != 1 || f.gotCaller.BranchIDs[0] != "b-kel" {
		t.Fatalf("branch grants did not travel: %+v", f.gotCaller)
	}

	// Tier 0 is refused BEFORE the seam is reached, unknown authority is not frontline authority.
	before := f.calls
	_, err := reg.Run(context.Background(), unassigned(), "order.activity", Args{"order_no": "SO-4"})
	wantErrIs(t, err, ErrRefused)
	if f.calls != before {
		t.Fatal("a refused caller's request reached the read seam")
	}
}

func TestOrderActivityTruncatesWithAnHonestNote(t *testing.T) {
	f := seededActivity()
	f.events = nil
	for i := 0; i < maxItems+2; i++ {
		f.events = append(f.events, OrderAuditEvent{
			Kind: AuditKindEdit, Field: "requested_date", Before: "2026-08-01", After: "2026-08-02", At: activityAt(1 + i%27),
		})
	}
	reg := activityRegistry(f)

	res := mustRun(t, reg, frontline(), "order.activity", Args{"order_no": "SO-4"})
	if len(res.Items) != maxItems {
		t.Fatalf("got %d items, want the %d-item bound", len(res.Items), maxItems)
	}
	if !strings.Contains(res.Note, "most recent 10 of 12") {
		t.Fatalf("note = %q, a truncated trail must say it was truncated", res.Note)
	}
	if !strings.Contains(res.Headline, "12 recorded changes") {
		t.Fatalf("headline = %q, the count covers the whole trail, not the visible page", res.Headline)
	}
}

func TestOrderActivityWithoutANumberAsksForOne(t *testing.T) {
	reg := activityRegistry(seededActivity())
	_, err := reg.Run(context.Background(), frontline(), "order.activity", Args{})
	if err == nil || !strings.Contains(err.Error(), "order number") {
		t.Fatalf("err = %v, want the command's own which-order message", err)
	}
}
