package purchasing

import (
	"slices"
	"testing"
)

// W3 FX-2b: the Controller's revaluation routes are in the served route table, gated to the
// leadership tier (the handler narrows it to the Controller roles).
func TestAPRevaluationRoutes_AreServedAndGated(t *testing.T) {
	for _, want := range []struct {
		method, path, op string
		level            string
	}{
		{"POST", "/api/v1/purchasing/ap-revaluations", "purchasingRunAPRevaluation", "admin"},
		{"POST", "/api/v1/purchasing/ap-revaluations/{month}/reversal", "purchasingReverseAPRevaluation", "admin"},
		{"GET", "/api/v1/purchasing/ap-revaluations", "purchasingListAPRevaluations", "admin"},
	} {
		found := findServedRoute(want.method, want.path)
		if found == nil {
			t.Fatalf("W3-FX2b red: no route %s %s in the served route table", want.method, want.path)
		}
		if found.OperationID != want.op || found.Tag != "staff-purchasing" || found.XRole != "requireAdmin" || found.Bearer != "staff" || !found.Addition {
			t.Fatalf("W3-FX2b red: %s %s = %+v, want operation %s, tag staff-purchasing, requireAdmin, staff bearer, x-addition", want.method, want.path, *found, want.op)
		}
		if found.Level != want.level || !slices.Contains(found.Surfaces, "money-desk") {
			t.Fatalf("W3-FX2b red: %s %s access = %s %v, want money-desk at %s", want.method, want.path, found.Level, found.Surfaces, want.level)
		}
	}
}
