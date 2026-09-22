package payment

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fakeConfigStore implements configStore without a database (the RBAC + response-shape tests).
type fakeConfigStore struct {
	configured bool
	mid        string
	proc       string
	currency   string
	lastCreds  map[string]string
}

func (f *fakeConfigStore) Status(context.Context) (bool, string, string, string, error) {
	return f.configured, f.mid, f.proc, f.currency, nil
}
func (f *fakeConfigStore) Configure(_ context.Context, processor, mid, currency, _ string, _ int64, _ bool, creds map[string]string, _ string) error {
	f.lastCreds = creds
	f.configured, f.mid, f.currency = true, mid, currency
	f.proc = processor
	if f.proc == "" {
		f.proc = "run_payments"
	}
	return nil
}

// Guards-swap carry note (HH-3.2 fold): the ERP's X-Test-Role guard doubles
// became the kit's real Guards over the DB-free harness, with the resolved
// authority set per leg, so the RBAC halves prove the actual constructor each
// route sits behind (RequireStaff on the read, RequireAdmin on the configure)
// rather than a stub of it.
func newTestMux(t *testing.T, store configStore) (*testkit.StaffGuards, *http.ServeMux) {
	sg := testkit.NewStaffGuards(t)
	h := &Handler{store: store, log: slog.Default()}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return sg, mux
}

func doReq(t *testing.T, sg *testkit.StaffGuards, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// T22, PUT /config requires admin: a non-admin → 403; an admin → 200 (US-RP01 RBAC).
func TestConfig_PUT_RequiresAdmin(t *testing.T) {
	sg, mux := newTestMux(t, &fakeConfigStore{})
	body := `{"merchant_id":"MID-1","credentials":{"api_key":"secret"}}`

	sg.SetAuthority(identity.StaffAuthority{RoleID: "cashier", Tier: 3, Scope: "branch"})
	if rec := doReq(t, sg, mux, http.MethodPut, "/api/v1/payments/config", body); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin PUT = %d, want 403", rec.Code)
	}

	sg.SetAuthority(identity.StaffAuthority{RoleID: "owner", Tier: 1, Scope: "global"})
	if rec := doReq(t, sg, mux, http.MethodPut, "/api/v1/payments/config", body); rec.Code != http.StatusOK {
		t.Fatalf("admin PUT = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// T23, GET /config requires staff: unauthenticated → 401; active staff → 200 with NO creds (US-RP01).
func TestConfig_GET_RequiresStaff(t *testing.T) {
	sg, mux := newTestMux(t, &fakeConfigStore{configured: true, mid: "MID-1", proc: "run_payments", currency: "CAD"})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/payments/config", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET = %d, want 401", rec.Code)
	}

	sg.SetAuthority(identity.StaffAuthority{RoleID: "cashier", Tier: 3, Scope: "branch"})
	rec2 := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/config", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("staff GET = %d, want 200", rec2.Code)
	}
	if b := rec2.Body.String(); strings.Contains(b, "credential") || strings.Contains(b, "api_key") {
		t.Fatalf("status response must never carry creds: %s", b)
	}
}

// T24, the configure response hides creds: the body carries no credentials/api_key (US-RP01).
func TestConfig_PUT_ResponseHidesCreds(t *testing.T) {
	fake := &fakeConfigStore{}
	sg, mux := newTestMux(t, fake)
	body := `{"merchant_id":"MID-1","currency":"CAD","credentials":{"api_key":"super-secret-token"}}`

	sg.SetAuthority(identity.StaffAuthority{RoleID: "owner", Tier: 1, Scope: "global"})
	rec := doReq(t, sg, mux, http.MethodPut, "/api/v1/payments/config", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	resp := rec.Body.String()
	for _, leak := range []string{"super-secret-token", "api_key", "credential"} {
		if strings.Contains(resp, leak) {
			t.Fatalf("configure response leaked %q: %s", leak, resp)
		}
	}
	// The creds did reach the store (write path) even though they are never echoed.
	if fake.lastCreds["api_key"] != "super-secret-token" {
		t.Fatal("the credentials must be passed to the store (write-only), not dropped")
	}
}
