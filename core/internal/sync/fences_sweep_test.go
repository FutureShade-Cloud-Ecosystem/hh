package sync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// sweepFixture is the real Handler behind the real RequireSync with a fake
// store wired under EVERY registry scope, driven by two settable role
// doubles: the staff list the fence reads and the portal row.
type sweepFixture struct {
	mux    *http.ServeMux
	sg     *testkit.StaffGuards
	staff  string
	tenant string
	roles  *roleDouble
	portal *portalDouble
	stores map[string]*fakeStore
	serial int
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch})
	sg.Harness.Membership.SetTenantOrgs(tenantSub, testkit.OrgNorthSlug)
	engine := NewEngine()
	stores := map[string]*fakeStore{}
	for _, info := range Scopes() {
		stores[info.Name] = &fakeStore{}
		if err := engine.RegisterScope(info.Name, stores[info.Name]); err != nil {
			t.Fatalf("register %s: %v", info.Name, err)
		}
	}
	f := &sweepFixture{sg: sg, stores: stores, roles: &roleDouble{}, portal: &portalDouble{}}
	h, err := NewHandler(engine, f.roles, f.portal, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	f.mux = http.NewServeMux()
	h.RegisterRoutes(f.mux, sg.Guards)
	f.staff = sg.Bearer(t)
	f.tenant = sg.Harness.Minter.TenantToken(t, tenantSub, tenantAlpha)
	return f
}

// push sends one outbox of the named op kinds to the scope as a staff or a
// tenant caller and returns the recorder.
func (f *sweepFixture) push(t *testing.T, scope string, tenant bool, kinds ...string) *httptest.ResponseRecorder {
	t.Helper()
	ops := make([]apigen.SyncPushOperation, 0, len(kinds))
	for _, k := range kinds {
		f.serial++
		payload := map[string]any{"id": rowID(f.serial), "v": "x"}
		ops = append(ops, apigen.SyncPushOperation{ClientId: key(f.serial), Op: k, Payload: payload})
	}
	body, err := json.Marshal(apigen.SyncPushRequest{Ops: ops})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/"+scope, bytes.NewReader(body))
	if tenant {
		req.Header.Set("Authorization", "Bearer "+f.tenant)
	} else {
		req.Header.Set("Authorization", "Bearer "+f.staff)
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *sweepFixture) rows(scope string) int {
	f.stores[scope].mu.Lock()
	defer f.stores[scope].mu.Unlock()
	return len(f.stores[scope].rows)
}

// wantForbidden asserts the shared RBAC-403, byte for byte.
func wantForbidden(t *testing.T, cell string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Errorf("%s: %d %q, want the shared 403 %q", cell, rec.Code, rec.Body.String(), authz.BodyForbidden)
	}
}

// TestWriteFenceSweep: every registry scope against every staff_role row and
// every contractor role, through the real handler. Writers reach the store
// (a 200 and a row), every other role gets the shared 403 with the identical
// body and leaves the store untouched, money scopes refuse everyone, a
// pull-only scope answers its 400.
func TestWriteFenceSweep(t *testing.T) {
	f := newSweepFixture(t)
	staff, portal := staffRoleRows(t), contractorRoles()
	pairs, admitted, refused := 0, 0, 0
	for _, info := range Scopes() {
		first := info.Name
		op := opCreate
		for _, role := range staff {
			cell := fmt.Sprintf("%s as staff %s", first, role)
			f.roles.roles = []string{role}
			before := f.rows(first)
			rec := f.push(t, first, false, op)
			pairs++
			switch {
			case info.Tenancy == TenancyTenant:
				wantForbidden(t, cell, rec)
				refused++
			case info.Money:
				wantForbidden(t, cell, rec)
				refused++
			case info.Policy == PolicyPullOnly:
				if rec.Code != http.StatusBadRequest {
					t.Errorf("%s: %d, want the pull-only 400", cell, rec.Code)
				}
				refused++
			case info.AllowsRole(role):
				if rec.Code != http.StatusOK || f.rows(first) != before+1 {
					t.Errorf("%s: %d rows %d->%d, want a 200 and one row", cell, rec.Code, before, f.rows(first))
				}
				admitted++
			default:
				wantForbidden(t, cell, rec)
				if f.rows(first) != before {
					t.Errorf("%s: refused push reached the store", cell)
				}
				refused++
			}
		}
		for _, role := range portal {
			cell := fmt.Sprintf("%s as contractor %s", first, role)
			f.sg.SetPortalRole(role)
			f.portal.role, f.portal.has = role, true
			before := f.rows(first)
			rec := f.push(t, first, true, op)
			pairs++
			switch {
			case info.Tenancy == TenancyOrg:
				wantForbidden(t, cell, rec)
				refused++
			case info.Money:
				wantForbidden(t, cell, rec)
				refused++
			case info.Policy == PolicyPullOnly:
				if rec.Code != http.StatusBadRequest {
					t.Errorf("%s: %d, want the pull-only 400", cell, rec.Code)
				}
				refused++
			case info.AllowsRole(role):
				if rec.Code != http.StatusOK || f.rows(first) != before+1 {
					t.Errorf("%s: %d rows %d->%d, want a 200 and one row", cell, rec.Code, before, f.rows(first))
				}
				admitted++
			default:
				wantForbidden(t, cell, rec)
				if f.rows(first) != before {
					t.Errorf("%s: refused push reached the store", cell)
				}
				refused++
			}
		}
	}
	if want := len(Scopes()) * (len(staff) + len(portal)); pairs != want {
		t.Fatalf("asserted %d pairs, want %d", pairs, want)
	}
	if admitted == 0 {
		t.Fatalf("the sweep admitted no pair at all")
	}
	t.Logf("sweep: %d scopes x (%d staff roles + %d contractor roles) = %d pairs asserted, %d admitted, %d refused",
		len(Scopes()), len(staff), len(portal), pairs, admitted, refused)
}

