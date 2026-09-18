package accept

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// SW-2, acceptance line 2: the anonymous sweeps. Every leg asserts the
// status, the byte-exact shared body (the oracle rule: no response may
// reveal which check failed), zero executed statements (the refusal runs
// before any repository), and zero reach on any handler. The public route
// is the control in both directions: it must not be over-blocked, and an
// X-Org header on it has zero effect (ADR-0020 leg 2, the named non-rule).
//
// Red proofs (run report): SW-2a by mutation SW-2a (one guarded route
// registered unguarded in shims.RegisterRoutes), SW-2b by mutation SW-2b
// (the middleware swallows the bearer parse failure and continues),
// SW-2c by mutation SW-2c (the verifier kid check disabled, the
// B2-verified hook at verifier.go's kid pair).

// TestGate_Anon_NoToken is SW-2a: with no Authorization header at all,
// every guarded operation answers 401, never 200, and never
// 404-before-auth.
func TestGate_Anon_NoToken(t *testing.T) {
	s := newSweep(t)
	snapshot := len(s.shimRec.Entries())
	s.resetProbes()

	s.b.counter.Reset()
	var responses []*httptest.ResponseRecorder
	for _, route := range s.guardedSurface() {
		rsp := s.do(t, route.method, route.target, "")
		testkit.AssertStatus(t, "no token "+route.method+" "+route.target, rsp, http.StatusUnauthorized)
		responses = append(responses, rsp)
	}
	s.b.counter.AssertCount(t, "no token, whole surface", 0)
	testkit.AssertOneBodyAcross(t, "no token 401 family", responses)
	if body := responses[0].Body.String(); body != string(authz.BodyUnauthorized) {
		t.Fatalf("accept: no-token body %q, want the pinned unauthorized body", body)
	}
	s.assertNoReachAnywhere(t, "no token, whole surface", snapshot)

	// The control half, both directions: the public operation answers
	// without any credential, and an X-Org header on it is an undeclared
	// header with zero effect.
	rsp := s.do(t, http.MethodGet, publicRouteTarget, "")
	testkit.AssertStatus(t, "public control, no credential", rsp, http.StatusOK)
	rsp = s.do(t, http.MethodGet, publicRouteTarget, "", testkit.OrgNorthSlug)
	testkit.AssertStatus(t, "public control, X-Org ignored", rsp, http.StatusOK)
	entry := s.lastShimOp(t, "public control", "quotePublicViewQuote")
	if entry.Staff != nil || entry.Tenant != nil {
		t.Fatalf("accept: public entry carries scope facts: %+v", entry)
	}
	if got := s.shimOpCount("quotePublicViewQuote"); got != 2 {
		t.Fatalf("accept: public route reached %d times, want 2", got)
	}
}

// TestGate_Anon_GarbageToken is SW-2b: a bearer that is not a parseable
// JWT answers 401 identically to the no-token case on every guarded
// operation: no 500, no panic, no swallowed error surfaced as a different
// status.
func TestGate_Anon_GarbageToken(t *testing.T) {
	s := newSweep(t)
	snapshot := len(s.shimRec.Entries())
	s.resetProbes()

	garbage := s.kit.Minter.GarbageBearers(t)
	s.b.counter.Reset()
	var responses []*httptest.ResponseRecorder
	for i, bearer := range garbage {
		for _, route := range s.guardedSurface() {
			rsp := s.do(t, route.method, route.target, bearer)
			testkit.AssertStatus(t, garbageLeg(i)+" "+route.method+" "+route.target, rsp, http.StatusUnauthorized)
			responses = append(responses, rsp)
		}
	}
	s.b.counter.AssertCount(t, "garbage bearer, whole surface", 0)
	testkit.AssertOneBodyAcross(t, "garbage bearer 401 family", responses)
	if body := responses[0].Body.String(); body != string(authz.BodyUnauthorized) {
		t.Fatalf("accept: garbage-bearer body %q, want the pinned unauthorized body", body)
	}
	s.assertNoReachAnywhere(t, "garbage bearer, whole surface", snapshot)
}

