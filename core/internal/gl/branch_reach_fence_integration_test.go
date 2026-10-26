//go:build integration

package gl

// branch_reach_fence_integration_test.go, FW1-10. ListJournals is fenced to the caller's branch reach
// (DIB-392, authorizeBranchRead), but the journal detail, the trial balance and the AR tie-out are not:
// a branch-scoped manager at Kingston can open a Trenton journal by id, read Trenton's trial balance,
// run Trenton's tie-out, and read the whole company consolidated, all through the API. These legs hold
// every one of those reads to the same rule: a Kingston-only caller answers Trenton as missing (a
// journal) or is refused (a figure), and a caller with every branch reads everything as before.

import (
	"context"
	"net/http"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1PlantTrentonJournal posts a small Trenton journal as the Controller and returns its id.
func fw1PlantTrentonJournal(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	controller := identity.StaffContext{
		UserID: aUser(t, pool), RoleCode: controllerRole, Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}
	svc := NewService(NewRepository(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return controller, true })
	j, err := svc.CreateJournal(context.Background(), CreateJournalInput{
		Memo:     "FW1-10 read fence proof at Trenton",
		BranchID: glTrenton,
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 15000, Memo: "cash"},
			{AccountCode: "4000", CreditCents: 15000, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("plant a Trenton journal: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id = $1`, j.ID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE id = $1`, j.ID)
	})
	return j.ID
}

func TestIntegration_FW1_10_JournalDetailAnswersOutOfReachAsMissing(t *testing.T) {
	pool := intPool(t)
	journalID := fw1PlantTrentonJournal(t, pool)

	kingston := identity.StaffContext{
		UserID: aUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{glKingston: true},
	}
	if got := liveMounted(t, pool, kingston, "/api/v1/gl/journals/"+journalID); got != http.StatusNotFound {
		t.Errorf("GET a Trenton journal as a Kingston-only manager = %d, want 404 (out of reach answers as missing)", got)
	}
}

func TestIntegration_FW1_10_TrialBalanceAndTieOutAreFencedToTheCallersReach(t *testing.T) {
	pool := intPool(t)
	fw1PlantTrentonJournal(t, pool)

	kingston := identity.StaffContext{
		UserID: aUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{glKingston: true},
	}
	if got := liveMounted(t, pool, kingston, "/api/v1/gl/trial-balance?year=2026&branch_id="+glTrenton); got != http.StatusForbidden {
		t.Errorf("GET Trenton's trial balance as a Kingston-only manager = %d, want 403", got)
	}
	if got := liveMounted(t, pool, kingston, "/api/v1/gl/tie-out?branch_id="+glTrenton); got != http.StatusForbidden {
		t.Errorf("GET Trenton's tie-out as a Kingston-only manager = %d, want 403", got)
	}
	// The consolidated figure (no branch named) is the whole company's books: not a branch-scoped
	// caller's to ask for, the same rule ListJournals already holds.
	if got := liveMounted(t, pool, kingston, "/api/v1/gl/trial-balance?year=2026"); got != http.StatusForbidden {
		t.Errorf("GET the consolidated trial balance as a Kingston-only manager = %d, want 403", got)
	}
}

// The positive control: a caller with every branch reads all of it, and a branch-scoped caller still
// reads its own branch.
func TestIntegration_FW1_10_ReachFenceKeepsRightfulReadsServing(t *testing.T) {
	pool := intPool(t)
	journalID := fw1PlantTrentonJournal(t, pool)

	controller := identity.StaffContext{
		UserID: aUser(t, pool), RoleCode: controllerRole, Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}
	kingston := identity.StaffContext{
		UserID: aUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{glKingston: true},
	}
	if got := liveMounted(t, pool, controller, "/api/v1/gl/journals/"+journalID); got != http.StatusOK {
		t.Errorf("GET the Trenton journal as the Controller = %d, want 200", got)
	}
	if got := liveMounted(t, pool, controller, "/api/v1/gl/trial-balance?year=2026"); got != http.StatusOK {
		t.Errorf("GET the consolidated trial balance as the Controller = %d, want 200", got)
	}
	if got := liveMounted(t, pool, controller, "/api/v1/gl/tie-out?branch_id="+glTrenton); got != http.StatusOK {
		t.Errorf("GET Trenton's tie-out as the Controller = %d, want 200", got)
	}
	if got := liveMounted(t, pool, kingston, "/api/v1/gl/trial-balance?year=2026&branch_id="+glKingston); got != http.StatusOK {
		t.Errorf("GET Kingston's own trial balance as a Kingston-only manager = %d, want 200", got)
	}
}
