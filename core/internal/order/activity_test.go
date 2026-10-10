// activity_test.go, DIB-492/493 unit tests: the audit-row humanizer (table-driven over every action
// this surface renders or deliberately omits), the merged newest-first stream, the branch-scope
// authz on the read, and the notes-write validation/attribution rules, all against in-memory fakes
// (the history_test.go substrate); the pgx sides are covered in activity_integration_test.go.
package order

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - fakes -

// activityFakeStore is fakeStore plus the two activity capabilities (notes + document events), so
// the capability type-assertions in OrderActivityFor/AddOrderNote resolve in unit tests exactly as
// they do against the real *Repository.
type activityFakeStore struct {
	*fakeStore
	notes           []OrderNote
	keys            map[string]string // order|idempotency_key → note id (the 0161 unique index)
	lastDraft       NoteDraft
	docs            []DocEvent
	editCalls       int
	gotWithPayments bool
}

// actBase is the pinned test clock: every fake timestamp derives from it, so this file mixes NO
// real clock into its fixed-date fixtures (the DIB-427 dated-time-bomb rule).
var actBase = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func (f *activityFakeStore) InsertOrderNote(_ context.Context, d NoteDraft) (OrderNote, bool, error) {
	f.lastDraft = d
	if f.keys == nil {
		f.keys = map[string]string{}
	}
	if id, seen := f.keys[d.OrderID+"|"+d.IdempotencyKey]; seen {
		for _, n := range f.notes {
			if n.ID == id {
				return n, true, nil // idempotent replay
			}
		}
	}
	// Mint a UUID-shaped id (the real table's gen_random_uuid()) so downstream shape guards hold.
	id := fmt.Sprintf("%08d-0000-4000-9000-000000000000", len(f.notes)+1)
	n := OrderNote{
		ID: id, OrderID: d.OrderID, AuthorID: d.AuthorID,
		Channel: d.Channel, Body: d.Body, CreatedAt: actBase.Add(time.Duration(len(f.notes)) * time.Second),
	}
	f.notes = append(f.notes, n)
	f.keys[d.OrderID+"|"+d.IdempotencyKey] = id
	return n, false, nil
}

func (f *activityFakeStore) UpdateOrderNoteBody(_ context.Context, orderID, noteID, body, channel, editorID string) (OrderNote, error) {
	f.editCalls++
	for i, n := range f.notes {
		if n.OrderID == orderID && n.ID == noteID {
			n.Body = body
			if channel != "" {
				n.Channel = channel
			}
			n.EditedBy = editorID
			at := actBase.Add(time.Hour)
			n.EditedAt = &at
			f.notes[i] = n
			return n, nil
		}
	}
	return OrderNote{}, ErrNoteNotFound
}