// TestGate_Anon_WrongKey is SW-2c: a well-formed JWT the served JWK Set
// cannot vouch for answers 401, at every layer the acceptance line names:
// the wrong key, an unknown kid, an expired token, and the
// algorithm-confusion shape (HS256, the legacy issuers' symmetric
// algorithm). The kid-substitution leg (a valid signature naming a kid the
// served set never carried) isolates the kid check, and the rotation leg
// turns a retired kid into an unknown one. The positive controls prove the
// refusals are about the credential, not a broken verifier: the active key
// admits on both allowed algorithms, and the retired kid admits before the
// rotation.
func TestGate_Anon_WrongKey(t *testing.T) {
	s := newSweep(t)
	snapshot := len(s.shimRec.Entries())
	s.resetProbes()

	read := staffLifecycleRoutes[0].target
	invoices := "/api/v1/tenant/invoices"
	m := s.kit.Minter

	legs := []struct {
		leg    string
		method string
		target string
		token  string
		xOrg   []string
	}{
		{"wrong key", http.MethodGet, read, m.WrongKeyToken(t, testkit.StaffClass, testkit.SubInsideSales, ""), []string{testkit.OrgNorthSlug}},
		{"unknown kid", http.MethodGet, read, m.UnknownKidToken(t, testkit.StaffClass, testkit.SubInsideSales, ""), []string{testkit.OrgNorthSlug}},
		{"kid substitution", http.MethodGet, read, m.Mint(t, testkit.StaffClass, testkit.SubInsideSales, testkit.TokenShape{KID: testkit.KidNeverKnown}), []string{testkit.OrgNorthSlug}},
		{"expired staff", http.MethodGet, read, m.ExpiredToken(t, testkit.StaffClass, testkit.SubInsideSales, ""), []string{testkit.OrgNorthSlug}},
		{"alg confusion", http.MethodGet, read, m.ConfusionToken(t, testkit.StaffClass, testkit.SubInsideSales, ""), []string{testkit.OrgNorthSlug}},
		{"dual audience", http.MethodGet, read, m.DualAudienceToken(t, testkit.SubInsideSales, testkit.TenantAlphaID), []string{testkit.OrgNorthSlug}},
		{"foreign audience", http.MethodGet, read, m.ForeignAudienceToken(t, testkit.SubInsideSales), []string{testkit.OrgNorthSlug}},
		{"wrong key tenant", http.MethodGet, invoices, m.WrongKeyToken(t, testkit.TenantClass, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), nil},
		{"expired tenant", http.MethodGet, invoices, m.ExpiredToken(t, testkit.TenantClass, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), nil},
		{"tenant without tenant claim", http.MethodGet, invoices, m.TenantWithoutTenantClaim(t, testkit.SubPortalAdminAlpha), nil},
	}
	for name, tok := range m.MissingClaimTokens(t, testkit.SubInsideSales) {
		legs = append(legs, struct {
			leg    string
			method string
			target string
			token  string
			xOrg   []string
		}{"missing claim " + name, http.MethodGet, read, tok, []string{testkit.OrgNorthSlug}})
	}

	s.b.counter.Reset()
	var responses []*httptest.ResponseRecorder
	for _, leg := range legs {
		rsp := s.do(t, leg.method, leg.target, leg.token, leg.xOrg...)
		testkit.AssertStatus(t, leg.leg, rsp, http.StatusUnauthorized)
		responses = append(responses, rsp)
	}
	s.b.counter.AssertCount(t, "wrong-key family", 0)
	testkit.AssertOneBodyAcross(t, "wrong-key 401 family", responses)
	if body := responses[0].Body.String(); body != string(authz.BodyUnauthorized) {
		t.Fatalf("accept: wrong-key family body %q, want the pinned unauthorized body", body)
	}
	s.assertNoReachAnywhere(t, "wrong-key family", snapshot)

	// The rotation leg: the retired kid verifies against the rotation-start
	// set, then the set rotates and the same token is the unknown-kid case.
	retired := m.RetiredKidToken(t, testkit.StaffClass, testkit.SubInsideSales, "")
	before := s.shimOpCount("orderRead")
	rsp := s.do(t, http.MethodGet, read, retired, testkit.OrgNorthSlug)
	testkit.AssertStatus(t, "retired kid before rotation", rsp, http.StatusOK)
	if got := s.shimOpCount("orderRead"); got != before+1 {
		t.Fatalf("accept: retired kid before rotation: orderRead entries %d", got)
	}
	s.kit.Server.Rotate()
	s.kit.Clock.Advance(100 * time.Millisecond)
	rsp = s.do(t, http.MethodGet, read, retired, testkit.OrgNorthSlug)
	testkit.AssertRefusalBody(t, "retired kid after rotation", rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))
	if got := s.shimOpCount("orderRead"); got != before+1 {
		t.Fatalf("accept: retired kid after rotation: the handler recorded a reach")
	}

	// The algorithm positive controls: both allowed algorithms admit.
	for _, c := range []struct{ leg, token string }{
		{"rs256 positive", s.staffToken(t, testkit.SubInsideSales)},
		{"es256 positive", m.ECStaffToken(t, testkit.SubInsideSales)},
	} {
		before = s.shimOpCount("orderRead")
		rsp = s.do(t, http.MethodGet, read, c.token, testkit.OrgNorthSlug)
		testkit.AssertStatus(t, c.leg, rsp, http.StatusOK)
		if got := s.shimOpCount("orderRead"); got != before+1 {
			t.Fatalf("accept: leg %s: orderRead entries %d", c.leg, got)
		}
	}
}

func garbageLeg(i int) string {
	names := []string{"not a jwt", "truncated", "random bytes", "dot run"}
	if i < len(names) {
		return names[i]
	}
	return "garbage"
}
