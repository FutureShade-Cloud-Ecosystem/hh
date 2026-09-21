//go:build integration

// Integration test for the GL chart-of-accounts + fiscal calendar seed shipped in
// migration 0007_gl_block.sql (DIB-31). Read-only, whole-chart invariant assertions
// against the seeded world, no rows are written, so the shared seed is never perturbed.
// Run with: make test-integration.
package db

import (
	"context"
	"testing"
)

// TestInvariant_GLChartAndCalendarSeed proves the 0007 seed landed and stays coherent:
//  1. No gl_accounts row disagrees with the normal-balance doctrine (ASSET/EXPENSE→DEBIT,
//     LIABILITY/EQUITY/REVENUE→CREDIT), a whole-chart invariant, robust to later
//     migrations that ADD accounts (count-of-violations style, no hardcoded total).
//  2. Each representative 0007 base code exists with its EXACT (account_type, normal_balance),
//     spanning all five account types.
//  3. fiscal_periods for year 2026 is exactly the 12 months 1..12.
func TestInvariant_GLChartAndCalendarSeed(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	// (1) Whole-chart normal-balance doctrine: zero rows may disagree. Independently
	// derived expectation, ASSET/EXPENSE are DEBIT-normal, the other three are CREDIT-normal ,
	// asserted against every seeded row, not against itself.
	var mismatches int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM gl_accounts
		WHERE (account_type IN ('ASSET','EXPENSE')            AND normal_balance <> 'DEBIT')
		   OR (account_type IN ('LIABILITY','EQUITY','REVENUE') AND normal_balance <> 'CREDIT')`).Scan(&mismatches); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Fatalf("%d gl_accounts row(s) violate the normal-balance doctrine (ASSET/EXPENSE=DEBIT, LIABILITY/EQUITY/REVENUE=CREDIT)", mismatches)
	}

	// (2) Representative 0007 base codes, one per account_type, asserted against the
	// migration's literal (account_type, normal_balance), reconciles to 0007_gl_block.sql.
	type acct struct {
		code, wantType, wantNormal string
	}
	want := []acct{
		{"1000", "ASSET", "DEBIT"},      // Cash
		{"2100", "LIABILITY", "CREDIT"}, // Accounts Payable
		{"3900", "EQUITY", "CREDIT"},    // Retained Earnings
		{"4000", "REVENUE", "CREDIT"},   // Sales Revenue
		{"5000", "EXPENSE", "DEBIT"},    // Cost of Goods Sold
		{"9999", "EXPENSE", "DEBIT"},    // Suspense
	}
	for _, a := range want {
		var gotType, gotNormal string
		if err := pool.QueryRow(ctx,
			`SELECT account_type, normal_balance FROM gl_accounts WHERE code = $1`, a.code).Scan(&gotType, &gotNormal); err != nil {
			t.Fatalf("seeded gl_account %s missing: %v", a.code, err)
		}
		if gotType != a.wantType || gotNormal != a.wantNormal {
			t.Fatalf("gl_account %s = (%s,%s), want (%s,%s) per 0007 seed", a.code, gotType, gotNormal, a.wantType, a.wantNormal)
		}
	}

	// (3) The 2026 fiscal calendar: exactly the 12 months 1..12. array_agg(ORDER BY month)
	// reconciles to `INSERT INTO fiscal_periods SELECT 2026, generate_series(1,12)`.
	var months []int32
	if err := pool.QueryRow(ctx,
		`SELECT array_agg(month ORDER BY month) FROM fiscal_periods WHERE year = 2026`).Scan(&months); err != nil {
		t.Fatal(err)
	}
	if len(months) != 12 {
		t.Fatalf("fiscal_periods for 2026 has %d rows, want exactly 12 (months 1..12)", len(months))
	}
	for i, m := range months {
		if int(m) != i+1 {
			t.Fatalf("fiscal_periods 2026 months = %v, want [1..12]", months)
		}
	}
}
