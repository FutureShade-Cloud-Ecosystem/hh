package audit

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// -- fakes (no DB, no live HTTP) ----------------------------------------------

// fakeStore is the in-memory store the middleware/Recorder write to in unit tests.
type fakeStore struct {
	mu           sync.Mutex
	entries      []Entry
	failInsert   error
	inserted     chan struct{} // buffered signal, one per Insert
	beforeInsert func()        // optional gate to prove async behaviour
}

func newFakeStore() *fakeStore { return &fakeStore{inserted: make(chan struct{}, 16)} }

func (f *fakeStore) Insert(_ context.Context, e Entry) error {
	if f.beforeInsert != nil {
		f.beforeInsert()
	}
	f.mu.Lock()
	f.entries = append(f.entries, e)
	f.mu.Unlock()
	select {
	case f.inserted <- struct{}{}:
	default:
	}
	return f.failInsert
}

// InsertTx records on a caller "tx" (the fake ignores the Execer, there is no real tx to honour) so
// RecordTx is exercisable DB-free. It shares the same capture + failure switch as Insert.
func (f *fakeStore) InsertTx(ctx context.Context, _ Execer, e Entry) error { return f.Insert(ctx, e) }

func (f *fakeStore) List(context.Context, Filter) ([]Entry, error) { return f.snapshot(), nil }
func (f *fakeStore) ListByEntity(_ context.Context, et, id string) ([]Entry, error) {
	var out []Entry
	for _, e := range f.snapshot() {
		if e.EntityType == et && e.EntityID == id {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeStore) GetByID(_ context.Context, id string) (*Entry, error) {
	for _, e := range f.snapshot() {
		if e.ID == id {
			c := e
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) snapshot() []Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Entry(nil), f.entries...)
}
func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

func newTestService(s store) *Service { return &Service{repo: s, log: testLogger()} }

// stubAuth mirrors identity.Actor: claims-only, derived from the Bearer token. Crucially it NEVER
// consults a client-supplied X-User-Id header (C-1), proving forged attribution has zero effect.
type stubAuth struct{ uid, role string }

func (a stubAuth) Actor(r *http.Request) (string, string) {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return a.uid, a.role
	}
	return "", ""
}

const sampleUUID = "11111111-2222-4333-8444-555555555555"

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// waitForInserts blocks until n signals arrive or the deadline passes.
func waitForInserts(t *testing.T, f *fakeStore, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-f.inserted:
		case <-deadline:
			t.Fatalf("timed out waiting for insert %d/%d (got %d)", i+1, n, f.count())
		}
	}
}

// -- U1-U5: pure path/mapping helpers -----------------------------------------

func TestU1_DeriveEntityType(t *testing.T) {
	cases := map[string]string{
		"/api/v1/pos/sales":                     "pos.sale",
		"/api/v1/staff/" + sampleUUID + "/role": "staff.role",
		"/api/v1/widgets":                       "widget",
		"/api/v1/":                              "unknown",
		"/api/v1/" + sampleUUID:                 "unknown",
		"/healthz":                              "healthz",
	}
	for in, want := range cases {
		if got := deriveEntityType(in); got != want {
			t.Errorf("deriveEntityType(%q) = %q, want %q", in, got, want)
		}
		if deriveEntityType(in) == "" {
			t.Errorf("deriveEntityType(%q) returned empty (violates NOT NULL)", in)
		}
	}
}

func TestU2_TrailingUUID(t *testing.T) {
	if got := trailingUUID("/api/v1/staff/" + sampleUUID); got != sampleUUID {
		t.Errorf("trailingUUID id = %q, want %q", got, sampleUUID)
	}
	for _, p := range []string{"/api/v1/pos/sales", "/api/v1/staff/" + sampleUUID + "/role", "/api/v1/"} {
		if got := trailingUUID(p); got != "" {
			t.Errorf("trailingUUID(%q) = %q, want empty", p, got)
		}
	}
}

func TestU3_HTTPAction(t *testing.T) {
	cases := map[string]string{
		http.MethodPost: ActHTTPPost, http.MethodPut: ActHTTPPut,
		http.MethodPatch: ActHTTPPatch, http.MethodDelete: ActHTTPDelete, http.MethodGet: "HTTP.GET",
	}
	for m, want := range cases {
		if got := httpAction(m); got != want {
			t.Errorf("httpAction(%q) = %q, want %q", m, got, want)
		}
	}
}

func TestU4_MutatingSet(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !mutating[m] {
			t.Errorf("expected %s in mutating set", m)
		}
	}
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if mutating[m] {
			t.Errorf("did not expect %s in mutating set", m)
		}
	}
	if len(mutating) != 4 {
		t.Errorf("mutating set has %d entries, want 4", len(mutating))
	}
}

