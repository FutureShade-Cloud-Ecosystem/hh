package payment

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fakeAttemptStore implements the payment handler's store seam with no
// database: the config methods answer zero values, the attempt read and
// move methods answer planted rows and record every call. The list tests
// and the resolve tests (handler_manager_resolve_test.go) share it.
//
// The method shapes are the *Store shapes the seam names (ListOpen, Resolve,
// getByID), so the fake and the real store are interchangeable behind the
// handler.
type fakeAttemptStore struct {
	// list answers ListOpen; lastQuery records the query the handler handed
	// over, so a test can pin the state/from/to parsing.
	list      []Attempt
	listErr   error
	lastQuery ListQuery
	listCalls int

	// byID answers getByID.
	byID map[string]Attempt

	// resolve answers Resolve; resolveCalls records every call with the
	// arguments the handler passed, in order.
	resolveResult Attempt
	resolveMoved  bool
	resolveErr    error
	resolveCalls  []resolveCall
}

type resolveCall struct {
	id, resolution, note, actor string
}

func (f *fakeAttemptStore) Status(context.Context) (bool, string, string, string, error) {
	return false, "", "", "", nil
}

func (f *fakeAttemptStore) Configure(context.Context, string, string, string, string, int64, bool, map[string]string, string) error {
	return nil
}

func (f *fakeAttemptStore) ListOpen(_ context.Context, q ListQuery) ([]Attempt, error) {
	f.listCalls++
	f.lastQuery = q
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.list, nil
}

func (f *fakeAttemptStore) getByID(_ context.Context, id string) (Attempt, bool, error) {
	a, ok := f.byID[id]
	return a, ok, nil
}

func (f *fakeAttemptStore) Resolve(_ context.Context, id, resolution, note, actor string) (Attempt, bool, error) {
	f.resolveCalls = append(f.resolveCalls, resolveCall{id: id, resolution: resolution, note: note, actor: actor})
	if f.resolveErr != nil {
		return Attempt{}, false, f.resolveErr
	}
	return f.resolveResult, f.resolveMoved, nil
}

// newManagerTestMux registers the payment routes with a handler whose user
// linker maps every platform user to one users.id, so a test can pin that
// the audited close carries the caller's id. The plain newTestMux in
// handler_test.go stamps authority only (no linker), which is the shape its
// config tests need.
func newManagerTestMux(t *testing.T, store configStore) (*testkit.StaffGuards, *http.ServeMux) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	h := &Handler{store: store, log: slog.Default()}
	h.WithUserLinker(func(_ context.Context, _ string) (string, bool) { return "users-1111", true })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return sg, mux
}

// managerAuth sets the tier 2 authority RequireManager admits.
func managerAuth(sg *testkit.StaffGuards) {
	sg.SetAuthority(identity.StaffAuthority{RoleID: "manager", Tier: 2, Scope: "global"})
}

// cardAttemptRow is the list row's wire shape, decoded locally so the test
// reads the response the route actually serves, never a Go struct the fix
// could quietly drift away from.
type cardAttemptRow struct {
	Ref           string    `json:"ref"`
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	AmountCents   int64     `json:"amount_cents"`
	BranchID      string    `json:"branch_id"`
	TillSessionID string    `json:"till_session_id"`
	CreatedAt     time.Time `json:"created_at"`
	SaleKey       string    `json:"sale_key"`
	ChargeRef     string    `json:"charge_ref"`
	ReversalRef   string    `json:"reversal_ref"`
	LastError     string    `json:"last_error"`
}

// listFixture is one planted attempt the shape and RBAC legs read back. The
// id's head is the short ref the manager-facing answers carry. The timestamp
// rides the real clock (an hour behind now): the shape leg only proves the
// value round-trips, and a pinned calendar instant beside the query leg's
// time.Now would be the dated time bomb the testaudit tripwire names.
var listFixture = Attempt{
	ID:            "abc12345-0000-0000-0000-000000000001",
	LegKey:        "leg-0001",
	SaleKey:       "9f8e7d6c-0000-0000-0000-000000000009",
	LegSeq:        1,
	AmountCents:   12999,
	BranchID:      "b1b1b1b1-0000-0000-0000-00000000000b",
	TillSessionID: "t1t1t1t1-0000-0000-0000-00000000000t",
	Status:        AttemptUnknown,
	ProcessorRef:  "trans_abc123",
	LastError:     "the processor answer was unreadable",
	CreatedAt:     time.Now().Add(-time.Hour),
}

