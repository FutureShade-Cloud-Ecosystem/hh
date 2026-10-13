//go:build demo

package membership

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
)

// A staff flow account has no tenant team: the tenant leg answers empty,
// so a staff subject can never ride the tenant path.
func TestStaffAccountsHaveNoTenantOrgs(t *testing.T) {
	s := New()
	for _, a := range flows.All() {
		if a.Class() != flows.ClassStaff {
			continue
		}
		orgs, err := s.TenantOrgs(context.Background(), a.PlatformUserID)
		if err != nil {
			t.Fatalf("TenantOrgs(%s): %v", a.Flow, err)
		}
		if len(orgs) != 0 {
			t.Fatalf("TenantOrgs(%s) answered %v, want empty", a.Flow, orgs)
		}
	}
}

// A contractor flow account holds exactly one tenant team, in the demo org.
func TestContractorAccountsHoldOneTenantTeamInTheDemoOrg(t *testing.T) {
	s := New()
	n := 0
	for _, a := range flows.All() {
		if a.Class() != flows.ClassTenant {
			continue
		}
		n++
		orgs, err := s.TenantOrgs(context.Background(), a.PlatformUserID)
		if err != nil {
			t.Fatalf("TenantOrgs(%s): %v", a.Flow, err)
		}
		if len(orgs) != 1 || orgs[0] != flows.OrgSlug {
			t.Fatalf("TenantOrgs(%s) = %v, want [%s]", a.Flow, orgs, flows.OrgSlug)
		}
	}
	if n != 5 {
		t.Fatalf("checked %d contractor accounts, want 5", n)
	}
}

// A contractor is never a staff member of any org, and nobody outside the
// table has a tenant team.
func TestContractorsAreNotStaffMembersAndStrangersHaveNoTenantTeam(t *testing.T) {
	s := New()
	ctx := context.Background()
	for _, a := range flows.All() {
		if a.Class() != flows.ClassTenant {
			continue
		}
		got, err := s.OrgMember(ctx, a.PlatformUserID, flows.OrgSlug)
		if err != nil || got {
			t.Fatalf("OrgMember(%s) = %v, %v; a contractor is no staff member", a.Flow, got, err)
		}
	}
	orgs, err := s.TenantOrgs(ctx, "some-real-platform-user")
	if err != nil || len(orgs) != 0 {
		t.Fatalf("a stranger holds tenant orgs %v, %v", orgs, err)
	}
	orgs, err = s.TenantOrgs(ctx, "")
	if err != nil || len(orgs) != 0 {
		t.Fatalf("an empty subject holds tenant orgs %v, %v", orgs, err)
	}
}
