package finstmt

// Unit tests, pure statement-composition logic with in-memory fake rows (no DB). They cover the A-04
// AC logic: branch-sum == consolidated (composeIncome/composeBalance additivity), the balance-sheet
// invariant incl. loan/lease liabilities (AC2), comparatives incl. empty history (AC4), drill-down tie
// (AC5), aging buckets + grand total (AC6), CSV/Excel parity (AC7), and schedule validation (AC9). The
// DB path (POSTED-only views, real branch-sum, real subledger tie) is exercised by integration_test.go.

import (
	"encoding/csv"
	"strings"
	"testing"
)

// -- AC2 balance-sheet invariant incl. 2500/2510/2520 loans & leases ----------

func TestComposeBalance_InvariantWithLoansAndLeases(t *testing.T) {
	rows := []StatementRow{
		{AccountCode: "1000", AccountName: "Cash", AccountType: "ASSET", AmountCents: 500_000},
		{AccountCode: "1100", AccountName: "AR", AccountType: "ASSET", AmountCents: 300_000},
		{AccountCode: "2500", AccountName: "RBC LOC", AccountType: "LIABILITY", AmountCents: 200_000},
		{AccountCode: "2510", AccountName: "Equipment Loans", AccountType: "LIABILITY", AmountCents: 150_000},
		{AccountCode: "2520", AccountName: "Lease Obligations", AccountType: "LIABILITY", AmountCents: 100_000},
		{AccountCode: "3000", AccountName: "Owner Equity", AccountType: "EQUITY", AmountCents: 250_000},
	}
	// assets 800_000 = liabilities 450_000 + equity 250_000 + net income 100_000
	rep := composeBalance(2026, "", rows, 100_000)
	if rep.TotalAssetsCents != 800_000 {
		t.Fatalf("assets = %d, want 800000", rep.TotalAssetsCents)
	}
	if rep.TotalLiabilitiesCents != 450_000 {
		t.Fatalf("liabilities = %d, want 450000 (must include 2500/2510/2520)", rep.TotalLiabilitiesCents)
	}
	if !rep.Balanced {
		t.Fatalf("balance sheet not balanced: A=%d L=%d E=%d NI=%d",
			rep.TotalAssetsCents, rep.TotalLiabilitiesCents, rep.TotalEquityCents, rep.NetIncomeCents)
	}
	// prove the loan/lease accounts are actually present in the liabilities section
	got := map[string]bool{}
	for _, r := range rep.Liabilities {
		got[r.AccountCode] = true
	}
	for _, code := range []string{"2500", "2510", "2520"} {
		if !got[code] {
			t.Errorf("liability account %s missing from balance sheet", code)
		}
	}
}

func TestComposeBalance_ImbalanceDetected(t *testing.T) {
	rows := []StatementRow{
		{AccountCode: "1000", AccountType: "ASSET", AmountCents: 100},
		{AccountCode: "2100", AccountType: "LIABILITY", AmountCents: 50},
	}
	if composeBalance(2026, "", rows, 0).Balanced {
		t.Fatal("expected Balanced=false when assets != liabilities + equity + net income")
	}
}

// -- AC3 branch-sum == consolidated (compose additivity) ----------------------

func TestComposeIncome_BranchSumEqualsConsolidated(t *testing.T) {
	trenton := []StatementRow{
		{AccountCode: "4000", AccountType: "REVENUE", AmountCents: 600_000},
		{AccountCode: "5000", AccountType: "EXPENSE", AmountCents: 200_000},
	}
	kingston := []StatementRow{
		{AccountCode: "4000", AccountType: "REVENUE", AmountCents: 400_000},
		{AccountCode: "5000", AccountType: "EXPENSE", AmountCents: 100_000},
	}
	consolidated := []StatementRow{
		{AccountCode: "4000", AccountType: "REVENUE", AmountCents: 1_000_000},
		{AccountCode: "5000", AccountType: "EXPENSE", AmountCents: 300_000},
	}
	t1 := composeIncome(2026, 0, "trenton", trenton)
	k1 := composeIncome(2026, 0, "kingston", kingston)
	c1 := composeIncome(2026, 0, "", consolidated)
	if t1.NetIncomeCents+k1.NetIncomeCents != c1.NetIncomeCents {
		t.Fatalf("branch sum %d != consolidated %d", t1.NetIncomeCents+k1.NetIncomeCents, c1.NetIncomeCents)
	}
	if c1.RevenueCents != 1_000_000 || c1.ExpenseCents != 300_000 || c1.NetIncomeCents != 700_000 {
		t.Fatalf("consolidated net income wrong: rev=%d exp=%d ni=%d", c1.RevenueCents, c1.ExpenseCents, c1.NetIncomeCents)
	}
}

