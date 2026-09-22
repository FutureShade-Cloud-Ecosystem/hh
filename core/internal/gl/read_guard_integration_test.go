//go:build integration

// read_guard_integration_test.go, DIB-392 against a live Postgres, so the manager leg is a real 200
// (a served ledger read), not merely "not a 403".
package gl

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	glTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	glKingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

func liveMounted(t *testing.T, pool *pgxpool.Pool, caller identity.StaffContext, path string) int {
	t.Helper()
	svc := NewService(NewRepository(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, caller.UserID != "" })
	h := NewHandler(svc, slog.New(slog.DiscardHandler))

	sg := testkit.NewStaffGuards(t)
	if caller.RoleCode == "" || caller.Tier == 0 {
		sg.SetNoAuthority()
	} else {
		roleID, _ := identity.CanonicalForERPCode(caller.RoleCode)
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

// TestIntegration_GL_ReadsAreManagerGated is the DIB-392 acceptance: a staff-tier caller is refused on
// the ledger reads; a manager is served.
func TestIntegration_GL_ReadsAreManagerGated(t *testing.T) {
	pool := intPool(t)
	staff := identity.StaffContext{
		UserID: "it-yard", RoleCode: "YARD_TEAM", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{glTrenton: true},
	}
	controller := identity.StaffContext{
		UserID: "it-fc", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}

	for name, path := range map[string]string{
		"accounts":      "/api/v1/gl/accounts",
		"journals":      "/api/v1/gl/journals",
		"trial-balance": "/api/v1/gl/trial-balance?year=2026",
		"periods":       "/api/v1/gl/periods",
	} {
		t.Run(name, func(t *testing.T) {
			if got := liveMounted(t, pool, staff, path); got != http.StatusForbidden {
				t.Errorf("staff-tier GET %s = %d, want 403", path, got)
			}
			if got := liveMounted(t, pool, controller, path); got != http.StatusOK {
				t.Errorf("manager GET %s = %d, want 200", path, got)
			}
		})
	}

	// Un-confusable route-guard check: a front-line caller naming their OWN branch clears the service's
	// branch fence, so this 403 can only be the route guard.
	t.Run("journals-own-branch-still-manager-gated", func(t *testing.T) {
		if got := liveMounted(t, pool, staff, "/api/v1/gl/journals?branch_id="+glTrenton); got != http.StatusForbidden {
			t.Errorf("staff-tier GET journals for their OWN branch = %d, want 403", got)
		}
	})

	// The journals branch filter takes the landedcost rule on top of the manager gate.
	t.Run("journals-branch-fence", func(t *testing.T) {
		scoped := identity.StaffContext{
			UserID: "it-mgr", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
			Status: identity.StatusActive, Branches: map[string]bool{glTrenton: true},
		}
		if got := liveMounted(t, pool, scoped, "/api/v1/gl/journals"); got != http.StatusForbidden {
			t.Errorf("scoped manager, unfiltered journals = %d, want 403", got)
		}
		if got := liveMounted(t, pool, scoped, "/api/v1/gl/journals?branch_id="+glKingston); got != http.StatusForbidden {
			t.Errorf("scoped manager, other branch = %d, want 403", got)
		}
		if got := liveMounted(t, pool, scoped, "/api/v1/gl/journals?branch_id="+glTrenton); got != http.StatusOK {
			t.Errorf("scoped manager, own branch = %d, want 200", got)
		}
	})
}