func TestU5_OrNewUUID(t *testing.T) {
	if got := orNewUUID("abc"); got != "abc" {
		t.Errorf("orNewUUID(abc) = %q, want passthrough", got)
	}
	a, b := orNewUUID(""), orNewUUID("")
	if a == "" || b == "" || a == b {
		t.Errorf("orNewUUID(\"\") should mint distinct non-empty uuids, got %q / %q", a, b)
	}
	if !looksLikeUUID(a) {
		t.Errorf("minted id %q is not a uuid", a)
	}
}

// fxPublicToken is a fixture raw share token: 43 characters of the mint's base64url alphabet
// (ADR-0022: 32 bytes, base64url, unpadded), the shape the public token routes carry in the path.
const fxPublicToken = "FxPublicTokenFixture43CharAlphabet_01"

// TestElidePublicToken pins the coarse trail's credential elision (the S-1 fix): every public
// token route family's credential segment is replaced by the "{token}" placeholder, keeping the
// family and the action leg visible, while non-public paths pass through verbatim.
func TestElidePublicToken(t *testing.T) {
	cases := map[string]string{
		// The three families the tree serves, action leg and bare form.
		"/api/public/v1/customer-quotes/" + fxPublicToken:             "/api/public/v1/customer-quotes/{token}",
		"/api/public/v1/customer-quotes/" + fxPublicToken + "/accept": "/api/public/v1/customer-quotes/{token}/accept",
		"/api/public/v1/quotes/" + fxPublicToken + "/accept":          "/api/public/v1/quotes/{token}/accept",
		"/api/public/v1/will-call/" + fxPublicToken + "/confirm":      "/api/public/v1/will-call/{token}/confirm",
		// A credential carrying an encoded slash elides wholesale: no fragment survives.
		"/api/public/v1/customer-quotes/abc/def/accept": "/api/public/v1/customer-quotes/{token}/accept",
		// An unknown action leg collapses into the placeholder rather than persisting a fragment.
		"/api/public/v1/quotes/" + fxPublicToken + "/future-leg": "/api/public/v1/quotes/{token}",
		// Degenerate shapes stay safe (empty token segment, trailing slash).
		"/api/public/v1/customer-quotes/": "/api/public/v1/customer-quotes/{token}",
		"/api/public/v1/will-call/x/":     "/api/public/v1/will-call/{token}",
		// Non-public paths are the trail's value and pass through verbatim.
		"/api/v1/pos/sales": "/api/v1/pos/sales",
		"/api/v1/customer-quotes/" + sampleUUID + "/send": "/api/v1/customer-quotes/" + sampleUUID + "/send",
		"/api/public/v1/other-resource/" + sampleUUID:     "/api/public/v1/other-resource/" + sampleUUID,
	}
	for in, want := range cases {
		if got := elidePublicToken(in); got != want {
			t.Errorf("elidePublicToken(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(elidePublicToken(in), fxPublicToken) {
			t.Errorf("elidePublicToken(%q) leaked the credential", in)
		}
	}
}

// TestU22_PublicTokenPathElidedInCoarseRow drives the middleware itself: the coarse row for a
// POST to a public token route carries the elided path, and no persisted string value carries the
// raw credential. The serve-shaped companion (core/cmd/core/cq_auditwrap_test.go) proves the same
// through the production wrap over the real surface and the real audit_log.
func TestU22_PublicTokenPathElidedInCoarseRow(t *testing.T) {
	f := newFakeStore()
	runThrough(newTestService(f), stubAuth{},
		http.MethodPost, "/api/public/v1/customer-quotes/"+fxPublicToken+"/accept", 200, nil, nil)
	waitForInserts(t, f, 1)
	e := f.snapshot()[0]
	if e.Changes["path"] != "/api/public/v1/customer-quotes/{token}/accept" {
		t.Errorf("changes.path = %v, want the elided spelling", e.Changes["path"])
	}
	noCleartext(t, e, fxPublicToken)

	// The mis-post leg (no /accept suffix) elides the same way.
	f2 := newFakeStore()
	runThrough(newTestService(f2), stubAuth{},
		http.MethodPost, "/api/public/v1/customer-quotes/"+fxPublicToken, 405, nil, nil)
	waitForInserts(t, f2, 1)
	e2 := f2.snapshot()[0]
	if e2.Changes["path"] != "/api/public/v1/customer-quotes/{token}" {
		t.Errorf("mis-post changes.path = %v, want the elided spelling", e2.Changes["path"])
	}
	noCleartext(t, e2, fxPublicToken)
}

// -- U6-U14, U21: middleware (httptest, stub authenticator, fake store) -------

// runThrough runs one request through the middleware wrapping a handler that returns `status` (and,
// optionally, calls extra(ctx), used to fire a Recorder row inside the same request).
func runThrough(svc *Service, authn Authenticator, method, path string, status int,
	hdrs map[string]string, extra func(ctx context.Context)) *httptest.ResponseRecorder {
	h := svc.Middleware(authn)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if extra != nil {
			extra(r.Context())
		}
		w.WriteHeader(status)
	}))
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func bearer() map[string]string { return map[string]string{"Authorization": "Bearer x"} }

