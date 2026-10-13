package authz

// The role gate's own suite: the whole-row admission rule, the platform
// roles' pass, the named list beside the level, the refusal families, the
// construction refusals, and the per dealer configuration seam. The gate
// is exercised through the package's middleware fixture (mint_test.go): a
// real verifier over runtime-generated keys, counting membership and org
// doubles, so the gate's own staff leg (verification, class, X-Org shape,
// membership, slug resolution) runs for real and the role resolution is a
// double whose answer each test states. No database.

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

// errRolesSourceGone is the resolution failure one refusal family test
// injects.
var errRolesSourceGone = errors.New("roles source gone")

// fakeRolesSource answers every resolution with one fixed list of role
// ids, so each test states the row of the matrix it is exercising.
type fakeRolesSource struct {
	roles    []string
	err      error
	resolved int
}

func (f *fakeRolesSource) ResolveRoles(context.Context, string, string) ([]string, error) {
	f.resolved++
	if f.err != nil {
		return nil, f.err
	}
	return f.roles, nil
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
// staff leg reads; reached reports whether the handler ran.
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

func gateOverRoles(t *testing.T, f *fixture, roles []string, cfg *roleconfig.Config) *RoleGate {
	t.Helper()
	g, err := NewRoleGate(f.gate, &fakeRolesSource{roles: roles}, cfg)
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	return g
}

func TestRoleGateConstructionRefusesNilCollaborators(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	if _, err := NewRoleGate(nil, &fakeRolesSource{roles: []string{"inside_sales"}}, cfg); err == nil {
		t.Fatal("nil middleware core constructed a gate")
	}
	if _, err := NewRoleGate(f.gate, nil, cfg); err == nil {
		t.Fatal("nil role source constructed a gate")
	}
	if _, err := NewRoleGate(f.gate, &fakeRolesSource{roles: []string{"inside_sales"}}, nil); err == nil {
		t.Fatal("nil configuration constructed a gate")
	}
}

func TestRoleGateMiddlewareRefusesInvalidAnnotation(t *testing.T) {
	f := newFixture(t)
	g := gateOverRoles(t, f, []string{"inside_sales"}, mustConfig(t))
	cases := map[string]RouteAccess{
		"no surfaces":          {Surfaces: nil, Level: roleconfig.LevelRead},
		"unknown surface":      {Surfaces: []string{"not-a-surface"}, Level: roleconfig.LevelRead},
		"unknown level":        {Surfaces: []string{"counter-till"}, Level: roleconfig.Level("read-all")},
		"level none":           {Surfaces: []string{"counter-till"}, Level: roleconfig.LevelNone},
		"unknown list":         {Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: "not_a_list"},
		"list that is not one": {Surfaces: []string{"money-desk"}, Level: roleconfig.LevelRead, List: "credit_at_till"},
	}
	for name, access := range cases {
		if _, err := g.Middleware(access); err == nil {
			t.Fatalf("%s: invalid annotation attached", name)
		}
	}
}

// The admission rule over the decided default: admitted when any held
// role's level reaches the route's level on at least one served surface,
// refused when every held role is below on every one.
func TestRoleGateAdmissionRuleOverTheDefaultMatrix(t *testing.T) {
	cases := []struct {
		name     string
		roles    []string
		access   RouteAccess
		admitted bool
	}{
		{"inside sales works the till", []string{"inside_sales"}, RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}, true},
		{"inside sales reads the till", []string{"inside_sales"}, RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelRead}, true},
		{"inside sales is held off dispatch", []string{"inside_sales"}, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelRead}, false},
		{"inside sales reads stock", []string{"inside_sales"}, RouteAccess{Surfaces: []string{"buying-and-stock"}, Level: roleconfig.LevelRead}, true},
		{"inside sales is held off pricing edits", []string{"inside_sales"}, RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: "pricing_editors"}, false},
		{"sales manager reads dispatch", []string{"sales_manager"}, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelRead}, true},
		{"sales manager edits prices, the list beside the level", []string{"sales_manager"}, RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: "pricing_editors"}, true},
		{"sales manager is held off the delivery date commit", []string{"sales_manager"}, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelWork}, false},
		{"general manager reads the till but does not ring", []string{"general_manager"}, RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}, false},
		{"general manager edits prices, the list beside the level", []string{"general_manager"}, RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: "pricing_editors"}, true},
		{"hr is held off the money desk", []string{"hr"}, RouteAccess{Surfaces: []string{"money-desk"}, Level: roleconfig.LevelRead}, false},
		{"sales manager reads the low margin report", []string{"sales_manager"}, RouteAccess{Surfaces: []string{"quote-desk", "money-desk"}, Level: roleconfig.LevelRead, List: ListLowMarginReport}, true},
		{"branch manager reads the low margin report", []string{"branch_manager"}, RouteAccess{Surfaces: []string{"quote-desk", "money-desk"}, Level: roleconfig.LevelRead, List: ListLowMarginReport}, true},
		{"financial controller reads the low margin report", []string{"financial_controller"}, RouteAccess{Surfaces: []string{"quote-desk", "money-desk"}, Level: roleconfig.LevelRead, List: ListLowMarginReport}, true},
		{"general manager reads the low margin report", []string{"general_manager"}, RouteAccess{Surfaces: []string{"quote-desk", "money-desk"}, Level: roleconfig.LevelRead, List: ListLowMarginReport}, true},
		{"inside sales is held off the low margin report", []string{"inside_sales"}, RouteAccess{Surfaces: []string{"quote-desk", "money-desk"}, Level: roleconfig.LevelRead, List: ListLowMarginReport}, false},
		{"yard manager is held off the low margin report", []string{"yard_manager"}, RouteAccess{Surfaces: []string{"quote-desk", "money-desk"}, Level: roleconfig.LevelRead, List: ListLowMarginReport}, false},
		{"hr is held off the low margin report", []string{"hr"}, RouteAccess{Surfaces: []string{"quote-desk", "money-desk"}, Level: roleconfig.LevelRead, List: ListLowMarginReport}, false},
		{"hr is refused the price writes the list names", []string{"hr"}, RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: "pricing_editors"}, false},
		{"logistics manager sets the dispatch policy, the list beside the level", []string{"logistics_manager"}, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelManage, List: "dispatch_policy_setters"}, true},
		{"branch manager sets the dispatch policy, the list beside the level", []string{"branch_manager"}, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelManage, List: "dispatch_policy_setters"}, true},
		{"yard manager is held off the dispatch policy write", []string{"yard_manager"}, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelManage, List: "dispatch_policy_setters"}, false},
		{"general manager is held off the dispatch policy write under the decided default", []string{"general_manager"}, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelManage, List: "dispatch_policy_setters"}, false},
		{"two roles get both: the manager's read and the seller's work", []string{"general_manager", "inside_sales"}, RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}, true},
		{"two roles refuse only when both rows fall short", []string{"general_manager", "inside_sales"}, RouteAccess{Surfaces: []string{"drive-run"}, Level: roleconfig.LevelWork}, false},
		{"two surfaces admit through the stronger cell", []string{"sales_manager"}, RouteAccess{Surfaces: []string{"dispatch-board", "quote-desk"}, Level: roleconfig.LevelManage}, true},
		{"two surfaces refuse when both cells fall short", []string{"sales_manager"}, RouteAccess{Surfaces: []string{"dispatch-board", "money-desk"}, Level: roleconfig.LevelManage}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			g := gateOverRoles(t, f, tc.roles, mustConfig(t))
			code, body, _ := runGated(t, f, g, tc.access, gatedStaffHeaders(t, f))
			if tc.admitted && code != http.StatusOK {
				t.Fatalf("admission refused: status %d body %s", code, body)
			}
			if !tc.admitted && (code != http.StatusForbidden || body != string(BodyForbidden)) {
				t.Fatalf("refusal is not the RBAC-403 family: status %d body %s", code, body)
			}
		})
	}
}

