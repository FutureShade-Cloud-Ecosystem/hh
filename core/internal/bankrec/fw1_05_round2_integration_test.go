//go:build integration

package bankrec

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// P1-B (S5's review of PR 632, round 2): the unmatch of a hand match must return the money through the
// clearing account the match booked, which follows the tender (item 1: 1010 card, 1020 cash and cheque),
// not always 1010. One leg per tender: match by hand, unmatch, and 1000, 1010 and 1020 are all back to
// zero across the clearing journal and its reversal.
func TestIntegration_FW1_05_R2_UnmatchRestoresEachTendersClearingAccount(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may24 := time.Date(year, 5, 24, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may24)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may24 }}).
		WithGLBalances(glSvc)

	for i, tt := range []struct {
		method   string
		payID    string
		lineDesc string
		lineRef  string
		clearing string
	}{
		{method: "CARD", payID: "87057702-0000-4000-8000-000000000001", lineDesc: "CARD DEPOSIT R2 UNMATCH", lineRef: "DEP-R2-CARD", clearing: AcctCardClearing},
		{method: "CASH", payID: "87057702-0000-4000-8000-000000000002", lineDesc: "CASH DEPOSIT R2 UNMATCH", lineRef: "DEP-R2-CASH", clearing: AcctUndeposited},
		{method: "CHEQUE", payID: "87057702-0000-4000-8000-000000000003", lineDesc: "CHEQUE DEPOSIT R2 UNMATCH", lineRef: "DEP-R2-CHQ", clearing: AcctUndeposited},
	} {
		t.Run(tt.method, func(t *testing.T) {
			const amount = int64(25000)
			insertPayment(t, pool, tt.payID, branch, tt.method, "", amount)
			lineID := fw104InsertLineAt(t, pool, acct, may24.AddDate(0, 0, i), tt.lineDesc, tt.lineRef, amount)

			m, err := svc.ManualMatch(ctx, MatchInput{
				Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: tt.payID,
				AmountCents: amount, CanAccessBranch: AllBranches,
			}, "")
			if err != nil {
				t.Fatalf("%s hand match: %v", tt.method, err)
			}
			// The hand match books DR 1000 / CR <the tender's clearing account>.
			assertJournalLegs(t, pool, m.JournalID, map[string][2]int64{
				AcctBank:         {amount, 0},
				tt.clearing:      {0, amount},
			})

			if err := svc.UnmatchReach(ctx, m.ID, "", AllBranches); err != nil {
				t.Fatalf("%s unmatch: %v", tt.method, err)
			}

			// The reversal exists under its own key and mirrors the ORIGINAL's clearing account.
			var reversalID string
			if err := pool.QueryRow(ctx,
				`SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(m.ID)).Scan(&reversalID); err != nil {
				t.Fatalf("%s reversal journal lookup: %v", tt.method, err)
			}
			assertJournalLegs(t, pool, reversalID, map[string][2]int64{
				tt.clearing: {amount, 0},
				AcctBank:    {0, amount},
			})

			// And every one of the three accounts is back to zero across clearing + reversal.
			var wrong []string
			for _, code := range []string{AcctBank, AcctCardClearing, AcctUndeposited} {
				var net int64
				if err := pool.QueryRow(ctx, `
					SELECT COALESCE(SUM(l.debit_cents),0) - COALESCE(SUM(l.credit_cents),0)
					FROM gl_journal_lines l
					JOIN gl_accounts a ON a.id = l.account_id
					JOIN gl_journals j ON j.id = l.journal_id
					WHERE j.idempotency_key IN ($1, $2) AND a.code = $3`,
					clearingKey(m.ID), unmatchKey(m.ID), code).Scan(&net); err != nil {
					t.Fatalf("%s net for %s: %v", tt.method, code, err)
				}
				if net != 0 {
					wrong = append(wrong, fmt.Sprintf("account %s legs = (net %d), want 0", code, net))
				}
			}
			if len(wrong) > 0 {
				t.Fatalf("P1-B %s: after the unmatch the clearing accounts are not restored: %v", tt.method, wrong)
			}
		})
	}
}