func TestU6_MutatingOnly_LoginSkipped(t *testing.T) {
	authn := stubAuth{uid: sampleUUID, role: "sales"}

	f := newFakeStore()
	runThrough(newTestService(f), authn, http.MethodPost, "/api/v1/widgets", 201, bearer(), nil)
	waitForInserts(t, f, 1)
	if f.count() != 1 {
		t.Fatalf("POST: want 1 entry, got %d", f.count())
	}

	fGet := newFakeStore()
	runThrough(newTestService(fGet), authn, http.MethodGet, "/api/v1/staff", 200, bearer(), nil)
	if got := drain(fGet); got != 0 {
		t.Fatalf("GET: want 0 entries, got %d", got)
	}

	fLogin := newFakeStore()
	runThrough(newTestService(fLogin), authn, http.MethodPost, "/api/v1/auth/login", 200, nil, nil)
	if got := drain(fLogin); got != 0 {
		t.Fatalf("login: want 0 entries, got %d", got)
	}
}

// The self-recorded routes (the demo session mint) take no coarse row:
// their module writes the finer row through the Recorder, and the coarse
// row would otherwise be one per call whatever the status, the per-call
// row defect the route's own bounded record exists to prevent. A generic
// mutating route beside it still lands exactly one coarse row, so the
// carve out cannot quietly widen.
func TestMiddlewareSkipsTheSelfRecordedRoutes(t *testing.T) {
	authn := stubAuth{uid: sampleUUID, role: "sales"}

	fDemo := newFakeStore()
	runThrough(newTestService(fDemo), authn, http.MethodPost, "/api/v1/demo/session", 400, nil, nil)
	runThrough(newTestService(fDemo), authn, http.MethodPost, "/api/v1/demo/session", 429, nil, nil)
	if got := drain(fDemo); got != 0 {
		t.Fatalf("demo session: want 0 coarse rows, got %d", got)
	}

	fOther := newFakeStore()
	runThrough(newTestService(fOther), authn, http.MethodPost, "/api/v1/demo/other", 400, nil, nil)
	waitForInserts(t, fOther, 1)
	if fOther.count() != 1 {
		t.Fatalf("a route beside the carve out: want 1 coarse row, got %d", fOther.count())
	}
}

// drain gives any async insert a brief window then returns the count (used for the "expect 0" cases).
func drain(f *fakeStore) int {
	select {
	case <-f.inserted:
	case <-time.After(150 * time.Millisecond):
	}
	return f.count()
}

func TestU7_ColumnMapping(t *testing.T) {
	f := newFakeStore()
	runThrough(newTestService(f), stubAuth{uid: sampleUUID, role: "manager"},
		http.MethodPost, "/api/v1/pos/sales", 201, bearer(), nil)
	waitForInserts(t, f, 1)
	e := f.snapshot()[0]
	if e.Action != ActHTTPPost {
		t.Errorf("action = %q, want %q", e.Action, ActHTTPPost)
	}
	if e.EntityType != "pos.sale" {
		t.Errorf("entity_type = %q, want pos.sale", e.EntityType)
	}
	if e.Changes["status"] != 201 {
		t.Errorf("changes.status = %v, want 201", e.Changes["status"])
	}
	if e.Changes["actor_role"] != "manager" {
		t.Errorf("changes.actor_role = %v, want manager", e.Changes["actor_role"])
	}
	if e.Changes["path"] != "/api/v1/pos/sales" || e.Changes["method"] != "POST" {
		t.Errorf("changes path/method wrong: %v", e.Changes)
	}
}

func TestU8_NonAPIPrefixSkipped(t *testing.T) {
	f := newFakeStore()
	runThrough(newTestService(f), stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodPost, "/healthz", 200, bearer(), nil)
	if got := drain(f); got != 0 {
		t.Fatalf("non-/api/ POST: want 0 entries, got %d", got)
	}
}

func TestU9_InsertErrorNeverFailsRequest(t *testing.T) {
	f := newFakeStore()
	f.failInsert = context.DeadlineExceeded
	rr := runThrough(newTestService(f), stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodPost, "/api/v1/widgets", 201, bearer(), nil)
	waitForInserts(t, f, 1)
	if rr.Code != 201 {
		t.Fatalf("request status = %d, want 201 (audit failure must not change it)", rr.Code)
	}
}

