package authz

// rolegate_qd5_test.go, the counter payment's role set at the gate, on the REAL route: the annotation
// read is the generated census entry of quoteTakeOrderPayment, so the contract is what is proven, not
// a literal copied beside it. Refused: Outside Sales and the General Manager, HR, the Yard team, the
// Financial controller, and the Yard manager, whose Counter Till cell decision log row 1156 removed
// (reversing the row 370 ruling that had admitted it at the cash customer's one action), with every
// other role that holds no Counter Till level. The admitted set (the payment's own rule, CP0-7 A as
// amended): Inside Sales, the Sales manager and the Branch manager reach the handler. Row 14, the
// Yard manager's refusal at the SEPARATE conversion of a quote, is the service's rule and is proven
// by request in cmd/core, as is branch reach.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func orderPaymentAccess(t *testing.T) RouteAccess {
	t.Helper()
	for _, r := range apigen.Routes {
		if r.OperationID == "quoteTakeOrderPayment" {
			return RouteAccess{Surfaces: r.Surfaces, Level: roleconfig.Level(r.Level), List: r.List}
		}
	}
	t.Fatal("quoteTakeOrderPayment is not in the route census")
	return RouteAccess{}
}

func TestQD5_Rows13And14_OrderPaymentGateOnTheRealRoute(t *testing.T) {
	f := newFixture(t)
	access := orderPaymentAccess(t)
	if len(access.Surfaces) != 1 || access.Surfaces[0] != "counter-till" || access.Level != roleconfig.LevelWork {
		t.Fatalf("the route must name the counter till at work, got %+v", access)
	}
	for _, role := range []string{
		"outside_sales", "general_manager", "hr", "yard_team", "yard_manager", "driver", "logistics_manager",
		"procurement_manager", "payables_receivables", "financial_controller",
	} {
		if code, reached := runGatedAs(t, f, access, role); code != 403 || reached {
			t.Errorf("%s must be refused at the gate, got %d reached=%v", role, code, reached)
		}
	}
	for _, role := range []string{"inside_sales", "sales_manager", "branch_manager"} {
		if code, reached := runGatedAs(t, f, access, role); code != 200 || !reached {
			t.Errorf("%s must reach the payment door, got %d reached=%v", role, code, reached)
		}
	}
}
