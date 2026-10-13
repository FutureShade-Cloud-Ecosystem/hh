//go:build demo

package membership

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
)

func TestFlowAccountsAreMembersOfTheDemoOrg(t *testing.T) {
	s := New()
	ctx := context.Background()
	for _, a := range flows.All() {
		if a.Class() == flows.ClassTenant {
			continue // a contractor is never a staff member, see membership_tenant_test.go
		}
		got, err := s.OrgMember(ctx, a.PlatformUserID, flows.OrgSlug)
		if err != nil {
			t.Fatalf("OrgMember(%s): %v", a.Flow, err)
		}
		if !got {
			t.Fatalf("flow account %s refused membership in %s", a.Flow, flows.OrgSlug)
		}
	}
}

// Any other subject, or the demo accounts against any other org, is a
// plain no: the demo org is the only org answered and the flow table the
// only membership list.
func TestEveryoneElseIsNotAMember(t *testing.T) {
	s := New()
	ctx := context.Background()
	cases := []struct {
		name string
		sub  string
		org  string
	}{
		{"unknown subject in the demo org", "some-real-platform-user", flows.OrgSlug},
		{"flow account in another org", flows.All()[0].PlatformUserID, "some-other-dealer"},
		{"unknown subject in another org", "some-real-platform-user", "some-other-dealer"},
		{"empty subject", "", flows.OrgSlug},
	}
	for _, c := range cases {
		got, err := s.OrgMember(ctx, c.sub, c.org)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", c.name, err)
		}
		if got {
			t.Fatalf("%s: answered member", c.name)
		}
	}
}
