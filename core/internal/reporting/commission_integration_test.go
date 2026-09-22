//go:build integration

package reporting

// commission_integration_test.go, DIB-96 end to end against the real substrate: attribution, rule
// selection, the freeze-and-drill-through artefact, the accrual boundary, and the seam conformance
// check that keeps the bulk SQL attribution honest.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// restatedResolver is a stand-in for the W5-D resolver, satisfying reporting.RepAssignmentReader.
//
// WHAT IT IS AND IS NOT. It is an INDEPENDENT re-statement of the 0136 ruling, read the customer's
// assignment rows, prefer the per-order override, fall back to the customer-level default, written
// procedurally in Go rather than as the report's COALESCE join. Cross-checking the two catches the
// realistic failure: someone edits the join, inverts the precedence, and split-book commission starts
// going to the wrong rep.
//
// It is NOT the messaging module's own code. This package imports no peer module, in tests as in
// production, so the last mile, binding *messaging.Service through the main.go adapter, is a UNION
// item (see NOTES-W6-B.md). What this proves is that the SQL implements the documented ruling; what the
// union proves is that the documented ruling is still what messaging does.
type restatedResolver struct {
	t    *testing.T
	pool *pgxpool.Pool
}

// ResolveRep implements the ruling procedurally: read the customer's rows, prefer the per-order
// override, else the customer-level default, else nothing (the house queue).
func (r *restatedResolver) ResolveRep(ctx context.Context, customerID, orderID string) (string, string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT COALESCE(order_id::text,''), rep_user_id::text FROM rep_assignments WHERE customer_id = $1::uuid`,
		customerID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	var customerLevel string
	var override string
	for rows.Next() {
		var ord, rep string
		if err := rows.Scan(&ord, &rep); err != nil {
			return "", "", err
		}
		switch {
		case ord == "":
			customerLevel = rep
		case orderID != "" && ord == orderID:
			override = rep
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if override != "" {
		return override, RepLevelOrder, nil
	}
	if customerLevel != "" {
		return customerLevel, RepLevelCustomer, nil
	}
	return "", RepLevelNone, nil
}

// constantResolver always answers the same rep, used to prove the conformance check can FAIL.
type constantResolver struct{ rep, level string }

func (c constantResolver) ResolveRep(context.Context, string, string) (string, string, error) {
	return c.rep, c.level, nil
}

// invoicesForOrders reads the invoice ids hanging off a set of orders.
func invoicesForOrders(t *testing.T, pool *pgxpool.Pool, orderIDs ...string) []string {
	t.Helper()
	var out []string
	for _, id := range orderIDs {
		rows, err := pool.Query(context.Background(),
			`SELECT id::text FROM invoices WHERE order_id = $1::uuid`, id)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var inv string
			if err := rows.Scan(&inv); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out = append(out, inv)
		}
		rows.Close()
	}
	return out
}

// TestIntegration_Commission_RevenueBasisEndToEnd, the simplest true statement the feature makes.
//
// Arithmetic: two lines, $200.00 and $100.00, attributed to one rep on a 5% REVENUE rule.
// 5% of $300.00 is $15.00, and the two drill-through lines are $10.00 and $5.00.
func TestIntegration_Commission_RevenueBasisEndToEnd(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	rep, _ := twoUsers(t, pool)
	cust := f.customer("acme")
	f.repAssignment(cust, "", rep)
	prod := f.product("PAV1", "Paver", f.category("pavers"))

	ord := f.order(cust, trentonBranch, from)
	inv := f.invoicePaid(cust, trentonBranch, ord, from, 30_000, 0)
	f.invoiceLine(inv, prod, 1, 10, 2_000, 20_000)
	f.invoiceLine(inv, prod, 2, 5, 2_000, 10_000)

	f.commissionRule(rep, BasisRevenue, 500, "", "", from)

	periods, err := svc.CommissionPreview(context.Background(), rep, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 {
		t.Fatalf("preview returned %d periods for one rep: %+v", len(periods), periods)
	}
	p := periods[0]
	if p.RevenueCents != 30_000 {
		t.Errorf("attributed revenue = %d, want 30000", p.RevenueCents)
	}
	if p.CommissionCents != 1_500 {
		t.Errorf("commission = %d cents, want 1500 ($15.00 = 5%% of $300.00)", p.CommissionCents)
	}
	if p.SaleCount != 2 {
		t.Errorf("sale_count = %d, want 2", p.SaleCount)
	}
	if p.PostsToGL {
		t.Error("a computed period claims it posts to the GL")
	}
	var lineSum int64
	for _, l := range p.Lines {
		lineSum += l.CommissionCents
		if l.Basis != BasisRevenue {
			t.Errorf("line basis = %q, want REVENUE", l.Basis)
		}
	}
	if lineSum != p.CommissionCents {
		t.Errorf("drill-through sums to %d but the header says %d", lineSum, p.CommissionCents)
	}
}

// TestIntegration_Commission_MarginBasisUsesWeightedAverageCost, the margin basis nets off the SAME
// cost the margin report uses, so a rep's number and the manager's report cannot disagree.
//
// Arithmetic: 10 received at $12.00, 10 sold at $20.00. Margin $80.00; a 10% margin rule pays $8.00.
// A 10% REVENUE rule on the same line would pay $20.00, the test asserts it did not.
func TestIntegration_Commission_MarginBasisUsesWeightedAverageCost(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	rep, _ := twoUsers(t, pool)
	cust := f.customer("acme")
	f.repAssignment(cust, "", rep)
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	loc := f.branchLocation(trentonBranch)
	f.stockReceipt(prod, trentonBranch, loc, from+"T00:00:00Z", 10, 1_200)

	ord := f.order(cust, trentonBranch, from)
	inv := f.invoicePaid(cust, trentonBranch, ord, from, 20_000, 0)
	f.invoiceLine(inv, prod, 1, 10, 2_000, 20_000)

	f.commissionRule(rep, BasisMargin, 1_000, "", "", from)

	periods, err := svc.CommissionPreview(context.Background(), rep, from, to, trentonBranch)
	if err != nil {
		t.Fatal(err)
	}
	p := periods[0]
	if p.CostCents != 12_000 {
		t.Errorf("cost = %d, want 12000 (the weighted average the margin report uses)", p.CostCents)
	}
	if p.MarginCents != 8_000 {
		t.Errorf("margin = %d, want 8000", p.MarginCents)
	}
	if p.CommissionCents != 800 {
		t.Errorf("commission = %d cents, want 800 ($8.00 = 10%% of the $80.00 MARGIN). "+
			"2000 would mean the rate was applied to revenue.", p.CommissionCents)
	}
}

// TestIntegration_Commission_CategoryRuleBeatsDefault, the carve-out. Pavers pay 2%, everything else
// pays 5%, and the report must pick per LINE, not per rep.
func TestIntegration_Commission_CategoryRuleBeatsDefault(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	rep, _ := twoUsers(t, pool)
	cust := f.customer("acme")
	f.repAssignment(cust, "", rep)
	pavers := f.category("pavers")
	mulch := f.category("mulch")
	paver := f.product("PAV1", "Paver", pavers)
	bag := f.product("MUL1", "Mulch", mulch)

	ord := f.order(cust, trentonBranch, from)
	inv := f.invoicePaid(cust, trentonBranch, ord, from, 30_000, 0)
	f.invoiceLine(inv, paver, 1, 10, 2_000, 20_000) // $200.00 of pavers @ 2% = $4.00
	f.invoiceLine(inv, bag, 2, 10, 1_000, 10_000)   // $100.00 of mulch  @ 5% = $5.00

	f.commissionRule(rep, BasisRevenue, 500, "", "", from)     // the default
	f.commissionRule(rep, BasisRevenue, 200, pavers, "", from) // the paver carve-out

	periods, err := svc.CommissionPreview(context.Background(), rep, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	p := periods[0]
	if p.CommissionCents != 900 {
		t.Errorf("commission = %d cents, want 900 ($4.00 pavers at 2%% + $5.00 mulch at 5%%). "+
			"1500 would mean the carve-out lost to the default.", p.CommissionCents)
	}
	for _, l := range p.Lines {
		wantRate := 500
		if strings.Contains(l.ProductSKU, "PAV1") {
			wantRate = 200
		}
		if l.RateBps != wantRate {
			t.Errorf("line %s used %d bps, want %d, the rule is chosen per LINE",
				l.ProductSKU, l.RateBps, wantRate)
		}
	}
}

// TestIntegration_Commission_FreezeAndDrillThrough, the artefact. What was computed is what is stored,
// the lines add to the header, and the RATE is frozen on each line so a later rule change cannot
// rewrite history.
func TestIntegration_Commission_FreezeAndDrillThrough(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	ctx := context.Background()
	from, to := window()

	rep, _ := twoUsers(t, pool)
	cust := f.customer("acme")
	f.repAssignment(cust, "", rep)
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	ord := f.order(cust, trentonBranch, from)
	inv := f.invoicePaid(cust, trentonBranch, ord, from, 30_000, 0)
	f.invoiceLine(inv, prod, 1, 10, 2_000, 20_000)
	f.invoiceLine(inv, prod, 2, 5, 2_000, 10_000)
	ruleID := f.commissionRule(rep, BasisRevenue, 500, "", "", from)

	frozen, err := svc.ComputeAndFreeze(ctx, rep, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM commission_run_lines WHERE run_id=$1::uuid`, frozen.RunID)
	f.del(`DELETE FROM commission_runs WHERE id=$1::uuid`, frozen.RunID)
	if frozen.RunID == "" {
		t.Fatal("freezing a period produced no run id")
	}

	read, err := svc.CommissionRun(ctx, frozen.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if read.CommissionCents != 1_500 {
		t.Errorf("frozen commission = %d, want 1500", read.CommissionCents)
	}
	if read.Status != "ACCRUED" {
		t.Errorf("status = %q, want ACCRUED", read.Status)
	}
	if len(read.Lines) != 2 {
		t.Fatalf("drill-through has %d lines, want 2", len(read.Lines))
	}
	var sum int64
	for _, l := range read.Lines {
		sum += l.CommissionCents
		if l.RateBps != 500 {
			t.Errorf("line rate = %d bps, want the rate frozen at computation time (500)", l.RateBps)
		}
		if l.RuleID != ruleID {
			t.Errorf("line rule_id = %q, want the rule that chose the rate (%q)", l.RuleID, ruleID)
		}
		if l.InvoiceNo == "" {
			t.Error("a drill-through line carries no invoice number, it cannot be traced back to a sale")
		}
	}
	if sum != read.CommissionCents {
		t.Errorf("frozen drill-through sums to %d but the frozen header says %d", sum, read.CommissionCents)
	}

	// -- THE PROPERTY THE COPIED RATE EXISTS FOR -----------------------------------------------------
	// A renegotiation in April must not rewrite what March paid. Two events, and the FIRST one is the
	// common one, an earlier version of this test only covered the second and a mutation that read the
	// rate live (COALESCE(rule.rate_bps, line.rate_bps)) SURVIVED it, because a retired rule still has
	// its old rate. Changing the rate is what actually happens when a pay plan is renegotiated.
	//
	// 1 · the rate CHANGES.
	if _, err := pool.Exec(ctx, `UPDATE commission_rules SET rate_bps = 900 WHERE id = $1::uuid`, ruleID); err != nil {
		t.Fatal(err)
	}
	renegotiated, err := svc.CommissionRun(ctx, frozen.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if renegotiated.Lines[0].RateBps != 500 {
		t.Errorf("after the rule's rate changed to 900 bps, the FROZEN period reports %d bps, a computed "+
			"period must keep saying what it said, or every renegotiation silently restates history",
			renegotiated.Lines[0].RateBps)
	}
	if renegotiated.CommissionCents != 1_500 {
		t.Errorf("after the rate change the frozen header says %d cents, want the frozen 1500",
			renegotiated.CommissionCents)
	}

	// 2 · the rule is RETIRED.
	if err := svc.RetireCommissionRule(ctx, ruleID); err != nil {
		t.Fatal(err)
	}
	after, err := svc.CommissionRun(ctx, frozen.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CommissionCents != 1_500 || len(after.Lines) != 2 || after.Lines[0].RateBps != 500 {
		t.Errorf("retiring the rule changed a frozen period: %d cents, %d lines, first rate %d bps",
			after.CommissionCents, len(after.Lines), after.Lines[0].RateBps)
	}
}

// TestIntegration_Commission_AccrualIsUnpostedAndSaysSo, the wave's stated boundary, checked at the
// substrate: a frozen run has NO gl_journal_id, and the accrual payload says so in words.
func TestIntegration_Commission_AccrualIsUnpostedAndSaysSo(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	ctx := context.Background()
	from, to := window()

	rep, _ := twoUsers(t, pool)
	cust := f.customer("acme")
	f.repAssignment(cust, "", rep)
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	ord := f.order(cust, trentonBranch, from)
	inv := f.invoicePaid(cust, trentonBranch, ord, from, 20_000, 0)
	f.invoiceLine(inv, prod, 1, 10, 2_000, 20_000)
	f.commissionRule(rep, BasisRevenue, 500, "", "", from)

	frozen, err := svc.ComputeAndFreeze(ctx, rep, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM commission_run_lines WHERE run_id=$1::uuid`, frozen.RunID)
	f.del(`DELETE FROM commission_runs WHERE id=$1::uuid`, frozen.RunID)

	var journal *string
	if err := pool.QueryRow(ctx, `SELECT gl_journal_id::text FROM commission_runs WHERE id=$1::uuid`,
		frozen.RunID).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != nil {
		t.Errorf("a commission run carries gl_journal_id=%q, NOTHING in this wave posts to the general "+
			"ledger, and a run that did would be an unratified liability in the trial balance", *journal)
	}

	acc, err := svc.CommissionAccruals(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if acc.PostsToGL {
		t.Error("the accrual register claims posts_to_gl=true")
	}
	if acc.Note == "" || !strings.Contains(acc.Note, "not posted") {
		t.Errorf("the accrual note does not state the boundary: %q", acc.Note)
	}
	if acc.TotalCents != 1_000 {
		t.Errorf("accrual total = %d cents, want 1000 (5%% of $200.00)", acc.TotalCents)
	}
}

// TestIntegration_Commission_RepWithARuleAndNoSalesStillAppears, a zero row answers "did I sell
// nothing?"; a missing row does not.
func TestIntegration_Commission_RepWithARuleAndNoSalesStillAppears(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	rep, _ := twoUsers(t, pool)
	f.commissionRule(rep, BasisRevenue, 500, "", "", from)

	periods, err := svc.CommissionPreview(context.Background(), rep, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 {
		t.Fatalf("a rep with a rule and no sales produced %d periods, want 1 zero period", len(periods))
	}
	if periods[0].CommissionCents != 0 || periods[0].SaleCount != 0 {
		t.Errorf("expected a zero period, got %+v", periods[0])
	}
}

// TestIntegration_SalesByRep_AgreesWithRepAssignmentSeam, THE SEAM CONFORMANCE CHECK, driven through
// the production AttributionCheck path rather than around it.
//
// The fixture deliberately contains all three resolution outcomes: an order override, a customer-level
// default, and an unassigned sale. If the bulk SQL's precedence ever inverts, the override row is the
// one that disagrees and this test names it.
func TestIntegration_SalesByRep_AgreesWithRepAssignmentSeam(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	from, to := window()

	repA, repB := twoUsers(t, pool)
	custAssigned := f.customer("assigned")
	custHouse := f.customer("house")

	f.repAssignment(custAssigned, "", repA)
	ordDefault := f.order(custAssigned, trentonBranch, from)
	f.invoicePaid(custAssigned, trentonBranch, ordDefault, from, 10_000, 0)

	ordOverride := f.order(custAssigned, trentonBranch, from)
	f.repAssignment(custAssigned, ordOverride, repB)
	f.invoicePaid(custAssigned, trentonBranch, ordOverride, from, 30_000, 0)

	ordHouse := f.order(custHouse, trentonBranch, from)
	f.invoicePaid(custHouse, trentonBranch, ordHouse, from, 5_000, 0)

	// Every invoice needs a line, or the attribution read (which is line-level) sees nothing.
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	for _, inv := range invoicesForOrders(t, pool, ordDefault, ordOverride, ordHouse) {
		f.invoiceLine(inv, prod, 1, 1, 1_000, 1_000)
	}

	svc := globalSvc(t, pool).WithRepAssignments(&restatedResolver{pool: pool, t: t})

	res, err := svc.AttributionCheck(context.Background(), from, to, trentonBranch)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Bound {
		t.Fatal("the seam is bound but AttributionCheck reported it unbound")
	}
	if res.Checked != 3 {
		t.Errorf("checked %d invoices, want 3", res.Checked)
	}
	if len(res.Mismatches) != 0 {
		t.Errorf("the report's bulk SQL attribution disagrees with the rep-assignment ruling on %d sales: %+v\n"+
			"This is the drift that quietly pays split-book commission to the wrong rep.",
			len(res.Mismatches), res.Mismatches)
	}
}

// TestIntegration_AttributionCheck_UnboundSeamExplainsItself, the unbound path is a 200 with a
// sentence, not an error and not a silent pass.
func TestIntegration_AttributionCheck_UnboundSeamExplainsItself(t *testing.T) {
	pool := intPool(t)
	svc := globalSvc(t, pool) // no WithRepAssignments
	from, to := window()

	res, err := svc.AttributionCheck(context.Background(), from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Bound {
		t.Error("AttributionCheck reported the seam bound when nothing was wired")
	}
	if !strings.Contains(res.Explanation, "not wired") {
		t.Errorf("the unbound explanation does not say the seam is unwired: %q", res.Explanation)
	}
}

// TestIntegration_AttributionCheck_ReportsARealDisagreement proves the check can FAIL. A check that has
// never been seen to fail is a check nobody should trust.
func TestIntegration_AttributionCheck_ReportsARealDisagreement(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	from, to := window()

	repA, repB := twoUsers(t, pool)
	cust := f.customer("acme")
	f.repAssignment(cust, "", repA)
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	ord := f.order(cust, trentonBranch, from)
	inv := f.invoicePaid(cust, trentonBranch, ord, from, 10_000, 0)
	f.invoiceLine(inv, prod, 1, 1, 10_000, 10_000)

	// A resolver that answers a DIFFERENT rep than the table says, the shape of a real ruling change
	// that the report's SQL has not caught up with.
	svc := globalSvc(t, pool).WithRepAssignments(constantResolver{rep: repB, level: RepLevelCustomer})

	res, err := svc.AttributionCheck(context.Background(), from, to, trentonBranch)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mismatches) != 1 {
		t.Fatalf("a disagreeing resolver produced %d mismatches, want 1: %+v", len(res.Mismatches), res.Mismatches)
	}
	m := res.Mismatches[0]
	if m.ReportRepUserID != repA || m.SeamRepUserID != repB {
		t.Errorf("the mismatch does not name both answers: report=%q seam=%q", m.ReportRepUserID, m.SeamRepUserID)
	}
	if !strings.Contains(res.Explanation, "wrong rep") {
		t.Errorf("the mismatch explanation does not say what is at stake: %q", res.Explanation)
	}
}
