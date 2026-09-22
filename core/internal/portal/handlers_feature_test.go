package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/procurement"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// These unit tests drive the BFF feature handlers via in-memory fakes of each domain
// module's store seam (no DB), mirroring the service_test.go style. They prove two
// contract guarantees: every domain call is scoped by the caller's CustomerID, and the
// writeErr mapper translates each domain sentinel to its HTTP status.

const (
	tenantA = "cust-aaaa"
	tenantB = "cust-bbbb"
)

// reqWithCaller builds a request carrying an authenticated caller in its context, as the
// route guards would, so a feature handler can be invoked directly.
func reqWithCaller(method, target, body, customerID string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	c := PortalContext{UserID: "u1", CustomerID: customerID, Role: string(RoleAccountAdmin)}
	return r.WithContext(context.WithValue(r.Context(), callerKey, c))
}

// -- fake domain stores ---------------------------------------------------------

type fakeBillingStore struct {
	gotCustomerID string
	invoice       billing.Invoice
	balance       int64
	err           error
}

func (f *fakeBillingStore) ListInvoices(_ context.Context, customerID string, _ billing.Filter) ([]billing.Invoice, error) {
	f.gotCustomerID = customerID
	if f.err != nil {
		return nil, f.err
	}
	return []billing.Invoice{f.invoice}, nil
}

func (f *fakeBillingStore) GetInvoice(_ context.Context, customerID, _ string) (billing.Invoice, error) {
	f.gotCustomerID = customerID
	if f.err != nil {
		return billing.Invoice{}, f.err
	}
	return f.invoice, nil
}

func (f *fakeBillingStore) OpenBalanceCents(_ context.Context, customerID string) (int64, error) {
	f.gotCustomerID = customerID
	return f.balance, f.err
}

type fakeCustomerStore struct {
	gotCustomerID string
	account       customer.Account
	err           error
}

func (f *fakeCustomerStore) ResolveAccount(_ context.Context, customerID string) (customer.Account, error) {
	f.gotCustomerID = customerID
	if f.err != nil {
		return customer.Account{}, f.err
	}
	return f.account, nil
}
func (f *fakeCustomerStore) ListContacts(_ context.Context, _ string) ([]customer.Contact, error) {
	return nil, nil
}
func (f *fakeCustomerStore) GetPrimaryContact(_ context.Context, _ string) (customer.Contact, error) {
	return customer.Contact{}, customer.ErrNotFound
}
func (f *fakeCustomerStore) SearchAccounts(_ context.Context, _ string, _ int) ([]customer.SearchResult, error) {
	return nil, nil
}

type fakeOrderStore struct {
	gotCustomerID string
	orders        []order.Order
}

func (f *fakeOrderStore) ListOrders(_ context.Context, customerID string, _ order.Filter) ([]order.Order, error) {
	f.gotCustomerID = customerID
	return f.orders, nil
}
func (f *fakeOrderStore) GetOrder(_ context.Context, customerID, _ string) (order.Detail, error) {
	f.gotCustomerID = customerID
	return order.Detail{}, order.ErrNotFound
}
func (f *fakeOrderStore) SetDeliveryInstructions(_ context.Context, customerID, _, _ string) error {
	f.gotCustomerID = customerID
	return nil
}

// UpdateStatus satisfies the DIB-159 status-transition seam on orderStore. The portal BFF never drives
// a status transition, so this fake just reports not-found.
func (f *fakeOrderStore) UpdateStatus(_ context.Context, _, _, _ string) (string, error) {
	return "", order.ErrNotFound
}

// UpdateStatusFrom satisfies the DIB-191 guarded-transition seam on orderStore. The portal BFF never
// drives a lifecycle transition, so this fake just reports not-found.
func (f *fakeOrderStore) UpdateStatusFrom(_ context.Context, _, _ string, _ []string) (string, error) {
	return "", order.ErrNotFound
}

// UpdateStatusFromChecked satisfies the B2-2 locked-predicate cancel seam on orderStore. The portal BFF
// never drives a cancel, so this fake just reports not-found.
func (f *fakeOrderStore) UpdateStatusFromChecked(_ context.Context, _, _ string, _ []string, _ func(context.Context) error) (string, error) {
	return "", order.ErrNotFound
}

