package accept

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// SW-4, acceptance line 4: a JWKS-valid staff bearer with a wrong X-Org is
// refused before any repository access, proven in the pgx tracer form
// (B3 review notes item 10): the org anchor resolver is the concrete
// PgxOrgResolver over the counting pool, so the statement counts below are
// measurements, not assertions about doubles. The zero-statement property
// binds every refusal except the provisioning gap, which performs exactly
// one anchor read (the ADR-0020 addendum's bounded exception, ruling
// B2-10).
//
// Route surface: one route per staff guard class (orderRead for
// requireStaff, orderCancel for requireManager, the admin probe for
// requireAdmin) plus the staff leg of the dual-bearer sync pair. The
// staff-side tenants/brand pair has no shim route this lane; it carries
// the same staff guard class and lands with its Phase C module (the
// inventory's F1 surface ruling).
//
// Red proof (run report, mutation SW-4): the Team-membership validation
// skipped in serveStaff (the B2-verified middleware-ordering hook). The
// wrong-org legs then execute statements and the two-org principal is
// admitted on the wrong org: the zero-statement assertions and the status
// assertions fail together.
func TestGate_WrongOrg_NeverReachesRepository(t *testing.T) {
	s := newSweep(t)

	// refuse drives one wrong-org call and asserts the full refusal shape:
	// the status, the byte-exact family body, exactly wantStatements
	// executed, and no handler reached anywhere. It returns the response so
	// the family-level one-body assertion can compare across legs.
	refuse := func(leg, method, target, bearer string, wantStatus, wantStatements int, body string, xOrg ...string) *httptest.ResponseRecorder {
		t.Helper()
		snapshot := len(s.shimRec.Entries())
		s.resetProbes()
		s.b.counter.Reset()
		rsp := s.do(t, method, target, bearer, xOrg...)
		testkit.AssertRefusalBody(t, leg, rsp, wantStatus, body)
		s.b.counter.AssertCount(t, leg, wantStatements)
		s.assertNoReachAnywhere(t, leg, snapshot)
		return rsp
	}

	staffSlice := []struct {
		leg    string
		method string
		target string
	}{
		{"staff read", http.MethodGet, staffLifecycleRoutes[0].target},
		{"manager cancel", http.MethodPost, staffLifecycleRoutes[4].target},
		{"admin probe", http.MethodGet, adminProbeTarget},
		{"sync pull staff leg", http.MethodGet, syncRoutes[0].target},
	}

	// Both wrong-org shapes across the slice: a real org the caller has no
	// membership in, and a well-formed slug no org row carries. One body
	// across both (the directory-oracle rule: unknown slug and absent
	// membership are indistinguishable), zero statements per refusal.
	var refusals []*httptest.ResponseRecorder
	for _, route := range staffSlice {
		for _, c := range []struct{ leg, slug string }{
			{"known slug, no membership", testkit.OrgSouthSlug},
			{"unknown slug", "fixture-nowhere"},
		} {
			refusals = append(refusals, refuse(route.leg+": "+c.leg, route.method, route.target,
				s.staffToken(t, testkit.SubInsideSales), http.StatusForbidden, 0, string(authz.BodyForbidden), c.slug))
		}
	}
	testkit.AssertOneBodyAcross(t, "wrong-org 403 family", refusals)

	// The provisioning gap, its own leg with its own expected count:
	// membership passes, the anchor row is missing, exactly one anchor read
	// executes, then 403 with the same body.
	s.kit.Membership.SetMember(testkit.SubInsideSales, "fixture-gap-org", true)
	refuse("provisioning gap", http.MethodGet, staffLifecycleRoutes[0].target,
		s.staffToken(t, testkit.SubInsideSales), http.StatusForbidden, 1, string(authz.BodyForbidden), "fixture-gap-org")

	// The realistic wrong-org case: the two-org principal holds grants in
	// both orgs but Team membership in the north only, so presenting the
	// south org is refused with zero statements, and presenting the north
	// org is admitted (the positive control: the sweep cannot pass by
	// refusing everything).
	refuse("two-org principal, wrong one of two", http.MethodGet, staffLifecycleRoutes[0].target,
		s.staffToken(t, testkit.SubTwoOrg), http.StatusForbidden, 0, string(authz.BodyForbidden), testkit.OrgSouthSlug)

	before := s.shimOpCount("orderRead")
	rsp := s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, testkit.SubTwoOrg), testkit.OrgNorthSlug)
	testkit.AssertStatus(t, "two-org principal, granted org", rsp, http.StatusOK)
	if got := s.shimOpCount("orderRead"); got != before+1 {
		t.Fatalf("accept: two-org positive control: orderRead entries %d", got)
	}
	entry := s.lastShimOp(t, "two-org positive control", "orderRead")
	if entry.Staff == nil || entry.Staff.PlatformUserID != testkit.SubTwoOrg || entry.Staff.OrgID != testkit.OrgNorthID {
		t.Fatalf("accept: two-org positive control: stamped facts %+v", entry.Staff)
	}

	// Membership revoked between calls (D28): the double flips and the very
	// next call refuses, then the restoration admits again. Per-request
	// resolution, both directions.
	before = s.shimOpCount("orderRead")
	rsp = s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, testkit.SubGeneralMgr), testkit.OrgNorthSlug)
	testkit.AssertStatus(t, "before revocation", rsp, http.StatusOK)
	s.kit.Membership.SetMember(testkit.SubGeneralMgr, testkit.OrgNorthSlug, false)
	refuse("membership revoked between calls", http.MethodGet, staffLifecycleRoutes[0].target,
		s.staffToken(t, testkit.SubGeneralMgr), http.StatusForbidden, 0, string(authz.BodyForbidden), testkit.OrgNorthSlug)
	s.kit.Membership.SetMember(testkit.SubGeneralMgr, testkit.OrgNorthSlug, true)
	rsp = s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, testkit.SubGeneralMgr), testkit.OrgNorthSlug)
	testkit.AssertStatus(t, "membership restored", rsp, http.StatusOK)
	if got := s.shimOpCount("orderRead"); got != before+2 {
		t.Fatalf("accept: revocation pair: orderRead entries %d, want the two admitted ends", got)
	}

	// The shape legs of the org-selection family, refused before any
	// platform or database call: a slug failing the pinned shape and a
	// repeated header, each 400 with its pinned body and zero statements.
	refuse("malformed slug", http.MethodGet, staffLifecycleRoutes[0].target,
		s.staffToken(t, testkit.SubInsideSales), http.StatusBadRequest, 0, bodyMalformedOrgHeader, "Fixture_North")
	refuse("repeated header", http.MethodGet, staffLifecycleRoutes[0].target,
		s.staffToken(t, testkit.SubInsideSales), http.StatusBadRequest, 0, bodyRepeatedOrgHeader, testkit.OrgNorthSlug, testkit.OrgSouthSlug)
}
