package main

// messaging_sync_comp_test.go: the four messaging sync scopes at the
// composition level, through the FULL served surface the binary wires. Red
// first: before the stores were registered every one of the four answered
// the honest 501 (the recorded stop the wiring comments carried), which is
// the red evidence in the item report. Wired, each scope must answer 200
// for a staff caller with X-Org (an empty or populated page, never a
// fabricated one and never the 501), and the tenant bearer must meet the
// shared RBAC-403 family before any store call.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestMessagingSyncScopesServeAtTheComposition(t *testing.T) {
	env := newCompEnv(t)
	rec, _ := registeredSurface(t, env)

	staff := env.minter.StaffToken(t, testkit.SubInsideSales)
	tenant := env.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID)

	for _, scope := range []string{"channels", "messages", "acks", "notify_prefs"} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/"+scope+"?since=0", nil)
		req.Header.Set("Authorization", "Bearer "+staff)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rec.inner.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Errorf("RED %s pull: %d %s, want 200 (the wired store answers)", scope, recorder.Code, recorder.Body.String())
		}
		// The tenant bearer meets the shared family refusal before any
		// store call, byte-identical across the four.
		trec := httptest.NewRecorder()
		treq := httptest.NewRequest(http.MethodGet, "/api/v1/sync/"+scope, nil)
		treq.Header.Set("Authorization", "Bearer "+tenant)
		rec.inner.ServeHTTP(trec, treq)
		if trec.Code != http.StatusForbidden {
			t.Errorf("%s tenant pull: %d %s, want the shared 403", scope, trec.Code, trec.Body.String())
		}
	}
}