func TestU10_RecordingIsAsync(t *testing.T) {
	f := newFakeStore()
	gate := make(chan struct{})
	f.beforeInsert = func() { <-gate } // block the insert until released
	rr := runThrough(newTestService(f), stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodPost, "/api/v1/widgets", 201, bearer(), nil)
	// Handler returned and response is flushed even though the insert is still blocked.
	if rr.Code != 201 {
		t.Fatalf("response status = %d before insert, want 201", rr.Code)
	}
	if f.count() != 0 {
		t.Fatalf("insert observed before release: count = %d, want 0 (proves async)", f.count())
	}
	close(gate)
	waitForInserts(t, f, 1)
	if f.count() != 1 {
		t.Fatalf("after release: count = %d, want 1", f.count())
	}
}

func TestU11_UnauthenticatedMutationTraced(t *testing.T) {
	f := newFakeStore()
	// No Bearer → stub resolves no actor; DELETE returns 401.
	runThrough(newTestService(f), stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodDelete, "/api/v1/staff/"+sampleUUID, 401, nil, nil)
	waitForInserts(t, f, 1)
	e := f.snapshot()[0]
	if e.ActorID != "" {
		t.Errorf("actor_id = %q, want empty (→ NULL) for unauthenticated", e.ActorID)
	}
	if e.Changes["status"] != 401 {
		t.Errorf("status = %v, want 401", e.Changes["status"])
	}
}

func TestU12_ForbiddenMutationAttributed(t *testing.T) {
	f := newFakeStore()
	runThrough(newTestService(f), stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodPut, "/api/v1/staff/"+sampleUUID+"/role", 403, bearer(), nil)
	waitForInserts(t, f, 1)
	e := f.snapshot()[0]
	if e.ActorID != sampleUUID {
		t.Errorf("actor_id = %q, want %q", e.ActorID, sampleUUID)
	}
	if e.Changes["status"] != 403 {
		t.Errorf("status = %v, want 403", e.Changes["status"])
	}
}

func TestU13_ForgedHeaderIgnored(t *testing.T) {
	f := newFakeStore()
	hdrs := bearer()
	hdrs["X-User-Id"] = "deadbeef-0000-4000-8000-000000000000" // forged, must be ignored
	runThrough(newTestService(f), stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodPost, "/api/v1/widgets", 201, hdrs, nil)
	waitForInserts(t, f, 1)
	if got := f.snapshot()[0].ActorID; got != sampleUUID {
		t.Errorf("actor_id = %q, want %q (forged X-User-Id must have no effect)", got, sampleUUID)
	}
}

func TestU14_RequestIDEchoedAndThreaded(t *testing.T) {
	f := newFakeStore()
	svc := newTestService(f)
	var recCtxID string
	rr := runThrough(svc, stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodPost, "/api/v1/widgets", 201, bearer(), func(ctx context.Context) {
			recCtxID = requestIDFromContext(ctx)
		})
	waitForInserts(t, f, 1)
	echoed := rr.Header().Get("X-Request-Id")
	if echoed == "" {
		t.Fatal("X-Request-Id not echoed on response")
	}
	if recCtxID != echoed {
		t.Errorf("ctx request id %q != echoed %q", recCtxID, echoed)
	}
	if f.snapshot()[0].RequestID != echoed {
		t.Errorf("coarse row request_id %q != echoed %q", f.snapshot()[0].RequestID, echoed)
	}
}

func TestU21_CorrelatedRowsShareRequestID(t *testing.T) {
	// One mutating request that ALSO fires a Recorder row → both rows share the echoed request id.
	f := newFakeStore()
	svc := newTestService(f)
	rr := runThrough(svc, stubAuth{uid: sampleUUID, role: "manager"},
		http.MethodPost, "/api/v1/pos/sales", 201, bearer(), func(ctx context.Context) {
			_ = svc.Record(ctx, SensitiveAccess{
				Action: ActTaxReliefAssess, EntityType: "pos.sale", EntityID: sampleUUID,
				Purpose: "TAX_RELIEF_ATTESTATION", Extra: map[string]any{"tax_reason_code": "ON_FN_POS_8"},
			})
		})
	waitForInserts(t, f, 2) // 1 synchronous Recorder row + 1 async coarse row
	echoed := rr.Header().Get("X-Request-Id")
	if echoed == "" {
		t.Fatal("request id empty")
	}
	for _, e := range f.snapshot() {
		if e.RequestID != echoed {
			t.Errorf("row %s request_id %q != echoed %q", e.Action, e.RequestID, echoed)
		}
	}
	if f.count() != 2 {
		t.Fatalf("want 2 correlated rows, got %d", f.count())
	}

	// Absent X-Request-Id header → minted UUID, never the empty-string default.
	f2 := newFakeStore()
	runThrough(newTestService(f2), stubAuth{uid: sampleUUID, role: "sales"},
		http.MethodPost, "/api/v1/widgets", 201, bearer(), nil)
	waitForInserts(t, f2, 1)
	if id := f2.snapshot()[0].RequestID; id == "" || !looksLikeUUID(id) {
		t.Errorf("absent header should mint a uuid, got %q", id)
	}
}

