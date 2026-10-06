package authz

// rolegate_holds_test.go holds RoleGate.Holds to the gate's own admission: for every row of the
// matrix below the answer must equal whether the middleware, run on a real request over the same
// roles, reaches the handler. Holds is the gate's walk asked ahead of the call (the payment grant
// read), so the one thing that must never happen is the two disagreeing.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func TestHoldsAgreesWithTheMiddlewareOnEveryRow(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	tillWork := RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}
	tillOrDesk := RouteAccess{Surfaces: []string{"counter-till", "quote-desk"}, Level: roleconfig.LevelRead}
	pricingWrite := RouteAccess{Surfaces: []string{"dealer-admin"}, Level: roleconfig.LevelManage, List: roleconfig.SettingPricingEditors}
	for _, tc := range []struct {
		name   string
		roles  []string
		access RouteAccess
		want   bool
	}{
		{"inside sales works the till", []string{"inside_sales"}, tillWork, true},
		{"general manager only reads the till", []string{"general_manager"}, tillWork, false},
		{"the whole row: general manager and inside sales", []string{"general_manager", "inside_sales"}, tillWork, true},
		{"outside sales has no till cell", []string{"outside_sales"}, tillWork, false},
		{"outside sales reaches the till or desk read", []string{"outside_sales"}, tillOrDesk, true},
		{"yard team reaches neither", []string{"yard_team"}, tillOrDesk, false},
		{"a platform role passes the level", []string{PlatformRoleDealerAdmin}, tillWork, true},
		{"a platform role is not on the named list", []string{PlatformRoleSuperAdmin}, pricingWrite, false},
		{"sales manager is on the pricing list", []string{"sales_manager"}, pricingWrite, true},
		{"hr has the level and is off the list", []string{"hr"}, pricingWrite, false},
		{"no active grant", []string{}, tillWork, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := gateOverRoles(t, f, tc.roles, cfg)
			got, err := g.Holds(context.Background(), "sub", "org", tc.access)
			if err != nil {
				t.Fatalf("Holds: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Holds = %v, want %v", got, tc.want)
			}
			_, _, reached := runGated(t, f, g, tc.access, gatedStaffHeaders(t, f))
			if reached != got {
				t.Fatalf("Holds says %v and the middleware reached=%v: the two walks disagree", got, reached)
			}
		})
	}
}

func TestHoldsErrorsAreNeverAnAnswer(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	tillWork := RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelWork}

	g, err := NewRoleGate(f.gate, &fakeRolesSource{err: errRolesSourceGone}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := g.Holds(context.Background(), "sub", "org", tillWork); held || !errors.Is(err, errRolesSourceGone) {
		t.Fatalf("an unreadable row is the error and never a no: held=%v err=%v", held, err)
	}
	g, _ = NewRoleGate(f.gate, &fakeRolesSource{err: identity.ErrStaffUserInactive}, cfg)
	if held, err := g.Holds(context.Background(), "sub", "org", tillWork); held || !errors.Is(err, identity.ErrStaffUserInactive) {
		t.Fatalf("an inactive account passes its error through: held=%v err=%v", held, err)
	}
	g = gateOverRoles(t, f, []string{"not_a_role"}, cfg)
	if held, err := g.Holds(context.Background(), "sub", "org", tillWork); held || err == nil {
		t.Fatalf("a role outside the vocabulary is a data defect, not level none: held=%v err=%v", held, err)
	}
	g = gateOverRoles(t, f, []string{"inside_sales"}, cfg)
	if _, err := g.Holds(context.Background(), "sub", "org", RouteAccess{Surfaces: []string{"counter-till"}, Level: roleconfig.LevelNone}); err == nil {
		t.Fatal("an annotation the gate would refuse to attach is refused here too")
	}
}

func TestRouteAccessForReadsTheCensus(t *testing.T) {
	access, ok := RouteAccessFor("POST", "/api/v1/pos/sales")
	if !ok || len(access.Surfaces) != 1 || access.Surfaces[0] != "counter-till" || access.Level != roleconfig.LevelWork {
		t.Fatalf("the till sale's access: %+v ok=%v", access, ok)
	}
	if _, ok := RouteAccessFor("GET", "/api/v1/no/such/route"); ok {
		t.Fatal("an unknown route has no access")
	}
	if _, ok := RouteAccessFor("GET", "/api/v1/tenant/config"); ok {
		t.Fatal("a public route carries no staff access")
	}
}