// OrderHeader satisfies the B2/R6 staff-read seam on orderStore. The portal BFF never drives the
// staff order read, so this fake just reports not-found.
func (f *fakeOrderStore) OrderHeader(_ context.Context, _ string) (string, string, error) {
	return "", "", order.ErrNotFound
}

// OrderBranch satisfies the DIB-159 branch-scope seam on orderStore (the staff-gated history read's
// branch source). The portal BFF never drives the staff history read, so this fake just reports not-found.
func (f *fakeOrderStore) OrderBranch(_ context.Context, _ string) (string, error) {
	return "", order.ErrNotFound
}

// ResolveOrderID satisfies the F3 order_no-alternate-key seam on orderStore. The portal BFF never
// drives the staff order read/transitions, so this fake is a passthrough (never exercised).
func (f *fakeOrderStore) ResolveOrderID(_ context.Context, idOrNo string, _ bool, _ []string) (string, error) {
	return idOrNo, nil
}

type fakeQuoteStore struct {
	gotCustomerID string
	convertErr    error
}

func (f *fakeQuoteStore) ListQuotes(_ context.Context, customerID string, _ quote.Filter) ([]quote.Quote, error) {
	f.gotCustomerID = customerID
	return nil, nil
}
func (f *fakeQuoteStore) GetQuote(_ context.Context, customerID, _ string) (quote.Detail, error) {
	f.gotCustomerID = customerID
	return quote.Detail{}, quote.ErrNotFound
}
func (f *fakeQuoteStore) Accept(_ context.Context, customerID, _ string) (quote.Quote, error) {
	f.gotCustomerID = customerID
	return quote.Quote{}, nil
}
func (f *fakeQuoteStore) Reject(_ context.Context, customerID, _ string) (quote.Quote, error) {
	f.gotCustomerID = customerID
	return quote.Quote{}, nil
}
func (f *fakeQuoteStore) Convert(_ context.Context, customerID, _ string, _ quote.ConvertOptions) (quote.ConvertResult, error) {
	f.gotCustomerID = customerID
	return quote.ConvertResult{}, f.convertErr
}

// fakeProcurementStore is an in-memory stand-in for the procurement store seam. It records
// the customerID every call was scoped by (so a test can prove tenant scoping) and returns
// canned rows / errors. getErr lets a test simulate the IDOR-safe cross-tenant miss.
type fakeProcurementStore struct {
	gotCustomerID string
	project       procurement.Project
	detail        procurement.ProjectDetail
	line          procurement.Line
	getErr        error
}

func (f *fakeProcurementStore) CreateProject(_ context.Context, customerID, _, name string) (procurement.Project, error) {
	f.gotCustomerID = customerID
	p := f.project
	p.CustomerID = customerID
	p.Name = name
	return p, nil
}
func (f *fakeProcurementStore) ListProjects(_ context.Context, customerID string) ([]procurement.Project, error) {
	f.gotCustomerID = customerID
	return nil, nil
}
func (f *fakeProcurementStore) GetProject(_ context.Context, customerID, _ string) (procurement.ProjectDetail, error) {
	f.gotCustomerID = customerID
	if f.getErr != nil {
		return procurement.ProjectDetail{}, f.getErr
	}
	return f.detail, nil
}
func (f *fakeProcurementStore) UpdateProject(_ context.Context, customerID, _ string, _, _ *string) (procurement.Project, error) {
	f.gotCustomerID = customerID
	return f.project, nil
}
func (f *fakeProcurementStore) DeleteProject(_ context.Context, customerID, _ string) error {
	f.gotCustomerID = customerID
	return nil
}
func (f *fakeProcurementStore) AddLine(_ context.Context, customerID, _ string, _ procurement.AddLineRequest) (procurement.Line, error) {
	f.gotCustomerID = customerID
	return f.line, nil
}
func (f *fakeProcurementStore) UpdateLine(_ context.Context, customerID, _, _ string, _ procurement.UpdateLineRequest) (procurement.Line, error) {
	f.gotCustomerID = customerID
	return f.line, nil
}
func (f *fakeProcurementStore) RemoveLine(_ context.Context, customerID, _, _ string) error {
	f.gotCustomerID = customerID
	return nil
}
func (f *fakeProcurementStore) SubmitRFQ(_ context.Context, customerID, _ string, _ []procurement.PricedLine) (procurement.ProjectDetail, error) {
	f.gotCustomerID = customerID
	return f.detail, nil
}