// -- U15-U19: Recorder masking boundary ---------------------------------------

func recordOne(t *testing.T, a SensitiveAccess) Entry {
	t.Helper()
	f := newFakeStore()
	svc := newTestService(f)
	if err := svc.Record(withRequestID(context.Background(), "req-1"), a); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if f.count() != 1 {
		t.Fatalf("want 1 row, got %d", f.count())
	}
	return f.snapshot()[0]
}

func noCleartext(t *testing.T, e Entry, needles ...string) {
	t.Helper()
	for k, v := range e.Changes {
		s, ok := v.(string)
		if !ok {
			continue
		}
		for _, n := range needles {
			if strings.Contains(s, n) {
				t.Errorf("cleartext %q leaked into changes[%q] = %q", n, k, s)
			}
		}
	}
}

func TestU15_MaskedOnlyContract(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActEvidenceView, EntityType: "fn.eligibility_profile", EntityID: sampleUUID,
		MaskedRef: "•••• 4321",
		Extra: map[string]any{
			"sin":             "123456789",   // non-allowlisted + cleartext → dropped
			"tax_reason_code": "ON_FN_POS_8", // allowlisted code → kept
		},
	})
	if e.Changes["masked_ref"] != "•••• 4321" {
		t.Errorf("masked_ref = %v, want '•••• 4321'", e.Changes["masked_ref"])
	}
	if _, ok := e.Changes["sin"]; ok {
		t.Error("non-allowlisted field 'sin' must be dropped")
	}
	if e.Changes["tax_reason_code"] != "ON_FN_POS_8" {
		t.Errorf("tax_reason_code = %v, want kept", e.Changes["tax_reason_code"])
	}
	noCleartext(t, e, "123456789")

	// A cleartext-looking value in an ALLOWLISTED field is still redacted at the boundary.
	e2 := recordOne(t, SensitiveAccess{
		Action: ActPriceOverride, EntityType: "pricing.override", EntityID: sampleUUID,
		MaskedRef: "4111111111111111", // a raw PAN slipped into masked_ref → must be redacted
	})
	if e2.Changes["masked_ref"] == "4111111111111111" {
		t.Error("raw PAN in masked_ref was not redacted")
	}
	noCleartext(t, e2, "4111111111111111")
}

// CP-04 quick win: the reorder audit Extra (source_order_id + lines) must survive the allowlist ,
// it was silently dropped before, while staying OUT of the operator-safe projection (admin-only).
func TestReorderExtraKeys_AllowlistedButOperatorMasked(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActOrderChanged, EntityType: "order", EntityID: sampleUUID,
		Extra: map[string]any{"source_order_id": sampleUUID, "lines": 3},
	})
	if e.Changes["source_order_id"] != sampleUUID {
		t.Errorf("source_order_id must survive the allowlist: %+v", e.Changes)
	}
	if e.Changes["lines"] != 3 {
		t.Errorf("lines must survive the allowlist: %+v", e.Changes)
	}
	// The operator reopen projection keeps both OUT (admin-only provenance).
	op := projectForOperator(e)
	if _, ok := op.Changes["source_order_id"]; ok {
		t.Error("source_order_id must not reach the operator-safe view")
	}
	if _, ok := op.Changes["lines"]; ok {
		t.Error("lines must not reach the operator-safe view")
	}
}

// DIB-159: the order-status history Extra (old_status/new_status) must survive the deny-by-default
// allowlist, a status code carries no PII, and, since the order-change timeline is a counter-level
// read, must also reach the operator-safe projection. Before the allowlist widen these were dropped.
func TestOrderStatusExtraKeys_AllowlistedAndOperatorVisible(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActOrderStatusChanged, EntityType: "order", EntityID: sampleUUID,
		Reason: "pos_fulfilment",
		Extra:  map[string]any{"old_status": "CONFIRMED", "new_status": "FULFILLED"},
	})
	// A status code has no 7-digit run, so it passes the cleartext detector VERBATIM (never [redacted]).
	if e.Changes["old_status"] != "CONFIRMED" || e.Changes["new_status"] != "FULFILLED" {
		t.Errorf("old/new status must survive the allowlist unredacted: %+v", e.Changes)
	}
	// The operator reopen projection shows the from→to codes (a counter-level timeline).
	op := projectForOperator(e)
	if op.Changes["old_status"] != "CONFIRMED" || op.Changes["new_status"] != "FULFILLED" {
		t.Errorf("old/new status must reach the operator-safe view: %+v", op.Changes)
	}
}

