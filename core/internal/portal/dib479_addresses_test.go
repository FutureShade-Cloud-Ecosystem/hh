package portal

// dib479_addresses_test.go, the account's delivery sites, and the fence around them.
//
// Two properties are under test and only one of them is about addresses: the list must be the
// caller's own sites, and a site id belonging to somebody else must answer 404 rather than 403.
// The second is the whole reason the id read exists in this ticket, 403 confirms the row is real,
// which is exactly the fact an IDOR probe is fishing for.

import (
	"context"
	"encoding/json"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeAddressStore is a projectStore that ALSO implements the optional addressStore, modelling the
// tenant fence the real SQL enforces in its WHERE clause.
type fakeAddressStore struct {
	fakeProjectStore
	byCustomer map[string][]Address
	gotFor     []string // every customerID the store was asked about, in order
}

func (f *fakeAddressStore) ListAddresses(_ context.Context, customerID string) ([]Address, error) {
	f.gotFor = append(f.gotFor, customerID)
	return f.byCustomer[customerID], nil
}

func (f *fakeAddressStore) GetAddress(ctx context.Context, customerID, addressID string) (Address, error) {
	list, _ := f.ListAddresses(ctx, customerID)
	for _, a := range list {
		if a.ID == addressID {
			return a, nil
		}
	}
	return Address{}, ErrAddressNotFound
}

func addressFixture() *fakeAddressStore {
	return &fakeAddressStore{
		byCustomer: map[string][]Address{
			tenantA: {
				{ID: "site:s1", Kind: AddressKindDelivery, Label: "Dana", Line1: "12 Maple Glen", City: "Trenton", Postal: "K8V 5P7"},
				{ID: "project:p1", Kind: AddressKindJobSite, Label: "Riverside Walkway", Line1: "42 Riverside Dr, Trenton, ON"},
			},
			tenantB: {
				{ID: "site:s9", Kind: AddressKindDelivery, Label: "Someone Else", Line1: "1 Other St", City: "Kingston"},
			},
		},
	}
}

func addressHandler(t *testing.T) (*Handler, *fakeAddressStore) {
	t.Helper()
	store := addressFixture()
	return NewHandler(nil, nil).WithProjects(NewProjectsService(store)), store
}

// The list is the CALLER'S sites, and the tenant it is fetched for comes from the authenticated
// context, never from the request.
func TestAddresses_ListIsScopedToTheCallersTenant(t *testing.T) {
	h, store := addressHandler(t)

	r := reqWithCaller("GET", "/api/portal/v1/addresses", "", tenantA)
	w := httptest.NewRecorder()
	h.listAddresses(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d (%s)", w.Code, w.Body.String())
	}
	var got AddressListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Addresses) != 2 {
		t.Fatalf("want tenant A's two sites, got %+v", got.Addresses)
	}
	for _, a := range got.Addresses {
		if a.ID == "site:s9" {
			t.Fatal("another tenant's site appeared in the list")
		}
	}
	if len(store.gotFor) != 1 || store.gotFor[0] != tenantA {
		t.Fatalf("the store must be asked for the CALLER's customer id, got %v", store.gotFor)
	}
}

// THE IDOR TEST. Another tenant's address id, a real, existing row, answers 404, never 403.
//
// This is the test the "403 instead of 404" mutation kills.
func TestAddresses_CrossTenantIDIs404Never403(t *testing.T) {
	h, _ := addressHandler(t)

	// tenant A asks for tenant B's real site id.
	r := reqWithCaller("GET", "/api/portal/v1/addresses/site:s9", "", tenantA)
	r.SetPathValue("id", "site:s9")
	w := httptest.NewRecorder()
	h.getAddress(w, r)
	if w.Code == http.StatusForbidden {
		t.Fatal("a cross-tenant id must NOT answer 403, that confirms the row exists")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant address = %d, want 404 (body %s)", w.Code, w.Body.String())
	}

	// An id that exists nowhere answers identically, the two cases must be indistinguishable.
	r2 := reqWithCaller("GET", "/api/portal/v1/addresses/site:nope", "", tenantA)
	r2.SetPathValue("id", "site:nope")
	w2 := httptest.NewRecorder()
	h.getAddress(w2, r2)
	if w2.Code != w.Code || w2.Body.String() != w.Body.String() {
		t.Fatalf("a cross-tenant miss (%d %s) must be indistinguishable from an unknown id (%d %s)",
			w.Code, w.Body.String(), w2.Code, w2.Body.String())
	}
}

// The caller's OWN id resolves, the fence refuses other people's rows, not everybody's.
func TestAddresses_OwnIDResolves(t *testing.T) {
	h, _ := addressHandler(t)
	r := reqWithCaller("GET", "/api/portal/v1/addresses/project:p1", "", tenantA)
	r.SetPathValue("id", "project:p1")
	w := httptest.NewRecorder()
	h.getAddress(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("own address = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var got Address
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "project:p1" || got.Kind != AddressKindJobSite {
		t.Fatalf("unexpected address: %+v", got)
	}
}

// An account with no sites is an empty LIST, not null and not an error. A client that renders
// `null.map(...)` is a client this endpoint broke.
func TestAddresses_EmptyAccountReturnsAnEmptyList(t *testing.T) {
	h, _ := addressHandler(t)
	r := reqWithCaller("GET", "/api/portal/v1/addresses", "", "cust-with-nothing")
	w := httptest.NewRecorder()
	h.listAddresses(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	if body := w.Body.String(); body != `{"addresses":[]}`+"\n" && body != `{"addresses":[]}` {
		t.Fatalf("an account with no sites must return an empty array, got %q", body)
	}
}

// A store that predates this read answers honestly (501) instead of pretending the account has no
// sites, "not wired here" and "you have none" are different sentences.
func TestAddresses_UnwiredStoreAnswers501(t *testing.T) {
	h := NewHandler(nil, nil).WithProjects(NewProjectsService(newProjectStore()))
	r := reqWithCaller("GET", "/api/portal/v1/addresses", "", tenantA)
	w := httptest.NewRecorder()
	h.listAddresses(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("unwired address store = %d, want 501", w.Code)
	}
}

// The routes are registered behind the kit's real Guards (the bearer-split
// carry note, HH-3.2 fold): the tenant bearer plus a resolved portal row of
// the caller's own role reaches the surface, and no bearer at all is the
// guard's own refusal, not an open route. The capability mapping itself is
// the identity package's carried CapabilitiesFor, proven there.
func TestAddressRoutes_AreGatedOnViewOrdersDeliveries(t *testing.T) {
	userStore := newFakeStore()
	svc := NewService(userStore, testSecret)
	store := addressFixture()
	h := NewHandler(svc, nil).WithProjects(NewProjectsService(store))
	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetTenantOrgs(testkit.StaffSubAny, testkit.OrgNorthSlug)
	bearer := sg.Harness.Minter.TenantToken(t, testkit.StaffSubAny, tenantA)
	sg.SetPortalRole(string(RoleFieldCrew))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	// A resolved, active user of the tenant reaches the surface.
	userStore.addUser(User{ID: "user0", CustomerID: tenantA, Email: "user0@x.test", Role: string(RoleFieldCrew), Status: "active"})
	r := httptest.NewRequest("GET", "/api/v1/tenant/addresses", nil)
	r.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("field_crew: addresses = %d (%s), want 200", w.Code, w.Body.String())
	}

	// And no bearer at all is the guard's refusal, not an open route.
	r = httptest.NewRequest("GET", "/api/v1/tenant/addresses", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("unauthenticated addresses = %d, want the guard's refusal", w.Code)
	}
}
