package accept

import (
	"context"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// SW-5, acceptance line 5: a tenant token cannot read another tenant's
// rows on any tenant route. Two tenants share the fixture north org (alpha
// and beta, the isolation pair) and a third anchors the south org (gamma,
// the compose leg).
//
// The F-1 cement (the B5 follow-up batch, on the tree that carries the
// Backend fix b5-fixes FIX 1): the deactivated portal principal is
// asserted refused 401 with the shared unauthorized body on every tenant
// surface the lane carries (the plain tenant class, the sync tenant leg,
// every capability route), admitted on none of them, with the
// active-principal positive control admitted on the same surface; and the
// no-row boundary the fix flagged is recorded as its own leg asserting
// what IS (admission on the plain class today), named
// deliberate-pending-Tech-Lead so any change is a visible ruling.
//
// What shim depth proves, stated honestly per the inventory's F1 note: the
// lane's tenant routes are reached-recorders, so the row-level list and
// by-id properties have no module surface until Phase C. This sweep proves
// the three properties the lane itself owns: the stamped tenant scope is
// always exactly the token's tenant claim on every tenant route; the one
// tenant-scoped read path the lane ships (the portal role resolver, keyed
// by platform user id AND tenant id) cannot see a row across the tenant
// boundary in either direction, proven against the real resolver over rows
// the fixture counts show exist; and org and tenant compose, so two orgs
// never mix. The contract's NotFound rule for cross-tenant data reads
// (404, never 200, never 403, openapi.yaml:21376-21377) binds the Phase C
// data handlers and is stated as covered there, not glossed here. The
// identical-visible-numbers variant of the compose leg is not plantable
// under the frozen schema (account_no and order_no are globally UNIQUE in
// the carried migrations 0006 and 0008), so the compose leg proves the
// (org_id, tenant_id) pair on distinct visible numbers, and the PR must
// say so (B3 review notes item 11, arbitration Q-4).
//
// Red proof (run report, mutation SW-5): the tenant predicate removed from
// PortalRoleResolver.PortalRole (the fence-rule violation the worktree
// rules name: a scoped table queried without its scope predicate). The
// cross-tenant fence legs then resolve the foreign tenant's role row and
// answer 200, failing every 403 assertion of the CrossTenantFence subtest.
func TestGate_TenantIsolation_AllTenantRoutes(t *testing.T) {
	s := newSweep(t)

	t.Run("StampedTenantIsAlwaysTheTokenTenant", func(t *testing.T) {
		// On every tenant route, the recorded scope carries exactly the
		// token's tenant and the org of the Team behind the token: alpha's
		// bearer stamps alpha, beta's stamps beta, never the other.
		for _, c := range []struct {
			leg, sub, tenant, orgID, orgSlug string
		}{
			{"tenant alpha", testkit.SubPortalAdminAlpha, testkit.TenantAlphaID, testkit.OrgNorthID, testkit.OrgNorthSlug},
			{"tenant beta", testkit.SubPortalAdminBeta, testkit.TenantBetaID, testkit.OrgNorthID, testkit.OrgNorthSlug},
		} {
			for _, route := range tenantShimRoutes {
				rsp := s.do(t, route.method, route.target, s.tenantToken(t, c.sub, c.tenant))
				testkit.AssertStatus(t, c.leg+" "+route.op, rsp, http.StatusOK)
				entry := s.lastShimOp(t, c.leg+" "+route.op, route.op)
				if entry.Tenant == nil || entry.Tenant.PlatformUserID != c.sub || entry.Tenant.TenantID != c.tenant ||
					entry.Tenant.OrgID != c.orgID || entry.Tenant.OrgSlug != c.orgSlug {
					t.Fatalf("accept: %s %s: stamped tenant facts %+v", c.leg, route.op, entry.Tenant)
				}
			}
		}
	})

	t.Run("CrossTenantFence", func(t *testing.T) {
		// The fence on the lane's tenant-scoped read path, both directions
		// and across every capability name: alpha's admin principal
		// presenting beta's tenant claim resolves no role (the alpha row is
		// unreadable under the beta predicate) and answers 403 on all eight
		// probe routes, and beta's admin against alpha answers the same.
		// Neither is ever 401 and none reaches a handler.
		for _, cap := range allCapabilities {
			rec := s.probes["cap:"+string(cap)]
			rec.Reset()
			rsp := s.do(t, http.MethodGet, capProbeTarget(cap),
				s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantBetaID))
			testkit.AssertRefusalBody(t, "fence alpha-as-beta "+string(cap), rsp, http.StatusForbidden, string(authz.BodyForbidden))
			rec.AssertNotReached(t, "fence alpha-as-beta "+string(cap))

			rec.Reset()
			rsp = s.do(t, http.MethodGet, capProbeTarget(cap),
				s.tenantToken(t, testkit.SubPortalAdminBeta, testkit.TenantAlphaID))
			testkit.AssertRefusalBody(t, "fence beta-as-alpha "+string(cap), rsp, http.StatusForbidden, string(authz.BodyForbidden))
			rec.AssertNotReached(t, "fence beta-as-alpha "+string(cap))

			// The same-tenant controls: each admin passes its own tenant on
			// every name (account_admin holds the full set).
			rec.Reset()
			rsp = s.do(t, http.MethodGet, capProbeTarget(cap),
				s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID))
			testkit.AssertStatus(t, "control alpha "+string(cap), rsp, http.StatusOK)
			rec.AssertTenantReach(t, "control alpha "+string(cap), testkit.SubPortalAdminAlpha, testkit.OrgNorthID, testkit.TenantAlphaID)
		}

		// The row-level proof at the resolver itself: the foreign row
		// exists (the fixture counts show nine portal rows) and is
		// unreadable across the tenant predicate in both directions, while
		// the own-tenant read resolves the role.
		portal, err := identity.NewPortalRoleResolver(s.b.pool)
		if err != nil {
			t.Fatalf("accept: portal resolver: %v", err)
		}
		counts, err := testkit.FixtureCounts(context.Background(), testkit.PgxPool(s.b.pool))
		if err != nil {
			t.Fatalf("accept: fixture counts: %v", err)
		}
		if counts["portal_users"] != 9 {
			t.Fatalf("accept: portal_users fixture rows %d, want 9 (the fence proof needs the rows present)", counts["portal_users"])
		}
		if _, ok, err := portal.PortalRole(context.Background(), testkit.SubPortalAdminAlpha, testkit.TenantBetaID); err != nil || ok {
			t.Fatalf("accept: alpha row read under beta tenant: ok=%v err=%v, want unreadable", ok, err)
		}
		if _, ok, err := portal.PortalRole(context.Background(), testkit.SubPortalAdminBeta, testkit.TenantAlphaID); err != nil || ok {
			t.Fatalf("accept: beta row read under alpha tenant: ok=%v err=%v, want unreadable", ok, err)
		}
		role, ok, err := portal.PortalRole(context.Background(), testkit.SubPortalAdminAlpha, testkit.TenantAlphaID)
		if err != nil || !ok || role != identity.PortalRoleAccountAdmin {
			t.Fatalf("accept: alpha row under alpha tenant: role=%q ok=%v err=%v", role, ok, err)
		}
	})

	t.Run("OrgAndTenantCompose", func(t *testing.T) {
		// Two orgs, one tenant each visible to its own bearer: gamma's
		// token stamps (south org, gamma tenant), alpha's stamps (north
		// org, alpha tenant), and the pairs never mix. The visible numbers
		// are distinct by construction (FXACC-SOUTH-1 against
		// FXACC-NORTH-1); the identical-numbers variant is not plantable
		// under the frozen UNIQUE constraints, stated here and in the
		// deliverable, never softened.
		// Gamma drives both routes first and its facts are captured before
		// alpha's calls, so the entries cannot be confused.
		rsp := s.do(t, http.MethodGet, "/api/v1/tenant/me", s.tenantToken(t, testkit.SubPortalAdminGamma, testkit.TenantGammaID))
		testkit.AssertStatus(t, "compose gamma tenantGetMe", rsp, http.StatusOK)
		gamma := s.lastShimOp(t, "compose gamma", "tenantGetMe")
		gammaProbe := s.probes["cap:"+string(identity.CapViewBilling)]
		gammaProbe.Reset()
		rsp = s.do(t, http.MethodGet, capProbeTarget(identity.CapViewBilling), s.tenantToken(t, testkit.SubPortalAdminGamma, testkit.TenantGammaID))
		testkit.AssertStatus(t, "compose gamma capability probe", rsp, http.StatusOK)
		gammaProbe.AssertTenantReach(t, "compose gamma capability probe", testkit.SubPortalAdminGamma, testkit.OrgSouthID, testkit.TenantGammaID)

		rsp = s.do(t, http.MethodGet, "/api/v1/tenant/me", s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID))
		testkit.AssertStatus(t, "compose alpha tenantGetMe", rsp, http.StatusOK)
		alpha := s.lastShimOp(t, "compose alpha", "tenantGetMe")
		alphaProbe := s.probes["cap:"+string(identity.CapViewBilling)]
		alphaProbe.Reset()
		rsp = s.do(t, http.MethodGet, capProbeTarget(identity.CapViewBilling), s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID))
		testkit.AssertStatus(t, "compose alpha capability probe", rsp, http.StatusOK)
		alphaProbe.AssertTenantReach(t, "compose alpha capability probe", testkit.SubPortalAdminAlpha, testkit.OrgNorthID, testkit.TenantAlphaID)

		if gamma.Tenant == nil || gamma.Tenant.OrgID != testkit.OrgSouthID || gamma.Tenant.TenantID != testkit.TenantGammaID {
			t.Fatalf("accept: compose gamma: stamped %+v", gamma.Tenant)
		}
		if alpha.Tenant == nil || alpha.Tenant.OrgID != testkit.OrgNorthID || alpha.Tenant.TenantID != testkit.TenantAlphaID {
			t.Fatalf("accept: compose alpha: stamped %+v", alpha.Tenant)
		}
		if gamma.Tenant.OrgID == alpha.Tenant.OrgID || gamma.Tenant.TenantID == alpha.Tenant.TenantID {
			t.Fatalf("accept: compose: the two pairs collapsed: gamma %+v alpha %+v", gamma.Tenant, alpha.Tenant)
		}
	})

	t.Run("InactivePortalAllTenantRoutes", func(t *testing.T) {
		// The F-1 cement (finding F-1, Backend fix b5-fixes FIX 1, the
		// ADR-0020 addendum pin P-2's tenant leg): the deactivated portal
		// principal (SubPortalInactiveAlpha: an account_admin row whose
		// status is inactive, otherwise a normal row, its north tenant
		// team membership normal) answers 401 with the shared unauthorized
		// body on every tenant surface the lane carries and is admitted
		// on none: the plain tenant class (me read and update, refresh),
		// the tenant leg of the sync pair, and every capability route (the
		// eight probe names plus the invoices shim). The row carries the
		// full-set account_admin role, so on the capability routes the
		// status is what refuses, never the role, and on the plain routes
		// no capability is asked at all and the status still refuses.
		// Each refusal costs exactly the two reads the decision needs (the
		// org anchor read of the tenant leg and the portal row read), so
		// the statement count is asserted exact, and no handler on the
		// surface is reached.
		snapshot := len(s.shimRec.Entries())
		s.resetProbes()
		refused := 0
		for _, c := range []struct{ method, target, leg string }{
			{http.MethodGet, "/api/v1/tenant/me", "inactive portal me read"},
			{http.MethodPut, "/api/v1/tenant/me", "inactive portal me update"},
			{http.MethodPost, "/api/v1/tenant/token/refresh", "inactive portal refresh"},
			{http.MethodGet, "/api/v1/sync/orders", "inactive portal sync pull"},
			{http.MethodPost, "/api/v1/sync/orders", "inactive portal sync push"},
			{http.MethodGet, "/api/v1/tenant/invoices", "inactive portal invoices"},
		} {
			s.b.counter.Reset()
			rsp := s.do(t, c.method, c.target, s.tenantToken(t, testkit.SubPortalInactiveAlpha, testkit.TenantAlphaID))
			testkit.AssertRefusalBody(t, c.leg, rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))
			s.b.counter.AssertCount(t, c.leg, 2)
			refused++
		}
		for _, cap := range allCapabilities {
			rec := s.probes["cap:"+string(cap)]
			rec.Reset()
			s.b.counter.Reset()
			rsp := s.do(t, http.MethodGet, capProbeTarget(cap),
				s.tenantToken(t, testkit.SubPortalInactiveAlpha, testkit.TenantAlphaID))
			testkit.AssertRefusalBody(t, "inactive portal capability "+string(cap), rsp,
				http.StatusUnauthorized, string(authz.BodyUnauthorized))
			s.b.counter.AssertCount(t, "inactive portal capability "+string(cap), 2)
			rec.AssertNotReached(t, "inactive portal capability "+string(cap))
			refused++
		}
		if refused != 6+len(allCapabilities) {
			t.Fatalf("accept: inactive portal drove %d tenant-surface legs, want %d", refused, 6+len(allCapabilities))
		}
		s.assertNoReachAnywhere(t, "inactive portal every tenant surface", snapshot)

		// The active-principal positive control on the same surface: the
		// active alpha admin is admitted on the plain class, the sync
		// tenant leg and the invoices shim (the eight probe names carry
		// their own control set in CrossTenantFence), proving the 401s
		// above are the deactivated row's status, never the routes.
		for _, c := range []struct{ method, target, op string }{
			{http.MethodGet, "/api/v1/tenant/me", "tenantGetMe"},
			{http.MethodPut, "/api/v1/tenant/me", "tenantUpdateMe"},
			{http.MethodPost, "/api/v1/tenant/token/refresh", "tenantRefreshToken"},
			{http.MethodGet, "/api/v1/sync/orders", "syncPull"},
			{http.MethodPost, "/api/v1/sync/orders", "syncPush"},
			{http.MethodGet, "/api/v1/tenant/invoices", "tenantListInvoices"},
		} {
			rsp := s.do(t, c.method, c.target, s.tenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID))
			testkit.AssertStatus(t, "active control "+c.op, rsp, http.StatusOK)
		}
	})

	t.Run("NoPortalRowAdmittedPendingRuling", func(t *testing.T) {
		// The no-row boundary the Backend fix flagged (b5-fixes deviation
		// flag 2): a tenant bearer whose subject resolves no portal_users
		// row in the token's tenant is ADMITTED on the plain tenant routes
		// today, because the status rule fires only on a resolved row that
		// is not active, and no decision pins the no-row leg. This leg
		// asserts what IS so any change is deliberate: DELIBERATE-
		// PENDING-TECH-LEAD. Flipping any 200 below to a refusal (or the
		// 403 contrast that follows) must arrive with a recorded ruling,
		// never as a silent drift of these assertions.
		s.kit.Membership.SetTenantOrgs(SubPortalAbsent, testkit.OrgNorthSlug)
		for _, c := range []struct{ method, target, op string }{
			{http.MethodGet, "/api/v1/tenant/me", "tenantGetMe"},
			{http.MethodPut, "/api/v1/tenant/me", "tenantUpdateMe"},
			{http.MethodPost, "/api/v1/tenant/token/refresh", "tenantRefreshToken"},
			{http.MethodGet, "/api/v1/sync/orders", "syncPull"},
			{http.MethodPost, "/api/v1/sync/orders", "syncPush"},
		} {
			rsp := s.do(t, c.method, c.target, s.tenantToken(t, SubPortalAbsent, testkit.TenantAlphaID))
			testkit.AssertStatus(t, "no portal row "+c.op, rsp, http.StatusOK)
		}
		// The admission stamps the token's own tenant, so it is not a
		// scope leak: the recorder carries exactly the bearer's claim.
		entry := s.lastShimOp(t, "no portal row scope", "tenantGetMe")
		if entry.Tenant == nil || entry.Tenant.PlatformUserID != SubPortalAbsent ||
			entry.Tenant.TenantID != testkit.TenantAlphaID || entry.Tenant.OrgID != testkit.OrgNorthID {
			t.Fatalf("accept: no portal row: stamped tenant facts %+v", entry.Tenant)
		}
		// The contrast half, asserted as it is: the same principal on a
		// capability route answers the RBAC-403 (no resolved role, no
		// authority), so the admitted boundary is exactly the plain class.
		rec := s.probes["cap:"+string(identity.CapViewBilling)]
		rec.Reset()
		rsp := s.do(t, http.MethodGet, capProbeTarget(identity.CapViewBilling),
			s.tenantToken(t, SubPortalAbsent, testkit.TenantAlphaID))
		testkit.AssertRefusalBody(t, "no portal row capability contrast", rsp,
			http.StatusForbidden, string(authz.BodyForbidden))
		rec.AssertNotReached(t, "no portal row capability contrast")
	})

	t.Run("TenantNegativeSpace", func(t *testing.T) {
		snapshot := len(s.shimRec.Entries())
		s.resetProbes()

		// Deactivated portal user: the plain 401 of the contract's refresh
		// rule (the tenant leg of the P-2 carve-out; accounted in SW-1;
		// the whole-tenant-surface breadth, statement exactness and the
		// active control are the InactivePortalAllTenantRoutes subtest).
		rsp := s.do(t, http.MethodGet, "/api/v1/tenant/invoices",
			s.tenantToken(t, testkit.SubPortalInactiveAlpha, testkit.TenantAlphaID))
		testkit.AssertRefusalBody(t, "deactivated portal user", rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))

		// Anonymous on a tenant route: 401, zero statements.
		s.b.counter.Reset()
		rsp = s.do(t, http.MethodGet, "/api/v1/tenant/me", "")
		testkit.AssertRefusalBody(t, "anonymous tenant route", rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))
		s.b.counter.AssertCount(t, "anonymous tenant route", 0)

		// The tenant team shape fails closed both ways: no tenant team, and
		// tenant teams in two orgs, each 403 with the shared body and zero
		// statements, and the restoration admits again.
		s.kit.Membership.SetTenantOrgs("fxportal-lost")
		s.b.counter.Reset()
		rsp = s.do(t, http.MethodGet, "/api/v1/tenant/me", s.tenantToken(t, "fxportal-lost", testkit.TenantAlphaID))
		testkit.AssertRefusalBody(t, "no tenant team", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "no tenant team", 0)

		s.kit.Membership.SetTenantOrgs(testkit.SubPortalCrewAlpha, testkit.OrgNorthSlug, testkit.OrgSouthSlug)
		s.b.counter.Reset()
		rsp = s.do(t, http.MethodGet, "/api/v1/tenant/me", s.tenantToken(t, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID))
		testkit.AssertRefusalBody(t, "tenant teams in two orgs", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		s.b.counter.AssertCount(t, "tenant teams in two orgs", 0)

		s.kit.Membership.SetTenantOrgs(testkit.SubPortalCrewAlpha, testkit.OrgNorthSlug)
		rsp = s.do(t, http.MethodGet, "/api/v1/tenant/me", s.tenantToken(t, testkit.SubPortalCrewAlpha, testkit.TenantAlphaID))
		testkit.AssertStatus(t, "tenant team restored", rsp, http.StatusOK)

		s.assertNoReachAnywhere(t, "tenant negative space", snapshot+1)
	})
}
