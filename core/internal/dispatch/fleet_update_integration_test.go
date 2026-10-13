//go:build integration

// fleet_update_integration_test.go, the truck and driver UPDATE routes (the roles roadmap's
// Dispatch core, item 2): the registry rows a yard outgrows its first spelling of.
//
// WHY THESE LEGS ARE HTTP LEGS WITH A REAL BEARER. The dispatch package's older testMux wires the
// kit's real Guards but sends no Authorization header, so every leg it serves reads the bearer-less
// 401 (the recorded-red entry in docs/BLOCKERS.md). These legs mint and send the bearer the guards
// verify, so the tier guard, the branch fence and the service all run for real: the update routes
// are manager-gated at the mux and branch-walled in the service, and only the real chain proves
// both halves agree.
//
// IDEMPOTENT BY SHAPE. The update is an absolute PUT: the same body twice leaves the same row and
// answers 200 both times. The conflict the (branch_id, code) uniqueness pins is a DIFFERENT code
// colliding with another truck, never a retry of the same one.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity2 "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// fleetMux builds the real route table behind the kit's Guards with the caller's authority set to
// the manager shape the update routes require, the branches module's bearer-correct pattern. The
// scope is the CANONICAL identity vocabulary ("global"/"branch"), not the carried staffcontext
// spelling: the bridge maps the canonical words onto the staff context the services read.
func fleetMux(t *testing.T, h *harness, scope string, branches ...string) (*http.ServeMux, map[string]string) {
	t.Helper()
	return fleetMuxAs(t, h, "logistics_manager", identity2.TierManager, scope, branches...)
}

// fleetMuxAs is fleetMux for any role and tier, so a leg can send the same real bearer as a caller
// below the manager tier and read the mux's own refusal.
func fleetMuxAs(t *testing.T, h *harness, role string, tier int, scope string, branches ...string) (*http.ServeMux, map[string]string) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity2.StaffAuthority{
		RoleID:   role,
		Tier:     tier,
		Scope:    scope,
		Branches: branches,
	})
	uid := seededUser(t, h.pool)
	mux := http.NewServeMux()
	NewHandler(h.svc, nil).
		WithUserLinker(func(context.Context, string) (string, bool) { return uid, true }).
		RegisterRoutes(mux, sg.Guards)
	return mux, map[string]string{
		"Authorization": "Bearer " + sg.Bearer(t),
		"X-Org":         testkit.OrgNorthSlug,
	}
}

// fleetDo issues one request with the bearer headers the guards verify.
func fleetDo(t *testing.T, mux *http.ServeMux, headers map[string]string, method, path, body string) (int, string) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// seedTruckAt registers one truck through the service (the create the update extends) and removes
// it when the test ends. The returned row carries the caller-supplied facts the legs re-assert.
func seedTruckAt(t *testing.T, h *harness, branchID string) Truck {
	t.Helper()
	ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, h.pool), RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
	code := "UPD-" + uniqueSuffix()
	id, err := h.svc.CreateTruck(ctx, Truck{
		BranchID: branchID, Code: code, Name: "First spelling", TruckType: "FLATBED",
	})
	if err != nil {
		t.Fatalf("seed truck: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM dispatch_trucks WHERE id = $1`, id)
	})
	return Truck{ID: id, BranchID: branchID, Code: code, Name: "First spelling", TruckType: "FLATBED", IsActive: true}
}