func TestCardAttempts_List_RBAC(t *testing.T) {
	fake := &fakeAttemptStore{list: []Attempt{listFixture}}
	sg, mux := newManagerTestMux(t, fake)

	// Unauthenticated: the staff gate's 401.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/payments/card-attempts", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list = %d, want 401", rec.Code)
	}

	// A tier 3 caller is refused the manager route: the RBAC 403.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "cashier", Tier: 3, Scope: "branch"})
	if rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("cashier list = %d, want 403", rec.Code)
	}

	// A tier 2 caller passes RequireManager: 200 with the rows.
	managerAuth(sg)
	rec = doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("manager list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCardAttempts_List_RowShape(t *testing.T) {
	reversed := listFixture
	reversed.ID = "def67890-0000-0000-0000-000000000002"
	reversed.Status = AttemptVoidDeclined
	reversed.ReversalRef = "reversal_def678"
	reversed.ProcessorRef = "trans_def678"
	fake := &fakeAttemptStore{list: []Attempt{listFixture, reversed}}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)

	rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("manager list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var rows []cardAttemptRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("the body must be a JSON array of attempt rows: %v (%s)", err, rec.Body.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (%s)", len(rows), rec.Body.String())
	}
	first := rows[0]
	wantRef := listFixture.ID[:8]
	if first.Ref != wantRef {
		t.Fatalf("first row ref = %q, want the id's short form %q", first.Ref, wantRef)
	}
	if first.ID != listFixture.ID {
		t.Fatalf("first row id = %q, want %q (the resolve path parameter)", first.ID, listFixture.ID)
	}
	if first.Status != AttemptUnknown {
		t.Fatalf("first row status = %q, want %q", first.Status, AttemptUnknown)
	}
	if first.AmountCents != listFixture.AmountCents {
		t.Fatalf("first row amount_cents = %d, want %d", first.AmountCents, listFixture.AmountCents)
	}
	if first.BranchID != listFixture.BranchID || first.TillSessionID != listFixture.TillSessionID {
		t.Fatalf("first row branch/till = %q/%q, want %q/%q", first.BranchID, first.TillSessionID, listFixture.BranchID, listFixture.TillSessionID)
	}
	if !first.CreatedAt.Equal(listFixture.CreatedAt) {
		t.Fatalf("first row created_at = %v, want %v", first.CreatedAt, listFixture.CreatedAt)
	}
	if first.SaleKey != listFixture.SaleKey {
		t.Fatalf("first row sale_key = %q, want %q", first.SaleKey, listFixture.SaleKey)
	}
	if first.ChargeRef != listFixture.ProcessorRef {
		t.Fatalf("first row charge_ref = %q, want %q", first.ChargeRef, listFixture.ProcessorRef)
	}
	if first.LastError != listFixture.LastError {
		t.Fatalf("first row last_error = %q, want %q", first.LastError, listFixture.LastError)
	}
	second := rows[1]
	if second.Status != AttemptVoidDeclined {
		t.Fatalf("second row status = %q, want %q", second.Status, AttemptVoidDeclined)
	}
	if second.ReversalRef != reversed.ReversalRef {
		t.Fatalf("second row reversal_ref = %q, want %q", second.ReversalRef, reversed.ReversalRef)
	}
}

// The route parses state, from and to itself: state=open is the needs-a-person
// filter, from and to are RFC3339, and a missing from defaults to the seven
// day window so the date arm of the list is bounded whatever the caller sends.
func TestCardAttempts_List_QueryParsing(t *testing.T) {
	fake := &fakeAttemptStore{}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)

	// No query at all: not open only, from defaulted to the seven day window,
	// to left open.
	rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("bare list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if fake.listCalls != 1 {
		t.Fatalf("ListOpen calls = %d, want 1", fake.listCalls)
	}
	if fake.lastQuery.OpenOnly {
		t.Fatal("a bare list must not set OpenOnly")
	}
	if want := time.Now().Add(-7 * 24 * time.Hour); fake.lastQuery.From.Sub(want) > time.Minute || want.Sub(fake.lastQuery.From) > time.Minute {
		t.Fatalf("bare list from = %v, want the seven day window behind now (%v)", fake.lastQuery.From, want)
	}
	if !fake.lastQuery.To.IsZero() {
		t.Fatalf("bare list to = %v, want zero (open ended)", fake.lastQuery.To)
	}

	// state=open is the needs-a-person filter.
	if rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", ""); rec.Code != http.StatusOK {
		t.Fatalf("state=open list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if !fake.lastQuery.OpenOnly {
		t.Fatal("state=open must set OpenOnly")
	}

	// An explicit from and to ride through as RFC3339. The instants are
	// computed off the real clock and formatted, never pinned: the leg
	// proves the parse round-trip, not any calendar fact.
	from := time.Now().Add(-24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	to := time.Now().Add(24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	if rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open&from="+from+"&to="+to, ""); rec.Code != http.StatusOK {
		t.Fatalf("ranged list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	wantFrom, _ := time.Parse(time.RFC3339, from)
	wantTo, _ := time.Parse(time.RFC3339, to)
	if !fake.lastQuery.From.Equal(wantFrom) || !fake.lastQuery.To.Equal(wantTo) {
		t.Fatalf("ranged list from/to = %v/%v, want %v/%v", fake.lastQuery.From, fake.lastQuery.To, wantFrom, wantTo)
	}

	// A malformed from is the client's error, refused before the store.
	fake.listCalls = 0
	if rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?from=notatime", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed from = %d, want 400", rec.Code)
	}
	if fake.listCalls != 0 {
		t.Fatalf("ListOpen calls after a refused from = %d, want 0", fake.listCalls)
	}
}

// The list body never carries a leg key, a customer id or a note: the row is
// the manager's reconciliation view, not the row's every column (the design's
// body rule for operator items, applied to the list).
func TestCardAttempts_List_BodyIsTheSummaryOnly(t *testing.T) {
	fake := &fakeAttemptStore{list: []Attempt{listFixture}}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)

	rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("manager list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leak := range []string{listFixture.LegKey, "leg_key", "resolution_note", "resolved_by", "created_by"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the list body must not carry %q: %s", leak, body)
		}
	}
}