// firstNonWriter returns a staff role that is not on the scope's list.
func firstNonWriter(t *testing.T, info ScopeInfo) string {
	t.Helper()
	for _, r := range staffRoleRows(t) {
		if !info.AllowsRole(r) {
			return r
		}
	}
	t.Fatalf("%s: every staff role writes it", info.Name)
	return ""
}

// TestWriteFenceHoldsTwoRoles: a person holding a writer role AND a
// non-writer role is admitted whichever order the roles resolve in (D48
// X10), on every staff-writable scope.
func TestWriteFenceHoldsTwoRoles(t *testing.T) {
	f := newSweepFixture(t)
	n := 0
	for _, info := range Scopes() {
		if info.Money || info.Policy == PolicyPullOnly || info.Tenancy != TenancyOrg {
			continue
		}
		non := firstNonWriter(t, info)
		for _, writer := range info.Writers {
			for _, roles := range [][]string{{non, writer}, {writer, non}} {
				f.roles.roles = roles
				before := f.rows(info.Name)
				rec := f.push(t, info.Name, false, opCreate)
				n++
				if rec.Code != http.StatusOK || f.rows(info.Name) != before+1 {
					t.Errorf("%s holding %v: %d, want admitted", info.Name, roles, rec.Code)
				}
			}
		}
		f.roles.roles = []string{non}
		wantForbidden(t, info.Name+" non writer alone", f.push(t, info.Name, false, opCreate))
	}
	t.Logf("two-role cells asserted: %d", n)
}

// TestWriteFenceOpsRefuseTheWholeRequest: one op outside the scope's list
// refuses the request before any store call, even beside an allowed op.
func TestWriteFenceOpsRefuseTheWholeRequest(t *testing.T) {
	f := newSweepFixture(t)
	n := 0
	for _, info := range Scopes() {
		if info.Money || info.Policy == PolicyPullOnly {
			continue
		}
		tenant := info.Tenancy == TenancyTenant
		if tenant {
			role := info.Writers[0]
			f.sg.SetPortalRole(role)
			f.portal.role, f.portal.has = role, true
		} else {
			f.roles.roles = []string{info.Writers[0]}
		}
		for _, bad := range []string{opCreate, opUpdate, opDelete} {
			if info.AllowsOp(bad) {
				continue
			}
			before := f.rows(info.Name)
			wantForbidden(t, fmt.Sprintf("%s op %s", info.Name, bad), f.push(t, info.Name, tenant, opCreate, bad))
			if f.rows(info.Name) != before {
				t.Errorf("%s: a request with a refused op reached the store", info.Name)
			}
			n++
		}
	}
	if n == 0 {
		t.Fatalf("no scope restricts any op")
	}
	t.Logf("refused-op cells asserted: %d", n)
}

// TestWriteFenceFaults: a resolver error answers the 503 family and never
// an admission, an inactive account the 401 family, an empty grant list and
// a missing portal row the shared 403.
func TestWriteFenceFaults(t *testing.T) {
	f := newSweepFixture(t)
	scope := "pick_tasks"
	f.roles.roles = []string{"yard_team"}
	f.roles.err = errors.New("boom")
	if rec := f.push(t, scope, false, opCreate); rec.Code != http.StatusServiceUnavailable || f.rows(scope) != 0 {
		t.Errorf("staff resolver fault: %d rows %d, want 503 and no row", rec.Code, f.rows(scope))
	}
	f.roles.err = identity.ErrStaffUserInactive
	if rec := f.push(t, scope, false, opCreate); rec.Code != http.StatusUnauthorized || f.rows(scope) != 0 {
		t.Errorf("inactive staff: %d, want 401", rec.Code)
	}
	f.roles.err, f.roles.roles = nil, nil
	wantForbidden(t, "no grants", f.push(t, scope, false, opCreate))

	tscope := "orders"
	f.sg.SetPortalRole(identity.PortalRoleBuyer)
	f.portal.role, f.portal.has, f.portal.err = identity.PortalRoleBuyer, true, errors.New("boom")
	if rec := f.push(t, tscope, true, opCreate); rec.Code != http.StatusServiceUnavailable || f.rows(tscope) != 0 {
		t.Errorf("portal fault: %d, want 503", rec.Code)
	}
	f.portal.err = identity.ErrPortalUserInactive
	if rec := f.push(t, tscope, true, opCreate); rec.Code != http.StatusUnauthorized {
		t.Errorf("inactive portal: %d, want 401", rec.Code)
	}
	f.portal.err, f.portal.has = nil, false
	wantForbidden(t, "no portal row", f.push(t, tscope, true, opCreate))
}

// TestWriteFenceReadsUnchanged: pulls ignore the writer lists.
func TestWriteFenceReadsUnchanged(t *testing.T) {
	f := newSweepFixture(t)
	f.roles.roles = []string{"hr"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/pick_tasks?since=0", nil)
	req.Header.Set("Authorization", "Bearer "+f.staff)
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pull by a non writer: %d, want 200", rec.Code)
	}
	if f.roles.calls.Load() != 0 {
		t.Fatalf("a pull read the writer roles")
	}
}
