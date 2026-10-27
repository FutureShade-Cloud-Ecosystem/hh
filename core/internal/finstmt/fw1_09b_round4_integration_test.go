//go:build integration

// fw1_09b_round4_integration_test.go, FW1-09b round 4's balance sheet red
// (S3's probe 4): after an unclosed year, the next year's balance sheet reads
// its carried balances against only THIS year's net income, so the books read
// $700.00 out of balance for $700.00 of income. The report carries retained
// earnings to date, the P&L of every earlier year not yet closed, as a
// computed equity line, and balances.
package finstmt

import (
	"context"
	"testing"
)

func TestIntegration_FW1_09bR4_TheBalanceSheetCarriesItsEarnings(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	const yr, next = 3091, 3092
	resetYear(t, pool, yr)
	resetYear(t, pool, next)
	t.Cleanup(func() {
		resetYear(t, pool, yr)
		resetYear(t, pool, next)
	})

	seedJournal(t, pool, yr, 6, 1, "POSTED", trenton, []jline{
		{code: "1000", debit: 700}, {code: "4000", credit: 700},
	})
	ensurePeriod(t, pool, next, 1)

	rep, err := svc.BalanceSheetReportFor(ctx, next, "")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Balanced {
		t.Fatalf("not balanced: A=%d L=%d E=%d NI=%d (the carried balances stand against this year's income only; the unclosed year's earnings must read as retained earnings to date)",
			rep.TotalAssetsCents, rep.TotalLiabilitiesCents, rep.TotalEquityCents, rep.NetIncomeCents)
	}
	if got := amount(rep.Equity, "3901"); got != 700 {
		t.Fatalf("retained earnings to date = %d, want the unclosed year's 700", got)
	}
	if rep.TotalAssetsCents != 700 {
		t.Fatalf("assets = %d, want the year's own 700", rep.TotalAssetsCents)
	}
}