// listCallStore records whether List reached the store, so the handler-validation regression can
// prove a malformed actor_id / since is rejected BEFORE the query (the query would otherwise blow
// up on the ::uuid / ::timestamptz cast and surface as a 503).
type listCallStore struct {
	fakeStore
	listCalled bool
}

func (s *listCallStore) List(ctx context.Context, f Filter) ([]Entry, error) {
	s.listCalled = true
	return []Entry{}, nil
}

// TestAuditList_ValidatesActorAndSince is the regression for the handler-side validation of the two
// filter params that flow raw into $3::uuid / $4::timestamptz. Malformed input must be a 400 at the
// handler (never a 503 from a failed cast), while empty stays a no-filter 200 and the query still runs.
func TestAuditList_ValidatesActorAndSince(t *testing.T) {
	newHandlerStore := func() (*Handler, *listCallStore) {
		st := &listCallStore{fakeStore: *newFakeStore()}
		return NewHandler(&Service{repo: st, log: testLogger()}, testLogger()), st
	}
	call := func(rawQuery string) (*httptest.ResponseRecorder, *listCallStore) {
		h, st := newHandlerStore()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?"+rawQuery, nil)
		rr := httptest.NewRecorder()
		h.list(rr, req, guards.StaffCaller{})
		return rr, st
	}

	// malformed actor_id → 400, query never runs
	if rr, st := call("actor_id=not-a-uuid"); rr.Code != http.StatusBadRequest || st.listCalled {
		t.Fatalf("malformed actor_id: code=%d listCalled=%v, want 400 and no query", rr.Code, st.listCalled)
	}
	// malformed since → 400, query never runs
	if rr, st := call("since=2026-07-15"); rr.Code != http.StatusBadRequest || st.listCalled {
		t.Fatalf("malformed since: code=%d listCalled=%v, want 400 and no query", rr.Code, st.listCalled)
	}
	// valid actor_id + valid RFC3339 since → 200, query runs
	if rr, st := call("actor_id=" + sampleUUID + "&since=2026-07-15T00:00:00Z"); rr.Code != http.StatusOK || !st.listCalled {
		t.Fatalf("valid filters: code=%d listCalled=%v, want 200 and query runs", rr.Code, st.listCalled)
	}
	// empty filters → 200 no-filter, query runs
	if rr, st := call(""); rr.Code != http.StatusOK || !st.listCalled {
		t.Fatalf("empty filters: code=%d listCalled=%v, want 200 no-filter and query runs", rr.Code, st.listCalled)
	}
}

// pathCallStore records whether the get/reopen store reads were reached, so a test can assert a
// malformed path id is rejected at the handler BEFORE any store/DB call.
type pathCallStore struct {
	fakeStore
	getByIDCalled      bool
	listByEntityCalled bool
}

func (s *pathCallStore) GetByID(ctx context.Context, id string) (*Entry, error) {
	s.getByIDCalled = true
	return nil, nil
}

func (s *pathCallStore) ListByEntity(ctx context.Context, et, id string) ([]Entry, error) {
	s.listByEntityCalled = true
	return []Entry{}, nil
}