// The pricing/account/catalog seams SubmitRFQ composes. SubmitRFQ answers 501 until all three
// are injected, so the 409 (non-PLANNING) test wires trivial fakes to reach the state check.
type fakePriceDeriver struct{}

func (fakePriceDeriver) DeriveLinePrice(_ context.Context, _ pricing.ResolveInput) (pricing.LinePrice, error) {
	return pricing.LinePrice{}, nil
}

type fakeAccountResolver struct{}

func (fakeAccountResolver) ResolveAccount(_ context.Context, _ string) (customer.Account, error) {
	return customer.Account{}, nil
}

type fakeCategoryResolver struct{}

func (fakeCategoryResolver) GetCategory(_ context.Context, _ string) (string, error) {
	return "", nil
}

// fakeProductStore is an in-memory stand-in for the product store seam. `searched` records whether
// the search path was reached; `row`/`err` back the DIB-479 detail path (GET /catalog/products/{id}),
// which reads one product by id.
type fakeProductStore struct {
	searched bool
	results  []product.CatalogResult
	row      product.Product
	err      error
}

func (f *fakeProductStore) List(_ context.Context, _ product.Filter, _ []string) ([]product.Product, error) {
	return nil, nil
}
func (f *fakeProductStore) Facets(_ context.Context, _ product.Filter, _ []string) ([]product.Facet, error) {
	return nil, nil
}
func (f *fakeProductStore) Get(_ context.Context, _ string) (product.Product, error) {
	if f.err != nil {
		return product.Product{}, f.err
	}
	return f.row, nil
}
func (f *fakeProductStore) Update(_ context.Context, _ string, _ map[string]any) (product.Product, error) {
	return product.Product{}, nil
}
func (f *fakeProductStore) Search(_ context.Context, _ string, _ int) ([]product.CatalogResult, error) {
	f.searched = true
	return f.results, nil
}
func (f *fakeProductStore) GetCategory(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeProductStore) ConvertUOM(_ context.Context, _, _ string) (float64, error) {
	return 0, nil
}
func (f *fakeProductStore) GetBaseUOM(_ context.Context, _ string) (string, error) { return "", nil }

// -- error mapping ---------------------------------------------------------------

func TestWriteErr_DomainSentinels(t *testing.T) {
	h := NewHandler(nil, nil)
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"customer not found", customer.ErrNotFound, http.StatusNotFound},
		{"billing not found", billing.ErrNotFound, http.StatusNotFound},
		{"order not found", order.ErrNotFound, http.StatusNotFound},
		{"quote not found", quote.ErrNotFound, http.StatusNotFound},
		{"payments not found", payments.ErrNotFound, http.StatusNotFound},
		{"quote invalid transition", quote.ErrInvalidTransition, http.StatusConflict},
		{"procurement not found", procurement.ErrNotFound, http.StatusNotFound},
		{"procurement invalid transition", procurement.ErrInvalidTransition, http.StatusConflict},
		{"procurement not implemented", procurement.ErrNotImplemented, http.StatusNotImplemented},
		{"payment declined", payments.ErrDeclined, http.StatusPaymentRequired},
		{"payment pending", payments.ErrChargePending, http.StatusConflict},
		{"client bad request", httpx.BadRequestf("bad filter"), http.StatusBadRequest},
		{"unknown -> 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.writeErr(w, httptest.NewRequest(http.MethodGet, "/x", nil), c.err)
			if w.Code != c.want {
				t.Fatalf("writeErr(%v) = %d, want %d", c.err, w.Code, c.want)
			}
		})
	}
}

// A wrapped domain sentinel must still map (errors.Is unwraps), and a 500 must never echo
// the internal error text to the client.
func TestWriteErr_WrappedAndNoLeak(t *testing.T) {
	h := NewHandler(nil, nil)
	w := httptest.NewRecorder()
	h.writeErr(w, httptest.NewRequest(http.MethodGet, "/x", nil), fmt.Errorf("ctx: %w", quote.ErrInvalidTransition))
	if w.Code != http.StatusConflict {
		t.Fatalf("wrapped ErrInvalidTransition = %d, want 409", w.Code)
	}
	w = httptest.NewRecorder()
	h.writeErr(w, httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("secret db dsn leaked"))
	if strings.Contains(w.Body.String(), "secret db dsn") {
		t.Fatalf("500 body leaked internal error: %s", w.Body.String())
	}
}

// -- tenant scoping --------------------------------------------------------------

