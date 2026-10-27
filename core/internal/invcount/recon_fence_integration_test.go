//go:build integration

package invcount

// recon_fence_integration_test.go, FW1-10. The inventory tie-out route takes any branch_id with no
// reach check (the audit's LEDGER-TIEOUT finding): a branch-scoped manager at Kingston can run and
// read Trenton's count reconciliation. These legs hold the recon to the caller's branch reach, the
// same DIB-390 rule every count-sheet transition already carries.

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

const fenceKingston = "5d1b3f6e-0000-4000-9000-000000000002"

// mountedCounts serves path through the real count route table behind the real guards, with the
// caller's authority resolved the way the middleware resolves it (the same shape gl's read_guard
// harness uses).
func mountedCounts(t *testing.T, pool *pgxpool.Pool, caller identity.StaffContext, path string) int {
	t.Helper()
	svc := NewService(NewRepository(pool)).WithStockCounter(&fakeStockSeam{})
	// The linker the composition root supplies in production: the harness handler stamps the acting
	// caller's ERP users.id onto the request, the way the real wire does.
	h := NewHandler(svc, slog.New(slog.DiscardHandler)).
		WithUserLinker(func(context.Context, string) (string, bool) { return caller.UserID, true })

	sg := testkit.NewStaffGuards(t)
	roleID, _ := identity.CanonicalForERPCode(caller.RoleCode)
	sg.SetAuthority(auth.StaffAuthority{
		RoleID:   roleID,
		Tier:     int(caller.Tier),
		Scope:    identity.CanonicalScope(caller.Scope),
		Branches: fenceBranchList(caller.Branches),
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec.Code
}

func fenceBranchList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for b, ok := range m {
		if ok {
			out = append(out, b)
		}
	}
	return out
}

func fenceKingstonManager(t *testing.T, pool *pgxpool.Pool) identity.StaffContext {
	t.Helper()
	return identity.StaffContext{
		UserID: aSeedUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{fenceKingston: true},
	}
}

func aSeedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return id
}

// The red leg: a Kingston-only manager runs Trenton's inventory tie-out today.
func TestIntegration_FW1_10_InventoryTieOutIsFencedToTheCallersReach(t *testing.T) {
	pool := intPool(t)
	kingston := fenceKingstonManager(t, pool)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM inventory_recon_runs WHERE fiscal_year=2026 AND month=4`)
	})

	path := "/api/v1/counts/recon?branch_id=" + branchTRT + "&year=2026&month=4"
	if got := mountedCounts(t, pool, kingston, path); got != http.StatusForbidden {
		t.Errorf("GET Trenton's inventory tie-out as a Kingston-only manager = %d, want 403", got)
	}
	// The tie-out needs a branch (there is no consolidated count); a missing branch is refused, not
	// run consolidated.
	if got := mountedCounts(t, pool, kingston, "/api/v1/counts/recon?year=2026&month=4"); got != http.StatusForbidden {
		t.Errorf("GET the inventory tie-out with no branch as a Kingston-only manager = %d, want 403", got)
	}
}

// The positive control: the caller's own branch and a caller with every branch both serve.
func TestIntegration_FW1_10_InventoryTieOutKeepsRightfulReadsServing(t *testing.T) {
	pool := intPool(t)
	kingston := fenceKingstonManager(t, pool)
	controller := identity.StaffContext{
		UserID: aSeedUser(t, pool), RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM inventory_recon_runs WHERE fiscal_year=2026 AND month=4`)
	})

	if got := mountedCounts(t, pool, kingston, "/api/v1/counts/recon?branch_id="+fenceKingston+"&year=2026&month=4"); got != http.StatusOK {
		t.Errorf("GET Kingston's own inventory tie-out as a Kingston-only manager = %d, want 200", got)
	}
	if got := mountedCounts(t, pool, controller, "/api/v1/counts/recon?branch_id="+branchTRT+"&year=2026&month=4"); got != http.StatusOK {
		t.Errorf("GET Trenton's inventory tie-out as the Controller = %d, want 200", got)
	}
}