// The platform roles stand outside the matrix and the gate must not break
// them (the PR 44 review's P1-2 ruling): each of the three passes the
// level check on a staff route the matrix would refuse for every matrix
// role it refuses outright, here a dispatch read against a caller the
// configuration holds no cells for.
func TestRoleGatePlatformRolesPassTheLevelCheck(t *testing.T) {
	for _, role := range platformStaffRoles {
		t.Run(role, func(t *testing.T) {
			f := newFixture(t)
			g := gateOverRoles(t, f, []string{role}, mustConfig(t))
			code, body, _ := runGated(t, f, g, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelRead}, gatedStaffHeaders(t, f))
			if code != http.StatusOK {
				t.Fatalf("the platform role was refused: status %d body %s", code, body)
			}
		})
	}
	t.Run("the platform pass does not open a named list", func(t *testing.T) {
		f := newFixture(t)
		g := gateOverRoles(t, f, []string{PlatformRoleSuperAdmin}, mustConfig(t))
		code, body, _ := runGated(t, f, g, RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: "pricing_editors"}, gatedStaffHeaders(t, f))
		if code != http.StatusForbidden || body != string(BodyForbidden) {
			t.Fatalf("status %d body %s, want the RBAC-403: the list is a rule's own wall", code, body)
		}
	})
}

