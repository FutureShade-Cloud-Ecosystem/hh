package customer

// write_fence_test.go (UX-89 round 2): the two customer writes hold the caller's reach. Billing address: an
// account the caller cannot reach (homed outside its grants, or no account) answers 404 and writes nothing.
// Create: a branch scoped caller must home the account at a granted branch (a missing, empty or ungranted home
// is the same 403); only a GLOBAL caller files a company wide (null home) account.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func fenceCaller(scope string, branches ...string) guards.StaffCaller {
	return guards.StaffCaller{StaffAuthority: coreidentity.StaffAuthority{Tier: coreidentity.TierManager, Scope: scope, Branches: branches}}
}

func TestCreateFence_NullHomeIsGlobalOnly(t *testing.T) {
	w := &fakeWriter{}
	h := NewHandler(NewService(newFakeStore()).WithWriter(w), nil)
	post := func(c guards.StaffCaller, home string) *httptest.ResponseRecorder {
		body := `{"account_no":"A-1","name":"N","customer_type":"RETAIL"` + home + `}`
		rec := httptest.NewRecorder()
		h.create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/customers", strings.NewReader(body)), c)
		return rec
	}
	scoped := fenceCaller(coreidentity.ScopeBranch, "b-1")
	for name, home := range map[string]string{"missing": "", "empty": `,"home_branch_id":""`, "ungranted": `,"home_branch_id":"b-2"`} {
		w.lastCreate = CreateInput{}
		if rec := post(scoped, home); rec.Code != http.StatusForbidden || w.lastCreate.AccountNo != "" {
			t.Errorf("scoped caller, home %s: want 403 and nothing written, got %d (%+v)", name, rec.Code, w.lastCreate)
		}
	}
	if rec := post(scoped, `,"home_branch_id":"b-1"`); rec.Code != http.StatusCreated {
		t.Errorf("scoped caller, granted home: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := post(fenceCaller(coreidentity.ScopeGlobal), ""); rec.Code != http.StatusCreated {
		t.Errorf("global caller, null home: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBillingAddressFence_UnreachableIsNotFoundAndWritesNothing(t *testing.T) {
	store := &fakeReadStore{fakeStore: newFakeStore(), found: false}
	w := &fakeWriter{}
	h := NewHandler(NewService(store).WithWriter(w), nil)
	put := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/customers/"+readID+"/billing-address", strings.NewReader(`{"postal":"K8V 5P7","province":"ON"}`))
		req.SetPathValue("id", readID)
		rec := httptest.NewRecorder()
		h.updateBillingAddress(rec, req.WithContext(readCtx(identity.StaffContext{UserID: "u", Scope: identity.ScopeBranch, Branches: map[string]bool{"b-1": true}})), fenceCaller(coreidentity.ScopeBranch, "b-1"))
		return rec
	}
	if rec := put(); rec.Code != http.StatusNotFound || w.lastAddr != nil {
		t.Fatalf("unreachable account: want 404 and no write, got %d (%v)", rec.Code, w.lastAddr)
	}
	store.found = true
	if rec := put(); rec.Code != http.StatusOK || w.lastAddr == nil {
		t.Fatalf("reachable account: want 200 and the write, got %d: %s", rec.Code, rec.Body.String())
	}
}
