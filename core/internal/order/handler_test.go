package order

import (
	"context"
	"encoding/json"
	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// handler_test.go, DIB-191 manual endpoints at the HTTP boundary (acceptance T3, T4, U4, U5). Uses the
// real caller-resolution path: identity.ContextWithStaff stamps the same context key the route guards
// use, and the default Service (callerFrom = identity.StaffContextFromContext) reads it back, so the
// error→status mapping and branch/authority gating are exercised end-to-end without middleware.

func newHandlerFixture(t *testing.T, status string) *Handler {
	t.Helper()
	return newHandlerFixtureWithGuards(t, status, &fakeCancelGuards{})
}

// newHandlerFixtureWithGuards builds the handler with a caller-supplied cancel predicate. Pass nil to
// leave the guards UNWIRED (R2 fail-closed: a cancel attempt must answer 503, while the order read must
// still answer 200 with CANCELLED simply omitted, supervisor C2).
func newHandlerFixtureWithGuards(t *testing.T, status string, guards *fakeCancelGuards) *Handler {
	t.Helper()
	store := newFakeStore(mkDetail(txnOrder, txnCust, status))
	store.branches = map[string]string{txnOrder: histBranchA}
	svc := NewService(store).WithAudit(&recordingRecorder{})
	if guards != nil {
		svc.WithCancelGuards(guards, guards)
	}
	return NewHandler(svc)
}

// req builds a POST to /orders/{id}/<action> with the given body and an injected StaffContext.
func req(t *testing.T, sc *identity.StaffContext, orderID, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+orderID+"/x", nil)
	} else {
		r = httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+orderID+"/x", strings.NewReader(body))
	}
	r.SetPathValue("id", orderID)
	if sc != nil {
		r = r.WithContext(identity.ContextWithStaff(r.Context(), *sc))
	}
	return r
}

// - ruling D2: the confirm endpoint (DRAFT → CONFIRMED) -

func TestHandler_Confirm_OK(t *testing.T) {
	h := newHandlerFixture(t, "DRAFT")
	sc := staffAt(txnUser, histBranchA) // frontline in-branch, confirm is NOT manager-gated
	rr := httptest.NewRecorder()
	h.confirm(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out["status"] != "CONFIRMED" {
		t.Fatalf("status = %q, want CONFIRMED", out["status"])
	}
}

func TestHandler_Confirm_CrossBranchForbidden(t *testing.T) {
	h := newHandlerFixture(t, "DRAFT")
	sc := staffAt(txnUser, histBranchB) // order is in histBranchA
	rr := httptest.NewRecorder()
	h.confirm(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rr.Code)
	}
}

