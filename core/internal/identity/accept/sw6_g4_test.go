package accept

import (
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// SW-6, acceptance line 6: G4's middleware half, two tests never one, per
// the brief's own wording and the quality bar's template. The statuses are
// the pinned ones (ADR-0020, resolving inventory finding F2): both halves
// answer 400, the contract's BadRequest shape class, with the pinned
// per-defect bodies, zero statements, and zero reach. Each test asserts
// both directions of its conditional, so neither can pass by refusing all
// traffic.
//
// Red proofs (run report): SW-6a by mutation SW-6a (staffOrgSelection
// defaults the org when the header is missing, the fail-open branch of
// the B2-verified G4 pair), SW-6b by mutation SW-6b (serveTenant ignores
// X-Org on tenant routes). Each fails its own test and no other sweep's
// named legs.

// TestGate_G4_StaffBearer_RequiresXOrg is SW-6a: a JWKS-valid staff bearer
// without the X-Org header is refused on staff operations sampled across
// the three tier guards and the staff leg of the dual-bearer sync pair.
func TestGate_G4_StaffBearer_RequiresXOrg(t *testing.T) {
	s := newSweep(t)

	// The missing-header half across the sample. orderCancel takes the
	// tier-2 principal so the admitted end is a fair control (tier 3 would
	// fail the tier gate even with the header), and the admin probe takes
	// the tier-1 principal.
	sample := []struct {
		leg    string
		method string
		target string
		sub    string
		op     string
	}{
		{"staff read", http.MethodGet, staffLifecycleRoutes[0].target, testkit.SubInsideSales, "orderRead"},
		{"manager cancel", http.MethodPost, staffLifecycleRoutes[4].target, testkit.SubSalesManager, "orderCancel"},
		{"admin probe", http.MethodGet, adminProbeTarget, testkit.SubGeneralMgr, ""},
		{"sync pull staff leg", http.MethodGet, syncRoutes[0].target, testkit.SubInsideSales, "syncPull"},
	}
	for _, route := range sample {
		snapshot := len(s.shimRec.Entries())
		s.resetProbes()
		s.b.counter.Reset()
		rsp := s.do(t, route.method, route.target, s.staffToken(t, route.sub))
		testkit.AssertRefusalBody(t, "missing X-Org "+route.leg, rsp, http.StatusBadRequest, bodyMissingOrgHeader)
		s.b.counter.AssertCount(t, "missing X-Org "+route.leg, 0)
		s.assertNoReachAnywhere(t, "missing X-Org "+route.leg, snapshot)

		// The admitted direction of the conditional: the same bearer with
		// the header present and correct passes the gate.
		rsp = s.do(t, route.method, route.target, s.staffToken(t, route.sub), testkit.OrgNorthSlug)
		testkit.AssertStatus(t, "X-Org present "+route.leg, rsp, http.StatusOK)
	}

	// The rest of the pinned shape family on one route: a repeated header
	// and a malformed slug answer their own 400 bodies, also before any
	// platform or database call.
	s.b.counter.Reset()
	rsp := s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, testkit.SubInsideSales), testkit.OrgNorthSlug, testkit.OrgSouthSlug)
	testkit.AssertRefusalBody(t, "repeated X-Org", rsp, http.StatusBadRequest, bodyRepeatedOrgHeader)
	s.b.counter.AssertCount(t, "repeated X-Org", 0)

	s.b.counter.Reset()
	rsp = s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, testkit.SubInsideSales), "Fixture_North")
	testkit.AssertRefusalBody(t, "malformed X-Org", rsp, http.StatusBadRequest, bodyMalformedOrgHeader)
	s.b.counter.AssertCount(t, "malformed X-Org", 0)
}

// TestGate_G4_TenantBearer_RejectsXOrg is SW-6b: a JWKS-valid tenant
// bearer carrying the X-Org header is refused on tenant operations sampled
// across the plain tenant routes, a capability-gated route and the tenant
// leg of the dual-bearer sync pair. The rule is "never sent", not "must
// match", so both sub-halves refuse: the header naming the token's own org
// and the header naming a different org, plus the empty-valued shape.
func TestGate_G4_TenantBearer_RejectsXOrg(t *testing.T) {
	s := newSweep(t)

	sample := []struct {
		leg    string
		method string
		target string
	}{
		{"tenant me read", http.MethodGet, "/api/v1/tenant/me"},
		{"tenant me update", http.MethodPatch, "/api/v1/tenant/me"},
		{"tenant refresh", http.MethodPost, "/api/v1/tenant/token/refresh"},
		{"tenant invoices", http.MethodGet, "/api/v1/tenant/invoices"},
		{"capability probe", http.MethodGet, capProbeTarget(identity.CapViewBilling)},
		{"sync push tenant leg", http.MethodPost, syncRoutes[1].target},
	}
	token := s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID)
	for _, route := range sample {
		for _, c := range []struct {
			leg  string
			xOrg []string
		}{
			{"own org", []string{testkit.OrgNorthSlug}},
			{"other org", []string{testkit.OrgSouthSlug}},
			{"empty value", []string{""}},
		} {
			leg := "tenant carries X-Org " + c.leg + " " + route.leg
			snapshot := len(s.shimRec.Entries())
			s.resetProbes()
			s.b.counter.Reset()
			rsp := s.do(t, route.method, route.target, token, c.xOrg...)
			testkit.AssertRefusalBody(t, leg, rsp, http.StatusBadRequest, bodyOrgHeaderNotAllowed)
			s.b.counter.AssertCount(t, leg, 0)
			s.assertNoReachAnywhere(t, leg, snapshot)
		}

		// The admitted direction: the same bearer without the header passes
		// the gate on every sampled route.
		rsp := s.do(t, route.method, route.target, token)
		testkit.AssertStatus(t, "no X-Org "+route.leg, rsp, http.StatusOK)
	}
}
