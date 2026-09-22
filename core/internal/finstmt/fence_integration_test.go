//go:build integration

// fence_integration_test.go, DIB-391 against a live Postgres, so the "own branch" leg is a real 200
// (a served statement), not merely "not a 403". Everything else mirrors fence_test.go.
package finstmt

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func liveHandler(pool *pgxpool.Pool, caller identity.StaffContext) *Handler {
	svc := NewService(NewRepository(pool)).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return caller, true
	})
	return NewHandler(svc, slog.New(slog.DiscardHandler))
}

func liveCall(t *testing.T, h *Handler, path string) int {
	t.Helper()
	// Guards-swap carry note (HH-3.2 fold): the pass-through stubs became the
	// kit's real Guards with a resolved manager authority.
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

// TestIntegration_Finstmt_BranchFence is the DIB-391 acceptance: for a BRANCH-scoped manager, an empty
// branch is 403, their own branch is 200, another branch is 403, and a GLOBAL caller passes everywhere.
func TestIntegration_Finstmt_BranchFence(t *testing.T) {
	pool := intPool(t)
	scoped := liveHandler(pool, identity.StaffContext{
		UserID: "it-mgr", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{trenton: true},
	})
	global := liveHandler(pool, identity.StaffContext{
		UserID: "it-fc", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})

	// Branch-bearing surfaces, including the CSV exports (same service calls, so the same fence).
	branched := map[string]string{
		"income-statement":     "/api/v1/finstmt/income-statement?year=2026",
		"income-statement-csv": "/api/v1/finstmt/income-statement?year=2026&format=csv",
		"balance-sheet":        "/api/v1/finstmt/balance-sheet?year=2026",
		"balance-sheet-csv":    "/api/v1/finstmt/balance-sheet?year=2026&format=csv",
		"gl-detail":            "/api/v1/finstmt/gl-detail?year=2026",
		"gl-detail-csv":        "/api/v1/finstmt/gl-detail?year=2026&format=csv",
		"ar-aging":             "/api/v1/finstmt/ar-aging?",
		"ar-aging-csv":         "/api/v1/finstmt/ar-aging?format=csv",
	}
	for name, base := range branched {
		t.Run(name, func(t *testing.T) {
			if got := liveCall(t, scoped, base+"&branch_id="); got != http.StatusForbidden {
				t.Errorf("empty branch = %d, want 403", got)
			}
			if got := liveCall(t, scoped, base+"&branch_id="+trenton); got != http.StatusOK {
				t.Errorf("own branch = %d, want 200", got)
			}
			if got := liveCall(t, scoped, base+"&branch_id="+kingston); got != http.StatusForbidden {
				t.Errorf("other branch = %d, want 403", got)
			}
			for _, b := range []string{"", trenton, kingston} {
				if got := liveCall(t, global, base+"&branch_id="+b); got != http.StatusOK {
					t.Errorf("GLOBAL caller, branch %q = %d, want 200", b, got)
				}
			}
		})
	}

	// The trial balance has no branch dimension: it IS the consolidated ledger, so it is GLOBAL-only.
	t.Run("trial-balance", func(t *testing.T) {
		if got := liveCall(t, scoped, "/api/v1/finstmt/trial-balance?year=2026"); got != http.StatusForbidden {
			t.Errorf("scoped manager = %d, want 403", got)
		}
		if got := liveCall(t, global, "/api/v1/finstmt/trial-balance?year=2026"); got != http.StatusOK {
			t.Errorf("GLOBAL caller = %d, want 200", got)
		}
	})
}
