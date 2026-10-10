package authz

// rolegate_received_payment_test.go, the wall of the one money route that moves a received payment
// across invoices (POST /api/v1/ar/payments/apply): the census annotates it money-desk work, so the
// roles that may record a payment today (Payables and Receivables at work, the controller and the
// general manager above it) pass, and every role the desk holds at read or nothing (the selling
// roles, HR) is refused in the gate's shared Forbidden body, never a route of its own words.

import (
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func TestRoleGateReceivedPaymentApplyWall(t *testing.T) {
	access, ok := RouteAccessFor("POST", "/api/v1/ar/payments/apply")
	if !ok {
		t.Fatal("the received payment route carries no staff access in the document")
	}
	if len(access.Surfaces) != 1 || access.Surfaces[0] != roleconfig.SurfaceMoneyDesk || access.Level != roleconfig.LevelWork || access.List != "" {
		t.Fatalf("the route is walled to money-desk work and no list, got %+v", access)
	}
	cases := []struct {
		name     string
		roles    []string
		admitted bool
	}{
		{"payables and receivables records payments", []string{"payables_receivables"}, true},
		{"the financial controller records payments", []string{"financial_controller"}, true},
		{"the general manager records payments", []string{"general_manager"}, true},
		{"inside sales reads the desk and may not record one", []string{"inside_sales"}, false},
		{"the branch manager reads the desk and may not record one", []string{"branch_manager"}, false},
		{"hr is held off the money desk outright", []string{"hr"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			g := gateOverRoles(t, f, tc.roles, mustConfig(t))
			code, body, _ := runGated(t, f, g, access, gatedStaffHeaders(t, f))
			if tc.admitted && code != http.StatusOK {
				t.Fatalf("admission refused: status %d body %s", code, body)
			}
			if !tc.admitted && (code != http.StatusForbidden || body != string(BodyForbidden)) {
				t.Fatalf("refusal is not the RBAC-403 family: status %d body %s", code, body)
			}
		})
	}
}