func TestGetInvoice_TenantScopedNotFound(t *testing.T) {
	store := &fakeBillingStore{err: billing.ErrNotFound}
	h := NewHandler(nil, nil).WithBilling(billing.NewService(store))

	r := reqWithCaller(http.MethodGet, "/api/portal/v1/invoices/inv-9", "", tenantA)
	r.SetPathValue("id", "inv-9")
	w := httptest.NewRecorder()
	h.getInvoice(w, r)

	if store.gotCustomerID != tenantA {
		t.Fatalf("invoice read scoped to %q, want %q", store.gotCustomerID, tenantA)
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant/missing invoice = %d, want 404", w.Code)
	}
}

func TestListInvoices_BadStatusIs400(t *testing.T) {
	store := &fakeBillingStore{}
	h := NewHandler(nil, nil).WithBilling(billing.NewService(store))

	r := reqWithCaller(http.MethodGet, "/api/portal/v1/invoices?status=BOGUS", "", tenantA)
	w := httptest.NewRecorder()
	h.listInvoices(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid status filter = %d, want 400", w.Code)
	}
	if store.gotCustomerID != "" {
		t.Fatalf("store must not be called on an invalid filter, got scope %q", store.gotCustomerID)
	}
}

func TestConvertQuote_InvalidTransitionIs409(t *testing.T) {
	store := &fakeQuoteStore{convertErr: quote.ErrInvalidTransition}
	h := NewHandler(nil, nil).WithQuotes(quote.NewService(store))

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/quotes/q-1/convert", "", tenantB)
	r.SetPathValue("id", "q-1")
	w := httptest.NewRecorder()
	h.convertQuote(w, r)

	if store.gotCustomerID != tenantB {
		t.Fatalf("convert scoped to %q, want %q", store.gotCustomerID, tenantB)
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("convert non-ACCEPTED quote = %d, want 409", w.Code)
	}
}

func TestSetDeliveryInstructions_TooLongIs400(t *testing.T) {
	store := &fakeOrderStore{}
	h := NewHandler(nil, nil).WithOrders(order.NewService(store))

	body, _ := json.Marshal(map[string]string{"instructions": strings.Repeat("x", maxDeliveryInstructionsLen+1)})
	r := reqWithCaller(http.MethodPut, "/api/portal/v1/orders/o-1/delivery-instructions", string(body), tenantA)
	r.SetPathValue("id", "o-1")
	w := httptest.NewRecorder()
	h.setDeliveryInstructions(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("over-long instructions = %d, want 400", w.Code)
	}
	if store.gotCustomerID != "" {
		t.Fatalf("store must not be called on an over-long note, got scope %q", store.gotCustomerID)
	}
}

