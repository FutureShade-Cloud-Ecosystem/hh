package authz

// rolegate_yard_test.go, who may record a short pick or a skid count, on the REAL routes: the annotation
// read is the generated census entry, so the contract is what is proven, not a literal beside it. The
// Yard team and the Yard manager reach both handlers; every other role in the catalogue is refused at
// the gate with the handler unreached.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func yardAccess(t *testing.T, operationID string) RouteAccess {
	t.Helper()
	for _, r := range apigen.Routes {
		if r.OperationID == operationID {
			return RouteAccess{Surfaces: r.Surfaces, Level: roleconfig.Level(r.Level), List: r.List}
		}
	}
	t.Fatalf("%s is not in the route census", operationID)
	return RouteAccess{}
}

func TestYardRoles_ShortPickAndSkidCountAreTheYardsAlone(t *testing.T) {
	f := newFixture(t)
	for _, op := range []string{"yardRecordPick", "yardRecordSkids"} {
		access := yardAccess(t, op)
		if len(access.Surfaces) != 1 || access.Surfaces[0] != "yard-pick" || access.Level != roleconfig.LevelWork {
			t.Fatalf("%s must name the yard pick surface at work, got %+v", op, access)
		}
		admitted := map[string]bool{"yard_team": true, "yard_manager": true}
		for _, role := range roleconfig.Roles() {
			code, reached := runGatedAs(t, f, access, role)
			if admitted[role] {
				if code != 200 || !reached {
					t.Errorf("%s: %s must reach the handler, got %d reached=%v", op, role, code, reached)
				}
				continue
			}
			if code != 403 || reached {
				t.Errorf("%s: %s must be refused at the gate, got %d reached=%v", op, role, code, reached)
			}
		}
	}
}
