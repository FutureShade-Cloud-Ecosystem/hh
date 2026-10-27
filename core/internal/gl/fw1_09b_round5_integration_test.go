//go:build integration

package gl

// fw1_09b_round5_integration_test.go, round 5 (S3's rounds 3 and 4 read). The red legs:
//
//   - two year end presses at once both post (no lock, no shared transaction: the once only check
//     reads before either press writes);
//   - a refused period close discloses the company's figures before the dealer wide fence runs.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// Two year end presses at once: one lock, one transaction, the second press waits and answers the
// first run's result. Today both post: 2 closing journals, and 3900 carries twice the income.
func TestIntegration_FW1_09bR5_TwoPressesAtOncePostOnce(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 3076)
	fw1ScrubAfter(t, pool, 3076)
	t.Cleanup(func() { fw1ScrubAfter(t, pool, 3076) })
	fw1SeedIncome(t, pool, 3076, 700)
	fw1CloseEveryMonth(t, pool, 3076, user)
	caller := identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	closer := svcAt(pool, caller, time.Date(3077, 1, 5, 12, 0, 0, 0, time.UTC))

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]YearCloseResult, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = closer.CloseYear(ctx, 3076, "round 5 concurrent press")
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("press %d failed: %v", i, err)
		}
	}
	n, _, _ := fw1ClosingFacts(t, pool, 3076)
	if n != 1 {
		t.Fatalf("%d closing journals after two presses at once, want 1", n)
	}
	var re int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents,0) - COALESCE(l.debit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.fiscal_year = 3076 AND j.source = 'CLOSING' AND a.code = '3900'`).Scan(&re); err != nil {
		t.Fatal(err)
	}
	if re != 700 {
		t.Fatalf("retained earnings in 3076 = %d after two concurrent presses, want 700 (one close)", re)
	}
	// The second press waited and answered the first run's result.
	if results[0].NetIncomeCents != results[1].NetIncomeCents ||
		len(results[0].ClosingJournalIDs) != len(results[1].ClosingJournalIDs) ||
		!results[0].PeriodsClosed || !results[1].PeriodsClosed {
		t.Fatalf("the two presses answered differently: %+v and %+v (the second must answer the first run's result)", results[0], results[1])
	}
}

// A refused close refuses with the fence, never with the company's figures: a Kingston scoped
// manager pressing close is answered 403 before the pre flight reads a thing.
func TestIntegration_FW1_09bR5_RefusedCloseNamesNoFigures(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2087)
	id := periodID(t, pool, 2087, 6)
	kingston := identity.StaffContext{
		UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{glKingston: true},
	}
	// The AR seam reads out of tie: the pre flight, if it ran, would name the company's receivables.
	svc := svcAt(pool, kingston, time.Date(2087, 7, 1, 12, 0, 0, 0, time.UTC)).WithARSubledger(fixedAR{v: 1_234_567_89})
	_, err := svc.ClosePeriod(ctx, id, "round 5 scoped press")
	if err == nil {
		t.Fatal("a scoped manager closed a company wide period")
	}
	if ce, ok := err.(clientError); !ok || ce.status != http.StatusForbidden {
		t.Fatalf("the refusal is %v, want the dealer wide fence's 403 before any pre flight word", err)
	}
}
