package pricing

import (
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - F5: a non-UUID category_id on GET /rules returns 400, not a 22P02-cast 500 -
// Fix #5: the repo casts $2::uuid; an invalid value must be rejected at the handler.
func TestListRules_NonUUIDCategoryID_400(t *testing.T) {
	h := NewHandler(NewService(&fakeStore{byID: map[string]Rule{}}), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pricing/rules?category_id=not-a-uuid", nil)
	h.listRules(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-uuid category_id must 400, got %d (%s)", rec.Code, rec.Body.String())
	}

	// A well-formed uuid is accepted (200), the validator does not reject valid input.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/pricing/rules?category_id=ca700000-0000-4000-9000-000000000001", nil)
	h.listRules(rec2, req2, guards.StaffCaller{})
	if rec2.Code != http.StatusOK {
		t.Fatalf("a valid uuid category_id must 200, got %d (%s)", rec2.Code, rec2.Body.String())
	}
}

// - F7: the authenticated staff caller is threaded into the audit actor (CREATE row) -
// Fix #7: staffUserID previously always returned "" → audit rows had no actor. The guard
// stamps the caller id onto the request context; the handler must record WHO.
func TestCreateRule_ThreadsStaffAuditActor(t *testing.T) {
	st := &fakeStore{byID: map[string]Rule{}}
	h := NewHandler(NewService(st), nil)

	body := `{"scope":"TIER_DEFAULT","calc_type":"PCT_OFF_LIST","discount_bps":1500,"price_uom":"EA","detail_text":"Contractor default"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pricing/rules", strings.NewReader(body))
	// Stamp the caller exactly as RequireManager would before dispatching the handler.
	req = req.WithContext(identity.ContextWithStaffUserID(req.Context(), "ryan-mgr"))

	h.createRule(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("createRule should 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	var found bool
	for _, a := range st.audits {
		if a.Action == "CREATE" && a.EntityType == "pricing_rule" && a.UserID == "ryan-mgr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the CREATE audit row must record the authenticated actor (ryan-mgr), got %+v", st.audits)
	}
}

// - Finding #9: a non-UUID path id returns 400 (not a Postgres 22P02 → 500) -
func TestGetRule_NonUUIDPath_400(t *testing.T) {
	h := NewHandler(NewService(&fakeStore{byID: map[string]Rule{}}), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pricing/rules/not-a-uuid", nil)
	req.SetPathValue("id", "not-a-uuid")
	h.getRule(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /pricing/rules/not-a-uuid must 400, got %d (%s)", rec.Code, rec.Body.String())
	}

	// A well-formed uuid clears the validator (it reaches the service → 404 for a missing row, never 400).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/pricing/rules/"+catPaver, nil)
	req2.SetPathValue("id", catPaver)
	h.getRule(rec2, req2, guards.StaffCaller{})
	if rec2.Code == http.StatusBadRequest {
		t.Fatalf("a valid uuid path must not 400, got %d (%s)", rec2.Code, rec2.Body.String())
	}
}

// - Finding #9: a non-UUID body scope-dim returns 400 -
func TestCreateRule_NonUUIDBodyDim_400(t *testing.T) {
	h := NewHandler(NewService(&fakeStore{byID: map[string]Rule{}}), nil)
	body := `{"scope":"CUSTOMER_PRODUCT","customer_id":"not-a-uuid","product_id":"` + catPaver +
		`","calc_type":"PCT_OFF_LIST","discount_bps":1500,"price_uom":"SF"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pricing/rules", strings.NewReader(body))
	h.createRule(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a non-uuid customer_id in the body must 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// - Finding #10: a foreign-key violation maps to a friendly 422 (not a 500) -
func TestWriteErr_FKViolation_422(t *testing.T) {
	h := NewHandler(NewService(&fakeStore{}), nil)
	rec := httptest.NewRecorder()
	// The repo wraps a Postgres 23503 as errFKViolation; the handler must surface it as 422.
	h.writeErr(rec, httptest.NewRequest(http.MethodPost, "/api/v1/pricing/rules", nil), fmt.Errorf("create rule: %w", errFKViolation))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an FK violation must map to 422, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// - Finding #4: DELETE override requires a ?version= (a stale/blind delete is refused) -
func TestDeleteOverride_RequiresVersionParam(t *testing.T) {
	h := NewHandler(NewService(&fakeStore{overridesByID: map[string]Override{}}), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pricing/overrides/"+custABC, nil)
	req.SetPathValue("id", custABC)
	h.deleteOverride(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a DELETE override with no ?version= must 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}