// -- AC4 comparatives incl. empty history -------------------------------------

func TestMergeComparative_EmptyPriorKeepsAllRows(t *testing.T) {
	current := []StatementRow{
		{AccountCode: "4000", AccountName: "Sales", AccountType: "REVENUE", AmountCents: 900_000},
		{AccountCode: "5000", AccountName: "COGS", AccountType: "EXPENSE", AmountCents: 300_000},
	}
	rows := mergeComparative(current, nil) // no prior year at all
	if len(rows) != 2 {
		t.Fatalf("got %d comparative rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.PriorCents != 0 {
			t.Errorf("account %s prior = %d, want 0 (empty history)", r.AccountCode, r.PriorCents)
		}
		if r.DeltaCents != r.CurrentCents {
			t.Errorf("account %s delta = %d, want %d", r.AccountCode, r.DeltaCents, r.CurrentCents)
		}
	}
}

func TestMergeComparative_UnionOfAccounts(t *testing.T) {
	current := []StatementRow{{AccountCode: "4000", AccountType: "REVENUE", AmountCents: 100}}
	prior := []StatementRow{
		{AccountCode: "4000", AccountType: "REVENUE", AmountCents: 40},
		{AccountCode: "4100", AccountType: "REVENUE", AmountCents: 25}, // present only in prior
	}
	rows := mergeComparative(current, prior)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (union of accounts)", len(rows))
	}
	by := map[string]ComparativeRow{}
	for _, r := range rows {
		by[r.AccountCode] = r
	}
	if by["4000"].CurrentCents != 100 || by["4000"].PriorCents != 40 || by["4000"].DeltaCents != 60 {
		t.Errorf("4000 wrong: %+v", by["4000"])
	}
	if by["4100"].CurrentCents != 0 || by["4100"].PriorCents != 25 || by["4100"].DeltaCents != -25 {
		t.Errorf("4100 (prior-only) wrong: %+v", by["4100"])
	}
	// deterministic sort by account code
	if rows[0].AccountCode != "4000" || rows[1].AccountCode != "4100" {
		t.Errorf("rows not sorted by account code: %s, %s", rows[0].AccountCode, rows[1].AccountCode)
	}
}

// -- AC5 drill-down tie (natural orientation per account type) -----------------

func TestSumDetailNatural_TiesByOrientation(t *testing.T) {
	// an expense account: two debits, one partial credit reversal → debit-normal
	expenseDetail := []GLDetailRow{
		{DebitCents: 500}, {DebitCents: 300}, {CreditCents: 100},
	}
	if got := sumDetailNatural(expenseDetail, "EXPENSE"); got != 700 {
		t.Fatalf("expense tie = %d, want 700 (debit-normal)", got)
	}
	// a revenue account: credits net of a debit → credit-normal
	revenueDetail := []GLDetailRow{
		{CreditCents: 900}, {CreditCents: 200}, {DebitCents: 150},
	}
	if got := sumDetailNatural(revenueDetail, "REVENUE"); got != 950 {
		t.Fatalf("revenue tie = %d, want 950 (credit-normal)", got)
	}
	// a liability: credit-normal
	if got := sumDetailNatural([]GLDetailRow{{CreditCents: 200}, {CreditCents: 50}}, "LIABILITY"); got != 250 {
		t.Fatalf("liability tie = %d, want 250", got)
	}
}

// -- AC6 aging buckets + grand total ------------------------------------------

