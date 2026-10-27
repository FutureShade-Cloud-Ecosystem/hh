//go:build integration

package finstmt

// round5_integration_test.go, round 5 (S3's rounds 3 and 4 read). The red legs:
//
//   - the trial balance's 3901 line carries the wrong sign, so SUM(balance_cents) is off by exactly
//     twice the line on a year behind an unclosed one;
//   - a closed year's income shows twice: the "retained earnings to date (unclosed years)" line
//     subtracts the year's own closing, so a fully closed history reads a negative unclosed line.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ensureStmtPeriods opens the named years' twelve periods, the way the gl tests do.
func ensureStmtPeriods(t *testing.T, pool *pgxpool.Pool, years ...int) {
	t.Helper()
	ctx := context.Background()
	for _, y := range years {
		if _, err := pool.Exec(ctx,
			`INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
			 ON CONFLICT (year, month) DO NOTHING`, y); err != nil {
			t.Fatalf("ensure periods %d: %v", y, err)
		}
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=$1`, y)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE fiscal_year=$1`, y)
			_, _ = pool.Exec(bg, `DELETE FROM fiscal_periods WHERE year=$1`, y)
		})
	}
}

// The trial balance must sum to zero on a year behind an unclosed one: every row's balance is
// debit minus credit, and the 3901 computed line must carry the same sign rule.
func TestIntegration_FW1_09bR5_TrialBalanceSumsToZeroBehindAnUnclosedYear(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	const unclosed = 3091
	const reporting = 3092
	resetYear(t, pool, unclosed)
	resetYear(t, pool, reporting)
	ensureStmtPeriods(t, pool, unclosed, reporting)
	// The unclosed year earns 2,750,000 and nothing closes it.
	seedJournal(t, pool, unclosed, 6, 1, "POSTED", trenton, []jline{{code: "1100", debit: 2_750_000}, {code: "4000", credit: 2_750_000}})

	var sum int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(balance_cents),0) FROM v_trial_balance WHERE fiscal_year = $1`, reporting).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != 0 {
		t.Fatalf("FW1-09b r5 red: SUM(balance_cents) for %d is %d, want 0 (the 3901 line's sign is off by twice its amount)", reporting, sum)
	}
}

// A closed year's income reads once. Closing a year moves its income out of the "retained earnings
// to date (unclosed years)" line exactly once: the closed year's own statement, read before and
// after its closing is planted, carries the same unclosed line (the income is closed, not unclosed).
func TestIntegration_FW1_09bR5_ClosedYearIncomeReadsOnce(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const closed = 3082
	resetYear(t, pool, closed)
	ensureStmtPeriods(t, pool, closed)
	// The year earns 700, unclosed: the baseline's unclosed line holds it (with the whole seeded
	// history behind it, so the proof is the delta the closing makes).
	seedJournal(t, pool, closed, 6, 1, "POSTED", trenton, []jline{{code: "1100", debit: 700}, {code: "4000", credit: 700}})
	base, err := svc.BalanceSheetReportFor(ctx, closed, "")
	if err != nil {
		t.Fatal(err)
	}
	before := amount(base.Equity, "3901")

	// The year end: the closing journal in the year's own December, exactly what CloseYear posts.
	seedJournal(t, pool, closed, 12, 2, "POSTED", trenton, []jline{
		{code: "4000", debit: 700},
		{code: "3900", credit: 700},
	})
	after, err := svc.BalanceSheetReportFor(ctx, closed, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := amount(after.Equity, "3901"); got != before {
		t.Fatalf("FW1-09b r5 red: the unclosed retained earnings line moved from %d to %d when the year's own close planted, want it unchanged (the closed year's income is closed, and must not read again as negative unclosed)", before, got)
	}
}
