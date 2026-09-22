package gl

// read_guard_test.go, DIB-392. The five ledger READS (chart of accounts, journals, journal detail, trial
// balance, periods) were mounted behind RequireStaff, which admits ANY active login, Tier-3 front line
// and a roleless Tier-0 account included, so a yard hand could read every journal in the company. These
// tests mount the real route table behind guard doubles with identity's own semantics (manager =
// StaffContext.IsManager) and assert who gets in. They need no database: a refused caller never reaches
// the store, and an admitted one fails later, on the absent pool, which is the distinction being made.

import (
	"context"
	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	guardTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	guardKingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

// glReadRoutes is every read DIB-392 moves. Keyed by name so a failure names the surface.
var glReadRoutes = map[string]string{
	"accounts":      "/api/v1/gl/accounts",
	"journals":      "/api/v1/gl/journals",
	"journal-by-id": "/api/v1/gl/journals/11111111-1111-4111-8111-111111111111",
	"trial-balance": "/api/v1/gl/trial-balance?year=2026",
	"periods":       "/api/v1/gl/periods",
}

// mountedFor serves `path` through the real RegisterRoutes table behind
// the real Guards (the kit's DB-free double with the caller's tier set as
// the resolved authority), driving a verified bearer the guard chain
// resolves. The service's callerFrom stays the carried test stub, so the
// reads see the same caller the tier gate just admitted or refused.
func mountedFor(t *testing.T, caller identity.StaffContext, path string) int {
	t.Helper()
	svc := NewService(NewRepository(nil)).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, caller.UserID != "" })
	h := NewHandler(svc, slog.New(slog.DiscardHandler))

	sg := testkit.NewStaffGuards(t)
	if caller.RoleCode == "" || caller.Tier == 0 {
		// A roleless login resolves no authority: the RBAC-403 leg.
		sg.SetNoAuthority()
	} else {
		roleID, _ := identity.CanonicalForERPCode(caller.RoleCode)
		if caller.RoleCode == "" {
			roleID = ""
		}
		sg.SetAuthority(auth.StaffAuthority{
			RoleID:   roleID,
			Tier:     int(caller.Tier),
			Scope:    identity.CanonicalScope(caller.Scope),
			Branches: branchList(caller.Branches),
		})
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec.Code
}

// branchList flattens the vocabulary's branch map for the authority shape.
func branchList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for b, ok := range m {
		if ok {
			out = append(out, b)
		}
	}
	return out
}

func frontline(branch string) identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-yard", RoleCode: "YARD_TEAM", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{branch: true},
	}
}

// roleless is the Tier-0 account RequireStaff also admitted: an active login with no role at all.
func roleless() identity.StaffContext {
	return identity.StaffContext{UserID: "u-none", Status: identity.StatusActive}
}

func glController() identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-fc", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}
}

func TestGLReads_FrontlineStaffIsForbidden(t *testing.T) {
	for name, path := range glReadRoutes {
		t.Run(name, func(t *testing.T) {
			if got := mountedFor(t, frontline(guardTrenton), path); got != http.StatusForbidden {
				t.Errorf("GET %s as Tier-3 front line = %d, want 403 (the ledger is manager-gated)", path, got)
			}
		})
	}
}

// The journals route needs its OWN un-confusable check: a front-line caller naming their own branch would
// pass the service's branch fence, so a 403 here can only come from the ROUTE guard. Without this, a
// revert of that guard would hide behind the fence.
func TestGLJournals_FrontlineIsRefusedByTheRouteGuard(t *testing.T) {
	path := "/api/v1/gl/journals?branch_id=" + guardTrenton
	if got := mountedFor(t, frontline(guardTrenton), path); got != http.StatusForbidden {
		t.Errorf("GET %s as Tier-3 front line = %d, want 403, the journals READ is manager-gated even for the caller's OWN branch", path, got)
	}
}

func TestGLReads_RolelessTier0IsForbidden(t *testing.T) {
	for name, path := range glReadRoutes {
		t.Run(name, func(t *testing.T) {
			if got := mountedFor(t, roleless(), path); got != http.StatusForbidden {
				t.Errorf("GET %s as a roleless Tier-0 login = %d, want 403", path, got)
			}
		})
	}
}

func TestGLReads_ManagerPassesTheGuard(t *testing.T) {
	for name, path := range glReadRoutes {
		t.Run(name, func(t *testing.T) {
			if got := mountedFor(t, glController(), path); got == http.StatusForbidden {
				t.Errorf("GET %s as the Financial Controller = 403; the reads must stay open to managers", path)
			}
		})
	}
}

// The journals list carries a branch filter, so it also takes the landedcost rule: a scoped manager must
// name a branch they can reach, and an unfiltered list (every branch's ledger) is GLOBAL-only.
func TestGLJournals_BranchFilterIsFenced(t *testing.T) {
	scoped := identity.StaffContext{
		UserID: "u-mgr", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{guardTrenton: true},
	}
	if got := mountedFor(t, scoped, "/api/v1/gl/journals"); got != http.StatusForbidden {
		t.Errorf("scoped manager, no branch filter = %d, want 403 (that is the whole org's ledger)", got)
	}
	if got := mountedFor(t, scoped, "/api/v1/gl/journals?branch_id="+guardKingston); got != http.StatusForbidden {
		t.Errorf("scoped manager, other branch = %d, want 403", got)
	}
	if got := mountedFor(t, scoped, "/api/v1/gl/journals?branch_id="+guardTrenton); got == http.StatusForbidden {
		t.Error("scoped manager, OWN branch = 403; they must be able to read their own branch's journals")
	}
	if got := mountedFor(t, glController(), "/api/v1/gl/journals"); got == http.StatusForbidden {
		t.Error("GLOBAL caller, no branch filter = 403; consolidated is theirs to read")
	}
}