// TestAuditPathID_ValidatesID is the regression for sweep #46 findings #7/#23: the get() and reopen()
// handlers now UUID-validate their path id BEFORE the `$1::uuid` / `$2::uuid` cast. A malformed id
// used to reach the cast → SQLSTATE 22P02 → HTTP 503 ("audit lookup failed"); it must now be a 400 at
// the handler, and the store must never be reached. A valid id passes the gate and reaches the store.
func TestAuditPathID_ValidatesID(t *testing.T) {
	newHandlerStore := func() (*Handler, *pathCallStore) {
		st := &pathCallStore{fakeStore: *newFakeStore()}
		return NewHandler(&Service{repo: st, log: testLogger()}, testLogger()), st
	}

	// get(): malformed {id} → 400, GetByID never called. The path id is taken from PathValue (the
	// request-line target is a fixed benign value), so a bad id with SQL-ish chars is exercised safely.
	for _, bad := range []string{"not-a-uuid", "123", "'; DROP TABLE audit_log;--", sampleUUID + "x"} {
		h, st := newHandlerStore()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/x", nil)
		req.SetPathValue("id", bad)
		rr := httptest.NewRecorder()
		h.get(rr, req, guards.StaffCaller{})
		if rr.Code != http.StatusBadRequest || st.getByIDCalled {
			t.Fatalf("get() bad id %q: code=%d getByIDCalled=%v, want 400 and no store call", bad, rr.Code, st.getByIDCalled)
		}
	}

	// get(): valid {id} → passes the gate and reaches the store (200 for the nil/nil not-found body).
	{
		h, st := newHandlerStore()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/x", nil)
		req.SetPathValue("id", sampleUUID)
		rr := httptest.NewRecorder()
		h.get(rr, req, guards.StaffCaller{})
		if rr.Code == http.StatusBadRequest || !st.getByIDCalled {
			t.Fatalf("get() valid id: code=%d getByIDCalled=%v, want gate-pass and store call", rr.Code, st.getByIDCalled)
		}
	}

	// reopen(): malformed {entityId} → 400, ListByEntity never called (entityType left unguarded).
	for _, bad := range []string{"not-a-uuid", "123", sampleUUID + "x"} {
		h, st := newHandlerStore()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/transaction/till.session/x", nil)
		req.SetPathValue("entityType", "till.session")
		req.SetPathValue("entityId", bad)
		rr := httptest.NewRecorder()
		h.reopen(rr, req, guards.StaffCaller{})
		if rr.Code != http.StatusBadRequest || st.listByEntityCalled {
			t.Fatalf("reopen() bad entityId %q: code=%d listByEntityCalled=%v, want 400 and no store call", bad, rr.Code, st.listByEntityCalled)
		}
	}

	// reopen(): valid {entityId} → passes the gate and reaches the store.
	{
		h, st := newHandlerStore()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/transaction/till.session/x", nil)
		req.SetPathValue("entityType", "till.session")
		req.SetPathValue("entityId", sampleUUID)
		rr := httptest.NewRecorder()
		h.reopen(rr, req, guards.StaffCaller{})
		if rr.Code == http.StatusBadRequest || !st.listByEntityCalled {
			t.Fatalf("reopen() valid entityId: code=%d listByEntityCalled=%v, want gate-pass and store call", rr.Code, st.listByEntityCalled)
		}
	}
}

func TestU16_EvidenceViewLogged(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActEvidenceView, EntityType: "fn.eligibility_profile", EntityID: sampleUUID,
		Purpose: "REOPEN_VIEW", MaskedRef: "•••• 4321",
	})
	if e.Action != ActEvidenceView || e.EntityType != "fn.eligibility_profile" || e.EntityID != sampleUUID {
		t.Errorf("evidence view not keyed correctly: %+v", e)
	}
	if e.Changes["purpose"] != "REOPEN_VIEW" {
		t.Errorf("purpose = %v, want REOPEN_VIEW", e.Changes["purpose"])
	}
}

func TestU17_SignatureCaptureNoBytes(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActSignatureCapture, EntityType: "signature", EntityID: sampleUUID,
		Purpose: "TAX_RELIEF_ATTESTATION",
		Extra:   map[string]any{"ref_type": "sig-ref", "image_bytes": "<rawpngbytes>"},
	})
	if e.Changes["purpose"] != "TAX_RELIEF_ATTESTATION" {
		t.Errorf("purpose missing: %v", e.Changes)
	}
	if e.Changes["ref_type"] != "sig-ref" {
		t.Errorf("ref_type missing: %v", e.Changes)
	}
	if _, ok := e.Changes["image_bytes"]; ok {
		t.Error("signature image bytes must NOT be stored")
	}
}

func TestU18_OverrideAccountability(t *testing.T) {
	for _, act := range []string{ActPriceOverride, ActTillOverride} {
		e := recordOne(t, SensitiveAccess{
			Action: act, EntityType: "till.session", EntityID: sampleUUID,
			Reason: "VARIANCE_AUTHORIZED",
			Extra: map[string]any{
				"approver_id": sampleUUID, "subject_ref": "•••• 9", "variance_grain": "OVER_50",
			},
		})
		if e.Changes["reason"] != "VARIANCE_AUTHORIZED" {
			t.Errorf("%s: reason missing", act)
		}
		if e.Changes["approver_id"] != sampleUUID || e.Changes["subject_ref"] != "•••• 9" ||
			e.Changes["variance_grain"] != "OVER_50" {
			t.Errorf("%s: approver/subject/variance not all present: %v", act, e.Changes)
		}
	}
}

func TestU19_TaxReliefAssess(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActTaxReliefAssess, EntityType: "pos.sale", EntityID: sampleUUID,
		Extra: map[string]any{"tax_reason_code": "ON_FN_POS_8", "customer_exempt": "true"},
	})
	if e.Changes["tax_reason_code"] != "ON_FN_POS_8" {
		t.Errorf("tax_reason_code = %v, want ON_FN_POS_8", e.Changes["tax_reason_code"])
	}
	if _, ok := e.Changes["customer_exempt"]; ok {
		t.Error("no permanent customer-exempt field may be persisted")
	}
}

// -- U20: append-only posture -------------------------------------------------

