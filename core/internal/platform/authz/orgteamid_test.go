package authz

import (
	"context"
	"strings"
	"testing"
)

func TestOrgTeamID(t *testing.T) {
	long32 := "a" + strings.Repeat("b", 31)
	cases := []struct {
		name string
		slug string
		want string
		ok   bool
	}{
		{"plain", "yardone", "org-yardone", true},
		{"hyphens", "hh-demo", "org-hh-demo", true},
		{"dots", "gable.landscape", "org-gable.landscape", true},
		{"underscore", "gable_ls", "org-gable_ls", true},
		{"digit start", "9yard", "org-9yard", true},
		{"32 characters", long32, "org-" + long32, true},
		{"33 characters", long32 + "c", "", false},
		{"64 characters", strings.Repeat("a", 64), "", false},
		{"empty", "", "", false},
		{"leading hyphen", "-yard", "", false},
		{"leading dot", ".yard", "", false},
		{"uppercase", "Yard", "", false},
		{"colon", "yard:one", "", false},
		{"space", "yard one", "", false},
	}
	for _, c := range cases {
		got, ok := OrgTeamID(c.slug)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: OrgTeamID(%q) = %q, %v; want %q, %v", c.name, c.slug, got, ok, c.want, c.ok)
		}
		if ok && len(got) > 36 {
			t.Errorf("%s: team id %q exceeds 36 characters", c.name, got)
		}
	}
}

func TestOrgSlugShapeFitsTeamID(t *testing.T) {
	for _, slug := range []string{"hh-demo", "another-dealer", "fixture-north", "fixture-south", "gable-landscape-supply", "other-yard"} {
		if !orgSlugPattern.MatchString(slug) {
			t.Errorf("seeded slug %q no longer passes the shape", slug)
		}
	}
	if !orgSlugPattern.MatchString("a" + strings.Repeat("b", 31)) {
		t.Errorf("32 character slug refused")
	}
	for _, slug := range []string{"a" + strings.Repeat("b", 32), strings.Repeat("a", 64), "", "-a", "_a", ".a", "A", "a:b"} {
		if orgSlugPattern.MatchString(slug) {
			t.Errorf("slug %q passes the shape", slug)
		}
	}
}

func TestOrgMemberUsesHyphenTeamID(t *testing.T) {
	src, stub := newAppwrite(t, func(s *appwriteStub) {
		s.page = appwriteMembershipPage{Total: 2, Memberships: []appwriteMembership{
			{TeamID: "org:yardone", UserID: "platform-user-1"},
			{TeamID: "org-yardtwo", UserID: "platform-user-1"},
		}}
	})
	ctx := context.Background()
	if ok, err := src.OrgMember(ctx, "platform-user-1", "yardtwo"); err != nil || !ok {
		t.Fatalf("org- team: ok=%v err=%v", ok, err)
	}
	if ok, err := src.OrgMember(ctx, "platform-user-1", "yardone"); err != nil || ok {
		t.Fatalf("old org: team admitted: ok=%v err=%v", ok, err)
	}
	before := stub.attempts
	long := "a" + strings.Repeat("b", 32)
	if ok, err := src.OrgMember(ctx, "platform-user-1", long); err == nil || ok {
		t.Fatalf("33 character slug: ok=%v err=%v, want a shape refusal", ok, err)
	}
	if stub.attempts != before {
		t.Fatalf("a slug that cannot form a team id reached the Console")
	}
}