func TestSummarizeAging_BucketsAndGrandTotal(t *testing.T) {
	rows := []AgingRow{
		{CustomerID: "c1", SourceType: "invoice", AmountCents: 10_000, Bucket: "CURRENT"},
		{CustomerID: "c1", SourceType: "invoice", AmountCents: 5_000, Bucket: "1_30"},
		{CustomerID: "c2", SourceType: "invoice", AmountCents: 8_000, Bucket: "1_30"},
		{CustomerID: "c2", SourceType: "customer_txn", AmountCents: 4_500, Bucket: "90_PLUS"}, // non-invoice AR (NSF fee)
	}
	sum := summarizeAging("", rows)
	if sum.GrandTotalCents != 27_500 {
		t.Fatalf("grand total = %d, want 27500 (must include non-invoice AR)", sum.GrandTotalCents)
	}
	want := map[string]int64{"CURRENT": 10_000, "1_30": 13_000, "31_60": 0, "61_90": 0, "90_PLUS": 4_500}
	if len(sum.Buckets) != len(agingBucketOrder) {
		t.Fatalf("got %d buckets, want %d", len(sum.Buckets), len(agingBucketOrder))
	}
	for i, b := range sum.Buckets {
		if b.Bucket != agingBucketOrder[i] {
			t.Errorf("bucket[%d] = %s, want %s (canonical order)", i, b.Bucket, agingBucketOrder[i])
		}
		if b.Cents != want[b.Bucket] {
			t.Errorf("bucket %s = %d, want %d", b.Bucket, b.Cents, want[b.Bucket])
		}
	}
	// grand total must equal sum of buckets (subledger tie property at the roll-up level)
	var bucketSum int64
	for _, b := range sum.Buckets {
		bucketSum += b.Cents
	}
	if bucketSum != sum.GrandTotalCents {
		t.Fatalf("Σbuckets %d != grand total %d", bucketSum, sum.GrandTotalCents)
	}
}

// -- AC7 CSV / .xlsx parity ---------------------------------------------------
//
// The parity assertion moved to xlsx_test.go when DIB-454 replaced the SpreadsheetML renderer with
// the shared native-.xlsx exporter. It is a stronger test now: it compares the CSV against the real
// workbook's TYPED cells and names the two places the formats deliberately diverge, instead of
// asserting a sameness that would have forbidden the blank-instead-of-zero rule.

func TestCentsToDecimal(t *testing.T) {
	cases := map[int64]string{0: "0.00", 5: "0.05", 100: "1.00", 123456: "1234.56", -9900: "-99.00"}
	for cents, want := range cases {
		if got := centsToDecimal(cents); got != want {
			t.Errorf("centsToDecimal(%d) = %q, want %q", cents, got, want)
		}
	}
}

// -- AC9 schedule validation --------------------------------------------------

func TestValidateScheduleInput(t *testing.T) {
	valid := ScheduleInput{Report: "INCOME_STATEMENT", Recipients: "a@b.com, c@d.com", Cadence: "MONTHLY", IsActive: true}
	if err := validateScheduleInput(valid); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
	bad := []ScheduleInput{
		{Report: "NONSENSE", Recipients: "a@b.com", Cadence: "MONTHLY"},
		{Report: "AR_AGING", Recipients: "a@b.com", Cadence: "HOURLY"},
		{Report: "AR_AGING", Recipients: "", Cadence: "DAILY"},
		{Report: "AR_AGING", Recipients: "not-an-email", Cadence: "DAILY"},
		{Report: "AR_AGING", Recipients: "ok@x.com, garbage", Cadence: "DAILY"},
	}
	for i, in := range bad {
		if err := validateScheduleInput(in); err == nil {
			t.Errorf("bad schedule[%d] %+v accepted, want rejected", i, in)
		}
	}
	// case-insensitive report/cadence normalize
	if err := validateScheduleInput(ScheduleInput{Report: "  ar_aging ", Recipients: "x@y.com", Cadence: " weekly "}); err != nil {
		t.Errorf("lower/space-padded valid input rejected: %v", err)
	}
}

func TestNormalizeScheduleInput(t *testing.T) {
	in := normalizeScheduleInput(ScheduleInput{Report: " ar_aging ", Recipients: " a@b.com ,, c@d.com ", Cadence: "monthly"})
	if in.Report != "AR_AGING" || in.Cadence != "MONTHLY" {
		t.Errorf("normalize report/cadence: %+v", in)
	}
	if in.Recipients != "a@b.com,c@d.com" {
		t.Errorf("normalize recipients = %q, want a@b.com,c@d.com", in.Recipients)
	}
}

// -- test helpers -------------------------------------------------------------

func parseCSVCells(t *testing.T, raw []byte) []string {
	t.Helper()
	rd := csv.NewReader(strings.NewReader(string(raw)))
	rd.FieldsPerRecord = -1 // the title row is a single cell; data rows are wider
	recs, err := rd.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	var cells []string
	for _, rec := range recs {
		cells = append(cells, rec...)
	}
	return cells
}
