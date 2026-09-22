package finstmt

// fence_test.go, DIB-391. The statement routes mount behind requireManager, which admits a BRANCH-scoped
// Tier-2 manager. These tests drive the real handler over a nil-pool repository: a caller who passes the
// fence reaches the repository (and fails there, on the absent DB, proof the fence LET THEM THROUGH),
// while a caller the fence refuses gets a 403 with no repository touch at all. That distinction is the
// whole assertion, and it needs no database.

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	fenceTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	fenceKingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

// fenceHandler builds the handler with a nil-pool repository and a fixed caller.
func fenceHandler(caller identity.StaffContext) *Handler {
	svc := NewService(NewRepository(nil)).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return caller, true
	})
	return NewHandler(svc, slog.New(slog.DiscardHandler))
}

func branchManager(branch string) identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-mgr", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{branch: true},
	}
}

func globalController() identity.StaffContext {
	return identity.StaffContext{
		UserID: "u-fc", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}
}

// call routes a GET through the real mux the module registers (guards are identity's job and are
// deliberately pass-through here, the point is what the SERVICE does once a manager is already inside).
func call(t *testing.T, h *Handler, path string) int {
	t.Helper()
	// Guards-swap carry note (HH-3.2 fold): the fence tests drive the real
	// registration through the kit's Guards over the DB-free harness with a
	// resolved manager authority (guards are identity's job; the point is
	// what the SERVICE does once a manager is already inside).
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: 2, Scope: "global"})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec.Code
}

// fenceSurfaces is every read the fence must cover: the five statements, their comparison legs, and the
// CSV export of each, the exports run through the same service calls, so a gap would show up here.
func fenceSurfaces(branchQuery string) map[string]string {
	q := ""
	if branchQuery != "" {
		q = "&branch_id=" + branchQuery
	}
	return map[string]string{
		"income-statement":     "/api/v1/finstmt/income-statement?year=2026" + q,
		"income-statement-csv": "/api/v1/finstmt/income-statement?year=2026&format=csv" + q,
		"income-comparison":    "/api/v1/finstmt/income-statement?year=2026&compare=1" + q,
		"balance-sheet":        "/api/v1/finstmt/balance-sheet?year=2026" + q,
		"balance-sheet-csv":    "/api/v1/finstmt/balance-sheet?year=2026&format=csv" + q,
		"balance-comparison":   "/api/v1/finstmt/balance-sheet?year=2026&compare=1" + q,
		"trial-balance":        "/api/v1/finstmt/trial-balance?year=2026" + q,
		"trial-balance-csv":    "/api/v1/finstmt/trial-balance?year=2026&format=csv" + q,
		"gl-detail":            "/api/v1/finstmt/gl-detail?year=2026" + q,
		"gl-detail-csv":        "/api/v1/finstmt/gl-detail?year=2026&format=csv" + q,
		"ar-aging":             "/api/v1/finstmt/ar-aging?" + q,
		"ar-aging-csv":         "/api/v1/finstmt/ar-aging?format=csv" + q,
	}
}

// A branch-scoped manager who names NO branch is asking for the CONSOLIDATED books. That is a GLOBAL
// capability: 403, on every surface including the exports.
func TestFence_BranchManager_EmptyBranchIsForbidden(t *testing.T) {
	h := fenceHandler(branchManager(fenceTrenton))
	for name, path := range fenceSurfaces("") {
		t.Run(name, func(t *testing.T) {
			if got := call(t, h, path); got != http.StatusForbidden {
				t.Errorf("GET %s = %d, want 403 (a scoped manager may not read the consolidated books)", path, got)
			}
		})
	}
}

// The same manager naming ANOTHER branch is the horizontal-access case: 403.
func TestFence_BranchManager_OtherBranchIsForbidden(t *testing.T) {
	h := fenceHandler(branchManager(fenceTrenton))
	for name, path := range fenceSurfaces(fenceKingston) {
		if name == "trial-balance" || name == "trial-balance-csv" {
			continue // no branch dimension, covered by the empty-branch case above
		}
		t.Run(name, func(t *testing.T) {
			if got := call(t, h, path); got != http.StatusForbidden {
				t.Errorf("GET %s = %d, want 403 (cross-branch read)", path, got)
			}
		})
	}
}

// Naming their OWN branch, the manager passes the fence, and then hits the absent repository (503/500).
// Anything but 403 proves the fence let them through; asserting "not 403" is the honest claim here.
func TestFence_BranchManager_OwnBranchPassesTheFence(t *testing.T) {
	h := fenceHandler(branchManager(fenceTrenton))
	for name, path := range fenceSurfaces(fenceTrenton) {
		if name == "trial-balance" || name == "trial-balance-csv" {
			continue // GLOBAL-only by construction
		}
		t.Run(name, func(t *testing.T) {
			if got := call(t, h, path); got == http.StatusForbidden {
				t.Errorf("GET %s = 403, but the caller named their OWN branch", path)
			}
		})
	}
}

// A GLOBAL caller is unchanged on every surface, with or without a branch.
func TestFence_GlobalCaller_Unchanged(t *testing.T) {
	h := fenceHandler(globalController())
	for _, branch := range []string{"", fenceTrenton, fenceKingston} {
		for name, path := range fenceSurfaces(branch) {
			t.Run(name+"/"+branch, func(t *testing.T) {
				if got := call(t, h, path); got == http.StatusForbidden {
					t.Errorf("GET %s = 403 for a GLOBAL caller; consolidated + any branch must stay open", path)
				}
			})
		}
	}
}

// No caller at all (a route that somehow escaped its guard) is 401, never a silent consolidated read.
func TestFence_NoCallerIsUnauthorized(t *testing.T) {
	svc := NewService(NewRepository(nil)).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{}, false
	})
	h := NewHandler(svc, slog.New(slog.DiscardHandler))
	if got := call(t, h, "/api/v1/finstmt/income-statement?year=2026"); got != http.StatusUnauthorized {
		t.Errorf("unauthenticated income statement = %d, want 401", got)
	}
}
