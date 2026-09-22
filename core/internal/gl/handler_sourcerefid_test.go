package gl

// handler_sourcerefid_test.go, regression coverage for the source_ref_id validation gate on
// GET /api/v1/gl/journals. gl_journals.source_ref_id is a UUID column and Repository.ListJournals
// filters on it via the `($3 = '' OR source_ref_id = $3::uuid)` predicate, so a malformed
// ?source_ref_id= value used to flow raw into the `$3::uuid` cast, raise SQLSTATE 22P02, and
// surface as an opaque HTTP 500. The handler now validates the UUID shape at the HTTP edge and
// answers 400 'invalid source_ref_id' BEFORE any service/DB call; an empty value stays the
// documented "no filter" case and passes the gate.
//
// These tests hit listJournals directly (no auth middleware) over a DB-free fake-backed service.
// Without the sourceRefIDParam gate the malformed cases fall through to the service and answer
// 200/500 instead of 400, so each case genuinely regresses the fix.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const validSourceRefUUID = "7a1e3b2c-9d4f-4a6b-8c1d-2e3f4a5b6c7d"

// A malformed source_ref_id → 400 'invalid source_ref_id' on GET /api/v1/gl/journals, BEFORE any DB call.
func TestSourceRefID_Malformed_Returns400(t *testing.T) {
	f := newFakeStore()
	h := newTestHandler(f)
	for _, bad := range []string{"not-a-uuid", "123", "'; DROP TABLE gl_journals;--", validSourceRefUUID + "x"} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			q := url.Values{"source_ref_id": {bad}}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/gl/journals?"+q.Encode(), nil)
			rr := httptest.NewRecorder()
			h.listJournals(rr, req, guards.StaffCaller{})
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("bad source_ref_id %q: want 400, got %d (body %s)", bad, rr.Code, rr.Body.String())
			}
			if msg := bodyMsg(rr.Body.Bytes()); msg != "invalid source_ref_id" {
				t.Fatalf("bad source_ref_id %q: want error 'invalid source_ref_id', got %q", bad, msg)
			}
		})
	}
}

// A valid or empty source_ref_id passes the validation gate (never 'invalid source_ref_id').
func TestSourceRefID_ValidOrEmpty_PassesGate(t *testing.T) {
	f := newFakeStore()
	h := newTestHandler(f)
	for _, ok := range []string{"", validSourceRefUUID} {
		ok := ok
		t.Run(ok, func(t *testing.T) {
			vals := url.Values{}
			if ok != "" {
				vals.Set("source_ref_id", ok)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/gl/journals?"+vals.Encode(), nil)
			rr := httptest.NewRecorder()
			h.listJournals(rr, req, guards.StaffCaller{})
			if rr.Code == http.StatusBadRequest && bodyMsg(rr.Body.Bytes()) == "invalid source_ref_id" {
				t.Fatalf("source_ref_id %q: wrongly rejected by UUID gate (400 invalid source_ref_id)", ok)
			}
		})
	}
}