// seedDriverAt registers one driver through the service and removes it when the test ends.
func seedDriverAt(t *testing.T, h *harness, branchID string) Driver {
	t.Helper()
	ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, h.pool), RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
	id, err := h.svc.CreateDriver(ctx, Driver{BranchID: branchID, Name: "First spelling (demo)", Phone: "613-555-0311"})
	if err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM dispatch_drivers WHERE id = $1`, id)
	})
	return Driver{ID: id, BranchID: branchID, Name: "First spelling (demo)", Phone: "613-555-0311", IsActive: true}
}

// TestFleetUpdate_TruckUpdatesTheRowAndIsIdempotent: a logistics manager renames a truck, retunes
// its advisory capacities and retires it (active off) in one PUT; the identical PUT again answers
// 200 with the same row.
func TestFleetUpdate_TruckUpdatesTheRowAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	tr := seedTruckAt(t, h, branchTRT)
	mux, headers := fleetMux(t, h, identity2.ScopeGlobal)

	body := fmt.Sprintf(`{"branch_id":%q,"code":%q,"name":"Second spelling","truck_type":"DUMP",
		"capacity_kg":9000.5,"capacity_m3":42,"pallet_positions":18,"is_active":false}`, tr.BranchID, tr.Code)
	code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/trucks/"+tr.ID, body)
	if code != http.StatusOK {
		t.Fatalf("the truck update answered %d: %s", code, resp)
	}
	var got Truck
	if err := json.Unmarshal([]byte(resp), &got); err != nil {
		t.Fatalf("the update body does not decode as a truck: %v", err)
	}
	if got.Name != "Second spelling" || got.TruckType != "DUMP" || got.IsActive ||
		got.CapacityKg == nil || *got.CapacityKg != 9000.5 ||
		got.CapacityM3 == nil || *got.CapacityM3 != 42 ||
		got.PalletPositions == nil || *got.PalletPositions != 18 {
		t.Fatalf("the stored truck is %+v, want the renamed, retuned, retired row", got)
	}

	// The identical PUT again: the same row, the same 200. A retry must never be a conflict.
	code, resp2 := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/trucks/"+tr.ID, body)
	if code != http.StatusOK {
		t.Fatalf("the identical retry answered %d: %s", code, resp2)
	}
	if resp2 != resp {
		t.Fatalf("the retry answered a different body: %s then %s", resp, resp2)
	}

	// What the DATABASE holds, not what the response said.
	var active bool
	var name string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT is_active, name FROM dispatch_trucks WHERE id = $1`, tr.ID).Scan(&active, &name); err != nil {
		t.Fatalf("read the truck back: %v", err)
	}
	if active || name != "Second spelling" {
		t.Fatalf("the row is active=%v name=%q, want the retired, renamed row", active, name)
	}
}

