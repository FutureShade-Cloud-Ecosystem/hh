package authz

// The role gate's own suite: the admission rule, the refusal families,
// the construction refusals, and the per dealer configuration seam. The
// gate is exercised through the package's middleware fixture
// (mint_test.go): a real verifier over runtime-generated keys, counting
// membership and org doubles, so the gate's own staff leg (verification,
// class, X-Org shape, membership, slug resolution) runs for real and the
// role resolution is a double whose answer each test states. No database.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// errRoleSourceGone is the resolution failure one refusal family test
// injects.
var errRoleSourceGone = errors.New("role source gone")

// fakeRoleSource answers every resolution with one fixed role id and one
// fixed outcome, so each test states the matrix cell it is exercising.
type fakeRoleSource struct {
	role     string
	has      bool
	err      error
	resolved int
}

func (f *fakeRoleSource) ResolveStaff(context.Context, string, string) (identity.StaffAuthority, bool, error) {
	f.resolved++
	if f.err != nil {
		return identity.StaffAuthority{}, false, f.err
	}
	return identity.StaffAuthority{RoleID: f.role, Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}, f.has, nil
}

func mustConfig(t *testing.T) *roleconfig.Config {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	return cfg
}

// runGated runs one request through the gate with the route's annotation.
// The headers carry the credential and the org selection the gate's own
// staff leg reads; next records whether the handler was reached.
func runGated(t *testing.T, f *fixture, g *RoleGate, access RouteAccess, headers map[string]string) (int, string, bool) {
	t.Helper()
	mw, err := g.Middleware(access)
	if err != nil {
		t.Fatalf("Middleware: %v", err)
	}
	reached := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if reached && rec.Code != http.StatusOK {
		t.Fatalf("handler reached but the recorder holds %d", rec.Code)
	}
	return rec.Code, rec.Body.String(), reached
}

func gatedStaffHeaders(t *testing.T, f *fixture) map[string]string {
	t.Helper()
	return map[string]string{
		"Authorization": "Bearer " + f.staffToken(t, fixtureSubject),
		HeaderXOrg:      fixtureOrgSlug,
	}
}

func TestRoleGateConstructionRefusesNilCollaborators(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	if _, err := NewRoleGate(nil, &fakeRoleSource{role: "inside_sales", has: true}, cfg); err == nil {
		t.Fatal("nil middleware core constructed a gate")
	}
	if _, err := NewRoleGate(f.gate, nil, cfg); err == nil {
		t.Fatal("nil role source constructed a gate")
	}
	if _, err := NewRoleGate(f.gate, &fakeRoleSource{role: "inside_sales", has: true}, nil); err == nil {
		t.Fatal("nil configuration constructed a gate")
	}
}

func TestRoleGateMiddlewareRefusesInvalidAnnotation(t *testing.T) {
	f := newFixture(t)
	g, err := NewRoleGate(f.gate, &fakeRoleSource{role: "inside_sales", has: true}, mustConfig(t))
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	cases := map[string]RouteAccess{
		"no surfaces":     {Surfaces: nil, Level: roleconfig.LevelRead},
		"unknown surface": {Surfaces: []string{"not-a-surface"}, Level: roleconfig.LevelRead},
		"unknown level":   {Surfaces: []string{"counter-till"}, Level: roleconfig.Level("read-all")},
	}
	for name, access := range cases {
		if _, err := g.Middleware(access); err == nil {
			t.Fatalf("%s: invalid annotation attached", name)
		}
	}
}

// The admission rule over the decided default: admitted when the role's
// level reaches the route's level on at least one served surface,
// refused when it is below on every one.
func TestRoleGateAdmissionRuleOverTheDefaultMatrix(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		access   RouteAccess
		admitted bool
	}{
		{"inside sales works the till", "inside_sales", RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}, true},
		{"inside sales reads the till", "inside_sales", RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelRead}, true},
		{"inside sales is held off dispatch", "inside_sales", RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelRead}, false},
		{"inside sales reads stock", "inside_sales", RouteAccess{Surfaces: []string{"buying-and-stock"}, Level: roleconfig.LevelRead}, true},
		{"inside sales is held off pricing edits", "inside_sales", RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage}, false},
		{"sales manager reads dispatch", "sales_manager", RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelRead}, true},
		{"sales manager edits prices", "sales_manager", RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage}, true},
		{"sales manager is held off the delivery date commit", "sales_manager", RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelWork}, false},
		{"general manager reads the till but does not ring", "general_manager", RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}, false},
		{"general manager reads the till", "general_manager", RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelRead}, true},
		{"hr is held off the money desk", "hr", RouteAccess{Surfaces: []string{"money-desk"}, Level: roleconfig.LevelRead}, false},
		{"two surfaces admit through the stronger cell", "sales_manager", RouteAccess{Surfaces: []string{"dispatch-board", "quote-desk"}, Level: roleconfig.LevelManage}, true},
		{"two surfaces refuse when both cells fall short", "sales_manager", RouteAccess{Surfaces: []string{"dispatch-board", "money-desk"}, Level: roleconfig.LevelManage}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			g, err := NewRoleGate(f.gate, &fakeRoleSource{role: tc.role, has: true}, mustConfig(t))
			if err != nil {
				t.Fatalf("NewRoleGate: %v", err)
			}
			code, body, _ := runGated(t, f, g, tc.access, gatedStaffHeaders(t, f))
			if tc.admitted && (code != http.StatusOK) {
				t.Fatalf("admission refused: status %d body %s", code, body)
			}
			if !tc.admitted && (code != http.StatusForbidden || body != string(BodyForbidden)) {
				t.Fatalf("refusal is not the RBAC-403 family: status %d body %s", code, body)
			}
		})
	}
}

