package portal

// reorder_test.go, CP-04 BFF handler tests (DB-free fakes, mirrors
// handlers_feature_test.go): tenant scoping (US-RO-04), today's-rates repricing
// (US-RO-01), partial selection (US-RO-02), and the 409 unavailable rejection
// (US-RO-03) as they surface over HTTP.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// fakeReorderOrderStore satisfies the order module's store seam PLUS its reorder
// extension (ReorderSource / CreateReorderDraft), enforcing tenant scope like the pgx
// repository: a foreign order reads as order.ErrNotFound.
type fakeReorderOrderStore struct {
	fakeOrderStore
	gotCustomerID string
	owner         string
	source        order.ReorderSource
	created       *order.ReorderDraft
}

func (f *fakeReorderOrderStore) ReorderSource(_ context.Context, customerID, orderID string) (order.ReorderSource, error) {
	f.gotCustomerID = customerID
	if customerID != f.owner || orderID != f.source.OrderID {
		return order.ReorderSource{}, order.ErrNotFound
	}
	return f.source, nil
}

func (f *fakeReorderOrderStore) CreateReorderDraft(_ context.Context, d order.ReorderDraft) (order.ReorderResult, error) {
	f.created = &d
	return order.ReorderResult{OrderID: "draft-1", OrderNo: "#SO-42", Status: "DRAFT"}, nil
}

// currentPricer prices every product at a fixed today's rate, distinct from the
// source order's historical prices.
type currentPricer struct{ unit int64 }

func (p currentPricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	return pricing.Breakdown{
		FinalCents:     p.unit,
		PriceUOM:       in.UOMEntered,
		QtyBase:        in.QtyEntered,
		LineTotalCents: int64(in.QtyEntered * float64(p.unit)),
	}, nil
}

// flaggingInventory marks configured products unavailable; everything else is in stock.
type flaggingInventory struct{ unavailable map[string]string }

func (i flaggingInventory) CheckAvailability(_ context.Context, productID, _ string, _ float64) (bool, string, error) {
	if r, bad := i.unavailable[productID]; bad {
		return false, r, nil
	}
	return true, "", nil
}
func (flaggingInventory) CurrentCostCents(_ context.Context, _, _ string) (int64, bool, error) {
	return 500, true, nil
}

func reorderTestHandler(t *testing.T, flagged map[string]string) (*Handler, *fakeReorderOrderStore) {
	t.Helper()
	store := &fakeReorderOrderStore{
		owner: tenantA,
		source: order.ReorderSource{
			OrderID: "ord-1", OrderNo: "#SO-1", BranchID: "b-1", Fulfillment: "PICKUP",
			Lines: []order.ReorderSourceLine{
				{LineNo: 1, ProductID: "prod-a", Description: "Paver A", QtyEntered: "10", UOMEntered: "EA", QtyBase: "10"},
				{LineNo: 2, ProductID: "prod-b", Description: "Gravel B", QtyEntered: "3", UOMEntered: "TON", QtyBase: "3"},
			},
		},
	}
	orderSvc := order.NewService(store).
		WithReorderPricing(currentPricer{unit: 777}).
		WithReorderInventory(flaggingInventory{unavailable: flagged})
	cust := &fakeCustomerStore{account: customer.Account{CustomerID: tenantA, CustomerType: "CONTRACTOR"}}
	h := NewHandler(nil, nil).WithOrders(orderSvc).WithCustomers(customer.NewService(cust))
	return h, store
}

func doReorderPreview(h *Handler, tenant string) *httptest.ResponseRecorder {
	r := reqWithCaller(http.MethodGet, "/api/portal/v1/orders/ord-1/reorder-preview", "", tenant)
	r.SetPathValue("id", "ord-1")
	w := httptest.NewRecorder()
	h.reorderPreview(w, r)
	return w
}

func doReorder(h *Handler, tenant, body string) *httptest.ResponseRecorder {
	r := reqWithCaller(http.MethodPost, "/api/portal/v1/orders/ord-1/reorder", body, tenant)
	r.SetPathValue("id", "ord-1")
	w := httptest.NewRecorder()
	h.createReorder(w, r)
	return w
}