func (f *activityFakeStore) ListOrderNotes(_ context.Context, orderID string) ([]OrderNote, error) {
	out := []OrderNote{}
	for _, n := range f.notes {
		if n.OrderID == orderID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *activityFakeStore) OrderDocumentEvents(_ context.Context, _ string, withPayments bool) ([]DocEvent, error) {
	f.gotWithPayments = withPayments
	return f.docs, nil
}

func newActivityStore(ds ...Detail) *activityFakeStore {
	return &activityFakeStore{fakeStore: newFakeStore(ds...)}
}

// entry builds an audit row for the fake trail.
func actEntry(id, action string, seq int64, at string, changes map[string]any) audit.Entry {
	if changes == nil {
		changes = map[string]any{}
	}
	return audit.Entry{ID: id, Action: action, EntityType: "order", EntityID: histOrder,
		Changes: changes, CreatedAt: at, Seq: seq}
}

// - the humanizer -

// Every row this surface renders must read as a person doing a thing, and the rows it must NOT
// render (HTTP verbs, edit-consequence columns, unknown actions) must be omitted, not shown raw.
func TestSummarizeAuditEntry_Table(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		changes map[string]any
		want    string // "" means the row must be OMITTED
	}{
		{"status move", audit.ActOrderStatusChanged,
			map[string]any{"old_status": "CONFIRMED", "new_status": "PICKING"},
			"moved the order from Confirmed to Picking"},
		{"multi-word status humanized", audit.ActOrderStatusChanged,
			map[string]any{"old_status": "READY", "new_status": "PARTIALLY_FULFILLED"},
			"moved the order from Ready to Partially fulfilled"},
		{"creation", audit.ActOrderStatusChanged,
			map[string]any{"old_status": "", "new_status": "CONFIRMED"},
			"created the order (Confirmed)"},
		{"creation from quote", audit.ActOrderStatusChanged,
			map[string]any{"old_status": "", "new_status": "DRAFT", "reason": "quote_converted"},
			"created the order from an accepted quote"},
		{"creation from reorder", audit.ActOrderStatusChanged,
			map[string]any{"old_status": "", "new_status": "DRAFT", "reason": "reorder_draft_created"},
			"started this order as a reorder draft"},
		{"cancel carries the human reason", audit.ActOrderStatusChanged,
			map[string]any{"old_status": "CONFIRMED", "new_status": "CANCELLED", "reason": "customer called it off"},
			"cancelled the order, customer called it off"},
		{"machine reason on a normal move is NOT surfaced", audit.ActOrderStatusChanged,
			map[string]any{"old_status": "CONFIRMED", "new_status": "PICKING", "reason": "picker_started"},
			"moved the order from Confirmed to Picking"},
		{"delivery date changed", audit.ActOrderChanged,
			map[string]any{"field": "requested_date", "before": "2026-08-01", "after": "2026-08-05"},
			"changed the requested delivery date from 2026-08-01 to 2026-08-05"},
		{"delivery date set", audit.ActOrderChanged,
			map[string]any{"field": "requested_date", "before": "", "after": "2026-08-05"},
			"set the requested delivery date to 2026-08-05"},
		{"delivery date cleared", audit.ActOrderChanged,
			map[string]any{"field": "requested_date", "before": "2026-08-01", "after": ""},
			"cleared the requested delivery date"},
		{"delivery instructions", audit.ActOrderChanged,
			map[string]any{"field": "delivery_instructions", "new_len": float64(42)},
			"updated the delivery instructions"},
		{"line qty", audit.ActOrderChanged,
			map[string]any{"field": "qty_entered", "line_no": float64(2), "before": "10", "after": "12.5"},
			"changed the quantity on line 2 from 10 to 12.5"},
		{"line added", audit.ActOrderChanged,
			map[string]any{"field": "line_added", "line_no": float64(3), "after": "prod-1"},
			"added line 3 to the order"},
		{"line removed", audit.ActOrderChanged,
			map[string]any{"field": "line_removed", "line_no": float64(3), "before": "prod-1"},
			"removed line 3 from the order"},
		{"change order", audit.ActOrderChanged,
			map[string]any{"purpose": "ORDER_CHANGE_ORDER", "change_no": "#CO-12"},
			"recorded change order #CO-12"},
		{"counter sale with tender", audit.ActSaleCompleted,
			map[string]any{"reason": "CASH"},
			"completed a counter sale (cash)"},
		{"counter sale, no tender recorded", audit.ActSaleCompleted, nil,
			"completed a counter sale"},
		// The deliberate omissions.
		{"coarse HTTP row is API framing, omitted", audit.ActHTTPPost,
			map[string]any{"method": "POST", "path": "/api/v1/orders/x"}, ""},
		{"edit-consequence column folded away", audit.ActOrderChanged,
			map[string]any{"field": "line_total_cents", "line_no": float64(2), "before": int64(1000), "after": int64(1250)}, ""},
		{"reprice consequence folded away", audit.ActOrderChanged,
			map[string]any{"field": "unit_price_cents", "line_no": float64(2), "before": int64(100), "after": int64(125)}, ""},
		{"reorder provenance row folded (its creation row tells the story)", audit.ActOrderChanged,
			map[string]any{"purpose": "PORTAL_REORDER", "source_order_id": histOrder, "lines": float64(3)}, ""},
		{"unknown action omitted, never rendered raw", "SOME_FUTURE_ACTION", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := summarizeAuditEntry(actEntry("e", tc.action, 1, "2026-08-20T12:00:00Z", tc.changes), true)
			if tc.want == "" {
				if ok {
					t.Fatalf("row must be omitted, got %q", got)
				}
				return
			}
			if !ok {
				t.Fatalf("row omitted, want %q", tc.want)
			}
			if got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

// No summary may carry an API/system framing, the client-facing wording rule, pinned as a scan
// over every rendered case rather than one example.
func TestSummarizeAuditEntry_NeverAPIFraming(t *testing.T) {
	rows := []audit.Entry{
		actEntry("1", audit.ActOrderStatusChanged, 1, "2026-08-20T12:00:00Z",
			map[string]any{"old_status": "CONFIRMED", "new_status": "PICKING"}),
		actEntry("2", audit.ActOrderChanged, 2, "2026-08-20T12:00:00Z",
			map[string]any{"field": "requested_date", "before": "a", "after": "b"}),
		actEntry("3", audit.ActSaleCompleted, 3, "2026-08-20T12:00:00Z", map[string]any{"reason": "ON_ACCOUNT"}),
	}
	for _, e := range rows {
		s, ok := summarizeAuditEntry(e, true)
		if !ok {
			t.Fatalf("expected a summary for %s", e.Action)
		}
		lower := strings.ToLower(s)
		for _, banned := range []string{"http", "post", "patch", "api", "endpoint", "payload", "_"} {
			if strings.Contains(lower, banned) {
				t.Errorf("summary %q for %s leaks system framing (%q)", s, e.Action, banned)
			}
		}
	}
}

// - the merged stream -

func activitySvc(store *activityFakeStore, trail *fakeTrail) *Service {
	return NewService(store).WithAuditTrail(trail).withCaller(testCallerFrom)
}

func TestOrderActivity_MergesNewestFirst(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	noteAt := time.Date(2026, 8, 20, 12, 30, 0, 0, time.UTC)
	store.notes = []OrderNote{{
		ID: "n1", OrderID: histOrder, AuthorID: histActor, AuthorName: "Tanya Ho",
		Channel: "PHONE", Body: "customer phoned about the flagstone", CreatedAt: noteAt,
	}}
	store.docs = []DocEvent{{
		ID: "invoice:i1:issued", Summary: "issued invoice INV-000042 for $1,250.00",
		ActorID: histActor, ActorName: "Tanya Ho",
		OccurredAt: time.Date(2026, 8, 20, 12, 45, 0, 0, time.UTC),
	}}
	trail := &fakeTrail{byEntity: map[string][]audit.Entry{
		"order|" + histOrder: {
			actEntry("a1", audit.ActOrderStatusChanged, 1, "2026-08-20T12:00:00Z",
				map[string]any{"old_status": "", "new_status": "CONFIRMED"}),
			actEntry("a2", audit.ActOrderStatusChanged, 2, "2026-08-20T13:00:00Z",
				map[string]any{"old_status": "CONFIRMED", "new_status": "PICKING"}),
			// A coarse middleware row that must NOT appear.
			actEntry("a3", audit.ActHTTPPost, 3, "2026-08-20T13:00:01Z",
				map[string]any{"method": "POST", "path": "/api/v1/orders/x/picking"}),
		},
		"pos.sale|" + histOrder: {
			actEntry("a4", audit.ActSaleCompleted, 4, "2026-08-20T12:10:00Z", map[string]any{"reason": "CASH"}),
		},
	}}
	svc := activitySvc(store, trail)
	ctx := ctxWith(context.Background(), globalStaff(histActor))

	out, err := svc.OrderActivityFor(ctx, histOrder, true)
	if err != nil {
		t.Fatalf("OrderActivityFor: %v", err)
	}
	ids := make([]string, 0, len(out.Rows))
	for _, r := range out.Rows {
		ids = append(ids, r.ID)
	}
	// Newest first: 13:00 status move, 12:45 invoice, 12:30 note, 12:10 sale, 12:00 creation.
	want := []string{"a2", "invoice:i1:issued", "n1", "a4", "a1"}
	if len(ids) != len(want) {
		t.Fatalf("rows = %v, want %v (the HTTP.* row must be absent)", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("rows = %v, want %v", ids, want)
		}
	}
	// The note row carries the note shape.
	note := out.Rows[2]
	if note.Kind != "note" || note.Channel != "PHONE" || note.Body == "" {
		t.Errorf("note row = %+v, want kind=note channel=PHONE with a body", note)
	}
	if note.Summary != "note taken via phone call" {
		t.Errorf("note summary = %q, want %q", note.Summary, "note taken via phone call")
	}
	// Event rows are kind=event with a human sentence.
	if out.Rows[0].Kind != "event" || out.Rows[0].Summary != "moved the order from Confirmed to Picking" {
		t.Errorf("top row = %+v, want the 13:00 status move", out.Rows[0])
	}
}

// Same-instant rows (one transaction) order by the audit seq, newest-on-top.
func TestOrderActivity_SameInstantOrdersBySeq(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	at := "2026-08-20T12:00:00Z"
	trail := &fakeTrail{byEntity: map[string][]audit.Entry{
		"order|" + histOrder: {
			actEntry("first", audit.ActOrderStatusChanged, 10, at,
				map[string]any{"old_status": "", "new_status": "DRAFT"}),
			actEntry("second", audit.ActOrderStatusChanged, 11, at,
				map[string]any{"old_status": "DRAFT", "new_status": "CONFIRMED"}),
		},
	}}
	svc := activitySvc(store, trail)
	out, err := svc.OrderActivityFor(ctxWith(context.Background(), globalStaff(histActor)), histOrder, true)
	if err != nil {
		t.Fatalf("OrderActivityFor: %v", err)
	}
	if len(out.Rows) != 2 || out.Rows[0].ID != "second" || out.Rows[1].ID != "first" {
		t.Fatalf("same-instant rows = %+v, want seq-descending (second, first)", out.Rows)
	}
}

// An order with nothing recorded answers an EMPTY stream, a calm empty state, never an error.
func TestOrderActivity_EmptyIsNotAnError(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "DRAFT"))
	store.branches = map[string]string{histOrder: histBranchA}
	svc := activitySvc(store, &fakeTrail{byEntity: map[string][]audit.Entry{}})
	out, err := svc.OrderActivityFor(ctxWith(context.Background(), globalStaff(histActor)), histOrder, true)
	if err != nil {
		t.Fatalf("empty activity must not error: %v", err)
	}
	if out.Rows == nil || len(out.Rows) != 0 {
		t.Fatalf("rows = %#v, want an empty (non-nil) slice", out.Rows)
	}
}

// The horizontal-access boundary: branch-restricted staff cannot read another branch's order
// activity by iterating ids, and a missing order never leaks existence.
func TestOrderActivity_BranchScope(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	svc := activitySvc(store, &fakeTrail{byEntity: map[string][]audit.Entry{}})

	if _, err := svc.OrderActivityFor(
		ctxWith(context.Background(), staffAt(histActor, histBranchB)), histOrder, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-branch read err = %v, want ErrForbidden", err)
	}
	if _, err := svc.OrderActivityFor(
		ctxWith(context.Background(), globalStaff(histActor)), "99999999-9999-4999-8999-999999999999", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing order err = %v, want ErrNotFound", err)
	}
	if _, err := svc.OrderActivityFor(context.Background(), histOrder, true); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unauthenticated err = %v, want ErrUnauthorized", err)
	}
}

