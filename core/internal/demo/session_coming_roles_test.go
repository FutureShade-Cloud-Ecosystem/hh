//go:build demo

package demo

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The coming roles cycle's logins mint like every other flow: the minter is
// asked to sign exactly the flow table's platform user id, and the block that
// comes back carries the table's role and label with the resolver's tier and
// scope. One row per role, added one signed off commit at a time.
func TestSessionMintsTheComingRoleFlows(t *testing.T) {
	for _, tc := range []struct {
		flow, id, role, label, scope string
		tier                         int
	}{
		{"general-manager", "de5b2000-0000-4000-9000-000000000001", "general_manager", "General Manager", "GLOBAL", identity.TierLeadership},
		{"hr", "de5b2000-0000-4000-9000-000000000002", "hr", "HR", "GLOBAL", identity.TierLeadership},
		{"branch-manager", "de5b2000-0000-4000-9000-000000000003", "branch_manager", "Branch Manager", "BRANCH", identity.TierManager},
	} {
		t.Run(tc.flow, func(t *testing.T) {
			scope := identity.ScopeBranch
			if tc.scope == "GLOBAL" {
				scope = identity.ScopeGlobal
			}
			minter := &fakeMinter{token: "demo-token-coming"}
			m, _, _ := newTestMint(t, minter, &fakeStaff{
				auth: identity.StaffAuthority{RoleID: tc.role, Tier: tc.tier, Scope: scope},
				ok:   true,
			})
			rsp := postSession(t, m, `{"flow":"`+tc.flow+`"}`)
			if rsp.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
			}
			if len(minter.subjects) != 1 || minter.subjects[0] != tc.id {
				t.Fatalf("the minter was asked to sign %v, want exactly %s", minter.subjects, tc.id)
			}
			var got sessionResponse
			if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
				t.Fatalf("body does not decode: %v", err)
			}
			if got.User.RoleCode != tc.role || got.User.RoleName != tc.label {
				t.Errorf("block carries role %q named %q, want %q named %q", got.User.RoleCode, got.User.RoleName, tc.role, tc.label)
			}
			if got.User.Tier != tc.tier || got.User.Scope != tc.scope {
				t.Errorf("tier %d scope %q, want %d %q", got.User.Tier, got.User.Scope, tc.tier, tc.scope)
			}
		})
	}
}