// US-RO-01: the preview carries TODAY'S price on every line, not the stored one.
func TestReorderPreview_TodaysRates(t *testing.T) {
	h, store := reorderTestHandler(t, nil)
	w := doReorderPreview(h, tenantA)
	if w.Code != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if store.gotCustomerID != tenantA {
		t.Fatalf("source read scoped to %q, want %q", store.gotCustomerID, tenantA)
	}
	var prev order.ReorderPreview
	if err := json.Unmarshal(w.Body.Bytes(), &prev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(prev.Lines) != 2 || prev.Lines[0].UnitPriceCents != 777 || !prev.Lines[0].Available {
		t.Fatalf("preview lines = %+v, want 2 available lines at today's 777", prev.Lines)
	}
}

// US-RO-03 (read half): flagged lines surface with their machine reason.
func TestReorderPreview_FlagsUnavailable(t *testing.T) {
	h, _ := reorderTestHandler(t, map[string]string{"prod-b": "OUT_OF_STOCK"})
	w := doReorderPreview(h, tenantA)
	var prev order.ReorderPreview
	_ = json.Unmarshal(w.Body.Bytes(), &prev)
	if prev.Lines[1].Available || prev.Lines[1].Reason != "OUT_OF_STOCK" {
		t.Fatalf("line 2 = %+v, want flagged OUT_OF_STOCK", prev.Lines[1])
	}
}

// US-RO-04: a foreign tenant's order id answers 404 on both routes, never 403.
func TestReorder_IDORIs404(t *testing.T) {
	h, _ := reorderTestHandler(t, nil)
	if w := doReorderPreview(h, tenantB); w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant preview = %d, want 404", w.Code)
	}
	if w := doReorder(h, tenantB, `{"lines":[1],"idempotency_key":"k1"}`); w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant reorder = %d, want 404", w.Code)
	}
}

// US-RO-02: a partial selection creates a 201 draft with only the chosen line, repriced.
func TestReorder_PartialCreates201Draft(t *testing.T) {
	h, store := reorderTestHandler(t, nil)
	w := doReorder(h, tenantA, `{"lines":[2],"idempotency_key":"k2"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("reorder = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	var res order.ReorderResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.OrderID != "draft-1" || res.Status != "DRAFT" {
		t.Fatalf("result = %+v, want the new DRAFT id", res)
	}
	d := store.created
	if d == nil || len(d.Lines) != 1 || d.Lines[0].ProductID != "prod-b" {
		t.Fatalf("draft = %+v, want only prod-b", d)
	}
	if d.Lines[0].UnitPriceCents != 777 {
		t.Fatalf("draft price = %d, want today's 777 (defect D1: historical price must never be copied)",
			d.Lines[0].UnitPriceCents)
	}
}

// US-RO-03 (write half): submitting a flagged line is a 409 carrying per-line reasons,
// and no draft is written.
func TestReorder_UnavailableSubmitIs409(t *testing.T) {
	h, store := reorderTestHandler(t, map[string]string{"prod-a": "DISCONTINUED"})
	w := doReorder(h, tenantA, `{"lines":[1,2],"idempotency_key":"k3"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("reorder = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
	var body struct {
		Lines []order.ReorderRejection `json:"lines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	if len(body.Lines) != 1 || body.Lines[0].LineNo != 1 || body.Lines[0].Reason != "DISCONTINUED" {
		t.Fatalf("409 lines = %+v, want line 1 DISCONTINUED", body.Lines)
	}
	if store.created != nil {
		t.Fatal("a 409 submit must not write a draft")
	}
}

// Missing idempotency key / malformed JSON are 400s; unwired seams answer 501.
func TestReorder_BadRequestsAndScaffold(t *testing.T) {
	h, _ := reorderTestHandler(t, nil)
	if w := doReorder(h, tenantA, `{"lines":[1]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency_key = %d, want 400", w.Code)
	}
	if w := doReorder(h, tenantA, `{not-json`); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body = %d, want 400", w.Code)
	}

	bare := NewHandler(nil, nil) // no seams wired at all
	if w := doReorderPreview(bare, tenantA); w.Code != http.StatusNotImplemented {
		t.Fatalf("unwired preview = %d, want 501", w.Code)
	}
	// Wired BFF seams but no pricing/inventory on the order service → still 501.
	store := &fakeReorderOrderStore{owner: tenantA, source: order.ReorderSource{OrderID: "ord-1"}}
	cust := &fakeCustomerStore{account: customer.Account{CustomerID: tenantA}}
	half := NewHandler(nil, nil).WithOrders(order.NewService(store)).WithCustomers(customer.NewService(cust))
	if w := doReorderPreview(half, tenantA); w.Code != http.StatusNotImplemented {
		t.Fatalf("seamless preview = %d, want 501", w.Code)
	}
}

// The POST is gated on CreateOrders: field_crew (view-only) holds the preview capability
// but not the create one, the guard pair in registerReorderRoutes relies on this split.
func TestReorder_CapabilitySplit(t *testing.T) {
	crew := CapabilitiesFor(string(RoleFieldCrew))
	if !crew.ViewOrdersDeliveries {
		t.Fatal("field_crew must be able to view the reorder preview")
	}
	if crew.CreateOrders {
		t.Fatal("field_crew must NOT hold CreateOrders (the reorder POST gate)")
	}
}
