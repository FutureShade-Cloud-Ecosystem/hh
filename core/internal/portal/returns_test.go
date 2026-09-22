package portal

// returns_test.go, A-06 BFF contract tests (the handlers_feature_test.go style): every returns call is
// scoped by the VERIFIED caller's CustomerID (never the body), the pos client-error taxonomy passes
// through with its status (the 404 tenant guard itself is proven in internal/pos), unknown errors stay
// a logged 500, and a nil seam answers 501.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

type fakeReturnsSvc struct {
	gotCustomerID string
	gotOrderID    string
	gotInput      pos.CreateReturnRequestInput
	lookup        pos.ReturnLookup
	rr            pos.ReturnRequest
	list          []pos.ReturnRequest
	err           error
}

func (f *fakeReturnsSvc) ReturnEligibility(_ context.Context, customerID, orderID string) (pos.ReturnLookup, error) {
	f.gotCustomerID, f.gotOrderID = customerID, orderID
	return f.lookup, f.err
}

func (f *fakeReturnsSvc) CreateReturnRequest(_ context.Context, in pos.CreateReturnRequestInput) (pos.ReturnRequest, error) {
	f.gotCustomerID, f.gotInput = in.CustomerID, in
	return f.rr, f.err
}

func (f *fakeReturnsSvc) ListCustomerReturnRequests(_ context.Context, customerID string) ([]pos.ReturnRequest, error) {
	f.gotCustomerID = customerID
	return f.list, f.err
}

// posClientErr manufactures a REAL pos client error (a bare service refuses an empty tenant before any
// store touch), so the passthrough mapping is tested against the actual taxonomy type.
func posClientErr(t *testing.T) error {
	t.Helper()
	_, err := pos.NewService(nil).ReturnEligibility(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected a pos client error")
	}
	if _, ok := pos.ClientStatus(err); !ok {
		t.Fatal("expected pos.ClientStatus to recognize the error")
	}
	return err
}

func TestReturns_Eligibility_ScopedByCaller(t *testing.T) {
	f := &fakeReturnsSvc{lookup: pos.ReturnLookup{OrderID: "o-1", CustomerID: tenantA}}
	h := NewHandler(nil, nil).WithReturns(f)
	r := reqWithCaller(http.MethodGet, "/api/portal/v1/orders/o-1/return-eligibility", "", tenantA)
	r.SetPathValue("id", "o-1")
	w := httptest.NewRecorder()
	h.returnEligibility(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("eligibility = %d: %s", w.Code, w.Body)
	}
	if f.gotCustomerID != tenantA || f.gotOrderID != "o-1" {
		t.Fatalf("must scope by the caller's tenant + the path order: %q %q", f.gotCustomerID, f.gotOrderID)
	}
}

func TestReturns_Create_TenantAndActorFromToken_NeverBody(t *testing.T) {
	f := &fakeReturnsSvc{rr: pos.ReturnRequest{ID: "rreq-1", Status: pos.RequestPending}}
	h := NewHandler(nil, nil).WithReturns(f)
	// A hostile body naming tenant B must be ignored, scoping comes from the verified token (tenant A).
	body := `{"reason_code":"DAMAGED","note":"","customer_id":"` + tenantB + `","lines":[{"order_line_id":"l-1","qty_base":2}]}`
	r := reqWithCaller(http.MethodPost, "/api/portal/v1/orders/o-1/return-requests", body, tenantA)
	r.SetPathValue("id", "o-1")
	w := httptest.NewRecorder()
	h.createReturnRequest(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body)
	}
	in := f.gotInput
	if in.CustomerID != tenantA || in.RequestedBy != "u1" || in.OrderID != "o-1" {
		t.Fatalf("tenant/actor/order must come from token + path: %+v", in)
	}
	if in.ReasonCode != "DAMAGED" || len(in.Lines) != 1 || in.Lines[0].QtyBase != 2 {
		t.Fatalf("body fields lost: %+v", in)
	}
}

