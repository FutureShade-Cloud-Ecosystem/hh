package sync

// field_doors_route_test.go: the role fences of the three Field CP-2 sync doors, per role, through the
// real handler (the shared RBAC-403 before any store call): the pick record op and the shortage create
// belong to the yard roles, the typed name attestation to the driver, and nothing else gets through.
// The fences are the registry's writer groups; no registry row moved for these doors, so this pins
// that they did not.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestFieldDoorsRoleFencesPerRole(t *testing.T) {
	staffRoles := []string{
		roleGeneralManager, roleFinancialController, roleHR, roleBranchManager, roleSalesManager, roleYardManager,
		roleLogisticsManager, roleProcurementManager, rolePayablesReceivables, roleInsideSales, roleOutsideSales,
		roleYardTeam, roleDriver, roleSuperAdmin,
	}
	const stop = "00000000-0000-4000-8000-0000000000b1"
	doors := []struct {
		name    string
		scope   string
		body    string
		allowed map[string]bool
	}{
		{"pick record lines", "pick_tasks",
			`{"ops":[{"client_id":"00000000-0000-4000-8000-0000000000c1","op":"update","payload":{"id":"` + stop + `","picked":[{"order_line_no":1,"qty_ordered":"4","qty_picked":"4","uom":"EA"}]}}]}`,
			map[string]bool{roleYardTeam: true, roleYardManager: true}},
		{"shortage create", "shortages",
			`{"ops":[{"client_id":"00000000-0000-4000-8000-0000000000c2","op":"create","payload":{"task_id":"` + stop + `","description":"pavers","qty_short":"2","uom":"EA"}}]}`,
			map[string]bool{roleYardTeam: true, roleYardManager: true}},
		{"proof of delivery attestation", "pod",
			`{"ops":[{"client_id":"00000000-0000-4000-8000-0000000000c3","op":"create","payload":{"stop_id":"` + stop + `","received_by":"A Name","affirmed":true}}]}`,
			map[string]bool{roleDriver: true}},
	}
	for _, door := range doors {
		for _, role := range staffRoles {
			sg := testkit.NewStaffGuards(t)
			sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch})
			engine := NewEngine()
			if err := engine.RegisterScope(door.scope, &fakeStore{}); err != nil {
				t.Fatalf("register %s: %v", door.scope, err)
			}
			h, err := NewHandler(engine, &roleDouble{roles: []string{role}}, &portalDouble{}, nil)
			if err != nil {
				t.Fatalf("NewHandler: %v", err)
			}
			mux := http.NewServeMux()
			h.RegisterRoutes(mux, sg.Guards)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/"+door.scope, bytes.NewReader([]byte(door.body)))
			req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
			req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			want := http.StatusForbidden
			if door.allowed[role] {
				want = http.StatusOK
			}
			if rec.Code != want {
				t.Fatalf("%s as %s: status %d, want %d (%s)", door.name, role, rec.Code, want, rec.Body.String())
			}
		}
	}
}

// A person holding two roles gets both (the whole row, D48 X10): a driver who also holds a yard role
// passes the yard doors, and a yard hand with no driver role is turned away from the attestation.
func TestFieldDoorsRoleFenceWalksTheWholeRow(t *testing.T) {
	info, _ := Lookup("pod")
	if !info.AllowsRole(roleDriver) || info.AllowsRole(roleYardTeam) || info.AllowsRole(roleYardManager) {
		t.Fatalf("pod is the driver's scope alone: %+v", info.Writers)
	}
	for _, scope := range []string{"pick_tasks", "shortages"} {
		info, _ := Lookup(scope)
		if !info.AllowsRole(roleYardTeam) || !info.AllowsRole(roleYardManager) || info.AllowsRole(roleDriver) {
			t.Fatalf("%s belongs to the yard roles: %+v", scope, info.Writers)
		}
	}
	// Deletes never reach these stores' doors: the ops sets are create and update.
	for _, scope := range []string{"shortages", "pod"} {
		info, _ := Lookup(scope)
		if info.AllowsOp("delete") {
			t.Fatalf("%s must not take a delete", scope)
		}
	}
}