func TestRoleGateRefusalFamilies(t *testing.T) {
	access := RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}

	t.Run("no authority is the RBAC-403", func(t *testing.T) {
		f := newFixture(t)
		g, err := NewRoleGate(f.gate, &fakeRoleSource{role: "inside_sales", has: false}, mustConfig(t))
		if err != nil {
			t.Fatalf("NewRoleGate: %v", err)
		}
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusForbidden || body != string(BodyForbidden) {
			t.Fatalf("status %d body %s", code, body)
		}
	})
	t.Run("resolution failure is the 503 family", func(t *testing.T) {
		f := newFixture(t)
		g, err := NewRoleGate(f.gate, &fakeRoleSource{err: errRoleSourceGone}, mustConfig(t))
		if err != nil {
			t.Fatalf("NewRoleGate: %v", err)
		}
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusServiceUnavailable || body != string(BodyUnavailable) {
			t.Fatalf("status %d body %s", code, body)
		}
	})
	t.Run("an inactive account is the 401 family", func(t *testing.T) {
		f := newFixture(t)
		g, err := NewRoleGate(f.gate, &fakeRoleSource{err: identity.ErrStaffUserInactive}, mustConfig(t))
		if err != nil {
			t.Fatalf("NewRoleGate: %v", err)
		}
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusUnauthorized || body != string(BodyUnauthorized) {
			t.Fatalf("status %d body %s", code, body)
		}
	})
	t.Run("a role outside the configuration is the 503 family", func(t *testing.T) {
		f := newFixture(t)
		g, err := NewRoleGate(f.gate, &fakeRoleSource{role: "not-a-role", has: true}, mustConfig(t))
		if err != nil {
			t.Fatalf("NewRoleGate: %v", err)
		}
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusServiceUnavailable || body != string(BodyUnavailable) {
			t.Fatalf("status %d body %s", code, body)
		}
	})
	t.Run("a scopeless request fails closed", func(t *testing.T) {
		f := newFixture(t)
		g, err := NewRoleGate(f.gate, &fakeRoleSource{role: "inside_sales", has: true}, mustConfig(t))
		if err != nil {
			t.Fatalf("NewRoleGate: %v", err)
		}
		code, body, reached := runGated(t, f, g, access, nil)
		if reached {
			t.Fatal("a request with no credential reached the handler")
		}
		if code != http.StatusUnauthorized || body != string(BodyUnauthorized) {
			t.Fatalf("status %d body %s, want the credential refusal the staff leg writes first", code, body)
		}
	})
	t.Run("a tenant bearer is refused the staff class", func(t *testing.T) {
		f := newFixture(t)
		g, err := NewRoleGate(f.gate, &fakeRoleSource{role: "inside_sales", has: true}, mustConfig(t))
		if err != nil {
			t.Fatalf("NewRoleGate: %v", err)
		}
		headers := map[string]string{
			"Authorization": "Bearer " + f.tenantToken(t, fixtureSubject, fixtureTenant),
			HeaderXOrg:      fixtureOrgSlug,
		}
		code, body, reached := runGated(t, f, g, access, headers)
		if reached {
			t.Fatal("a tenant bearer reached the handler")
		}
		if code != http.StatusForbidden || body != string(BodyForbidden) {
			t.Fatalf("status %d body %s, want the RBAC-403 on the class leg", code, body)
		}
	})
}

// The per dealer configuration seam: the same gate code over a
// configuration built by Validate on a modified document. The decided
// default holds inside_sales off dispatch entirely; a dealer granting
// dispatch at Read admits the route with no code change.
func TestRoleGateReadsTheDealerConfiguration(t *testing.T) {
	doc := mustDefaultDocument(t)
	for i := range doc.Cells {
		if doc.Cells[i].Role == "inside_sales" && doc.Cells[i].Surface == "dispatch-board" {
			doc.Cells[i].Level = roleconfig.LevelRead
		}
	}
	cfg, err := roleconfig.Validate(doc)
	if err != nil {
		t.Fatalf("Validate on the modified document: %v", err)
	}
	f := newFixture(t)
	g, err := NewRoleGate(f.gate, &fakeRoleSource{role: "inside_sales", has: true}, cfg)
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	headers := gatedStaffHeaders(t, f)
	code, body, _ := runGated(t, f, g, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelRead}, headers)
	if code != http.StatusOK {
		t.Fatalf("the configured read was refused: status %d body %s", code, body)
	}
	code, body, _ = runGated(t, f, g, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelWork}, headers)
	if code != http.StatusForbidden {
		t.Fatalf("a read cell must not carry a work route: status %d body %s", code, body)
	}
}

// mustDefaultDocument decodes the embedded decided default into its
// document form, the shape a per dealer configuration edits before
// Validate builds its Config.
func mustDefaultDocument(t *testing.T) *roleconfig.Document {
	t.Helper()
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("decode the default document: %v", err)
	}
	return &doc
}
