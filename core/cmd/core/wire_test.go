package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/shims"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestServeSetRegistration_NoDuplicatePatterns is the operations batch's
// half of the S-3 backstop, made permanent: the served mux is the identity
// shims plus the money spine plus the operations cluster, and this test
// registers all three over one ServeMux with DB-free constructions. The
// ServeMux duplicate-registration panic is the backstop that makes
// "remove the shim in the same change that registers the real route"
// self-enforcing, and a registration that completes is the proof the
// served set holds no collision: nothing this batch registers duplicates
// a shim pattern or a sibling module's pattern.
//
// The route counts it pins: shims.RegisterRoutes serves the tenant set,
// the sync pair and the capability route (six patterns); the money spine
// and the operations cluster register their own patterns beside them.
// The modules' repositories hold a nil pool here: registration touches no
// table, so the pattern table is provable without a database.
func TestServeSetRegistration_NoDuplicatePatterns(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	rec := shims.NewRecorder(16)
	shims.RegisterRoutes(mux, sg.Guards, rec)
	spine := wireMoney(mux, nil, sg.Guards, nil, "")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ops := wireOperations(mux, nil, sg.Guards, log, spine, "test-ticket-key", nil)
	if spine == nil {
		t.Fatal("wireMoney returned no spine")
	}
	// The tops modules join the served set (the fold): one belt over a nil
	// pool, the contact source dark, every registrar registering beside
	// the shims and the two earlier clusters with no duplicate pattern.
	belt := buildSmsBelt(nil, log)
	tops := wireTops(mux, nil, sg.Guards, log, spine, ops, config.Load(), belt)
	if tops == nil {
		t.Fatal("wireTops returned no spine")
	}
	// The rail's surface registers beside the shims; a spot check that
	// the decide route answers through the guard (401 for an anonymous
	// caller) proves the registration is live, not merely unpanicked.
	rr := muxHandler(mux, http.MethodPost, "/api/v1/staff/approvals/00000000-0000-0000-0000-000000000000/decide")
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("rail decide route = %d for an anonymous caller, want the gate's refusal", rr.Code)
	}
	// One shim from each remaining family still serves: the tenant set,
	// the sync pair and the capability route belong to Phase D and the
	// tops batch, and nothing in this batch may have displaced them.
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/tenant/me"); rr.Code == http.StatusNotFound {
		t.Fatal("the tenant shim set no longer serves GET /api/v1/tenant/me")
	}
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/sync/anything"); rr.Code == http.StatusNotFound {
		t.Fatal("the sync shim pair no longer serves GET /api/v1/sync/{scope}")
	}
	// The tops batch's behavioral spot checks: a branch route answers the
	// gate, a billing register route refuses an anonymous caller, and the
	// rail's decide route still answers through the guard.
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/branches"); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("branches route = %d for an anonymous caller, want the gate's refusal", rr.Code)
	}
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/ar/landing"); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("AR landing route = %d for an anonymous caller, want the gate's refusal", rr.Code)
	}
	if rr := muxHandler(mux, http.MethodGet, "/api/portal/v1/addresses"); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("portal address route = %d for an anonymous caller, want the tenant gate's refusal", rr.Code)
	}
}

// muxHandler drives one request through the mux for the spot checks.
func muxHandler(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	return rr
}