func TestU20_AppendOnly(t *testing.T) {
	for _, target := range []reflect.Type{reflect.TypeOf(&Repo{}), reflect.TypeOf(&Service{})} {
		for i := 0; i < target.NumMethod(); i++ {
			name := target.Method(i).Name
			if strings.Contains(name, "Update") || strings.Contains(name, "Delete") ||
				strings.Contains(name, "Remove") {
				t.Errorf("append-only violated: %s exposes %s", target, name)
			}
		}
	}
}

// sensitiveActions must contain every EVIDENCE_*/SIGNATURE_*/*_OVERRIDE/TAX_RELIEF_ASSESS constant
// (no typo drift), the masked-only contract keys off this set.
func TestSensitiveActionsComplete(t *testing.T) {
	want := []string{
		ActEvidenceView, ActEvidenceChange, ActEvidenceDownload, ActEvidenceOverride,
		ActSignatureCapture, ActSignatureView, ActPriceOverride, ActTillOverride, ActTaxReliefAssess,
	}
	for _, a := range want {
		if !sensitiveActions[a] || !IsSensitiveAction(a) {
			t.Errorf("sensitiveActions missing %q", a)
		}
	}
	// Coarse HTTP actions are NOT sensitive.
	for _, a := range []string{ActHTTPPost, ActHTTPGet()} {
		if sensitiveActions[a] {
			t.Errorf("%q must not be sensitive", a)
		}
	}
}

func ActHTTPGet() string { return "HTTP.GET" }

// -- operator reopen projection (US-B1/US-B2) ---------------------------------

func TestReopenProjectionStripsSensitive(t *testing.T) {
	full := Entry{
		Action: ActTaxReliefAssess, EntityType: "pos.sale", EntityID: sampleUUID, ActorID: sampleUUID,
		Changes: map[string]any{
			"purpose": "TAX_RELIEF_ATTESTATION", "tax_reason_code": "ON_FN_POS_8",
			"masked_ref": "•••• 4321", "reason": "MANAGER_OVERRIDE", "status": 201,
		},
	}
	op := projectForOperator(full)
	if op.Changes["tax_reason_code"] != "ON_FN_POS_8" {
		t.Error("operator should see the tax reason code")
	}
	if _, ok := op.Changes["masked_ref"]; ok {
		t.Error("operator view must not include the FN masked identifier")
	}
	if _, ok := op.Changes["reason"]; ok {
		t.Error("operator view must not include the override reason")
	}
	if op.ActorID == sampleUUID || op.ActorID == "" {
		t.Errorf("operator actor label should be masked, got %q", op.ActorID)
	}
	if !strings.Contains(op.ActorID, "5555") {
		t.Errorf("masked actor should keep last 4, got %q", op.ActorID)
	}
}

func TestLooksLikeCleartextID(t *testing.T) {
	cleartext := []string{"123456789", "4111111111111111", "abc1234567def"}
	safe := []string{"•••• 4321", "ON_FN_POS_8", "OVER_50", "VISA", "", "v1"}
	for _, s := range cleartext {
		if !looksLikeCleartextID(s) {
			t.Errorf("%q should be flagged as cleartext", s)
		}
	}
	for _, s := range safe {
		if looksLikeCleartextID(s) {
			t.Errorf("%q should NOT be flagged as cleartext", s)
		}
	}
}

// TestOrderStatusChanged_ReasonCodeDropped locks the L8 dead-code removal: "reason_code" is no
// longer an allowlisted Extra key, so a caller supplying it has it DROPPED (deny-by-default), while
// the transition reason travels, as it always did, via the top-level Reason field (persisted as
// changes["reason"]). Before the removal, reason_code would have survived as changes["reason_code"];
// this test fails if the entry is re-added to allowedExtraKeys.
func TestOrderStatusChanged_ReasonCodeDropped(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActOrderStatusChanged, EntityType: "order", EntityID: sampleUUID,
		ActorID: sampleUUID, Purpose: "ORDER_STATUS_TRANSITION", Reason: "picker_started",
		Extra: map[string]any{
			"old_status":  "CONFIRMED",
			"new_status":  "PICKING",
			"reason_code": "SHOULD_BE_DROPPED", // no longer allowlisted → must not survive
		},
	})
	if _, ok := e.Changes["reason_code"]; ok {
		t.Errorf("reason_code = %v, want DROPPED (not an allowlisted key)", e.Changes["reason_code"])
	}
	if e.Changes["reason"] != "picker_started" {
		t.Errorf("reason = %v, want picker_started (the reason travels via the top-level Reason field)", e.Changes["reason"])
	}
	if e.Changes["old_status"] != "CONFIRMED" || e.Changes["new_status"] != "PICKING" {
		t.Errorf("status codes = %v→%v, want CONFIRMED→PICKING", e.Changes["old_status"], e.Changes["new_status"])
	}
}
