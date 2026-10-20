package authz

import (
	"net/http"
	"testing"
	"time"
)

// TanyAI slice 1 sub item 9: the verified bearer's jti is carried into the staff scope as
// SessionID, and nothing else about identity moves. A token with no jti carries the empty string.
func TestStaffScopeCarriesTheVerifiedJTI(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ name, jti string }{{"with a jti", "0123456789abcdef0123456789abcdef"}, {"without a jti", ""}} {
		rec := &recorder{}
		h := f.gate.StaffScoped(func(w http.ResponseWriter, r *http.Request, scope StaffScope) {
			rec.mu.Lock()
			rec.reached++
			rec.staff = scope
			rec.mu.Unlock()
		})
		claims := map[string]any{
			"iss": fixtureIssuer, "sub": fixtureSubject, "aud": fixtureStaffAud,
			"iat": f.clock.now.Add(-time.Minute).Unix(), "exp": f.clock.now.Add(10 * time.Minute).Unix(),
		}
		if tc.jti != "" {
			claims["jti"] = tc.jti
		}
		resp := capture(h, map[string]string{"Authorization": "Bearer " + f.mint(t, claims), HeaderXOrg: fixtureOrgSlug})
		if resp.status != http.StatusOK {
			t.Fatalf("%s: status %d body %s", tc.name, resp.status, resp.body)
		}
		_, staff, _ := rec.result()
		if staff.SessionID != tc.jti {
			t.Fatalf("%s: SessionID = %q, want %q", tc.name, staff.SessionID, tc.jti)
		}
		if staff.PlatformUserID != fixtureSubject || staff.OrgID != fixtureOrgID {
			t.Fatalf("%s: scope = %+v", tc.name, staff)
		}
	}
}