// - notes writes -

func TestAddOrderNote_AuthorIsTheSessionCaller(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	svc := activitySvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt(histActor, histBranchA))

	n, err := svc.AddOrderNote(ctx, histOrder, "phone", "customer phoned, hold until the 14th", "key-1")
	if err != nil {
		t.Fatalf("AddOrderNote: %v", err)
	}
	if store.lastDraft.AuthorID != histActor {
		t.Fatalf("author = %q, want the session caller %q, the author must NEVER be client-supplied",
			store.lastDraft.AuthorID, histActor)
	}
	if store.lastDraft.Channel != "PHONE" {
		t.Fatalf("channel = %q, want normalized PHONE", store.lastDraft.Channel)
	}
	if n.Replayed {
		t.Fatal("first write flagged as a replay")
	}

	// The SAME key replays the SAME note; a fresh key writes a second one.
	n2, err := svc.AddOrderNote(ctx, histOrder, "phone", "customer phoned, hold until the 14th", "key-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !n2.Replayed || n2.ID != n.ID {
		t.Fatalf("replay = %+v, want the original note flagged Replayed", n2)
	}
	if _, err := svc.AddOrderNote(ctx, histOrder, "email", "second note", "key-2"); err != nil {
		t.Fatalf("fresh key: %v", err)
	}
	if got := len(store.notes); got != 2 {
		t.Fatalf("stored notes = %d, want 2 (one per key)", got)
	}
}

