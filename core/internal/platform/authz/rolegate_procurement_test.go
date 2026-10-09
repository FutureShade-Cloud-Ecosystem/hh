package authz

// rolegate_procurement_test.go, who may buy and who may post a vendor bill, on the REAL routes: the
// annotation read is the generated census entry, so the contract is what is proven, not a literal
// beside it. The procurement manager is the decided matrix's only manage seat on buying-and-stock,
// so every write on that surface is the buyer's alone; the vendor bill post is money-desk's, and
// the buyer's money-desk cell is a read (ROLE-ACCESS-r3: "posts no vendor bill; Payables and
// Receivables posts them"), so the gate refuses it with the shared RBAC body the handler never
// adds to.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func procurementAccess(t *testing.T, operationID string) RouteAccess {
	t.Helper()
	for _, r := range apigen.Routes {
		if r.OperationID == operationID {
			return RouteAccess{Surfaces: r.Surfaces, Level: roleconfig.Level(r.Level), List: r.List}
		}
	}
	t.Fatalf("%s is not in the route census", operationID)
	return RouteAccess{}
}

// TestProcurementManager_TheBuyingWritesAreTheBuyersAlone: the vendor master pair (vendor_master.go)
// and the PO write doors name buying-and-stock at manage, and exactly one role in the catalogue
// holds that seat.
func TestProcurementManager_TheBuyingWritesAreTheBuyersAlone(t *testing.T) {
	f := newFixture(t)
	for _, op := range []string{"purchasingCreateVendor", "purchasingUpdateVendor", "purchasingUpdateLine", "purchasingCreatePO", "purchasingTransitionPO", "inventoryCreatePOFromShortfall", "inventoryReorderPO"} {
		access := procurementAccess(t, op)
		if len(access.Surfaces) != 1 || access.Surfaces[0] != "buying-and-stock" || access.Level != roleconfig.LevelManage {
			t.Fatalf("%s must name buying-and-stock at manage, got %+v", op, access)
		}
		for _, role := range roleconfig.Roles() {
			code, reached := runGatedAs(t, f, access, role)
			if role == "procurement_manager" {
				if code != 200 || !reached {
					t.Errorf("%s: the procurement manager must reach the handler, got %d reached=%v", op, code, reached)
				}
				continue
			}
			if code != 403 || reached {
				t.Errorf("%s: %s must be refused at the gate, got %d reached=%v", op, role, code, reached)
			}
		}
	}
}

// TestProcurementManager_CannotPostAVendorBill: the bill post names money-desk at manage; the
// buyer's money-desk cell is a read, so the gate refuses it with the shared body. The route the
// roadmap's row 345 wall names, proved at the gate every role walks through.
//
// RECORDED, NOT DECIDED HERE: under the census row as it stands the admitted set is money-desk at
// manage and above (the controller, the general manager), which leaves Payables and Receivables,
// whose money-desk cell is work, refused both the bill post and the bill entry despite
// ROLE-ACCESS-r3 section 10 naming "posts vendor bills" among its money actions. That disagreement
// between the census rows and the matrix prose is the supervisor's to settle; this test pins the
// buyer's wall either way and does not guess the other side of it.
func TestProcurementManager_CannotPostAVendorBill(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	for _, op := range []string{"purchasingPostBill", "purchasingCreateBill"} {
		access := procurementAccess(t, op)
		if len(access.Surfaces) != 1 || access.Surfaces[0] != "money-desk" || access.Level != roleconfig.LevelManage {
			t.Fatalf("%s must name money-desk at manage, got %+v", op, access)
		}
		for _, role := range roleconfig.Roles() {
			code, reached := runGatedAs(t, f, access, role)
			reach, err := cfg.Holds(role, "money-desk", roleconfig.LevelManage)
			if err != nil {
				t.Fatalf("%s: %s: %v", op, role, err)
			}
			if reach && (code != 200 || !reached) {
				t.Errorf("%s: %s holds money-desk at manage or above, must reach the handler, got %d reached=%v", op, role, code, reached)
			}
			if !reach && (code != 403 || reached) {
				t.Errorf("%s: %s holds less than manage on money-desk, must be refused, got %d reached=%v", op, role, code, reached)
			}
		}
		// The buyer's own leg, named: not a silence among the loop's refusals but the wall the
		// roadmap states in words, answered by the gate's shared Forbidden body.
		if code, reached := runGatedAs(t, f, access, "procurement_manager"); code != 403 || reached {
			t.Errorf("the procurement manager must be refused %s with the shared body, got %d reached=%v", op, code, reached)
		}
	}
}

// TestProcurementManager_ReadsTheShortfallAndIncoming: the shortfall and incoming reads are the
// buying surface at read, so every role the matrix gives buying-and-stock at any level reaches
// them and the buyer among the rest.
func TestProcurementManager_ReadsTheShortfallAndIncoming(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	for _, op := range []string{"inventoryShortfall", "purchasingListPOs", "purchasingListIncoming"} {
		access := procurementAccess(t, op)
		if len(access.Surfaces) != 1 || access.Surfaces[0] != "buying-and-stock" || access.Level != roleconfig.LevelRead {
			t.Fatalf("%s must name buying-and-stock at read, got %+v", op, access)
		}
		for _, role := range roleconfig.Roles() {
			code, reached := runGatedAs(t, f, access, role)
			reach, err := cfg.Holds(role, access.Surfaces[0], roleconfig.LevelRead)
			if err != nil {
				t.Fatalf("%s: %s: %v", op, role, err)
			}
			if reach && (code != 200 || !reached) {
				t.Errorf("%s: %s holds buying-and-stock at read or above, must reach the handler, got %d reached=%v", op, role, code, reached)
			}
			if !reach && (code != 403 || reached) {
				t.Errorf("%s: %s holds nothing on buying-and-stock, must be refused, got %d reached=%v", op, role, code, reached)
			}
		}
	}
}
