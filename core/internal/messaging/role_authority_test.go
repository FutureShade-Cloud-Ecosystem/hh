package messaging

// role_authority_test.go: the membership wall's authority half against the
// decided default matrix itself (core/internal/roleconfig/default.json,
// embedded): the general manager's cell is the one Admin cell on
// house-messages, every other role answers no, and an unpublished
// configuration answers no for everyone (fail closed, never a permissive
// default). The thirteen-cell shape is pinned too, so a matrix edit that
// moves the Admin cell moves this test with it.

import (
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// publishDefaultRolesForTest publishes the decided default matrix and
// returns the previous value for the caller's cleanup (the publish is
// package-wide, tests must not leak their configuration into each other).
func publishDefaultRolesForTest(t *testing.T) *roleconfig.Config {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig default: %v", err)
	}
	prev := publishedRoles.Load()
	PublishRoleConfig(cfg)
	return prev
}

func TestHouseMessagesAdmin_TheDecidedMatrix(t *testing.T) {
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig default: %v", err)
	}
	admins := 0
	for _, role := range roleconfig.Roles() {
		holds, err := cfg.Holds(role, SurfaceHouseMessages, roleconfig.LevelAdmin)
		if err != nil {
			t.Fatalf("matrix cell %s: %v", role, err)
		}
		want := holds
		erp, ok := identity.ERPCodeFor(role)
		if !ok {
			continue
		}
		if got := houseMessagesAdmin(Caller{RoleCode: erp}); got != want {
			t.Fatalf("role %s (erp %s): houseMessagesAdmin=%v, the matrix says %v", role, erp, got, want)
		}
		if want {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("the decided matrix holds %d Admin cells on house-messages, want the general manager alone", admins)
	}
	if !houseMessagesAdmin(Caller{RoleCode: "GENERAL_MANAGER"}) {
		t.Fatalf("the general manager must hold the pen")
	}
	if houseMessagesAdmin(Caller{RoleCode: "SALES_MANAGER"}) {
		t.Fatalf("a work-level role must not hold the pen")
	}
}

// Unpublished, the wall knows no admin: owner-only, never a permissive
// fallback.
func TestHouseMessagesAdmin_UnpublishedFailsClosed(t *testing.T) {
	prev := publishedRoles.Load()
	PublishRoleConfig(nil)
	defer PublishRoleConfig(prev)
	if houseMessagesAdmin(Caller{RoleCode: "GENERAL_MANAGER"}) {
		t.Fatalf("an unpublished configuration must answer no admin")
	}
	if houseMessagesAdmin(Caller{RoleCode: ""}) {
		t.Fatalf("a caller with no role must answer no admin")
	}
	if houseMessagesAdmin(Caller{RoleCode: "NOT_A_ROLE"}) {
		t.Fatalf("an unmapped role code must answer no admin")
	}
}