func TestReturns_List_ScopedAndNeverNil(t *testing.T) {
	f := &fakeReturnsSvc{list: nil} // a fresh account has no requests
	h := NewHandler(nil, nil).WithReturns(f)
	w := httptest.NewRecorder()
	h.listReturnRequests(w, reqWithCaller(http.MethodGet, "/api/portal/v1/return-requests", "", tenantA))
	if w.Code != http.StatusOK || f.gotCustomerID != tenantA {
		t.Fatalf("list = %d scoped to %q", w.Code, f.gotCustomerID)
	}
	var items []pos.ReturnRequest
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil || items == nil {
		t.Fatalf("list must serialize [] never null: %s (%v)", w.Body, err)
	}
}

// M2, staff identity never leaves through the portal: a decided request's decided_by (staff user
// UUID) must not serialize on the tenant list or the create response; the tenant-relevant decision
// facts (when/why, fee, linked return) stay.
func TestReturns_PortalProjection_OmitsDecidedBy(t *testing.T) {
	decided := pos.ReturnRequest{
		ID: "rreq-1", RequestNo: "#RREQ-1", OrderID: "o-1", OrderNo: "#SO-1",
		CustomerID: tenantA, Status: pos.RequestApproved,
		DecidedBy: "11111111-2222-4333-8444-555555555555", DecidedAt: "2026-07-13T12:00:00Z",
		DecisionReason: "ok", FeeCents: 400, ReturnID: "ret-1", ReturnNo: "#RET-1",
	}
	f := &fakeReturnsSvc{list: []pos.ReturnRequest{decided}, rr: decided}
	h := NewHandler(nil, nil).WithReturns(f)

	w := httptest.NewRecorder()
	h.listReturnRequests(w, reqWithCaller(http.MethodGet, "/api/portal/v1/return-requests", "", tenantA))
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if strings.Contains(body, "decided_by") || strings.Contains(body, decided.DecidedBy) {
		t.Fatalf("portal list must not carry staff attribution: %s", body)
	}
	for _, want := range []string{"decided_at", "decision_reason", "fee_cents", "#RET-1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("portal list lost a tenant-relevant field %q: %s", want, body)
		}
	}

	// The create response rides the same projection.
	w = httptest.NewRecorder()
	r := reqWithCaller(http.MethodPost, "/api/portal/v1/orders/o-1/return-requests",
		`{"reason_code":"DAMAGED","note":"x","lines":[{"order_line_id":"l-1","qty_base":1}]}`, tenantA)
	r.SetPathValue("id", "o-1")
	h.createReturnRequest(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "decided_by") {
		t.Fatalf("portal create response must not carry staff attribution: %s", w.Body)
	}
}

func TestReturns_ErrorMapping(t *testing.T) {
	// A pos client error passes through with its own status (400 here; the tenant-miss 404 rides the
	// same path, proven status-for-status in internal/pos).
	f := &fakeReturnsSvc{err: posClientErr(t)}
	h := NewHandler(nil, nil).WithReturns(f)
	w := httptest.NewRecorder()
	h.listReturnRequests(w, reqWithCaller(http.MethodGet, "/api/portal/v1/return-requests", "", tenantA))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("pos client error must keep its status, got %d", w.Code)
	}
	// An unknown error is a generic 500 (no internal detail leaked).
	f.err = context.DeadlineExceeded
	w = httptest.NewRecorder()
	h.listReturnRequests(w, reqWithCaller(http.MethodGet, "/api/portal/v1/return-requests", "", tenantA))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("unknown error must 500, got %d", w.Code)
	}
	// A nil seam answers honestly.
	h2 := NewHandler(nil, nil)
	w = httptest.NewRecorder()
	h2.listReturnRequests(w, reqWithCaller(http.MethodGet, "/api/portal/v1/return-requests", "", tenantA))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("nil seam must 501, got %d", w.Code)
	}
}