// TestFleetUpdate_TruckRefusesACrossBranchCaller: the branch wall. A Trenton truck in a caller's
// hands who reaches Kingston only is refused with the fence's own 403 and the row does not move.
func TestFleetUpdate_TruckRefusesACrossBranchCaller(t *testing.T) {
	h := newHarness(t)
	tr := seedTruckAt(t, h, branchTRT)
	mux, headers := fleetMux(t, h, identity2.ScopeBranch, branchKGN)

	code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/trucks/"+tr.ID,
		fmt.Sprintf(`{"branch_id":%q,"code":%q,"name":"Smuggled rename","is_active":false}`, tr.BranchID, tr.Code))
	if code != http.StatusForbidden {
		t.Fatalf("a cross-branch truck update answered %d: %s, want the fence's 403", code, resp)
	}
	var name string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT name FROM dispatch_trucks WHERE id = $1`, tr.ID).Scan(&name); err != nil {
		t.Fatalf("read the truck back: %v", err)
	}
	if name != "First spelling" {
		t.Fatalf("a refused caller changed the truck to %q", name)
	}
}

// TestFleetUpdate_TruckValidatesLikeTheCreate: the create's refusals are the update's, stated in
// words before any CHECK can fire, and a branch move is refused outright.
func TestFleetUpdate_TruckValidatesLikeTheCreate(t *testing.T) {
	h := newHarness(t)
	tr := seedTruckAt(t, h, branchTRT)
	mux, headers := fleetMux(t, h, identity2.ScopeGlobal)

	for name, body := range map[string]string{
		"an empty code":       fmt.Sprintf(`{"branch_id":%q,"code":"","name":"x"}`, tr.BranchID),
		"an unknown type":     fmt.Sprintf(`{"branch_id":%q,"code":%q,"truck_type":"SPACESHIP"}`, tr.BranchID, tr.Code),
		"a negative capacity": fmt.Sprintf(`{"branch_id":%q,"code":%q,"capacity_kg":-1}`, tr.BranchID, tr.Code),
		"a branch move":       fmt.Sprintf(`{"branch_id":%q,"code":%q,"name":"x"}`, branchKGN, tr.Code),
	} {
		code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/trucks/"+tr.ID, body)
		if code != http.StatusBadRequest {
			t.Errorf("%s answered %d: %s, want the create's own 400 family", name, code, resp)
		}
	}
}

// TestFleetUpdate_TruckCodeCollisionIsAStatedConflict: renaming onto another truck's code at the
// same branch is the create's 409, in words, not a 500 carrying a constraint name.
func TestFleetUpdate_TruckCodeCollisionIsAStatedConflict(t *testing.T) {
	h := newHarness(t)
	one := seedTruckAt(t, h, branchTRT)
	other := seedTruckAt(t, h, branchTRT)
	mux, headers := fleetMux(t, h, identity2.ScopeGlobal)

	code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/trucks/"+one.ID,
		fmt.Sprintf(`{"branch_id":%q,"code":%q,"name":"x"}`, one.BranchID, other.Code))
	if code != http.StatusConflict {
		t.Fatalf("a code collision answered %d: %s, want the stated 409", code, resp)
	}
}

// TestFleetUpdate_MissingTruckIs404: a garbage or absent id is NOT FOUND, never a 500.
func TestFleetUpdate_MissingTruckIs404(t *testing.T) {
	h := newHarness(t)
	mux, headers := fleetMux(t, h, identity2.ScopeGlobal)
	for _, id := range []string{"not-a-uuid", "11111111-1111-4111-8111-111111111111"} {
		code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/trucks/"+id,
			`{"branch_id":"`+branchTRT+`","code":"X"}`)
		if code != http.StatusNotFound {
			t.Errorf("PUT %s answered %d: %s, want 404", id, code, resp)
		}
	}
}

// TestFleetUpdate_DriverUpdatesTheRowAndIsIdempotent: the driver twin of the truck leg, including
// the login link the create takes (user_id).
func TestFleetUpdate_DriverUpdatesTheRowAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	d := seedDriverAt(t, h, branchTRT)
	mux, headers := fleetMux(t, h, identity2.ScopeGlobal)

	body := fmt.Sprintf(`{"branch_id":%q,"name":"Second spelling (demo)","phone":"613-555-0399",
		"licence_class":"DZ","is_active":false}`, d.BranchID)
	code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/drivers/"+d.ID, body)
	if code != http.StatusOK {
		t.Fatalf("the driver update answered %d: %s", code, resp)
	}
	var got Driver
	if err := json.Unmarshal([]byte(resp), &got); err != nil {
		t.Fatalf("the update body does not decode as a driver: %v", err)
	}
	if got.Name != "Second spelling (demo)" || got.Phone != "613-555-0399" ||
		got.LicenceClass != "DZ" || got.IsActive {
		t.Fatalf("the stored driver is %+v, want the retuned, retired row", got)
	}

	code, resp2 := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/drivers/"+d.ID, body)
	if code != http.StatusOK || resp2 != resp {
		t.Fatalf("the identical retry answered %d (then %s), want the same 200 and body", code, resp2)
	}

	var active bool
	if err := h.pool.QueryRow(context.Background(),
		`SELECT is_active FROM dispatch_drivers WHERE id = $1`, d.ID).Scan(&active); err != nil {
		t.Fatalf("read the driver back: %v", err)
	}
	if active {
		t.Fatal("the driver row is still active after the retiring PUT")
	}
}

// TestFleetUpdate_DriverRefusesACrossBranchCaller: the branch wall, the driver twin.
func TestFleetUpdate_DriverRefusesACrossBranchCaller(t *testing.T) {
	h := newHarness(t)
	d := seedDriverAt(t, h, branchTRT)
	mux, headers := fleetMux(t, h, identity2.ScopeBranch, branchKGN)

	code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/drivers/"+d.ID,
		fmt.Sprintf(`{"branch_id":%q,"name":"Smuggled rename","is_active":false}`, d.BranchID))
	if code != http.StatusForbidden {
		t.Fatalf("a cross-branch driver update answered %d: %s, want the fence's 403", code, resp)
	}
	var name string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT name FROM dispatch_drivers WHERE id = $1`, d.ID).Scan(&name); err != nil {
		t.Fatalf("read the driver back: %v", err)
	}
	if name != "First spelling (demo)" {
		t.Fatalf("a refused caller changed the driver to %q", name)
	}
}

