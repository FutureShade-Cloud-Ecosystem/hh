package reporting

// commission_test.go, the commission arithmetic, tested as pure functions with no database.
//
// The first test in this file is the MUTATION TARGET named in the acceptance criteria: swap the two
// arms of commissionBase and it dies. The rest pin the properties that make a commission report
// defensible in an argument, the drill-through adds up, the rounding is symmetric, and the most
// specific rule wins deterministically.

import (
	"testing"
	"time"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(DateLayout, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestCommission_MarginBasisIsNotRevenueBasis, THE MUTATION TEST.
//
// APPLY: swap the BasisMargin and BasisRevenue arms in commissionBase.
// EXPECT: this test fails on both sub-assertions.
//
// The numbers are chosen so the two bases are not merely different but different by a factor a payroll
// would notice: a $1,000.00 line at $750.00 cost is $250.00 of margin, so a 5% rule pays $50.00 on
// revenue and $12.50 on margin, four times.
func TestCommission_MarginBasisIsNotRevenueBasis(t *testing.T) {
	t.Parallel()
	const revenue, cost = int64(100_000), int64(75_000) // $1,000.00 and $750.00

	if got := commissionBase(BasisRevenue, revenue, cost); got != revenue {
		t.Errorf("REVENUE basis applied to %d/%d = %d, want the full revenue %d, "+
			"a revenue rule must NOT net off cost", revenue, cost, got, revenue)
	}
	if got := commissionBase(BasisMargin, revenue, cost); got != revenue-cost {
		t.Errorf("MARGIN basis applied to %d/%d = %d, want revenue-cost %d, "+
			"a margin rule must NOT pay on gross revenue", revenue, cost, got, revenue-cost)
	}
	// And the consequence, stated in money, so a reader of a failure knows what it costs.
	revPay := commissionOn(commissionBase(BasisRevenue, revenue, cost), 500)
	marPay := commissionOn(commissionBase(BasisMargin, revenue, cost), 500)
	if revPay != 5000 {
		t.Errorf("5%% of revenue = %d cents, want 5000 ($50.00)", revPay)
	}
	if marPay != 1250 {
		t.Errorf("5%% of margin = %d cents, want 1250 ($12.50)", marPay)
	}
	if revPay == marPay {
		t.Fatal("revenue and margin bases produced the SAME commission, the two bases have been swapped or merged")
	}
}

// TestCommission_UnknownBasisEarnsNothing: a basis the substrate cannot produce still cannot invent
// money if it ever appears.
func TestCommission_UnknownBasisEarnsNothing(t *testing.T) {
	t.Parallel()
	if got := commissionBase("GROSS_PROFIT_PERCENT", 100_000, 75_000); got != 0 {
		t.Errorf("unknown basis returned %d, want 0", got)
	}
}

// TestCommission_RoundingIsHalfUpAndSymmetric. Half-up on the MAGNITUDE, so a below-cost margin line
// rounds the same distance in the other direction; rounding a negative toward zero would forgive part
// of a clawback.
func TestCommission_RoundingIsHalfUpAndSymmetric(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base int64
		bps  int
		want int64
		why  string
	}{
		{100, 500, 5, "5% of 100 cents is exactly 5"},
		{101, 500, 5, "5.05 rounds down to 5"},
		{110, 500, 6, "5.50 rounds up (half-up), not to even"},
		{130, 500, 7, "6.50 rounds up (half-up), not to even"},
		{-110, 500, -6, "a negative base rounds by MAGNITUDE, mirroring +110"},
		{100_000, 0, 0, "a zero rate pays nothing"},
		{0, 500, 0, "a zero base pays nothing"},
		{-100_000, 250, -2500, "a below-cost margin line produces a negative commission, not zero"},
	}
	for _, c := range cases {
		if got := commissionOn(c.base, c.bps); got != c.want {
			t.Errorf("commissionOn(%d, %d) = %d, want %d, %s", c.base, c.bps, got, c.want, c.why)
		}
	}
}

// TestCommission_RuleSpecificityOrder pins 0144's stated ordering. The failure this prevents is a rep
// with a category carve-out being paid the company default because two rules matched and the wrong one
// sorted first.
func TestCommission_RuleSpecificityOrder(t *testing.T) {
	t.Parallel()
	const cat, branch = "cat-pavers", "branch-kgn"
	base := CommissionRule{RepUserID: "rep", Basis: BasisRevenue, RateBps: 500, IsActive: true,
		effFrom: mustDate(t, "2026-01-01")}
	mk := func(id, category, br string, bps int) CommissionRule {
		r := base
		r.ID, r.CategoryID, r.BranchID, r.RateBps = id, category, br, bps
		return r
	}
	rules := []CommissionRule{
		mk("default", "", "", 500),
		mk("branch-only", "", branch, 400),
		mk("category-only", cat, "", 300),
		mk("both", cat, branch, 200),
	}
	on := mustDate(t, "2026-02-15")

	if got := chooseRule(rules, cat, branch, on); got == nil || got.ID != "both" {
		t.Errorf("category+branch line chose %v, want the most specific rule 'both'", idOf(got))
	}
	if got := chooseRule(rules, cat, "branch-trt", on); got == nil || got.ID != "category-only" {
		t.Errorf("category match in another branch chose %v, want 'category-only'", idOf(got))
	}
	if got := chooseRule(rules, "cat-other", branch, on); got == nil || got.ID != "branch-only" {
		t.Errorf("branch match with an unrelated category chose %v, want 'branch-only'", idOf(got))
	}
	if got := chooseRule(rules, "cat-other", "branch-trt", on); got == nil || got.ID != "default" {
		t.Errorf("no scope match chose %v, want 'default'", idOf(got))
	}
}

// TestCommission_RuleWindowIsRespected: a rule that has not started, or has ended, does not pay.
func TestCommission_RuleWindowIsRespected(t *testing.T) {
	t.Parallel()
	end := mustDate(t, "2026-03-31")
	rules := []CommissionRule{{
		ID: "q1", RepUserID: "rep", Basis: BasisRevenue, RateBps: 500, IsActive: true,
		effFrom: mustDate(t, "2026-01-01"), effTo: &end,
	}}
	if got := chooseRule(rules, "", "", mustDate(t, "2025-12-31")); got != nil {
		t.Errorf("a rule starting 2026-01-01 paid on 2025-12-31 (chose %v)", idOf(got))
	}
	if got := chooseRule(rules, "", "", mustDate(t, "2026-03-31")); got == nil {
		t.Error("a rule ending 2026-03-31 did not pay ON its last day, the window is inclusive")
	}
	if got := chooseRule(rules, "", "", mustDate(t, "2026-04-01")); got != nil {
		t.Errorf("a rule ending 2026-03-31 paid on 2026-04-01 (chose %v)", idOf(got))
	}
}

// TestCommission_InactiveRuleNeverPays, retiring a rule is how a rate is changed, so an inactive rule
// paying would make every renegotiation a double-pay.
func TestCommission_InactiveRuleNeverPays(t *testing.T) {
	t.Parallel()
	rules := []CommissionRule{{ID: "retired", Basis: BasisRevenue, RateBps: 900, IsActive: false,
		effFrom: mustDate(t, "2026-01-01")}}
	if got := chooseRule(rules, "", "", mustDate(t, "2026-02-01")); got != nil {
		t.Errorf("an inactive rule was chosen (%v)", idOf(got))
	}
}

// TestCommission_PeriodTotalIsTheSumOfItsLines is the property a rep checks by hand. The header must be
// the sum of the drill-through, not the rounded sum of the bases, which would differ by up to a cent
// per line and make the report look wrong to the one person guaranteed to add it up.
func TestCommission_PeriodTotalIsTheSumOfItsLines(t *testing.T) {
	t.Parallel()
	rules := []CommissionRule{{ID: "r", RepUserID: "rep", Basis: BasisMargin, RateBps: 750,
		IsActive: true, effFrom: mustDate(t, "2026-01-01")}}
	lines := []AttributedLine{
		{InvoiceLineID: "1", InvoiceDate: "2026-01-05", RevenueCents: 33_333, CostCents: 11_111, RepUserID: "rep"},
		{InvoiceLineID: "2", InvoiceDate: "2026-01-06", RevenueCents: 66_667, CostCents: 22_222, RepUserID: "rep"},
		{InvoiceLineID: "3", InvoiceDate: "2026-01-07", RevenueCents: 1, CostCents: 0, RepUserID: "rep"},
	}
	p := computePeriod("rep", "Alex", "2026-01-01", "2026-01-31", "", lines, rules, time.Now())

	var sum int64
	for _, l := range p.Lines {
		sum += l.CommissionCents
	}
	if p.CommissionCents != sum {
		t.Errorf("header commission %d != sum of %d lines %d, the drill-through must add up",
			p.CommissionCents, len(p.Lines), sum)
	}
	if p.SaleCount != 3 {
		t.Errorf("sale_count = %d, want 3", p.SaleCount)
	}
	if p.MarginCents != p.RevenueCents-p.CostCents {
		t.Errorf("margin %d != revenue %d - cost %d", p.MarginCents, p.RevenueCents, p.CostCents)
	}
}

// TestCommission_UnattributedRevenueIsReportedNotDropped. A commission report whose revenue silently
// falls short of the sales report is a report two people will spend an afternoon reconciling.
func TestCommission_UnattributedRevenueIsReportedNotDropped(t *testing.T) {
	t.Parallel()
	rules := []CommissionRule{{ID: "r", RepUserID: "rep", Basis: BasisRevenue, RateBps: 500,
		IsActive: true, effFrom: mustDate(t, "2026-01-01")}}
	lines := []AttributedLine{
		{InvoiceLineID: "1", InvoiceDate: "2026-01-05", RevenueCents: 10_000, RepUserID: "rep"},
		{InvoiceLineID: "2", InvoiceDate: "2026-01-05", RevenueCents: 40_000}, // house queue
	}
	p := computePeriod("rep", "Alex", "2026-01-01", "2026-01-31", "", lines, rules, time.Now())
	if p.RevenueCents != 10_000 {
		t.Errorf("attributed revenue = %d, want 10000", p.RevenueCents)
	}
	if p.UnattributedCents != 40_000 || p.UnattributedCount != 1 {
		t.Errorf("unattributed = %d cents over %d lines, want 40000 over 1, house-queue revenue must be "+
			"visible so the report reconciles to sales", p.UnattributedCents, p.UnattributedCount)
	}
}

// TestCommission_LineWithNoRuleEarnsZeroWithAReason. Silent zeros are indistinguishable from missing
// rows, and the difference is somebody's pay.
func TestCommission_LineWithNoRuleEarnsZeroWithAReason(t *testing.T) {
	t.Parallel()
	lines := []AttributedLine{{InvoiceLineID: "1", InvoiceDate: "2026-01-05", RevenueCents: 10_000, RepUserID: "rep"}}
	p := computePeriod("rep", "Alex", "2026-01-01", "2026-01-31", "", lines, nil, time.Now())
	if len(p.Lines) != 1 {
		t.Fatalf("expected the line to be listed, got %d rows", len(p.Lines))
	}
	if p.Lines[0].CommissionCents != 0 {
		t.Errorf("commission = %d with no rule, want 0", p.Lines[0].CommissionCents)
	}
	if p.Lines[0].NoRuleReason == "" {
		t.Error("a zero-commission line carries no reason, a rep cannot tell it from an omission")
	}
}

// TestCommission_AccrualNeverClaimsToPost pins the wave's stated boundary at the type level: whatever a
// caller does, the computed period says it is not in the general ledger.
func TestCommission_AccrualNeverClaimsToPost(t *testing.T) {
	t.Parallel()
	p := computePeriod("rep", "Alex", "2026-01-01", "2026-01-31", "", nil, nil, time.Now())
	if p.PostsToGL {
		t.Error("a computed period claims posts_to_gl=true, nothing in this wave posts to the GL")
	}
	if p.Status != "ACCRUED" {
		t.Errorf("status = %q, want ACCRUED", p.Status)
	}
}

func idOf(r *CommissionRule) string {
	if r == nil {
		return "<none>"
	}
	return r.ID
}