func TestAddOrderNote_Validation(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	svc := activitySvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt(histActor, histBranchA))

	cases := []struct {
		name               string
		channel, body, key string
	}{
		{"unknown channel", "FAX", "hello", "k"},
		{"blank body", "PHONE", "   ", "k"},
		{"over-long body", "PHONE", strings.Repeat("x", maxNoteLen+1), "k"},
		{"missing idempotency key", "PHONE", "hello", ""},
		{"over-long idempotency key", "PHONE", "hello", strings.Repeat("k", 129)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.AddOrderNote(ctx, histOrder, tc.channel, tc.body, tc.key)
			var br httpx.BadRequestError
			if !errors.As(err, &br) {
				t.Fatalf("err = %v, want BadRequestError", err)
			}
		})
	}
	// The refusals are walkthrough-style: they say what is missing and what to do.
	_, err := svc.AddOrderNote(ctx, histOrder, "FAX", "hello", "k")
	if err == nil || !strings.Contains(err.Error(), "phone, email, in_person or other") {
		t.Fatalf("channel refusal %q must name the accepted channels", err)
	}
	// "in-person" and mixed case normalize onto the vocabulary rather than being refused.
	if _, err := svc.AddOrderNote(ctx, histOrder, "In-Person", "spoke at the counter", "k-norm"); err != nil {
		t.Fatalf("normalized channel refused: %v", err)
	}
	if store.lastDraft.Channel != "IN_PERSON" {
		t.Fatalf("channel = %q, want IN_PERSON", store.lastDraft.Channel)
	}
}

