package authz

// rolegate_t5018_test.go, who may set the T5018 subcontractor flag on a vendor. The annotation
// read is the generated census entry, so the contract is what is proven, not a literal beside it.
// The Financial Controller is the decided matrix's only admin seat on money-desk, so the flag
// route names money-desk at admin and every other role in the catalogue is refused at the gate
// with the shared RBAC body the handler never adds to. The year's report has its own wall, on
// money-desk at read, in rolegate_t5018_report_test.go.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// TestController_OnlyTheControllerSetsTheT5018Flag: the flag write names money-desk at admin, and
// exactly one role in the catalogue holds that seat. The general manager, whose money-desk cell is
// manage, is refused with it: the flag is the controller's alone, not the desk's.
func TestController_OnlyTheControllerSetsTheT5018Flag(t *testing.T) {
	f := newFixture(t)
	access := procurementAccess(t, "purchasingSetVendorT5018")
	if len(access.Surfaces) != 1 || access.Surfaces[0] != "money-desk" || access.Level != roleconfig.LevelAdmin {
		t.Fatalf("the T5018 flag must name money-desk at admin, got %+v", access)
	}
	for _, role := range roleconfig.Roles() {
		code, reached := runGatedAs(t, f, access, role)
		if role == "financial_controller" {
			if code != 200 || !reached {
				t.Errorf("the financial controller must reach the handler, got %d reached=%v", code, reached)
			}
			continue
		}
		if code != 403 || reached {
			t.Errorf("%s must be refused at the gate, got %d reached=%v", role, code, reached)
		}
	}
}
