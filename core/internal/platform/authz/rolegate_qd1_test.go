package authz

// rolegate_qd1_test.go, the wrong role ledger's gate rows (plan 3.4): rows
// 1, 11, 12 and 13, one request per row, proven at the gate that holds them.
// Row 1: any quote route as a role with no Quote Desk level. Row 11: a
// pricing write as Inside Sales. Row 12: the pricing log as HR (the cost and
// margin list, D10). Row 13: a Counter Till work route as Outside Sales and
// as the General Manager (the operation itself is QD-5's; the ruled
// refusals are the configuration's and this gate's, pinned here).
import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// runGatedAs runs one request through the gate as one held role.
func runGatedAs(t *testing.T, f *fixture, access RouteAccess, role string) (int, bool) {
	t.Helper()
	g, err := NewRoleGate(f.gate, &fakeRolesSource{roles: []string{role}}, mustConfig(t))
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	code, _, reached := runGated(t, f, g, access, gatedStaffHeaders(t, f))
	return code, reached
}

// - ledger row 1: any quote route, as a role with no Quote Desk level -

func TestQD1_Row1_NoQuoteDeskRoleRefusedAtGate(t *testing.T) {
	f := newFixture(t)
	access := RouteAccess{Surfaces: []string{"quote-desk"}, Level: roleconfigLevel("work")}
	for _, role := range []string{
		"yard_team", "driver", "logistics_manager", "procurement_manager",
		"payables_receivables", "financial_controller", "hr",
	} {
		code, reached := runGatedAs(t, f, access, role)
		if code != 403 || reached {
			t.Errorf("row 1 (%s): want the RBAC 403 with the handler unreached, got %d reached=%v", role, code, reached)
		}
	}
	// A Work role passes the same route: the gate is the level, not a blanket.
	code, reached := runGatedAs(t, f, access, "inside_sales")
	if code != 200 || !reached {
		t.Errorf("inside sales must reach a quote desk work route, got %d reached=%v", code, reached)
	}
}

// - ledger row 11: write a pricing rule, as Inside Sales -

func TestQD1_Row11_InsideSalesRefusedPricingWrite(t *testing.T) {
	f := newFixture(t)
	access := RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfigLevel("manage"), List: "pricing_editors"}
	code, reached := runGatedAs(t, f, access, "inside_sales")
	if code != 403 || reached {
		t.Errorf("row 11: inside sales must be refused the pricing writes, got %d reached=%v", code, reached)
	}
}

// - ledger row 12: read the pricing log, as HR (the cost and margin list) -

func TestQD1_Row12_HRRefusedThePricingLog(t *testing.T) {
	f := newFixture(t)
	access := RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfigLevel("read"), List: "cost_visibility"}
	code, reached := runGatedAs(t, f, access, "hr")
	if code != 403 || reached {
		t.Errorf("row 12: HR holds Dealer Admin at admin but is off the cost list, want the RBAC 403, got %d reached=%v", code, reached)
	}
	// A role on the cost list that also reaches the route passes it.
	code, reached = runGatedAs(t, f, access, "general_manager")
	if code != 200 || !reached {
		t.Errorf("the General Manager is on the cost list and must read the pricing log, got %d reached=%v", code, reached)
	}
}

// - ledger row 13: a Counter Till work route, as Outside Sales and the GM -

func TestQD1_Row13_OutsideSalesAndGMRefusedCounterTillWork(t *testing.T) {
	f := newFixture(t)
	access := RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfigLevel("work")}
	for _, role := range []string{"outside_sales", "general_manager"} {
		code, reached := runGatedAs(t, f, access, role)
		if code != 403 || reached {
			t.Errorf("row 13 (%s): want the RBAC 403 on a counter till work route, got %d reached=%v", role, code, reached)
		}
	}
	// Inside Sales is the counter's own: it passes.
	code, reached := runGatedAs(t, f, access, "inside_sales")
	if code != 200 || !reached {
		t.Errorf("inside sales must reach a counter till work route, got %d reached=%v", code, reached)
	}
}

// roleconfigLevel is a local alias so this file reads at the annotation's
// vocabulary.
func roleconfigLevel(l string) roleconfig.Level { return roleconfig.Level(l) }