func TestRoleGateRefusalFamilies(t *testing.T) {
	access := RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}

	t.Run("no active grant is the RBAC-403", func(t *testing.T) {
		f := newFixture(t)
		g := gateOverRoles(t, f, []string{}, mustConfig(t))
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusForbidden || body != string(BodyForbidden) {
			t.Fatalf("status %d body %s", code, body)
		}
	})
	t.Run("resolution failure is the 503 family", func(t *testing.T) {
		f := newFixture(t)
		g, err := NewRoleGate(f.gate, &fakeRolesSource{err: errRolesSourceGone}, mustConfig(t))
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
		g, err := NewRoleGate(f.gate, &fakeRolesSource{err: identity.ErrStaffUserInactive}, mustConfig(t))
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
		g := gateOverRoles(t, f, []string{"not-a-role"}, mustConfig(t))
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusServiceUnavailable || body != string(BodyUnavailable) {
			t.Fatalf("status %d body %s", code, body)
		}
	})
	t.Run("a scopeless request fails closed", func(t *testing.T) {
		f := newFixture(t)
		g := gateOverRoles(t, f, []string{"inside_sales"}, mustConfig(t))
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
		g := gateOverRoles(t, f, []string{"inside_sales"}, mustConfig(t))
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

// The per dealer configuration seam, on both walls the gate reads: the
// matrix (a cell edit flips an admission) and the named list (a dealer
// adding an editor flips the price write) with no code change.
func TestRoleGateReadsTheDealerConfiguration(t *testing.T) {
	doc := mustDefaultDocument(t)
	for i := range doc.Cells {
		if doc.Cells[i].Role == "inside_sales" && doc.Cells[i].Surface == "dispatch-board" {
			doc.Cells[i].Level = roleconfig.LevelRead
		}
	}
	// The dealer's shape: the branch manager joins the editors (X6 is the
	// dealer's data inside the wall), which also needs the cell that
	// reaches the writes, so the same document moves the branch manager's
	// dealer-admin cell up to manage. Settings stay raw in the document,
	// so the list is decoded, edited and re-encoded as the loader
	// expects it.
	for i := range doc.Cells {
		if doc.Cells[i].Role == "branch_manager" && doc.Cells[i].Surface == "dealer-admin" {
			doc.Cells[i].Level = roleconfig.LevelManage
		}
	}
	var editors []string
	if err := json.Unmarshal(doc.Settings[roleconfig.SettingPricingEditors], &editors); err != nil {
		t.Fatalf("decode the default editors list: %v", err)
	}
	for i, v := range editors {
		if v == "general_manager" {
			editors[i] = "branch_manager"
		}
	}
	raw, err := json.Marshal(editors)
	if err != nil {
		t.Fatalf("encode the edited editors list: %v", err)
	}
	doc.Settings[roleconfig.SettingPricingEditors] = raw
	cfg, err := roleconfig.Validate(doc)
	if err != nil {
		t.Fatalf("Validate on the modified document: %v", err)
	}
	f := newFixture(t)
	g := gateOverRoles(t, f, []string{"inside_sales", "branch_manager"}, cfg)
	headers := gatedStaffHeaders(t, f)
	code, body, _ := runGated(t, f, g, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelRead}, headers)
	if code != http.StatusOK {
		t.Fatalf("the configured read was refused: status %d body %s", code, body)
	}
	code, body, _ = runGated(t, f, g, RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: "pricing_editors"}, headers)
	if code != http.StatusOK {
		t.Fatalf("the configured editor was refused the price write: status %d body %s", code, body)
	}
	// The same configured cell is still only a read: a work route on the
	// surface stays refused to the caller whose row now holds read there.
	gSales := gateOverRoles(t, f, []string{"inside_sales"}, cfg)
	code, body, _ = runGated(t, f, gSales, RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelWork}, headers)
	if code != http.StatusForbidden {
		t.Fatalf("a read cell must not carry a work route: status %d body %s", code, body)
	}
}

