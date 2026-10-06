package pricing

// The floor warning's two routes sit behind the same wall as every price write: the
// dealer admin surface at manage, with the pricing editors list beside the level. The
// preview reads the editors' costs and margins and the confirmation writes the log, so
// neither is open to a role that may only read prices. The gate itself is the authz
// package's (its suites prove the list refuses hr and admits the Sales manager and the
// General Manager); this pins that the contract hands these two routes to it.
import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

func TestFloorRoutesAreBehindThePricingEditorsList(t *testing.T) {
	want := map[string]string{
		"pricingPreviewFloor":        "/api/v1/pricing/floor-preview",
		"pricingConfirmFloorWarning": "/api/v1/pricing/floor-confirmations",
	}
	seen := map[string]bool{}
	for _, r := range apigen.Routes {
		path, ok := want[r.OperationID]
		if !ok {
			continue
		}
		seen[r.OperationID] = true
		if r.Method != "POST" || r.Path != path {
			t.Errorf("%s: route is %s %s, want POST %s", r.OperationID, r.Method, r.Path, path)
		}
		if r.XRole != "requireManager" || r.Level != "manage" || r.List != "pricing_editors" {
			t.Errorf("%s: gate is %s at %s with list %q, want requireManager at manage with pricing_editors", r.OperationID, r.XRole, r.Level, r.List)
		}
		if len(r.Surfaces) != 1 || r.Surfaces[0] != "dealer-admin" {
			t.Errorf("%s: surfaces %v, want [dealer-admin]", r.OperationID, r.Surfaces)
		}
		if !r.Addition {
			t.Errorf("%s: an addition with no ERP source route must be marked as one", r.OperationID)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s is not in the contract's route table", id)
		}
	}
}
