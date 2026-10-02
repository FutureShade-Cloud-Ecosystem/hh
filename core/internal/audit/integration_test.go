//go:build integration

// Integration tests run the real pgx Repo + the recording middleware + the Recorder seam against the
// Postgres 16 that `make reset` provisions, writing to the existing audit_log (migration 0013).
// Run with: make test-integration (after make migrate). They prove the end-to-end coarse trail (I1),
// the reopen + masking projection (I3/I4), the FN-evidence masked view (I5), the viewer filters +
// limit clamp (I6), the cross-row request_id correlation that persists through the DB (I8), and the
// reopen read's branch fence (HH-180).
//
// Every row written here is tagged with a per-run request_id (and a unique entity_id) so cleanup is
// targeted and the suite is reentrant. I2 (RBAC 403 for lower tiers) and I7 (hash-chain) are out of
// this module's reach: I2 exercises identity's RequireAdmin at the server level (commander full-stack
// prove-run); I7 only exists if D-A21-1 ships migration 0030 (not built at W4 default).
package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// realRepo builds a DB-backed Repo, skipping (locally) or failing (CI) when no DB is reachable.
func realRepo(t *testing.T) *Repo {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewRepository(pool)
}

func cleanupReq(t *testing.T, r *Repo, requestID string) {
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM audit_log WHERE request_id = $1`, requestID)
	})
}

// pollRows waits (async insert) for the row count keyed to a request_id to reach want.
func pollRows(t *testing.T, r *Repo, requestID string, want int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		var n int
		if err := r.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM audit_log WHERE request_id = $1`, requestID).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: request_id %s had %d rows, want %d", requestID, n, want)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// I1, full middleware → real audit_log insert with all 0013 columns incl. request_id.
func TestIntegration_I1_CoarseInsert(t *testing.T) {
	repo := realRepo(t)
	svc := NewService(repo, testLogger())
	reqID := newUUID()
	cleanupReq(t, repo, reqID)

	h := svc.Middleware(stubAuth{uid: intActor, role: "manager"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) }))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", nil)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("X-Request-Id", reqID)
	h.ServeHTTP(httptest.NewRecorder(), req)

	pollRows(t, repo, reqID, 1)
	rows, err := repo.List(context.Background(), Filter{Action: ActHTTPPost, ActorID: intActor})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got *Entry
	for i := range rows {
		if rows[i].RequestID == reqID {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatal("inserted row not found via List")
	}
	if got.EntityType != "pos.sale" || got.ActorID != intActor || got.ActorRole != "manager" {
		t.Errorf("column mapping wrong: %+v", got)
	}
	if got.Changes["status"].(float64) != 201 {
		t.Errorf("changes.status = %v, want 201", got.Changes["status"])
	}
	if got.CreatedAt == "" {
		t.Error("created_at not set by DB")
	}
}

