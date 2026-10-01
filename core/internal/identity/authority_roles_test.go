package identity

// ResolveRoles' own suite (the re-read review's note b): the whole-row
// read beside the winner-only ResolveStaff, over the same b3 fixture the
// resolver's other legs use. The legs state what the role gate leans on:
// one role comes back alone, the whole row comes back in tier order, the
// platform roles ride the row they sit on, and no grant is an empty row
// with no error. Gated on the same scratch database variable.
import (
	"context"
	"slices"
	"testing"
)

func TestResolveRolesSingleRole(t *testing.T) {
	f := newB3Fixture(t)
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "inside_sales", b3BranchOne)
	roles, err := resolver.ResolveRoles(context.Background(), f.platformUser, f.orgID)
	if err != nil {
		t.Fatalf("ResolveRoles: %v", err)
	}
	if !slices.Equal(roles, []string{"inside_sales"}) {
		t.Fatalf("roles = %v, want [inside_sales]", roles)
	}
}

// The whole row, not the winner (X10): a caller granted general_manager
// and inside_sales resolves the general manager as the winner (the tier
// guard's fact) and both roles from ResolveRoles (the role gate's fact),
// the winner first by the resolver's tier order.
func TestResolveRolesWholeRowBesideTheWinner(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "general_manager", "")
	f.grant(t, "inside_sales", b3BranchOne)

	winner, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("ResolveStaff: ok=%v err=%v", ok, err)
	}
	if winner.RoleID != "general_manager" {
		t.Fatalf("winner = %s, want general_manager", winner.RoleID)
	}
	roles, err := resolver.ResolveRoles(ctx, f.platformUser, f.orgID)
	if err != nil {
		t.Fatalf("ResolveRoles: %v", err)
	}
	if !slices.Equal(roles, []string{"general_manager", "inside_sales"}) {
		t.Fatalf("roles = %v, want [general_manager inside_sales]", roles)
	}
}

// The platform roles ride the row they sit on: the role gate's pass reads
// them from the same walk as any matrix role.
func TestResolveRolesPlatformRolesAmongThem(t *testing.T) {
	f := newB3Fixture(t)
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "super_admin", "")
	f.grant(t, "dealer_admin", "")
	f.grant(t, "inside_sales", b3BranchOne)
	roles, err := resolver.ResolveRoles(context.Background(), f.platformUser, f.orgID)
	if err != nil {
		t.Fatalf("ResolveRoles: %v", err)
	}
	// The tier order first, and within one tier the resolver's role-id
	// byte-order tie-break: dealer_admin before super_admin.
	if !slices.Equal(roles, []string{"dealer_admin", "super_admin", "inside_sales"}) {
		t.Fatalf("roles = %v, want [dealer_admin super_admin inside_sales]", roles)
	}
}

func TestResolveRolesNoGrantIsEmpty(t *testing.T) {
	f := newB3Fixture(t)
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	roles, err := resolver.ResolveRoles(context.Background(), f.platformUser, f.orgID)
	if err != nil {
		t.Fatalf("ResolveRoles: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("roles = %v, want empty", roles)
	}
}