func TestAddOrderNote_BranchScopeAndWiring(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	svc := activitySvc(store, nil)

	// Cross-branch write refused before any insert.
	if _, err := svc.AddOrderNote(
		ctxWith(context.Background(), staffAt(histActor, histBranchB)),
		histOrder, "phone", "x", "k"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-branch note err = %v, want ErrForbidden", err)
	}
	if len(store.notes) != 0 {
		t.Fatal("a refused note must not be stored")
	}

	// A store with no 0161 capability (the bare fakeStore) reports itself unconfigured, the
	// walkthrough-style 503, never a panic or a silent drop.
	bare := NewService(newFakeStore(mkDetail(histOrder, histCust, "CONFIRMED"))).withCaller(testCallerFrom)
	if _, err := bare.AddOrderNote(
		ctxWith(context.Background(), globalStaff(histActor)),
		histOrder, "phone", "x", "k"); !errors.Is(err, ErrNotesNotConfigured) {
		t.Fatalf("unwired notes err = %v, want ErrNotesNotConfigured", err)
	}
}

func TestEditOrderNote_StampsEditorAndGuards(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	svc := activitySvc(store, nil)
	author := ctxWith(context.Background(), staffAt(histActor, histBranchA))
	editorID := "44444444-4444-4444-8444-444444444444"
	editor := ctxWith(context.Background(), staffAt(editorID, histBranchA))

	n, err := svc.AddOrderNote(author, histOrder, "phone", "original wording", "k-edit")
	if err != nil {
		t.Fatalf("seed note: %v", err)
	}

	got, err := svc.EditOrderNote(editor, histOrder, n.ID, "corrected wording", "")
	if err != nil {
		t.Fatalf("EditOrderNote: %v", err)
	}
	if got.Body != "corrected wording" || got.EditedBy != editorID || got.EditedAt == nil {
		t.Fatalf("edited note = %+v, want the new body + edited-by %s + an edited-at stamp", got, editorID)
	}
	if got.AuthorID != histActor {
		t.Fatalf("author changed on edit: %q, want %q (attribution is append-style)", got.AuthorID, histActor)
	}

	// A malformed or unknown note id answers as missing (no ::uuid blowup, no cross-order reach).
	if _, err := svc.EditOrderNote(editor, histOrder, "not-a-uuid", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed note id err = %v, want ErrNotFound", err)
	}
	if _, err := svc.EditOrderNote(editor, histOrder,
		"99999999-9999-4999-8999-999999999999", "x", ""); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("unknown note id err = %v, want ErrNoteNotFound", err)
	}
	// A blank correction is refused with instructions, not stored.
	if _, err := svc.EditOrderNote(editor, histOrder, n.ID, "   ", ""); !isBadRequest(err) {
		t.Fatalf("blank correction err = %v, want BadRequestError", err)
	}
}

// Ruling 87: the stream asks the store for payments only for a caller who holds the trail, and the
// tender named on a counter sale goes with them (the sale row itself stays).
func TestOrderActivity_PaymentsReachOnlyACallerWhoAsksForThem(t *testing.T) {
	store := newActivityStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	store.branches = map[string]string{histOrder: histBranchA}
	trail := &fakeTrail{byEntity: map[string][]audit.Entry{
		"pos.sale|" + histOrder: {
			actEntry("a4", audit.ActSaleCompleted, 4, "2026-08-20T12:10:00Z", map[string]any{"reason": "CASH"}),
		},
	}}
	svc := activitySvc(store, trail)
	ctx := ctxWith(context.Background(), globalStaff(histActor))

	with, err := svc.OrderActivityFor(ctx, histOrder, true)
	if err != nil || !store.gotWithPayments || with.Rows[0].Summary != "completed a counter sale (cash)" {
		t.Fatalf("with the trail: err %v, asked %v, row %+v", err, store.gotWithPayments, with.Rows)
	}
	without, err := svc.OrderActivityFor(ctx, histOrder, false)
	if err != nil || store.gotWithPayments || len(without.Rows) != 1 || without.Rows[0].Summary != "completed a counter sale" {
		t.Fatalf("without the trail: err %v, asked %v, rows %+v", err, store.gotWithPayments, without.Rows)
	}
}