func TestHandler_Confirm_InactiveForbidden(t *testing.T) {
	h := newHandlerFixture(t, "DRAFT")
	sc := inactiveStaff(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.confirm(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for an inactive caller", rr.Code)
	}
}

func TestHandler_Confirm_Unauthenticated(t *testing.T) {
	h := newHandlerFixture(t, "DRAFT")
	rr := httptest.NewRecorder()
	h.confirm(rr, req(t, nil, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rr.Code)
	}
}

// A confirm on an order past DRAFT is a forward-only refusal (409), not a silent no-op.
func TestHandler_Confirm_AlreadyPastDraftConflict(t *testing.T) {
	h := newHandlerFixture(t, "PICKING")
	sc := staffAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.confirm(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 (cannot confirm an order already past DRAFT)", rr.Code)
	}
}

// - route layer: RegisterTransitionRoutes wiring + guard placement -

// TestRegisterTransitionRoutes_WiringAndGuards closes the audit's "RegisterTransitionRoutes zero
// coverage" finding. It registers the real routes on a real mux behind the real Guards (the kit's
// DB-free double with a settable resolved authority), then drives each path through the mux with a
// verified bearer to prove: (a) every action is reachable at its documented method+path, (b)
// confirm/picking/ready pass the staff constructor for a tier 3 caller while cancel is refused the
// RBAC-403 the manager constructor answers, and the tier 2 half proves cancel's gate opens for a
// manager, so a guard swap that would let frontline staff cancel is caught here, and (c) GET is not
// accepted.
func TestRegisterTransitionRoutes_WiringAndGuards(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	bearer := sg.Bearer(t)

	h := newHandlerFixture(t, "DRAFT").WithUserLinker(func(_ context.Context, _ string) (string, bool) {
		return txnUser, true
	})
	mux := http.NewServeMux()
	h.RegisterTransitionRoutes(mux, sg.Guards)

	do := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"reason":"r"}`))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr
	}

	// Tier 3 half: the staff routes admit, the manager route refuses.
	sg.SetAuthority(auth.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{histBranchA}})
	for _, path := range []string{
		"/api/v1/orders/" + txnOrder + "/confirm",
		"/api/v1/orders/" + txnOrder + "/picking",
		"/api/v1/orders/" + txnOrder + "/ready",
	} {
		if rr := do(path); rr.Code == http.StatusNotFound {
			t.Errorf("%s is not registered (404 from the mux)", path)
		} else if rr.Code == 403 || rr.Code == 401 {
			t.Errorf("%s is refused (%d) for a tier 3 caller, want the staff constructor to admit it", path, rr.Code)
		}
	}
	if rr := do("/api/v1/orders/" + txnOrder + "/cancel"); rr.Code != http.StatusForbidden {
		t.Errorf("cancel = %d for a tier 3 caller, want the manager constructor's 403", rr.Code)
	}

	// Tier 2 half: cancel's manager gate opens (the service then decides).
	sg.SetAuthority(auth.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{histBranchA}})
	if rr := do("/api/v1/orders/" + txnOrder + "/cancel"); rr.Code == http.StatusNotFound {
		t.Errorf("cancel is not registered (404 from the mux)")
	} else if rr.Code == 403 || rr.Code == 401 {
		t.Errorf("cancel = %d for a tier 2 caller, want the manager constructor to admit it", rr.Code)
	}

	// The routes are POST-only, a GET must not reach a handler.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/orders/"+txnOrder+"/confirm", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /confirm = %d, want 405", rr.Code)
	}
}

// - cancel reason bounds (audit M: previously unbounded) -

func TestHandler_Cancel_OverlongReasonRejected(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := managerAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"reason": strings.Repeat("x", maxReasonLen+1)})
	h.cancel(rr, req(t, &sc, txnOrder, string(body)), guards.StaffCaller{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 for a reason over %d chars", rr.Code, maxReasonLen)
	}
}

// TestHandler_Cancel_OversizedBodyRefused pins the R3 http.MaxBytesReader (B3-4).
//
// Designing this to actually FAIL without the cap takes care: a body whose *reason* is overlong is
// refused by maxReasonLen anyway, so it would 400 either way and prove nothing. Instead the reason here
// is perfectly VALID and the bulk sits in a separate padding field placed FIRST in the JSON, so the
// decoder hits the byte cap before it ever reaches "reason":
//
//	with the cap     → decode aborts, reason stays "" → 400 (a required reason is missing)
//	without the cap  → the whole body parses, the reason is valid → 200 CANCELLED
//
// so the assertion flips on the presence of the reader rather than on the length bound.
func TestHandler_Cancel_OversizedBodyRefused(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := managerAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()

	// Padding first, then a short valid reason, well past maxBodyBytes in total.
	body := `{"pad":"` + strings.Repeat("x", maxBodyBytes+2048) + `","reason":"a perfectly valid reason"}`
	h.cancel(rr, req(t, &sc, txnOrder, body), guards.StaffCaller{})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400, an over-%dB body must be refused at the boundary, "+
			"not parsed into a successful cancel; body=%s", rr.Code, maxBodyBytes, rr.Body.String())
	}
}

// A body comfortably under the cap still works, so the cap is not simply refusing everything.
func TestHandler_Cancel_NormalBodyUnaffectedByCap(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := managerAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.cancel(rr, req(t, &sc, txnOrder, `{"pad":"small","reason":"customer cancelled"}`), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 for an ordinary body; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandler_Cancel_ReasonAtBoundAccepted(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := managerAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"reason": strings.Repeat("x", maxReasonLen)})
	h.cancel(rr, req(t, &sc, txnOrder, string(body)), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 at exactly the %d-char bound; body=%s", rr.Code, maxReasonLen, rr.Body.String())
	}
}

// - B2 / R2 + C2 at the HTTP boundary -

// TestHandler_CancelBlocked_Conflict: money attached → 409 naming the returns flow (not a 500, not a
// silent success).
func TestHandler_CancelBlocked_Conflict(t *testing.T) {
	h := newHandlerFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{hasInvoice: true})
	sc := managerAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.cancel(rr, req(t, &sc, txnOrder, `{"reason":"customer changed their mind"}`), guards.StaffCaller{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "returns flow") {
		t.Errorf("body should point at the POS returns flow, got %s", rr.Body.String())
	}
}

// TestHandler_UnwiredCancelGuards_C2 is the supervisor's C2 ruling at the boundary: with the predicate
// unwired, the ORDER READ still answers 200 with CANCELLED merely absent from legal_actions, while an
// actual CANCEL ATTEMPT answers 503. Fail closed on the offer; the 503 belongs to the attempt alone.
func TestHandler_UnwiredCancelGuards_C2(t *testing.T) {
	h := newHandlerFixtureWithGuards(t, "CONFIRMED", nil) // guards deliberately not wired
	sc := managerAt(txnUser, histBranchA)

	// GET → 200, and CANCELLED must not be offered.
	getRR := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/orders/"+txnOrder, nil)
	getReq.SetPathValue("id", txnOrder)
	getReq = getReq.WithContext(identity.ContextWithStaff(getReq.Context(), sc))
	h.orderRead(getRR, getReq, guards.StaffCaller{})
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET code = %d, want 200, an unwired predicate must not break the order view; body=%s",
			getRR.Code, getRR.Body.String())
	}
	var view OrderView
	if err := json.Unmarshal(getRR.Body.Bytes(), &view); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	for _, a := range view.LegalActions {
		if a == "CANCELLED" {
			t.Errorf("legal_actions = %v, must omit CANCELLED when the predicate cannot be evaluated", view.LegalActions)
		}
	}
	if view.Status != "CONFIRMED" {
		t.Errorf("status = %q, want CONFIRMED", view.Status)
	}

	// POST cancel → 503, naming the unwired seam rather than a generic 500.
	postRR := httptest.NewRecorder()
	h.cancel(postRR, req(t, &sc, txnOrder, `{"reason":"a real reason"}`), guards.StaffCaller{})
	if postRR.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST cancel code = %d, want 503; body=%s", postRR.Code, postRR.Body.String())
	}
}

// TestHandler_OrderRead_OK exercises the happy path of the new staff read.
func TestHandler_OrderRead_OK(t *testing.T) {
	h := newHandlerFixture(t, "DRAFT")
	sc := staffAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/orders/"+txnOrder, nil)
	r.SetPathValue("id", txnOrder)
	r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
	h.orderRead(rr, r, guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var view OrderView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if view.Status != "DRAFT" || view.ID != txnOrder {
		t.Fatalf("view = %+v, want DRAFT for %s", view, txnOrder)
	}
	if len(view.LegalActions) != 1 || view.LegalActions[0] != "CONFIRMED" {
		t.Fatalf("legal_actions = %v, want exactly [CONFIRMED] for a frontline caller on a DRAFT order",
			view.LegalActions)
	}
}

func TestHandler_MarkPicking_OK(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := staffAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.markPicking(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out["status"] != "PICKING" {
		t.Fatalf("status = %q, want PICKING", out["status"])
	}
}

func TestHandler_MarkPicking_CrossBranchForbidden(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := staffAt(txnUser, histBranchB) // order is in histBranchA
	rr := httptest.NewRecorder()
	h.markPicking(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rr.Code)
	}
}

func TestHandler_MarkPicking_InactiveForbidden(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := inactiveStaff(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.markPicking(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for an inactive caller", rr.Code)
	}
}

func TestHandler_MarkPicking_Unauthenticated(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	rr := httptest.NewRecorder()
	h.markPicking(rr, req(t, nil, txnOrder, ""), guards.StaffCaller{}) // no StaffContext on the request
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rr.Code)
	}
}

func TestHandler_MarkPicking_MissingOrder(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := globalStaff(txnUser)
	rr := httptest.NewRecorder()
	h.markPicking(rr, req(t, &sc, "10000000-0000-4000-8000-000000000000", ""), guards.StaffCaller{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

func TestHandler_MarkReady_IllegalTransitionConflict(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED") // READY only legal from PICKING
	sc := staffAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.markReady(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 for an illegal forward-only move", rr.Code)
	}
}

// U5: forward-only refusal at the HTTP boundary, READY→PICKING is a 409.
func TestHandler_ForwardOnly_ReadyToPickingConflict(t *testing.T) {
	h := newHandlerFixture(t, "READY")
	sc := staffAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.markPicking(rr, req(t, &sc, txnOrder, ""), guards.StaffCaller{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 (cannot go READY→PICKING)", rr.Code)
	}
}

func TestHandler_Cancel_MissingReasonBadRequest(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := managerAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.cancel(rr, req(t, &sc, txnOrder, `{"reason":""}`), guards.StaffCaller{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 when the cancellation reason is blank", rr.Code)
	}
}

func TestHandler_Cancel_OK(t *testing.T) {
	h := newHandlerFixture(t, "CONFIRMED")
	sc := managerAt(txnUser, histBranchA)
	rr := httptest.NewRecorder()
	h.cancel(rr, req(t, &sc, txnOrder, `{"reason":"customer cancelled"}`), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["status"] != "CANCELLED" {
		t.Fatalf("status = %q, want CANCELLED", out["status"])
	}
}
