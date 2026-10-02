package main

// HH-196: portal invites are created but can never be delivered or accepted
// (the accept route is unregistered until HH-4.2 and no live notifier is
// wired), so the served create route answers 501 and writes nothing. This is
// the composition leg: the real registerRoutes, a manage_users bearer, the
// scratch database behind it.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestPortalInviteCreateIsNotImplementedAndWritesNothing(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()

	count := func() int {
		var n int
		if err := graph.pool.QueryRow(ctx, `SELECT count(*) FROM portal_invites WHERE email = $1`, "hh196-crew@example.test").Scan(&n); err != nil {
			t.Fatalf("count invites: %v", err)
		}
		return n
	}
	before := count()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenant/invites",
		strings.NewReader(`{"email":"hh196-crew@example.test","role":"field_crew"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID))
	rsp := httptest.NewRecorder()
	rec.inner.ServeHTTP(rsp, req)

	if rsp.Code != http.StatusNotImplemented {
		t.Fatalf("POST /api/v1/tenant/invites as manage_users: status %d body %s, want 501", rsp.Code, rsp.Body.String())
	}
	if !strings.Contains(rsp.Body.String(), `"error"`) {
		t.Fatalf("501 body %q lacks the module's error shape", rsp.Body.String())
	}
	if strings.Contains(rsp.Body.String(), "token") {
		t.Fatalf("501 body %q must not carry a token", rsp.Body.String())
	}
	if after := count(); after != before {
		t.Fatalf("portal_invites rows for the probe email: %d before, %d after; a 501 must write nothing", before, after)
	}
}