// TestRoleGateDispatchPolicySettersIsTheRulesOwnWall, X13 beside X6: the policy write names the
// configuration's dispatch_policy_setters list, so only a listed role sets the dispatch policy
// whatever its cells say. The decided default's list (the logistics and branch managers) equals the
// dispatch board's manage row, so the default's behaviour is exactly the unlisted gate's; the wall
// itself shows when a dealer moves a cell without moving the list, the same seam the pricing
// editors leg rides. The platform roles' pass never opens the list.
func TestRoleGateDispatchPolicySettersIsTheRulesOwnWall(t *testing.T) {
	access := RouteAccess{Surfaces: []string{"dispatch-board"}, Level: roleconfig.LevelManage, List: "dispatch_policy_setters"}
	t.Run("the default list is the manage row: the logistics manager writes the policy", func(t *testing.T) {
		f := newFixture(t)
		g := gateOverRoles(t, f, []string{"logistics_manager"}, mustConfig(t))
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusOK {
			t.Fatalf("the listed logistics manager was refused: status %d body %s", code, body)
		}
	})
	t.Run("the platform pass does not open the list", func(t *testing.T) {
		f := newFixture(t)
		g := gateOverRoles(t, f, []string{PlatformRoleDealerAdmin}, mustConfig(t))
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusForbidden || body != string(BodyForbidden) {
			t.Fatalf("status %d body %s, want the RBAC-403: the list is a rule's own wall", code, body)
		}
	})
	t.Run("a configured manage cell off the list is refused the shared body", func(t *testing.T) {
		doc := mustDefaultDocument(t)
		changed := false
		for i := range doc.Cells {
			if doc.Cells[i].Role == "yard_manager" && doc.Cells[i].Surface == "dispatch-board" {
				doc.Cells[i].Level = roleconfig.LevelManage
				changed = true
			}
		}
		if !changed {
			t.Fatal("the default document lacks the yard manager's dispatch-board cell")
		}
		cfg, err := roleconfig.Validate(doc)
		if err != nil {
			t.Fatalf("Validate on the modified document: %v", err)
		}
		f := newFixture(t)
		g := gateOverRoles(t, f, []string{"yard_manager"}, cfg)
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusForbidden || body != string(BodyForbidden) {
			t.Fatalf("status %d body %s, want the RBAC-403 the gate writes for every named-list refusal", code, body)
		}
		// The same configuration keeps the listed branch manager admitted, so the refusal is the
		// list and not the cell edit.
		gListed := gateOverRoles(t, f, []string{"branch_manager"}, cfg)
		code, body, _ = runGated(t, f, gListed, access, gatedStaffHeaders(t, f))
		if code != http.StatusOK {
			t.Fatalf("the listed branch manager was refused under the same configuration: status %d body %s", code, body)
		}
	})
	t.Run("a dealer adding a setter admits it with no code change", func(t *testing.T) {
		doc := mustDefaultDocument(t)
		cellChanged := false
		for i := range doc.Cells {
			if doc.Cells[i].Role == "general_manager" && doc.Cells[i].Surface == "dispatch-board" {
				doc.Cells[i].Level = roleconfig.LevelManage
				cellChanged = true
			}
		}
		if !cellChanged {
			t.Fatal("the default document lacks the general manager's dispatch-board cell")
		}
		var setters []string
		if err := json.Unmarshal(doc.Settings[roleconfig.SettingDispatchPolicySetters], &setters); err != nil {
			t.Fatalf("decode the default setters list: %v", err)
		}
		setters = append(setters, "general_manager")
		raw, err := json.Marshal(setters)
		if err != nil {
			t.Fatalf("encode the edited setters list: %v", err)
		}
		doc.Settings[roleconfig.SettingDispatchPolicySetters] = raw
		cfg, err := roleconfig.Validate(doc)
		if err != nil {
			t.Fatalf("Validate on the modified document: %v", err)
		}
		f := newFixture(t)
		g := gateOverRoles(t, f, []string{"general_manager"}, cfg)
		code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
		if code != http.StatusOK {
			t.Fatalf("the configured setter was refused the policy write: status %d body %s", code, body)
		}
	})
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
