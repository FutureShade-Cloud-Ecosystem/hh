package billing

import (
	"slices"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// W3 FC-1b: the Controller's finance charge doors are in the served route table, gated to the
// leadership tier (the handler narrows them to the Controller roles).
func TestFinanceChargeRoutes_AreServedAndGated(t *testing.T) {
	for _, want := range []struct{ method, path, op string }{
		{"POST", "/api/v1/ar/finance-charges", "billingRunFinanceCharges"},
		{"GET", "/api/v1/ar/finance-charges", "billingListFinanceCharges"},
		{"PUT", "/api/v1/ar/customers/{customerID}/finance-charge-waiver", "billingSetFinanceChargeWaiver"},
		{"POST", "/api/v1/ar/customers/{customerID}/finance-charge-waiver/clear", "billingClearFinanceChargeWaiver"},
	} {
		var found *apigen.Route
		for i := range apigen.Routes {
			if apigen.Routes[i].Method == want.method && apigen.Routes[i].Path == want.path {
				found = &apigen.Routes[i]
			}
		}
		if found == nil {
			t.Fatalf("W3-FC1b red: no route %s %s in the served route table", want.method, want.path)
		}
		if found.OperationID != want.op || found.Tag != "staff-billing" || found.XRole != "requireAdmin" || found.Bearer != "staff" || !found.Addition {
			t.Fatalf("W3-FC1b red: %s %s = %+v, want operation %s, tag staff-billing, requireAdmin, staff bearer, x-addition", want.method, want.path, *found, want.op)
		}
		if found.Level != "admin" || !slices.Contains(found.Surfaces, "money-desk") {
			t.Fatalf("W3-FC1b red: %s %s access = %s %v, want money-desk at admin", want.method, want.path, found.Level, found.Surfaces)
		}
	}
}
