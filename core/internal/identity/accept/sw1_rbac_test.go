package accept

import (
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// SW-1, acceptance line 1: the RBAC-403 sweep on JWKS test tokens.
//
// Invariant (property, not example): the gate decision is a function of
// the principal's resolved grants, read live from staff_role and
// staff_role_grant through the real resolver over the fixture database,
// and the refusal status separates who from what: 401 means the
// credential, 403 means authenticated and refused by the gate. No route
// answers 200 to a principal whose grants are below its x-role, and no
// route refuses a principal whose grants meet it. The tenant capability
// leg (SW-1c) counts under this line per the EM's F6 ruling, recorded in
// ADR-0020.
//
// Red proof (run report, mutation SW-1): the RequireManager predicate
// swapped to always-true (the B3-verified hook, review notes item 14),
// which fails the Tiers leg exactly as the inventory's named mutation
// demands: the tier-3 principal is admitted on orderCancel and the
// refusal count drops.
func TestGate_RBAC403(t *testing.T) {
	s := newSweep(t)
	// The P-2 accounting: exactly three authenticated 401 legs of this
	// sweep exist (the carve-out, quoted verbatim in FIXTURES.md), and the
	// third spans six routes, because the deactivated portal principal
	// drives the whole tenant surface it can present (one capability route,
	// the three plain tenant routes, the sync pair's tenant leg), so the
	// accounted 401 response count is eight. Every other authenticated leg
	// asserts its own non-401 status as it runs, and the counter is
	// asserted at the end, so an unexpected 401 anywhere breaks the sweep.
	authenticated401 := 0

	t.Run("Tiers", func(t *testing.T) {
		// requireStaff: any active staff grant passes, all three tiers.
		for _, c := range []struct {
			leg, sub, role string
			tier           int
			scope          string
		}{
			{"staff tier 3", testkit.SubInsideSales, testkit.RoleInsideSales, identity.TierFrontline, identity.ScopeBranch},
			{"staff tier 2", testkit.SubSalesManager, testkit.RoleSalesManager, identity.TierManager, identity.ScopeBranch},
			{"staff tier 1", testkit.SubGeneralMgr, testkit.RoleGeneralMgr, identity.TierLeadership, identity.ScopeGlobal},
		} {
			before := s.shimOpCount("orderRead")
			rsp := s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, c.sub), testkit.OrgNorthSlug)
			testkit.AssertStatus(t, c.leg, rsp, http.StatusOK)
			if got := s.shimOpCount("orderRead"); got != before+1 {
				t.Fatalf("accept: leg %s: orderRead entries %d, want one more than %d", c.leg, got, before)
			}
			entry := s.lastShimOp(t, c.leg, "orderRead")
			if entry.Staff == nil || entry.Staff.PlatformUserID != c.sub || entry.Staff.OrgSlug != testkit.OrgNorthSlug ||
				entry.Staff.OrgID != testkit.OrgNorthID || entry.Staff.RoleID != c.role || entry.Staff.Tier != c.tier || entry.Staff.Scope != c.scope {
				t.Fatalf("accept: leg %s: stamped facts %+v", c.leg, entry.Staff)
			}
			// The staff probe route, the second requireStaff spelling.
			s.probes["staff"].Reset()
			rsp = s.do(t, http.MethodGet, staffProbeTarget, s.staffToken(t, c.sub), testkit.OrgNorthSlug)
			testkit.AssertStatus(t, c.leg+" probe", rsp, http.StatusOK)
			s.probes["staff"].AssertStaffReach(t, c.leg+" probe", c.sub, testkit.OrgNorthID)
		}

		// requireManager (orderCancel), both halves: tier 3 refused with the
		// RBAC-403, tiers 2 and 1 admitted past the gate.
		s.resetProbes()
		before := s.shimOpCount("orderCancel")
		rsp := s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, testkit.SubInsideSales), testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "manager tier 3 refused", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		if got := s.shimOpCount("orderCancel"); got != before {
			t.Fatalf("accept: manager tier 3 refused: the handler recorded a reach")
		}
		for _, c := range []struct{ leg, sub, role string }{
			{"manager tier 2 admitted", testkit.SubSalesManager, testkit.RoleSalesManager},
			{"manager tier 1 admitted", testkit.SubGeneralMgr, testkit.RoleGeneralMgr},
		} {
			rsp = s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, c.sub), testkit.OrgNorthSlug)
			testkit.AssertStatus(t, c.leg, rsp, http.StatusOK)
			entry := s.lastShimOp(t, c.leg, "orderCancel")
			if entry.Staff == nil || entry.Staff.RoleID != c.role || entry.Staff.PlatformUserID != c.sub {
				t.Fatalf("accept: leg %s: stamped facts %+v", c.leg, entry.Staff)
			}
		}

		// requireAdmin (the probe route over the shipped constructor), both
		// halves: tier 3 and tier 2 refused, tier 1 global admitted. The
		// tier-1 branch-scoped arm of IsAdmin has no fixture principal: the
		// seeded catalogue carries no tier-1 branch-scope role, so that arm
		// stays at unit depth (guards_test.go TestTierLegsBothHalves).
		for _, c := range []struct{ leg, sub string }{
			{"admin tier 3 refused", testkit.SubInsideSales},
			{"admin tier 2 refused", testkit.SubSalesManager},
		} {
			s.probes["admin"].Reset()
			rsp = s.do(t, http.MethodGet, adminProbeTarget, s.staffToken(t, c.sub), testkit.OrgNorthSlug)
			testkit.AssertRefusalBody(t, c.leg, rsp, http.StatusForbidden, string(authz.BodyForbidden))
			s.probes["admin"].AssertNotReached(t, c.leg)
		}
		s.probes["admin"].Reset()
		rsp = s.do(t, http.MethodGet, adminProbeTarget, s.staffToken(t, testkit.SubGeneralMgr), testkit.OrgNorthSlug)
		testkit.AssertStatus(t, "admin tier 1 admitted", rsp, http.StatusOK)
		s.probes["admin"].AssertStaffReach(t, "admin tier 1 admitted", testkit.SubGeneralMgr, testkit.OrgNorthID)
	})

	t.Run("CrossBearer", func(t *testing.T) {
		// SW-1b, both halves: a valid tenant bearer on a staff operation and
		// a valid staff bearer on a tenant operation each answer 403
		// (authenticated, the wrong kind), never 401, and the refusal runs
		// at the class check, before any statement and before any handler.
		snapshot := len(s.shimRec.Entries())
		s.resetProbes()

		s.b.counter.Reset()
		rsp := s.do(t, http.MethodGet, staffLifecycleRoutes[0].target,
			s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "tenant on staff read", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "tenant on staff read", 0)

		s.b.counter.Reset()
		rsp = s.do(t, http.MethodPost, staffLifecycleRoutes[4].target,
			s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID), testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "tenant on staff cancel", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "tenant on staff cancel", 0)

		s.b.counter.Reset()
		rsp = s.do(t, http.MethodGet, "/api/v1/tenant/me", s.staffToken(t, testkit.SubGeneralMgr))
		testkit.AssertRefusalBody(t, "staff on tenant me", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "staff on tenant me", 0)

		s.b.counter.Reset()
		rsp = s.do(t, http.MethodGet, "/api/v1/tenant/invoices", s.staffToken(t, testkit.SubGeneralMgr))
		testkit.AssertRefusalBody(t, "staff on tenant invoices", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "staff on tenant invoices", 0)

		s.b.counter.Reset()
		rsp = s.do(t, http.MethodGet, capProbeTarget(identity.CapViewBilling), s.staffToken(t, testkit.SubGeneralMgr))
		testkit.AssertRefusalBody(t, "staff on capability probe", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "staff on capability probe", 0)

		s.assertNoReachAnywhere(t, "cross-bearer", snapshot)
	})

	t.Run("PortalCapability", func(t *testing.T) {
		// SW-1c, both halves per capability name: the holder passes the gate,
		// the lacker answers the RBAC-403, and the absent-link principal (no
		// portal row in the tenant) answers 403 on every name.
		// view_orders_deliveries is held by every canonical role, so its
		// lacker half is the no-capability principal resolved through the
		// capability source double (FIXTURES.md's boundary note: never a
		// portal row, because no canonical role carries the empty set).
		s.kit.Membership.SetTenantOrgs(SubPortalAbsent, testkit.OrgNorthSlug)
		s.kit.Membership.SetTenantOrgs(SubNoCaps, testkit.OrgNorthSlug)
		s.portal.setRole(SubNoCaps, noCapabilityRole)
		cases := []struct {
			cap                     identity.Capability
			holderSub, holderTenant string
			lackerSub, lackerTenant string
		}{
			{identity.CapManageUsers, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID, testkit.SubPortalBuyerBeta, testkit.TenantBetaID},
			{identity.CapViewBilling, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID},
			{identity.CapPayInvoices, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID, testkit.SubPortalBuyerNoPayAlpha, testkit.TenantAlphaID},
			{identity.CapManagePaymentMethods, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID, testkit.SubPortalBuyerNoPayAlpha, testkit.TenantAlphaID},
			{identity.CapSubmitRFQ, testkit.SubPortalBuyerNoPayAlpha, testkit.TenantAlphaID, testkit.SubPortalApGamma, testkit.TenantGammaID},
			{identity.CapCreateOrders, testkit.SubPortalBuyerNoPayAlpha, testkit.TenantAlphaID, testkit.SubPortalApGamma, testkit.TenantGammaID},
			{identity.CapViewOrdersDeliveries, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID, SubNoCaps, testkit.TenantAlphaID},
			{identity.CapEditDeliveryInstructions, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID, testkit.SubPortalApGamma, testkit.TenantGammaID},
		}
		if len(cases) != len(allCapabilities) {
			t.Fatalf("accept: capability leg table covers %d names, want %d", len(cases), len(allCapabilities))
		}
		for _, c := range cases {
			name := string(c.cap)
			rec := s.probes["cap:"+name]

			rec.Reset()
			rsp := s.do(t, http.MethodGet, capProbeTarget(c.cap), s.tenantToken(t, c.holderSub, c.holderTenant))
			testkit.AssertStatus(t, "cap "+name+" holder", rsp, http.StatusOK)
			rec.AssertTenantReach(t, "cap "+name+" holder", c.holderSub, s.orgOf(c.holderTenant), c.holderTenant)

			if c.lackerSub != "" {
				rec.Reset()
				rsp = s.do(t, http.MethodGet, capProbeTarget(c.cap), s.tenantToken(t, c.lackerSub, c.lackerTenant))
				testkit.AssertRefusalBody(t, "cap "+name+" lacker", rsp, http.StatusForbidden, string(authz.BodyForbidden))
				rec.AssertNotReached(t, "cap "+name+" lacker")
			}

			rec.Reset()
			rsp = s.do(t, http.MethodGet, capProbeTarget(c.cap), s.tenantToken(t, SubPortalAbsent, testkit.TenantAlphaID))
			testkit.AssertRefusalBody(t, "cap "+name+" absent link", rsp, http.StatusForbidden, string(authz.BodyForbidden))
			rec.AssertNotReached(t, "cap "+name+" absent link")
		}
	})

	t.Run("NegativeSpace", func(t *testing.T) {
		snapshot := len(s.shimRec.Entries())
		s.resetProbes()

		// Wrong org: the X-Org names a real org with no membership for the
		// principal; refused 403 at org resolution, before tier logic, with
		// zero statements (the full wrong-org property is SW-4).
		s.b.counter.Reset()
		rsp := s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, testkit.SubInsideSales), testkit.OrgSouthSlug)
		testkit.AssertRefusalBody(t, "wrong org", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "wrong org", 0)

		// Revoked grant: the tombstoned sales_manager grant collapses the
		// principal to no authority; the manager route and the staff route
		// both refuse 403.
		for _, c := range []struct{ leg, method, target string }{
			{"revoked grant on manager route", http.MethodPost, staffLifecycleRoutes[4].target},
			{"revoked grant on staff route", http.MethodGet, staffLifecycleRoutes[0].target},
		} {
			rsp = s.do(t, c.method, c.target, s.staffToken(t, testkit.SubRevoked), testkit.OrgNorthSlug)
			testkit.AssertRefusalBody(t, c.leg, rsp, http.StatusForbidden, string(authz.BodyForbidden))
		}

		// SubInert: the branch-id rule violation (a branch-scoped grant with
		// a NULL branch_id) is a provisioning data defect, answered with the
		// 503 family and the shared unavailable body, never an inert grant
		// (arbitration Q-1, pin P-1).
		rsp = s.do(t, http.MethodGet, staffLifecycleRoutes[0].target, s.staffToken(t, testkit.SubInert), testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "inert grant data defect", rsp, http.StatusServiceUnavailable, string(authz.BodyUnavailable))

		// The P-2 carve-out, the three authentication-lifetime legs, each
		// 401 with the shared unauthorized body.
		//
		// Leg one: a staff token whose self-declared window exceeds the
		// ceiling resolved for its role (the standard 12h staff session for
		// a non-driver role).
		iat := s.kit.Clock.Now().Add(-12*time.Hour - 30*time.Minute)
		exp := s.kit.Clock.Now().Add(30 * time.Minute)
		rsp = s.do(t, http.MethodGet, staffLifecycleRoutes[0].target,
			s.mintWindowedStaff(t, testkit.SubInsideSales, iat, exp), testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "over-ceiling window", rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))
		authenticated401++
		// Control: a within-ceiling explicit window is admitted, and the
		// handler saw exactly the minted window.
		iatIn := s.kit.Clock.Now().Add(-20 * time.Minute)
		expIn := s.kit.Clock.Now().Add(10 * time.Minute)
		s.probes["staff"].Reset()
		rsp = s.do(t, http.MethodGet, staffProbeTarget, s.mintWindowedStaff(t, testkit.SubInsideSales, iatIn, expIn), testkit.OrgNorthSlug)
		testkit.AssertStatus(t, "within-ceiling control", rsp, http.StatusOK)
		s.assertStampedWindow(t, "within-ceiling control", iatIn, expIn)

		// Leg two: a staff bearer whose linked users row is not ACTIVE
		// (SubSuspended: the grant is active, so the status decides, never
		// the grant; driven on the manager route, where the grant is
		// tier 1, so the status refusal is seen to outrank the tier gate).
		rsp = s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, testkit.SubSuspended), testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "suspended staff on manager route", rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))
		authenticated401++

		// Leg three: a tenant bearer whose portal_users row is not active
		// (SubPortalInactiveAlpha carries the account_admin role, so the
		// status decides, never the capability), driven across the whole
		// tenant surface the lane carries: the capability route (invoices,
		// the original leg), the plain tenant class (me read and update,
		// refresh) and the tenant leg of the sync pair. This is the F-1
		// cement: before the Backend fix (b5-fixes, FIX 1) the 401 fired
		// only where a capability was asked and the plain class admitted;
		// with the fix every tenant route the deactivated portal bearer
		// presents answers the carve-out's 401 with the shared body, so
		// each driven route increments the account below.
		for _, c := range []struct{ method, target, leg string }{
			{http.MethodGet, "/api/v1/tenant/invoices", "inactive portal user invoices"},
			{http.MethodGet, "/api/v1/tenant/me", "inactive portal user me read"},
			{http.MethodPatch, "/api/v1/tenant/me", "inactive portal user me update"},
			{http.MethodPost, "/api/v1/tenant/token/refresh", "inactive portal user refresh"},
			{http.MethodGet, "/api/v1/sync/orders", "inactive portal user sync pull"},
			{http.MethodPost, "/api/v1/sync/orders", "inactive portal user sync push"},
		} {
			rsp := s.do(t, c.method, c.target, s.tenantToken(t, testkit.SubPortalInactiveAlpha, testkit.TenantAlphaID))
			testkit.AssertRefusalBody(t, c.leg, rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))
			authenticated401++
		}
		// Control: the active admin principal passes the same gate.
		s.probes["cap:"+string(identity.CapViewBilling)].Reset()
		rsp = s.do(t, http.MethodGet, capProbeTarget(identity.CapViewBilling),
			s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID))
		testkit.AssertStatus(t, "active portal control", rsp, http.StatusOK)

		// The two control legs above admitted on probe routes (their reaches
		// were asserted at the legs); clear them so the final assertion
		// covers exactly the refusal legs of this subtest.
		s.resetProbes()
		s.assertNoReachAnywhere(t, "negative space refusals", snapshot)
	})

	// The status separation of the sweep's own invariant: across every
	// authenticated leg above, the only 401 answers are the carve-out's
	// (the over-ceiling window, the suspended staff row, and the six
	// tenant-surface routes of the deactivated portal principal). Every
	// leg asserted its status as it ran, so the count can be exactly
	// eight only when nothing else answered 401.
	if authenticated401 != 8 {
		t.Fatalf("accept: SW-1 answered %d authenticated 401 legs, want exactly the 8 of the P-2 carve-out", authenticated401)
	}
}

// orgOf maps the fixture tenant to its fixture org id, for reach
// assertions on tenant legs.
func (s *sweep) orgOf(tenantID string) string {
	switch tenantID {
	case testkit.TenantAlphaID, testkit.TenantBetaID:
		return testkit.OrgNorthID
	case testkit.TenantGammaID:
		return testkit.OrgSouthID
	}
	return ""
}
