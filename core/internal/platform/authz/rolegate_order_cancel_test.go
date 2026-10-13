package authz

// rolegate_order_cancel_test.go, who may cancel a special order and keep its deposit, on the REAL
// route: the annotation read is the generated census entry, so the contract is what is proven. The
// ruling (decision log row 1579) reads "a manager with a reason": the Sales Manager, the Branch
// Manager and the General Manager, each with the reason the cancel already requires, and nobody
// else. The super admin keeps the access it had (a platform role outside the matrix, and on the
// list by name as it is on the purchase order writes).

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// orderCancellers is written out longhand, not derived.
var orderCancellers = map[string]bool{
	"sales_manager": true, "branch_manager": true, "general_manager": true, "super_admin": true,
}

func orderCancelAccess(t *testing.T) RouteAccess {
	t.Helper()
	for _, r := range apigen.Routes {
		if r.OperationID == "orderCancel" {
			if r.List != ListOrderCancellers {
				t.Fatalf("orderCancel must name the %s list, has %q", ListOrderCancellers, r.List)
			}
			return procurementAccess(t, "orderCancel")
		}
	}
	t.Fatal("orderCancel is not in the route census")
	return RouteAccess{}
}

// TestOrderCancel_AdmitsTheThreeManagersAndNoOneElse: each listed role reaches the handler; every
// other business role and platform role is refused at the gate with the handler unreached.
func TestOrderCancel_AdmitsTheThreeManagersAndNoOneElse(t *testing.T) {
	f := newFixture(t)
	access := orderCancelAccess(t)
	roles := append(roleconfig.Roles(), PlatformRoleSuperAdmin, PlatformRoleDealerAdmin, PlatformRoleFBOperator)
	for _, role := range roles {
		code, reached := runGatedAs(t, f, access, role)
		if orderCancellers[role] {
			if code != 200 || !reached {
				t.Errorf("%s may cancel and must reach the handler, got %d reached=%v", role, code, reached)
			}
			continue
		}
		if code != 403 || reached {
			t.Errorf("%s may not cancel and must be refused at the gate, got %d reached=%v", role, code, reached)
		}
	}
}

// TestOrderCancel_NamedRefusals: the roles the ruling names as refused, each by name.
func TestOrderCancel_NamedRefusals(t *testing.T) {
	f := newFixture(t)
	access := orderCancelAccess(t)
	for _, role := range []string{
		"inside_sales", "outside_sales", "yard_manager", "yard_team", "payables_receivables",
		"financial_controller", "procurement_manager", "logistics_manager", "hr", "driver",
	} {
		if code, reached := runGatedAs(t, f, access, role); code != 403 || reached {
			t.Errorf("%s must be refused, got %d reached=%v", role, code, reached)
		}
	}
}

// TestOrderCancel_TheWholeRowCounts: a person who holds the General Manager seat beside a
// front line seat may cancel, the gate's whole row rule.
func TestOrderCancel_TheWholeRowCounts(t *testing.T) {
	f := newFixture(t)
	access := orderCancelAccess(t)
	g, err := NewRoleGate(f.gate, &fakeRolesSource{roles: []string{"inside_sales", "branch_manager"}}, mustConfig(t))
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	code, _, reached := runGated(t, f, g, access, gatedStaffHeaders(t, f))
	if code != 200 || !reached {
		t.Errorf("a held branch manager seat beside inside sales must reach the handler, got %d reached=%v", code, reached)
	}
}
