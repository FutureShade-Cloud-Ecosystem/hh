package accept

import (
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// SW-3, acceptance line 3: the order lifecycle route test on JWKS-signed
// test tokens, over the lane's five shim routes (orderRead, orderConfirm,
// orderMarkPicking, orderMarkReady behind requireStaff; orderCancel behind
// requireManager), each driven at the contract's path shape.
//
// At shim depth this sweep proves the delegation contract and the total
// guard ordering: authenticate (401), the X-Org shape (400), the tier gate
// (403), then the handler, with a refused call never reaching the handler
// and never moving the fixture row. The row-state legs of the ERP
// antecedent (the 409 transition matrix and the status moves on admitted
// transitions) have no surface until the order module lands in Phase C;
// they stay with the ERP gate suite re-run (SW-7), per the inventory's F1
// deep-transition note, and the deliverable states which depth was proven
// where.
//
// Red proof (run report, mutation SW-3): orderCancel's wiring swapped from
// RequireManager to RequireStaff in shims.RegisterRoutes. The tier-3
// cancel leg then answers 200 and the recorder shows a reach the gate
// should have denied: both assertions of that leg fail.
func TestGate_OrderLifecycle_RouteGuards_JWKS(t *testing.T) {
	s := newSweep(t)

	t.Run("GuardOrderingIsTotal", func(t *testing.T) {
		// On orderCancel the ordering is observable end to end: no token
		// answers 401 before the shape check, a missing X-Org answers 400
		// before membership, an under-tier principal answers 403 after
		// authority resolution, and a manager reaches the handler. The
		// pre-membership refusals execute zero statements.
		s.b.counter.Reset()
		rsp := s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, "", testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "cancel anonymous", rsp, http.StatusUnauthorized, string(authz.BodyUnauthorized))
		s.b.counter.AssertCount(t, "cancel anonymous", 0)

		s.b.counter.Reset()
		rsp = s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, testkit.SubInsideSales))
		testkit.AssertRefusalBody(t, "cancel missing X-Org", rsp, http.StatusBadRequest, bodyMissingOrgHeader)
		s.b.counter.AssertCount(t, "cancel missing X-Org", 0)

		rsp = s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, testkit.SubInsideSales), testkit.OrgNorthSlug)
		testkit.AssertRefusalBody(t, "cancel under-tier", rsp, http.StatusForbidden, string(authz.BodyForbidden))

		rsp = s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, testkit.SubSalesManager), testkit.OrgNorthSlug)
		testkit.AssertStatus(t, "cancel manager", rsp, http.StatusOK)

		if got := s.shimOpCount("orderCancel"); got != 1 {
			t.Fatalf("accept: ordering leg: orderCancel reached %d times, want exactly the admitted manager leg", got)
		}
	})

	t.Run("AnonymousOnEveryTransition", func(t *testing.T) {
		// Overlaps SW-2a on this slice on purpose: the lifecycle is the
		// acceptance line, so the anonymous leg is asserted here route by
		// route.
		snapshot := len(s.shimRec.Entries())
		s.resetProbes()
		s.b.counter.Reset()
		for _, route := range staffLifecycleRoutes {
			rsp := s.do(t, route.method, route.target, "", testkit.OrgNorthSlug)
			testkit.AssertStatus(t, "anonymous "+route.op, rsp, http.StatusUnauthorized)
		}
		s.b.counter.AssertCount(t, "anonymous lifecycle", 0)
		s.assertNoReachAnywhere(t, "anonymous lifecycle", snapshot)
	})

	t.Run("TierThreeBothHalves", func(t *testing.T) {
		// The tier-3 principal (SubInsideSales, branch scope on branch A):
		// the read and the three forward transitions pass the gate and stamp
		// the resolved facts; the cancel is refused 403 and the refusal
		// moves nothing.
		for _, route := range staffLifecycleRoutes[:4] {
			before := s.shimOpCount(route.op)
			rsp := s.do(t, route.method, route.target, s.staffToken(t, testkit.SubInsideSales), testkit.OrgNorthSlug)
			testkit.AssertStatus(t, "tier 3 "+route.op, rsp, http.StatusOK)
			if got := s.shimOpCount(route.op); got != before+1 {
				t.Fatalf("accept: tier 3 %s: entries %d, want one more than %d", route.op, got, before)
			}
			entry := s.lastShimOp(t, "tier 3 "+route.op, route.op)
			if entry.Staff == nil || entry.Staff.PlatformUserID != testkit.SubInsideSales ||
				entry.Staff.RoleID != testkit.RoleInsideSales || entry.Staff.Tier != 3 ||
				entry.Staff.Scope != "branch" || entry.Staff.OrgID != testkit.OrgNorthID {
				t.Fatalf("accept: tier 3 %s: stamped facts %+v", route.op, entry.Staff)
			}
		}

		// The refused cancel: 403, no reach, and the fixture row's status is
		// unchanged across the refusal (a refused call must not move the
		// row; at shim depth the handler never ran, and the row read pins
		// it).
		before := s.shimOpCount("orderCancel")
		s.orderStatusUnchanged(t, "tier 3 cancel refused", testkit.OrderAlphaConfID, func() {
			rsp := s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, testkit.SubInsideSales), testkit.OrgNorthSlug)
			testkit.AssertRefusalBody(t, "tier 3 cancel refused", rsp, http.StatusForbidden, string(authz.BodyForbidden))
		})
		if got := s.shimOpCount("orderCancel"); got != before {
			t.Fatalf("accept: tier 3 cancel refused: the handler recorded a reach")
		}
	})

	t.Run("ManagerCancelAdmitted", func(t *testing.T) {
		// Both manager halves: tier 2 branch (sales_manager) and tier 1
		// global (general_manager) pass the cancel gate.
		for _, c := range []struct{ leg, sub, role string }{
			{"tier 2 cancel", testkit.SubSalesManager, testkit.RoleSalesManager},
			{"tier 1 cancel", testkit.SubGeneralMgr, testkit.RoleGeneralMgr},
		} {
			before := s.shimOpCount("orderCancel")
			rsp := s.do(t, http.MethodPost, staffLifecycleRoutes[4].target, s.staffToken(t, c.sub), testkit.OrgNorthSlug)
			testkit.AssertStatus(t, c.leg, rsp, http.StatusOK)
			if got := s.shimOpCount("orderCancel"); got != before+1 {
				t.Fatalf("accept: %s: entries %d", c.leg, got)
			}
			entry := s.lastShimOp(t, c.leg, "orderCancel")
			if entry.Staff == nil || entry.Staff.RoleID != c.role {
				t.Fatalf("accept: %s: stamped facts %+v", c.leg, entry.Staff)
			}
		}
	})

	t.Run("CrossBranchCarry", func(t *testing.T) {
		// The carried branch-grant rule at shim depth: the tier-3 caller's
		// resolved authority reaches branch A (the fixture grant in
		// user_branch_grants) and not branch B (the absent row is the
		// fixture), resolved live from the database through the real
		// resolver; the global caller reaches both. The request-level
		// cross-branch refusal on real order rows is the ERP antecedent's
		// and stays with SW-7.
		s.branch.reached = 0
		rsp := s.do(t, http.MethodGet, branchProbeTarget, s.staffToken(t, testkit.SubInsideSales), testkit.OrgNorthSlug)
		testkit.AssertStatus(t, "tier 3 branch facts", rsp, http.StatusOK)
		if !s.branch.grantedA || s.branch.grantedB {
			t.Fatalf("accept: tier 3 branch facts: A=%v B=%v, want A reachable and B refused", s.branch.grantedA, s.branch.grantedB)
		}
		rsp = s.do(t, http.MethodGet, branchProbeTarget, s.staffToken(t, testkit.SubGeneralMgr), testkit.OrgNorthSlug)
		testkit.AssertStatus(t, "tier 1 branch facts", rsp, http.StatusOK)
		if !s.branch.grantedA || !s.branch.grantedB {
			t.Fatalf("accept: tier 1 branch facts: A=%v B=%v, want global scope reaching both", s.branch.grantedA, s.branch.grantedB)
		}
	})

	// The audit half the lane can prove: no audit row is ever stamped with
	// a fixture subject, on any refusal or admission above (the middleware
	// and shims write no audit rows this lane; the write half on real
	// mutating routes is SW-7's audit sweep).
	if n := s.auditRowsForFixtureSubjects(t); n != 0 {
		t.Fatalf("accept: %d audit rows stamped with fixture subjects, want 0", n)
	}
}
