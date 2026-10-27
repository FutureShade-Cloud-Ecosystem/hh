//go:build integration

package tax

// recon_fence_integration_test.go, FW1-10. The HST reconciliation route takes any branch_id with no
// reach check (the audit's SECURITY-SOD finding: ledger and tax reads cross branches): a
// branch-scoped manager at Kingston can read the HST reconcile for Trenton, or for the whole company
// consolidated. These legs hold the recon to the caller's branch reach, the rule the fenced GL reads
// already carry (ListJournals, DIB-392).

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
	fenceTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	fenceKingston = "5d1b3f6e-0000-4000-9000-000000000002"
)

// mountedTaxPost is mountedTax for a POST route.
func mountedTaxPost(t *testing.T, pool *pgxpool.Pool, caller identity.StaffContext, path string) int {
	t.Helper()
	svc := NewService(NewRepository(pool)).WithSalesHSTs(fenceFigures{}).WithReturnHSTs(fenceFigures{}).WithBillITCs(fenceFigures{})
	h := NewHandler(svc, slog.New(slog.DiscardHandler))

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

	r := httptest.NewRequest(http.MethodPost, path, nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec.Code
}

// mountedTax serves path through the real tax route table behind the real guards, with the caller's
// authority resolved the way the middleware resolves it (the same shape gl's read_guard harness uses).
// fenceFigures stands in for the billing, pos and purchasing document seams: the fence legs assert on
// statuses, never on figures.
type fenceFigures struct{}

func (fenceFigures) DocumentSalesHSTCents(context.Context, int, int, string) (int64, error) {
	return 0, nil
}
func (fenceFigures) ReturnHSTCents(context.Context, int, int, string) (int64, error) {
	return 0, nil
}
func (fenceFigures) VendorBillITCCents(context.Context, int, int, string) (int64, error) {
	return 0, nil
}

func mountedTax(t *testing.T, pool *pgxpool.Pool, caller identity.StaffContext, path string) int {
	t.Helper()
	svc := NewService(NewRepository(pool)).WithSalesHSTs(fenceFigures{}).WithReturnHSTs(fenceFigures{}).WithBillITCs(fenceFigures{})
	h := NewHandler(svc, slog.New(slog.DiscardHandler))

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

func fenceSeedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return id
}

// The red legs: a Kingston-only manager reads Trenton's HST reconcile, and the consolidated one,
// today.
func TestIntegration_FW1_10_HSTReconcileIsFencedToTheCallersReach(t *testing.T) {
	pool := intPool(t)
	kingston := identity.StaffContext{
		UserID: fenceSeedUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{fenceKingston: true},
	}

	if got := mountedTax(t, pool, kingston, "/api/v1/tax/recon?year=2026&month=5&branch_id="+fenceTrenton); got != http.StatusForbidden {
		t.Errorf("GET Trenton's HST reconcile as a Kingston-only manager = %d, want 403", got)
	}
	if got := mountedTax(t, pool, kingston, "/api/v1/tax/recon?year=2026&month=5"); got != http.StatusForbidden {
		t.Errorf("GET the consolidated HST reconcile as a Kingston-only manager = %d, want 403", got)
	}
}

// The positive control: the caller's own branch and a caller with every branch both serve.
func TestIntegration_FW1_10_HSTReconcileKeepsRightfulReadsServing(t *testing.T) {
	pool := intPool(t)
	kingston := identity.StaffContext{
		UserID: fenceSeedUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{fenceKingston: true},
	}
	controller := identity.StaffContext{
		UserID: fenceSeedUser(t, pool), RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}

	if got := mountedTax(t, pool, kingston, "/api/v1/tax/recon?year=2026&month=5&branch_id="+fenceKingston); got != http.StatusOK {
		t.Errorf("GET Kingston's own HST reconcile as a Kingston-only manager = %d, want 200", got)
	}
	if got := mountedTax(t, pool, controller, "/api/v1/tax/recon?year=2026&month=5&branch_id="+fenceTrenton); got != http.StatusOK {
		t.Errorf("GET Trenton's HST reconcile as the Controller = %d, want 200", got)
	}
	if got := mountedTax(t, pool, controller, "/api/v1/tax/recon?year=2026&month=5"); got != http.StatusOK {
		t.Errorf("GET the consolidated HST reconcile as the Controller = %d, want 200", got)
	}
}

// 615 round 2, P2-3: the exemption evidence (list, item, download) and the review queue hold customer
// details and masked card numbers, and their rows carry no branch of their own: they are the dealer's
// whole book, a dealer wide caller's read.
func TestIntegration_FW1_10_TaxEvidenceAndReviewQueueAreDealerWideReads(t *testing.T) {
	pool := intPool(t)
	kingston := identity.StaffContext{
		UserID: fenceSeedUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{fenceKingston: true},
	}
	controller := identity.StaffContext{
		UserID: fenceSeedUser(t, pool), RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}

	for name, path := range map[string]string{
		"evidence list": "/api/v1/tax/evidence",
		"evidence item": "/api/v1/tax/evidence/7c000000-0000-4000-8000-000000000001",
		"review queue":  "/api/v1/tax/review-queue",
	} {
		t.Run(name, func(t *testing.T) {
			if got := mountedTax(t, pool, kingston, path); got != http.StatusForbidden {
				t.Errorf("GET %s as a Kingston-only manager = %d, want 403 (the dealer's whole certificate book)", name, got)
			}
		})
	}
	// The download is a POST; the fence must answer before any existence read.
	t.Run("evidence download", func(t *testing.T) {
		if got := mountedTaxPost(t, pool, kingston, "/api/v1/tax/evidence/7c000000-0000-4000-8000-000000000001/download"); got != http.StatusForbidden {
			t.Errorf("POST the evidence download as a Kingston-only manager = %d, want 403", got)
		}
	})
	// The dealer wide caller still reads all four.
	for name, path := range map[string]string{
		"evidence list": "/api/v1/tax/evidence",
		"review queue":  "/api/v1/tax/review-queue",
	} {
		t.Run("controller "+name, func(t *testing.T) {
			if got := mountedTax(t, pool, controller, path); got != http.StatusOK {
				t.Errorf("GET %s as the Controller = %d, want 200", name, got)
			}
		})
	}
}