// dashboard composes the customer + billing seams, fills the AR balance into the account,
// and floors available credit; every read is scoped to the caller's tenant.
func TestDashboard_ComposesAndScopes(t *testing.T) {
	cust := &fakeCustomerStore{account: customer.Account{CustomerID: tenantA, CreditLimitCents: 100000}}
	bill := &fakeBillingStore{balance: 30000}
	ord := &fakeOrderStore{orders: []order.Order{{ID: "o-1"}}}
	h := NewHandler(nil, nil).
		WithCustomers(customer.NewService(cust)).
		WithBilling(billing.NewService(bill)).
		WithOrders(order.NewService(ord))

	r := reqWithCaller(http.MethodGet, "/api/portal/v1/dashboard", "", tenantA)
	w := httptest.NewRecorder()
	h.dashboard(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("dashboard = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if cust.gotCustomerID != tenantA || bill.gotCustomerID != tenantA || ord.gotCustomerID != tenantA {
		t.Fatalf("dashboard not scoped to %q: cust=%q bill=%q ord=%q",
			tenantA, cust.gotCustomerID, bill.gotCustomerID, ord.gotCustomerID)
	}
	var resp DashboardResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OpenBalanceCents != 30000 {
		t.Errorf("open balance = %d, want 30000", resp.OpenBalanceCents)
	}
	if resp.AvailableCreditCents != 70000 {
		t.Errorf("available credit = %d, want 70000 (limit 100000 - bal 30000)", resp.AvailableCreditCents)
	}
	if resp.Account.BalanceCents != 30000 {
		t.Errorf("composed account balance = %d, want 30000", resp.Account.BalanceCents)
	}
	if len(resp.RecentOrders) != 1 {
		t.Errorf("recent orders = %d, want 1", len(resp.RecentOrders))
	}
}

// availableCredit never goes negative: an over-limit account has zero headroom.
func TestAvailableCredit_FloorsAtZero(t *testing.T) {
	if got := availableCredit(100000, 130000); got != 0 {
		t.Fatalf("availableCredit(100000,130000) = %d, want 0", got)
	}
	if got := availableCredit(100000, 40000); got != 60000 {
		t.Fatalf("availableCredit(100000,40000) = %d, want 60000", got)
	}
}

// -- procurement + catalog handlers -----------------------------------------------

// A project id owned by another tenant resolves to 404 (IDOR-safe): the store gets the
// caller's CustomerID and a cross-tenant row returns ErrNotFound, which maps to 404.
func TestGetProject_TenantScopedNotFound(t *testing.T) {
	store := &fakeProcurementStore{getErr: procurement.ErrNotFound}
	h := NewHandler(nil, nil).WithProcurement(procurement.NewService(store))

	r := reqWithCaller(http.MethodGet, "/api/portal/v1/procurement/projects/p-9", "", tenantA)
	r.SetPathValue("id", "p-9")
	w := httptest.NewRecorder()
	h.getProject(w, r)

	if store.gotCustomerID != tenantA {
		t.Fatalf("project read scoped to %q, want %q", store.gotCustomerID, tenantA)
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant project = %d, want 404", w.Code)
	}
}

// Creating a project returns 201 with the persisted object, scoped to the caller's tenant.
func TestCreateProject_Returns201(t *testing.T) {
	store := &fakeProcurementStore{project: procurement.Project{ID: "p-1", Status: procurement.StatusPlanning}}
	h := NewHandler(nil, nil).WithProcurement(procurement.NewService(store))

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/procurement/projects", `{"name":"Patio Rebuild"}`, tenantA)
	w := httptest.NewRecorder()
	h.createProject(w, r)

	if store.gotCustomerID != tenantA {
		t.Fatalf("create scoped to %q, want %q", store.gotCustomerID, tenantA)
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("create project = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	var p procurement.Project
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "Patio Rebuild" {
		t.Errorf("created name = %q, want Patio Rebuild", p.Name)
	}
}

// Submitting an RFQ on a project that is not in PLANNING is an illegal lane move: the
// procurement service returns ErrInvalidTransition, which maps to 409.
func TestSubmitRFQ_NonPlanningIs409(t *testing.T) {
	store := &fakeProcurementStore{detail: procurement.ProjectDetail{
		Project: procurement.Project{ID: "p-1", Status: procurement.StatusQuoting},
	}}
	svc := procurement.NewService(store).
		WithPricing(fakePriceDeriver{}).
		WithAccounts(fakeAccountResolver{}).
		WithCatalog(fakeCategoryResolver{})
	h := NewHandler(nil, nil).WithProcurement(svc)

	r := reqWithCaller(http.MethodPost, "/api/portal/v1/procurement/projects/p-1/submit-rfq", "", tenantA)
	r.SetPathValue("id", "p-1")
	w := httptest.NewRecorder()
	h.submitProjectRFQ(w, r)

	if store.gotCustomerID != tenantA {
		t.Fatalf("submit-rfq scoped to %q, want %q", store.gotCustomerID, tenantA)
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("submit-rfq on non-PLANNING project = %d, want 409", w.Code)
	}
}

// An empty catalog search query is a client error (400): the product service rejects it via
// httpx.BadRequestf before reaching the store.
func TestSearchCatalog_EmptyQueryIs400(t *testing.T) {
	store := &fakeProductStore{}
	h := NewHandler(nil, nil).WithProducts(product.NewService(store))

	r := reqWithCaller(http.MethodGet, "/api/portal/v1/catalog/search?q=", "", tenantA)
	w := httptest.NewRecorder()
	h.searchCatalog(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty catalog query = %d, want 400", w.Code)
	}
	if store.searched {
		t.Fatalf("store.Search must not run on an empty query")
	}
}

// A nil seam answers 501 (honest "not wired") rather than panicking.
func TestNilSeam_Is501(t *testing.T) {
	h := NewHandler(nil, nil)
	r := reqWithCaller(http.MethodGet, "/api/portal/v1/invoices", "", tenantA)
	w := httptest.NewRecorder()
	h.listInvoices(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("nil billing seam = %d, want 501", w.Code)
	}
}
