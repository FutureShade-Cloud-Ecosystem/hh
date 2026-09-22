package gl

// bistrack_tieout_test.go, DB-free unit coverage for the M2-C1 static BisTrack GL tie-out: exact
// dollars→cents parsing (no float), natural-orientation sign handling, canonical-fixture load + self-balance,
// and a full reconcile against a fake ERP TrialBalance (zero-discrepancy, single-account perturbation, and an
// internally-balanced misclassification that only a per-account tie-out, not a grand total, can catch).

import (
	"context"
	"testing"
)

func TestParseDollarsToCents(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		bad  bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"0.00", 0, false},
		{"10000.00", 1_000_000, false},
		{"10000", 1_000_000, false},
		{"25.00", 2_500, false},
		{"25", 2_500, false},
		{"0.5", 50, false}, // one decimal place pads to 50
		{"0.05", 5, false}, // two decimal places
		{"1234.56", 123_456, false},
		{"-25.00", -2_500, false},
		{"+25.00", 2_500, false},
		{".99", 99, false},          // no whole part
		{"  12.34  ", 1_234, false}, // trimmed
		{"1.234", 0, true},          // too many decimals
		{"12,345.00", 0, true},      // thousands separator not allowed
		{"abc", 0, true},
		{"1.2a", 0, true},
		{"-", 0, true},
		// Overflow guard: oversized magnitudes must ERROR, never silently wrap / flip sign.
		{"92233720368547758.08", 0, true},                    // one over int64-max cents (would wrap to a negative)
		{"922337203685477580.99", 0, true},                   // an order of magnitude over → would wrap to a small positive
		{"92233720368547758.07", 9223372036854775807, false}, // exactly int64-max cents parses cleanly (boundary)
		{"-92233720368547758.08", 0, true},                   // negative magnitude that overflows before the sign flip
	}
	for _, c := range cases {
		got, err := parseDollarsToCents(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("parseDollarsToCents(%q) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDollarsToCents(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseDollarsToCents(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNaturalOrient(t *testing.T) {
	if got := naturalOrient(630_000, NormalDebit); got != 630_000 {
		t.Errorf("debit-normal +630000 = %d, want 630000", got)
	}
	if got := naturalOrient(-1_000_000, NormalCredit); got != 1_000_000 {
		t.Errorf("credit-normal net-debit -1000000 = %d natural, want +1000000", got)
	}
	if got := naturalOrient(250_000, NormalDebit); got != 250_000 {
		t.Errorf("debit-normal +250000 = %d, want 250000", got)
	}
}

// The canonical committed fixture must load, be flagged synthetic, and self-balance (Σdebit = Σcredit), the
// double-entry invariant that makes it a valid closing trial balance.
func TestCanonicalFixtureSelfBalances(t *testing.T) {
	snap, err := LoadBisTrackSnapshot("")
	if err != nil {
		t.Fatalf("load canonical fixture: %v", err)
	}
	if !snap.IsSynthetic {
		t.Fatal("canonical fixture must be flagged is_synthetic (it is the static fallback, not a real export)")
	}
	if snap.FiscalYear != 2026 || snap.FiscalMonth != 6 {
		t.Fatalf("canonical fixture should be FY2026-06, got %d-%02d", snap.FiscalYear, snap.FiscalMonth)
	}
	var sumND int64
	for _, r := range snap.Accounts {
		nd, err := r.netDebitCents()
		if err != nil {
			t.Fatalf("row %s: %v", r.BisTrackAccount, err)
		}
		sumND += nd
		if r.ERPAccountCode == "" {
			t.Fatalf("row %s missing erp_account_code crosswalk", r.BisTrackAccount)
		}
	}
	if sumND != 0 {
		t.Fatalf("fixture must self-balance (Σ net-debit = 0), got %d", sumND)
	}
}

// erpTBFromScenario returns the ERP TrialBalance rows the deterministic M2-C1 scenario produces, the same
// closing net-debit balances the canonical fixture carries. Used to drive BisTrackTieOut without a DB.
func erpTBFromScenario() []TrialBalanceRow {
	return []TrialBalanceRow{
		{AccountCode: "1020", AccountName: "Undeposited Funds", AccountType: "ASSET", NormalBalance: NormalDebit, BalanceCents: 497_500},
		{AccountCode: "1100", AccountName: "Accounts Receivable", AccountType: "ASSET", NormalBalance: NormalDebit, BalanceCents: 630_000},
		{AccountCode: "2100", AccountName: "Accounts Payable", AccountType: "LIABILITY", NormalBalance: NormalCredit, BalanceCents: -250_000},
		{AccountCode: "2200", AccountName: "HST Payable", AccountType: "LIABILITY", NormalBalance: NormalCredit, BalanceCents: -130_000},
		{AccountCode: "4000", AccountName: "Sales Revenue", AccountType: "REVENUE", NormalBalance: NormalCredit, BalanceCents: -1_000_000},
		{AccountCode: "6100", AccountName: "Rent", AccountType: "EXPENSE", NormalBalance: NormalDebit, BalanceCents: 250_000},
		{AccountCode: "6350", AccountName: "Cash Over/Short", AccountType: "EXPENSE", NormalBalance: NormalDebit, BalanceCents: 2_500},
	}
}

// Zero-discrepancy reconcile against the canonical fixture: every account ties, grand delta 0, ties within
// tolerance 0 (exact). Proves the harness agrees the scenario reproduces the static snapshot.
func TestBisTrackTieOut_ZeroDiscrepancy(t *testing.T) {
	f := newFakeStore()
	f.tbRows = erpTBFromScenario()
	svc := newTestService(f)

	rep, err := svc.BisTrackTieOut(context.Background(), 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if !rep.TiesWithinTol {
		t.Fatalf("expected exact tie-out, got discrepancies: %+v", rep.Discrepancies)
	}
	if rep.GrandDeltaCents != 0 || rep.TotalERPCents != 0 || rep.TotalBisTrackCents != 0 {
		t.Fatalf("balanced books+export → all net-debit totals 0, got grand=%d erp=%d bt=%d",
			rep.GrandDeltaCents, rep.TotalERPCents, rep.TotalBisTrackCents)
	}
	if len(rep.Lines) != 7 {
		t.Fatalf("expected 7 reconciled accounts, got %d", len(rep.Lines))
	}
	for _, ln := range rep.Lines {
		if ln.Source != "BOTH" || ln.DeltaCents != 0 {
			t.Fatalf("account %s: source=%s delta=%d (want BOTH/0)", ln.AccountCode, ln.Source, ln.DeltaCents)
		}
	}
	if !rep.IsSynthetic {
		t.Fatal("report must flag IsSynthetic while on the static fallback fixture")
	}
}

// Single-account perturbation: bump ERP 1100 AR by +$50.00. Exactly one account goes out of tie by +5000; the
// grand delta becomes +5000 too (this break is not internally offset). Proves the tie-out is not a tautology.
func TestBisTrackTieOut_ReportsPerturbation(t *testing.T) {
	f := newFakeStore()
	rows := erpTBFromScenario()
	for i := range rows {
		if rows[i].AccountCode == "1100" {
			rows[i].BalanceCents += 5_000 // AR overstated by $50 vs BisTrack
		}
	}
	f.tbRows = rows
	svc := newTestService(f)

	rep, err := svc.BisTrackTieOut(context.Background(), 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if rep.TiesWithinTol {
		t.Fatal("perturbed ledger must NOT tie out")
	}
	if len(rep.Discrepancies) != 1 || rep.Discrepancies[0].AccountCode != "1100" || rep.Discrepancies[0].DeltaCents != 5_000 {
		t.Fatalf("expected 1100 out of tie by +5000, got %+v", rep.Discrepancies)
	}
	if rep.GrandDeltaCents != 5_000 {
		t.Fatalf("grand delta should be +5000, got %d", rep.GrandDeltaCents)
	}
}

// A tolerance band wider than the break makes the same perturbation pass, proving tolerance is honored and
// configurable (a real GLExport with sub-cent rounding is tied out with a small material band).
func TestBisTrackTieOut_ToleranceHonored(t *testing.T) {
	f := newFakeStore()
	rows := erpTBFromScenario()
	for i := range rows {
		if rows[i].AccountCode == "1100" {
			rows[i].BalanceCents += 3 // 3-cent rounding-scale break
		}
	}
	f.tbRows = rows
	svc := newTestService(f)

	rep, err := svc.BisTrackTieOut(context.Background(), 2026, 6, "", "", 5) // 5-cent tolerance
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if !rep.TiesWithinTol {
		t.Fatalf("a 3-cent break within a 5-cent tolerance must tie; discrepancies: %+v", rep.Discrepancies)
	}
	// Same break with zero tolerance must be reported.
	rep0, err := svc.BisTrackTieOut(context.Background(), 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("tie-out tol 0: %v", err)
	}
	if rep0.TiesWithinTol {
		t.Fatal("a 3-cent break at tolerance 0 must be reported")
	}
}

// Internally-balanced misclassification: move $50 from 1100 AR into 1020 Undeposited. The GRAND delta stays 0
// (the ERP still balances internally) yet TWO accounts are out of tie, the case a grand-total-only check
// misses and the per-account tie-out catches. This is the core reason the report is per-account.
func TestBisTrackTieOut_CatchesOffsettingMisclassification(t *testing.T) {
	f := newFakeStore()
	rows := erpTBFromScenario()
	for i := range rows {
		switch rows[i].AccountCode {
		case "1100":
			rows[i].BalanceCents -= 5_000
		case "1020":
			rows[i].BalanceCents += 5_000
		}
	}
	f.tbRows = rows
	svc := newTestService(f)

	rep, err := svc.BisTrackTieOut(context.Background(), 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if rep.GrandDeltaCents != 0 {
		t.Fatalf("an internally-balanced misclassification leaves grand delta 0, got %d", rep.GrandDeltaCents)
	}
	if rep.TiesWithinTol || len(rep.Discrepancies) != 2 {
		t.Fatalf("expected 2 out-of-tie accounts despite grand delta 0, got %d: %+v", len(rep.Discrepancies), rep.Discrepancies)
	}
}

// An ERP account absent from the BisTrack snapshot (or vice-versa) surfaces as a one-sided discrepancy rather
// than being silently dropped from the reconciliation.
func TestBisTrackTieOut_OneSidedAccountSurfaces(t *testing.T) {
	f := newFakeStore()
	rows := erpTBFromScenario()
	// Add an ERP-only account (6300 Bank Fees) not present in the fixture.
	rows = append(rows, TrialBalanceRow{AccountCode: "6300", AccountName: "Bank Fees", AccountType: "EXPENSE", NormalBalance: NormalDebit, BalanceCents: 1_200})
	f.tbRows = rows
	svc := newTestService(f)

	rep, err := svc.BisTrackTieOut(context.Background(), 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	var found bool
	for _, d := range rep.Discrepancies {
		if d.AccountCode == "6300" {
			found = true
			if d.Source != "ERP_ONLY" || d.DeltaCents != 1_200 {
				t.Fatalf("6300 should be ERP_ONLY delta +1200, got %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("an ERP-only account must surface as a discrepancy, not be dropped")
	}
}

// A snapshot whose fiscal period does not match the requested one is rejected loudly (a wrong-period tie-out
// is a silent-mismatch trap the harness must not allow).
func TestBisTrackTieOut_PeriodMismatchRejected(t *testing.T) {
	f := newFakeStore()
	f.tbRows = erpTBFromScenario()
	svc := newTestService(f)
	if _, err := svc.BisTrackTieOut(context.Background(), 2025, 6, "", "", 0); err == nil {
		t.Fatal("a FY mismatch between the snapshot (2026) and the request (2025) must error")
	}
}

// REGRESSION (period-basis MAJOR): the tie-out reconciles the INCEPTION-TO-DATE closing balance, not the
// single-period MOVEMENT. Here the movement side (tbRows) understates two balance-sheet accounts by an opening
// balance carried in from a prior period, while the inception-to-date side (asOfRows) carries the full closing
// balance the BisTrack fixture expects. The tie-out must consult TrialBalanceAsOf (asOfRows) and tie exactly;
// if it regressed to period-movement it would flag 1020 and 1100 as false discrepancies.
func TestBisTrackTieOut_UsesInceptionToDateNotPeriodMovement(t *testing.T) {
	f := newFakeStore()
	// asOfRows = the correct closing balances (what a BisTrack closing TB carries), these tie the fixture.
	f.asOfRows = erpTBFromScenario()
	// tbRows = single-period MOVEMENT: 1020 and 1100 are each $20 short (their opening balances landed in a
	// PRIOR period, so the current month's movement alone is understated). If the harness used this side it
	// would manufacture two false discrepancies.
	movement := erpTBFromScenario()
	for i := range movement {
		switch movement[i].AccountCode {
		case "1020":
			movement[i].BalanceCents -= 2_000
		case "1100":
			movement[i].BalanceCents -= 2_000
		}
	}
	f.tbRows = movement
	svc := newTestService(f)

	rep, err := svc.BisTrackTieOut(context.Background(), 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	if !rep.TiesWithinTol {
		t.Fatalf("closing-balance tie-out must use inception-to-date and tie exactly; got discrepancies: %+v", rep.Discrepancies)
	}
	if rep.GrandDeltaCents != 0 {
		t.Fatalf("grand delta must be 0 on the inception-to-date basis, got %d", rep.GrandDeltaCents)
	}
	// Prove the two sides genuinely differ (otherwise the test would pass vacuously): reconciling on the
	// movement side would break. We assert the movement side is NOT what the report used by checking a line.
	for _, ln := range rep.Lines {
		if ln.AccountCode == "1100" && ln.ERPCents != 630_000 {
			t.Fatalf("1100 must reflect the inception-to-date closing balance 630000, got %d (period-movement leak)", ln.ERPCents)
		}
	}
}

// REGRESSION (month=0 endpoint guard): a closing-balance tie-out is reconciled at ONE period end, so month must
// be 1-12. month=0 (the handler's whole-year default) must be REJECTED, not silently reconciled against a
// single-period fixture.
func TestBisTrackTieOut_MonthZeroRejected(t *testing.T) {
	f := newFakeStore()
	f.tbRows = erpTBFromScenario()
	svc := newTestService(f)
	for _, m := range []int{0, -1, 13} {
		if _, err := svc.BisTrackTieOut(context.Background(), 2026, m, "", "", 0); err == nil {
			t.Fatalf("month %d must be rejected (a closing balance needs an explicit fiscal month 1-12)", m)
		}
	}
}