// I3/I4, seed coarse + Recorder rows for one pos.sale id; reopen returns the chronological trail and
// the operator masking projection omits sensitive payloads. The four-transaction-type evidence (I4)
// is the same shape repeated per sale type; here we prove one keyed transaction end-to-end.
func TestIntegration_I3_ReopenMaskingProjection(t *testing.T) {
	repo := realRepo(t)
	svc := NewService(repo, testLogger())
	reqID := newUUID()
	saleID := newUUID()
	cleanupReq(t, repo, reqID)
	ctx := withRequestID(context.Background(), reqID)

	// coarse row (the sale POST) + a sensitive TAX_RELIEF_ASSESS Recorder row, both keyed to the sale.
	if err := repo.Insert(ctx, Entry{
		Action: ActHTTPPost, EntityType: "pos.sale", EntityID: saleID, ActorID: intActor,
		RequestID: reqID, Changes: map[string]any{"method": "POST", "status": 201, "actor_role": "sales"},
	}); err != nil {
		t.Fatalf("insert coarse: %v", err)
	}
	if err := svc.Record(ctx, SensitiveAccess{
		Action: ActTaxReliefAssess, EntityType: "pos.sale", EntityID: saleID, ActorID: intActor,
		Purpose: "TAX_RELIEF_ATTESTATION", MaskedRef: "•••• 4321", Reason: "MANAGER_OVERRIDE",
		Extra: map[string]any{"tax_reason_code": "ON_FN_POS_8"},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	trail, err := svc.ReopenTransaction(context.Background(), "pos.sale", saleID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(trail) != 2 {
		t.Fatalf("reopen returned %d rows, want 2", len(trail))
	}
	for _, e := range trail {
		op := projectForOperator(e)
		if _, ok := op.Changes["masked_ref"]; ok {
			t.Error("operator reopen leaked masked_ref (FN identifier)")
		}
		if _, ok := op.Changes["reason"]; ok {
			t.Error("operator reopen leaked override reason")
		}
		if op.Action == ActTaxReliefAssess && op.Changes["tax_reason_code"] != "ON_FN_POS_8" {
			t.Error("operator reopen should show the tax reason code")
		}
	}
}

// I5, an EVIDENCE_VIEW row on an FN profile read stores a masked identifier; the persisted changes
// contain no cleartext; the operator reopen omits it entirely.
func TestIntegration_I5_EvidenceViewMasked(t *testing.T) {
	repo := realRepo(t)
	svc := NewService(repo, testLogger())
	reqID := newUUID()
	profileID := newUUID()
	cleanupReq(t, repo, reqID)
	ctx := withRequestID(context.Background(), reqID)

	if err := svc.Record(ctx, SensitiveAccess{
		Action: ActEvidenceView, EntityType: "fn.eligibility_profile", EntityID: profileID,
		ActorID: intActor, Purpose: "REOPEN_VIEW", MaskedRef: "•••• 4321",
		Extra: map[string]any{"sin": "123456789"}, // non-allowlisted cleartext → must be dropped
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Verify directly from the DB that no cleartext landed.
	var changes string
	if err := repo.pool.QueryRow(ctx,
		`SELECT changes::text FROM audit_log WHERE request_id = $1`, reqID).Scan(&changes); err != nil {
		t.Fatalf("select changes: %v", err)
	}
	if contains(changes, "123456789") {
		t.Errorf("cleartext SIN leaked into audit_log.changes: %s", changes)
	}
	if !contains(changes, "4321") {
		t.Errorf("masked ref not stored: %s", changes)
	}
}

// I6, viewer filters return the right subset and the limit clamps to <=500.
func TestIntegration_I6_FiltersAndClamp(t *testing.T) {
	repo := realRepo(t)
	reqID := newUUID()
	cleanupReq(t, repo, reqID)
	ctx := withRequestID(context.Background(), reqID)
	for i := 0; i < 3; i++ {
		if err := repo.Insert(ctx, Entry{
			Action: ActHTTPPut, EntityType: "till.session", EntityID: newUUID(), ActorID: intActor,
			RequestID: reqID, Changes: map[string]any{"status": 200},
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := repo.List(ctx, Filter{EntityType: "till.session", Action: ActHTTPPut, ActorID: intActor, Limit: 100000})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	mine := 0
	for _, e := range rows {
		if e.RequestID == reqID {
			mine++
		}
	}
	if mine != 3 {
		t.Errorf("filtered subset = %d, want 3", mine)
	}
	if len(rows) > 500 {
		t.Errorf("limit not clamped: got %d rows (>500)", len(rows))
	}
}

// I8, one request that records BOTH a coarse row AND a Recorder row, sharing the request_id, persists
// >=2 rows under that id in the DB (end-to-end correlation through Postgres).
func TestIntegration_I8_RequestIDCorrelation(t *testing.T) {
	repo := realRepo(t)
	svc := NewService(repo, testLogger())
	reqID := newUUID()
	saleID := newUUID()
	cleanupReq(t, repo, reqID)

	h := svc.Middleware(stubAuth{uid: intActor, role: "manager"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = svc.Record(r.Context(), SensitiveAccess{
				Action: ActTaxReliefAssess, EntityType: "pos.sale", EntityID: saleID, ActorID: intActor,
				Purpose: "TAX_RELIEF_ATTESTATION", Extra: map[string]any{"tax_reason_code": "ON_FN_POS_8"},
			})
			w.WriteHeader(201)
		}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", nil)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("X-Request-Id", reqID)
	h.ServeHTTP(rr, req)

	if rr.Header().Get("X-Request-Id") != reqID {
		t.Fatalf("echoed request id %q != %q", rr.Header().Get("X-Request-Id"), reqID)
	}
	pollRows(t, repo, reqID, 2)
	var n int
	if err := repo.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE request_id = $1`, reqID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n < 2 {
		t.Errorf("correlated rows under request_id = %d, want >=2", n)
	}
}

const intActor = "a1a1a1a1-2222-4333-8444-555555555555"

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// DIB-25, the Customer + Product legs. Production wraps the ENTIRE mux with the coarse audit
// middleware (cmd/server/main.go: `auditSvc.Middleware(identityHandler)(mux)`), so every mutating
// /api request, including POST /api/v1/customers and PUT /api/v1/products/{id}, is coarse-audited.
// This drives that same Service.Middleware over the real pgx Repo with those request paths and proves
// exactly ONE audit_log row lands per request with the correctly-derived SINGULAR entity_type plus the
// lifted actor/role/id/status. What it deliberately does NOT re-prove (and so does not claim to): that
// the customer/product routes are registered at those exact paths, that lives in internal/customer +
// internal/product route registration, nor that main.go applies the middleware to the mux; both are
// stable one-line wiring facts, not behavior. ORDER is additionally proven end-to-end by
// order/history + lifecycle integration tests, and the same generic mechanism covers the Invoice leg
// identically (any /api/v1/invoices mutation derives entity_type "invoice" through this exact path).
//
// Not a tautology: exactlyOne + rowFor together pin that the STORED type is the singular "customer"/
// "product", a raw-plural derivation would leave rowFor's filtered List empty and Fatalf, and the
// actor_role / entity_id / status fields reconcile to independently-chosen literals, never to
// themselves. (The redundant entity_type/action echo assertions rowFor already filters on were removed
// so the test states only what it genuinely proves.)
func TestIntegration_DIB25_CoarseEntityWiring_CustomerProduct(t *testing.T) {
	repo := realRepo(t)
	svc := NewService(repo, testLogger())

	// exactlyOne asserts precisely one row landed under reqID (targeted, reentrant cleanup already armed).
	exactlyOne := func(reqID string) {
		t.Helper()
		var n int
		if err := repo.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM audit_log WHERE request_id = $1`, reqID).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Fatalf("request_id %s produced %d rows, want exactly 1", reqID, n)
		}
	}
	// rowFor returns the single coarse row keyed to reqID (via the same List path the viewer uses).
	rowFor := func(reqID, entityType, action string) *Entry {
		t.Helper()
		rows, err := repo.List(context.Background(),
			Filter{EntityType: entityType, Action: action, ActorID: intActor, Limit: 500})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range rows {
			if rows[i].RequestID == reqID {
				return &rows[i]
			}
		}
		t.Fatalf("no %s/%s row found for request_id %s", entityType, action, reqID)
		return nil
	}

	// -- Customer leg: POST /api/v1/customers -> 201, entity_type "customer" --
	custReq := newUUID()
	cleanupReq(t, repo, custReq)
	custH := svc.Middleware(stubAuth{uid: intActor, role: "manager"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	custRq := httptest.NewRequest(http.MethodPost, "/api/v1/customers", nil)
	custRq.Header.Set("Authorization", "Bearer x")
	custRq.Header.Set("X-Request-Id", custReq)
	custH.ServeHTTP(httptest.NewRecorder(), custRq)

	pollRows(t, repo, custReq, 1)
	exactlyOne(custReq)
	// rowFor filters List by entity_type="customer"/action=POST, so finding a row IS the proof the
	// middleware derived the singular "customer" (raw "customers" would leave this empty -> Fatalf);
	// exactlyOne above pins it to that single request. The remaining fields are unfiltered, real checks.
	cust := rowFor(custReq, "customer", ActHTTPPost)
	if cust.ActorID != intActor {
		t.Errorf("customer actor_id = %q, want %q", cust.ActorID, intActor)
	}
	if cust.ActorRole != "manager" {
		t.Errorf("customer actor_role = %q, want %q (lifted from changes.actor_role)", cust.ActorRole, "manager")
	}
	if s, ok := cust.Changes["status"].(float64); !ok || s != 201 {
		t.Errorf("customer changes.status = %v, want 201", cust.Changes["status"])
	}

	// -- Product leg: PUT /api/v1/products/{uuid} -> 200, entity_type "product" --
	prodReq := newUUID()
	prodID := newUUID()
	cleanupReq(t, repo, prodReq)
	prodH := svc.Middleware(stubAuth{uid: intActor, role: "clerk"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	prodRq := httptest.NewRequest(http.MethodPut, "/api/v1/products/"+prodID, nil)
	prodRq.Header.Set("Authorization", "Bearer x")
	prodRq.Header.Set("X-Request-Id", prodReq)
	prodH.ServeHTTP(httptest.NewRecorder(), prodRq)

	pollRows(t, repo, prodReq, 1)
	exactlyOne(prodReq)
	// As above: rowFor's found-or-Fatalf is the singular-"product" derivation proof; the fields below
	// (entity_id from the trailing UUID, actor_role, status) are unfiltered and independently reconciled.
	prod := rowFor(prodReq, "product", ActHTTPPut)
	// The trailing UUID segment is the entity_id, reconciles the id-extraction leg of the derivation.
	if prod.EntityID != prodID {
		t.Errorf("product entity_id = %q, want %q (trailing-uuid extraction)", prod.EntityID, prodID)
	}
	if prod.ActorRole != "clerk" {
		t.Errorf("product actor_role = %q, want %q (lifted from changes.actor_role)", prod.ActorRole, "clerk")
	}
	if s, ok := prod.Changes["status"].(float64); !ok || s != 200 {
		t.Errorf("product changes.status = %v, want 200", prod.Changes["status"])
	}
}

// DIB-25 (optional hardening), the Order leg's coarse row and its entity-grain Recorder row must
// CO-RESIDE under one order id and reopen together, oldest->newest. order/history_integration_test.go
// proves the Recorder timeline via OrderHistory (status edges only); this instead proves the audit
// reopen path (ListByEntity) returns BOTH the coarse HTTP.POST row (the /api/v1/orders middleware
// entity_type "order") AND an ORDER_STATUS_CHANGED entity-grain row for the same id, the co-location
// that I3 asserts for pos.sale but nothing asserts for "order". Non-redundant with the order package.
func TestIntegration_DIB25_OrderCoarseAndRecorderColocated(t *testing.T) {
	repo := realRepo(t)
	svc := NewService(repo, testLogger())
	reqID := newUUID()
	orderID := newUUID()
	cleanupReq(t, repo, reqID)
	ctx := withRequestID(context.Background(), reqID)

	// 1. Coarse row: the shape the middleware writes for POST /api/v1/orders (entity_type "order").
	if err := repo.Insert(ctx, Entry{
		Action: ActHTTPPost, EntityType: "order", EntityID: orderID, ActorID: intActor,
		RequestID: reqID, Changes: map[string]any{"method": "POST", "status": 201, "actor_role": "sales"},
	}); err != nil {
		t.Fatalf("insert coarse order row: %v", err)
	}
	// A small strict-ordering gap so created_at (microsecond precision) is deterministically increasing
	// and the coarse row is unambiguously the oldest, the reopen path is a chronological trail.
	time.Sleep(3 * time.Millisecond)
	// 2. Entity-grain row: an ORDER_STATUS_CHANGED transition through the Recorder seam (same as
	//    order/service.go recordStatusChange), keyed to the SAME order id.
	if err := svc.Record(ctx, SensitiveAccess{
		Action: ActOrderStatusChanged, EntityType: "order", EntityID: orderID, ActorID: intActor,
		Reason: "picker_started",
		Extra:  map[string]any{"old_status": "CONFIRMED", "new_status": "PICKING"},
	}); err != nil {
		t.Fatalf("record status change: %v", err)
	}

	pollRows(t, repo, reqID, 2)
	trail, err := svc.ReopenTransaction(context.Background(), "order", orderID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(trail) != 2 {
		t.Fatalf("reopen returned %d rows, want 2 (coarse + entity-grain co-resident)", len(trail))
	}
	// Oldest->newest: the coarse HTTP.POST first, the ORDER_STATUS_CHANGED second.
	if trail[0].Action != ActHTTPPost {
		t.Errorf("trail[0].action = %q, want %q (coarse row is oldest)", trail[0].Action, ActHTTPPost)
	}
	if trail[1].Action != ActOrderStatusChanged {
		t.Errorf("trail[1].action = %q, want %q (entity-grain row is newest)", trail[1].Action, ActOrderStatusChanged)
	}
	// created_at ordering is authoritative (oldest strictly before newest given the gap above).
	t0, err0 := time.Parse(time.RFC3339Nano, trail[0].CreatedAt)
	t1, err1 := time.Parse(time.RFC3339Nano, trail[1].CreatedAt)
	if err0 != nil || err1 != nil {
		t.Fatalf("parse created_at: %v / %v", err0, err1)
	}
	if !t0.Before(t1) {
		t.Errorf("trail not ordered oldest->newest: %v then %v", t0, t1)
	}
	// Both legs are keyed to the one order id (the co-location the reopen depends on).
	for i, e := range trail {
		if e.EntityType != "order" || e.EntityID != orderID {
			t.Errorf("trail[%d] = (%q,%q), want (order,%s)", i, e.EntityType, e.EntityID, orderID)
		}
	}
	// The entity-grain row carried its status transition through the masked Recorder boundary intact.
	if trail[1].Changes["old_status"] != "CONFIRMED" || trail[1].Changes["new_status"] != "PICKING" {
		t.Errorf("status transition = %v->%v, want CONFIRMED->PICKING",
			trail[1].Changes["old_status"], trail[1].Changes["new_status"])
	}
}

// HH-180, the operator reopen read is branch fenced. The route's staff gate proves the caller is
// staff, not that they may reach THIS trail (the same gap the order edit history closes with
// CanAccessBranch), and the rows carry their own branch (0013 branch_id), so the fence is computed
// from the rows: a scoped caller sees only rows in branches their grants reach plus branch-less rows
// (the coarse middleware rows carry no branch), a global caller keeps the full trail, and a scoped
// caller reading a fully out-of-branch entity answers an EMPTY trail, never a refusal, so the route
// stays no existence oracle. The masking projection is untouched by the fence and proven separately
// (I3 above); every row that does come back still goes through projectForOperator on the way out.
func TestIntegration_HH180_ReopenBranchFence(t *testing.T) {
	repo := realRepo(t)
	svc := NewService(repo, testLogger())
	h := NewHandler(svc, testLogger())
	reqID := newUUID()
	cleanupReq(t, repo, reqID)
	ctx := withRequestID(context.Background(), reqID)

	home := newUUID() // the branch the scoped caller is granted
	away := newUUID() // a branch the scoped caller cannot reach

	seed := func(entityID, branch, field string) {
		t.Helper()
		if err := repo.Insert(ctx, Entry{
			Action: ActOrderChanged, EntityType: "order", EntityID: entityID, ActorID: intActor,
			BranchID: branch, RequestID: reqID,
			Changes: map[string]any{"field": field, "line_no": 1, "before": "1000", "after": "1250"},
		}); err != nil {
			t.Fatalf("insert %s row: %v", field, err)
		}
	}
	// One order whose price-edit trail spans both branches, plus the branch-less coarse POST row every
	// order also carries: one entity id, so the only variable across the legs below is the caller.
	shared := newUUID()
	seed(shared, home, "unit_price_cents")
	seed(shared, away, "line_total_cents")
	if err := repo.Insert(ctx, Entry{
		Action: ActHTTPPost, EntityType: "order", EntityID: shared, ActorID: intActor,
		RequestID: reqID, Changes: map[string]any{"method": "POST", "status": 201, "actor_role": "sales"},
	}); err != nil {
		t.Fatalf("insert coarse row: %v", err)
	}
	// An away-branch-only order: the granted caller's fully out-of-branch read.
	awayOnly := newUUID()
	seed(awayOnly, away, "unit_price_cents")

	call := func(caller guards.StaffCaller, entityID string) ([]Entry, int) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/transaction/order/"+entityID, nil)
		req.SetPathValue("entityType", "order")
		req.SetPathValue("entityId", entityID)
		rr := httptest.NewRecorder()
		h.reopen(rr, req, caller)
		var got []Entry
		if rr.Code == http.StatusOK {
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode reopen body %q: %v", rr.Body.String(), err)
			}
		}
		return got, rr.Code
	}
	scoped := guards.StaffCaller{StaffAuthority: identity.StaffAuthority{
		RoleID: "int-test-role", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Branches: []string{home},
	}}
	global := guards.StaffCaller{StaffAuthority: identity.StaffAuthority{
		RoleID: "int-test-role", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}}

	// Same-branch caller: their branch's rows plus the branch-less coarse row, never the other
	// branch's. Two rows, the home edit and the coarse POST.
	got, code := call(scoped, shared)
	if code != http.StatusOK {
		t.Fatalf("scoped caller: status %d, want 200", code)
	}
	if len(got) != 2 {
		t.Errorf("scoped caller got %d rows, want 2 (their branch's edit + the branch-less coarse row): %+v", len(got), got)
	}
	for _, e := range got {
		if e.BranchID == away {
			t.Errorf("scoped caller leaked the other branch's row: action %q branch %q", e.Action, e.BranchID)
		}
	}

	// Global caller: the full trail, all three rows.
	got, code = call(global, shared)
	if code != http.StatusOK {
		t.Fatalf("global caller: status %d, want 200", code)
	}
	if len(got) != 3 {
		t.Errorf("global caller got %d rows, want 3 (the full cross-branch trail): %+v", len(got), got)
	}

	// Fully out-of-branch read: an empty trail answers, never a refusal and never the rows.
	got, code = call(scoped, awayOnly)
	if code != http.StatusOK {
		t.Fatalf("out-of-branch read: status %d, want 200 with an empty trail", code)
	}
	if len(got) != 0 {
		t.Errorf("out-of-branch read returned %d rows, want 0 (empty trail, never the other branch's)", len(got))
	}
}