// TestFleetUpdate_DriverValidatesLikeTheCreate: the create's refusals are the update's.
func TestFleetUpdate_DriverValidatesLikeTheCreate(t *testing.T) {
	h := newHarness(t)
	d := seedDriverAt(t, h, branchTRT)
	mux, headers := fleetMux(t, h, identity2.ScopeGlobal)

	for name, body := range map[string]string{
		"an empty name":                fmt.Sprintf(`{"branch_id":%q,"name":"  "}`, d.BranchID),
		"a branch move":                fmt.Sprintf(`{"branch_id":%q,"name":"x"}`, branchKGN),
		"no branch at all":             `{"name":"x"}`,
		"a user id that is not a uuid": fmt.Sprintf(`{"branch_id":%q,"name":"x","user_id":"not-a-uuid"}`, d.BranchID),
	} {
		code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/drivers/"+d.ID, body)
		if code != http.StatusBadRequest {
			t.Errorf("%s answered %d: %s, want the create's own 400 family", name, code, resp)
		}
	}
}

// TestFleetUpdate_MissingDriverIs404: the driver twin of the truck's 404 leg.
func TestFleetUpdate_MissingDriverIs404(t *testing.T) {
	h := newHarness(t)
	mux, headers := fleetMux(t, h, identity2.ScopeGlobal)
	for _, id := range []string{"not-a-uuid", "11111111-1111-4111-8111-111111111111"} {
		code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/drivers/"+id,
			`{"branch_id":"`+branchTRT+`","name":"X"}`)
		if code != http.StatusNotFound {
			t.Errorf("PUT %s answered %d: %s, want 404", id, code, resp)
		}
	}
}

// TestFleetUpdate_BelowManagerIsRefusedOnBothRoutes: the update routes are manager gated at the mux.
// A frontline caller with the same real bearer, reaching the same branch, is refused with a 403 on
// the truck and on the driver update, and neither row moves.
func TestFleetUpdate_BelowManagerIsRefusedOnBothRoutes(t *testing.T) {
	h := newHarness(t)
	tr := seedTruckAt(t, h, branchTRT)
	d := seedDriverAt(t, h, branchTRT)
	mux, headers := fleetMuxAs(t, h, "yard_team", identity2.TierFrontline, identity2.ScopeBranch, branchTRT)

	code, resp := fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/trucks/"+tr.ID,
		fmt.Sprintf(`{"branch_id":%q,"code":%q,"name":"Frontline rename","is_active":true}`, tr.BranchID, tr.Code))
	if code != http.StatusForbidden {
		t.Fatalf("a frontline truck update answered %d: %s, want 403", code, resp)
	}
	code, resp = fleetDo(t, mux, headers, http.MethodPut, "/api/v1/dispatch/drivers/"+d.ID,
		fmt.Sprintf(`{"branch_id":%q,"name":"Frontline rename (demo)","is_active":true}`, d.BranchID))
	if code != http.StatusForbidden {
		t.Fatalf("a frontline driver update answered %d: %s, want 403", code, resp)
	}
	var truckName, driverName string
	if err := h.pool.QueryRow(context.Background(), `SELECT name FROM dispatch_trucks WHERE id = $1`, tr.ID).Scan(&truckName); err != nil {
		t.Fatalf("read the truck back: %v", err)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT name FROM dispatch_drivers WHERE id = $1`, d.ID).Scan(&driverName); err != nil {
		t.Fatalf("read the driver back: %v", err)
	}
	if truckName != "First spelling" || driverName != "First spelling (demo)" {
		t.Fatalf("a refused caller changed a row: truck %q, driver %q", truckName, driverName)
	}
}
