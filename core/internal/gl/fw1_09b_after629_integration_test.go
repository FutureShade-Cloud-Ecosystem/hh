//go:build integration

// fw1_09b_after629_integration_test.go, the merge leg for PR 613 taken after
// 629 landed (BRIEF-FW1-09b-MAIN-AFTER-629): BOTH year end rules hold together
// on one book with two trading branches and a company level line. The year end
// runs under one lock and one transaction, so two presses at once post once and
// the second answers the first run's result; and every closing line carries a
// branch, the company level lines under the head office branch (ruling 82.3).
//
// ISOLATION. Fiscal year 2113, outside the seeded 2026 periods and every band
// the package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09b_After629 ./internal/gl/
package gl

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegration_FW1_09b_After629_TwoBranchYearEndRunsOnceAndStampsEveryLine
// is the merge leg: a two branch book with one company level sale, closed by
// two presses at once. One set of closing journals lands; every closing line
// carries a branch, so the company level results close under the head office
// branch and no closing line is left with a NULL branch.
func TestIntegration_FW1_09b_After629_TwoBranchYearEndRunsOnceAndStampsEveryLine(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 2113
	ensurePeriods(t, pool, year)
	branchA, branchB := fw1TwoBranches(t, pool)
	at := time.Date(year, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)

	sell := func(branch string, amount int64, n int) {
		t.Helper()
		_, err := svc.PostExternal(ctx, ExternalJournal{
			Source: SourceInvoice, BranchID: branch,
			IdempotencyKey: fmt.Sprintf("fw109b-a629-%d", n),
			Lines: []ExternalLine{
				{AccountCode: "1000", DebitCents: amount, Memo: "bank"},
				{AccountCode: "4000", CreditCents: amount, Memo: "revenue"},
			},
		})
		if err != nil {
			t.Fatalf("post the sale for %s: %v", branch, err)
		}
	}
	sell(branchA, 30000, 1)
	sell(branchB, 20000, 2)
	// One company level sale with no branch stamp: its closing lines are the
	// company level lines, which carry the head office branch (ruling 82.3).
	sell("", 10000, 3)

	// Two presses at once: both start together, the transaction scoped advisory
	// lock on the year serializes them, and the second must answer the first
	// run's result instead of posting again.
	barrier := make(chan struct{})
	type press struct {
		res YearCloseResult
		err error
	}
	results := make([]press, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			closer := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal},
				time.Date(year+1, 1, 5, 12, 0, 0, 0, time.UTC))
			res, err := closer.CloseYear(ctx, year, "FW1-09b after 629, two presses at once")
			results[i] = press{res, err}
		}(i)
	}
	close(barrier)
	wg.Wait()
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("press %d failed: %v", i+1, r.err)
		}
	}
	if results[0].res.NetIncomeCents != 60000 {
		t.Fatalf("net income = %d, want 60000 (30000 + 20000 + the 10000 company level sale)", results[0].res.NetIncomeCents)
	}
	ids := func(r YearCloseResult) string {
		cp := append([]string(nil), r.ClosingJournalIDs...)
		sort.Strings(cp)
		return strings.Join(cp, ",")
	}
	if ids(results[0].res) == "" {
		t.Fatalf("the first press named no closing journals")
	}
	if ids(results[0].res) != ids(results[1].res) {
		t.Fatalf("the two presses answered different closing journals: %s and %s (the second must answer the first run's result)",
			ids(results[0].res), ids(results[1].res))
	}
	// Posted once: the year carries exactly the named closing journals, and the
	// 3900 legs carry the net income once (a second post would double it).
	closings := fw1ClosingJournals(t, pool, year)
	if len(closings) != len(results[0].res.ClosingJournalIDs) {
		t.Fatalf("%d closing journals in the year, the result names %d", len(closings), len(results[0].res.ClosingJournalIDs))
	}
	var net3900 int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents,0) - COALESCE(l.debit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.fiscal_year = $1 AND j.source = 'CLOSING' AND a.code = $2`, year, RetainedEarningsCode).Scan(&net3900); err != nil {
		t.Fatalf("read the 3900 closing legs: %v", err)
	}
	if net3900 != 60000 {
		t.Fatalf("the 3900 closing legs carry %d, want the year's 60000 net income exactly once", net3900)
	}
	// Every closing line carries a branch: the trading branches' lines under
	// their own stamp, the company level lines under the head office branch
	// (ruling 82.3).
	var unbranched int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		 WHERE j.fiscal_year = $1 AND j.source = 'CLOSING' AND l.branch_id IS NULL`, year).Scan(&unbranched); err != nil {
		t.Fatalf("read the closing lines: %v", err)
	}
	if unbranched != 0 {
		t.Fatalf("%d closing lines carry no branch; every closing line carries one (the company level lines the head office branch, ruling 82.3)", unbranched)
	}
}
