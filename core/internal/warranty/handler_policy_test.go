package warranty

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// handler_policy_test.go, the walk change 6 policy HTTP surface over httptest. The handler bodies are
// driven the way h.staff delivers them: the request stamped with the guard-resolved StaffContext (the
// stamp is what the service reads for updated_by and the audit row). The GUARDS themselves are not
// rebuilt here: RequireStaff/RequireAdmin equality with the document is the cmd/core xrole suite's
// assertion over the real fixture; this file pins the shapes and the refusals in the core's own words.

func policyHandler(t *testing.T, rows policyRows) (*Handler, *fakeStore, *recordingAudit) {
	t.Helper()
	f := newFakeStore()
	f.policy = rows
	rec := &recordingAudit{}
	// The caller resolution stays the production default (StaffContextFromContext): the stamp the
	// request carries IS what the service must read, never a test hook.
	svc := NewService(nil).withStore(f).WithAudit(rec)
	return NewHandler(svc, nil), f, rec
}

func serveStaff(handlerFn guards.StaffHandler, r *http.Request, sc identity.StaffContext) *httptest.ResponseRecorder {
	stamped := r.WithContext(identity.ContextWithStaff(r.Context(), sc))
	rec := httptest.NewRecorder()
	handlerFn(rec, stamped, guards.StaffCaller{})
	return rec
}

func TestPolicyHTTP_GetReturnsTheEffectiveThreshold(t *testing.T) {
	h, _, _ := policyHandler(t, policyRows{"": {BranchID: "", ClaimThresholdCents: 5000, UpdatedAt: "2026-10-15T12:00:00Z"}})
	rec := serveStaff(h.getPolicy, httptest.NewRequest("GET", "/api/v1/warranty/policy", nil), identityAdmin())
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/warranty/policy answered %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	if body["claim_threshold_cents"] != float64(5000) {
		t.Fatalf("claim_threshold_cents = %v, want 5000", body["claim_threshold_cents"])
	}
	if _, ok := body["updated_by"]; ok {
		t.Fatalf("updated_by rode the wire: %s; a person id never rides it, the audit spine carries who", rec.Body.String())
	}
}

func TestPolicyHTTP_GetResolvesTheBranchQuery(t *testing.T) {
	h, _, _ := policyHandler(t, policyRows{
		"":         {BranchID: "", ClaimThresholdCents: 5000},
		testBranch: {BranchID: testBranch, ClaimThresholdCents: 2500},
	})
	rec := serveStaff(h.getPolicy,
		httptest.NewRequest("GET", "/api/v1/warranty/policy?branch_id="+testBranch, nil), identityAdmin())
	if rec.Code != http.StatusOK {
		t.Fatalf("the branch read answered %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["branch_id"] != testBranch || body["claim_threshold_cents"] != float64(2500) {
		t.Fatalf("branch read = %s, want branch %q at 2500", rec.Body.String(), testBranch)
	}
}

func TestPolicyHTTP_PutStoresAndAnswersInCents(t *testing.T) {
	h, fs, rec2 := policyHandler(t, policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}})
	body, _ := json.Marshal(PolicyInput{ClaimThresholdCents: 2500})
	req := httptest.NewRequest("PUT", "/api/v1/warranty/policy", bytes.NewReader(body))
	res := serveStaff(h.putPolicy, req, identityAdmin())
	if res.Code != http.StatusOK {
		t.Fatalf("PUT /api/v1/warranty/policy answered %d, want 200 (%s)", res.Code, res.Body.String())
	}
	var saved map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &saved)
	if saved["claim_threshold_cents"] != float64(2500) {
		t.Fatalf("the answer carries %v, want the stored 2500", saved["claim_threshold_cents"])
	}
	if fs.policy[""].ClaimThresholdCents != 2500 {
		t.Fatalf("the store holds %d, want 2500", fs.policy[""].ClaimThresholdCents)
	}
	if rec2.count() != 1 {
		t.Fatalf("a moved threshold filed %d audit rows, want 1", rec2.count())
	}
}

func TestPolicyHTTP_PutRefusesANegativeThresholdInTheCoresOwnWords(t *testing.T) {
	h, fs, rec2 := policyHandler(t, policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}})
	body, _ := json.Marshal(PolicyInput{ClaimThresholdCents: -1})
	req := httptest.NewRequest("PUT", "/api/v1/warranty/policy", bytes.NewReader(body))
	res := serveStaff(h.putPolicy, req, identityAdmin())
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a negative threshold answered %d, want 422 (%s)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "claim_threshold_cents must be >= 0") {
		t.Fatalf("the refusal %s does not carry the core's own words", res.Body.String())
	}
	if fs.policy[""].ClaimThresholdCents != 5000 {
		t.Fatalf("a refused write changed the stored row to %d", fs.policy[""].ClaimThresholdCents)
	}
	if rec2.count() != 0 {
		t.Fatalf("a refused write filed %d audit rows, want none", rec2.count())
	}
}

func TestPolicyHTTP_PutWithAMalformedBodyIs400(t *testing.T) {
	h, _, _ := policyHandler(t, policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}})
	req := httptest.NewRequest("PUT", "/api/v1/warranty/policy", strings.NewReader("{not json"))
	res := serveStaff(h.putPolicy, req, identityAdmin())
	if res.Code != http.StatusBadRequest {
		t.Fatalf("a malformed body answered %d, want 400", res.Code)
	}
}
