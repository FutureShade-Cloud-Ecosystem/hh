//go:build integration

package reporting

// supplier_deposit_integration_test.go, item 9 delta review P2-B: the supplier
// deposit document (sub item 9.5) is a liability billed in advance (its GL
// posting is DR AR / CR Customer Deposits, no revenue leg), so the line based
// readers that sum invoice_lines must exclude it. The rest's invoice keeps the
// goods lines at their REAL prices (the sign discipline forbids striking the
// deposit off a goods line), so a reader that sums lines without the exclusion
// counts the sale once AND the deposit again beside it: more revenue than the
// sale, and commission on money that is not revenue.

import (
	"context"
	"testing"
)

// supplierDepositInvoice plants the deposit document the ring writes: one
// FINALIZED invoice under the SUPPLIER_DEPOSIT source kind, one line with no
// product at the deposit amount.
func supplierDepositInvoice(f *fixture, customerID, branchID, orderID, date string, amount int64) string {
	id := f.invoice(customerID, branchID, orderID, date, amount, 0, "PAID", 0)
	f.exec(`UPDATE invoices SET source_kind='SUPPLIER_DEPOSIT' WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO invoice_lines (id, invoice_id, line_no, description, qty_entered,
	          uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
	        VALUES ($1::uuid, $2::uuid, 1, 'Supplier deposit on a special order', 1, 'EA', 1, $3, 'EA', $3, 0)`,
		f.newID(), id, amount)
	return id
}

// A deposit document beside its sale: the line based reports count the goods
// once. Arithmetic: a $300.00 sale (two lines, $200.00 and $100.00) with a
// $50.00 supplier deposit. Revenue by category, margin by product and the
// rep's commissionable basis all answer $300.00, never $350.00, and the
// deposit's uncategorised line appears in none of them.
func TestIntegration_Reporting_SupplierDepositDocument_CountsTheGoodsOnce(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	svc := globalSvc(t, pool)
	from, to := window()

	rep, _ := twoUsers(t, pool)
	cust := f.customer("deposit proof")
	f.repAssignment(cust, "", rep)
	cat := f.category("deposit proof cat")
	prod := f.product("DEP1", "Deposit proof paver", cat)
	f.commissionRule(rep, BasisRevenue, 500, cat, "", from)

	ord := f.order(cust, trentonBranch, from)
	rest := f.invoicePaid(cust, trentonBranch, ord, from, 30_000, 0)
	f.invoiceLine(rest, prod, 1, 10, 2_000, 20_000)
	f.invoiceLine(rest, prod, 2, 5, 2_000, 10_000)
	supplierDepositInvoice(f, cust, trentonBranch, ord, from, 5_000)

	params := map[string]string{"from_date": from, "to_date": to, "branch_id": trentonBranch}

	// Sales by category: exactly the goods, once.
	res := run(t, svc, "sales_by_category", params)
	if len(res.Rows) != 1 {
		t.Fatalf("sales_by_category answered %d rows for one deposit sale, want exactly the goods' category: %v",
			len(res.Rows), res.Rows)
	}
	wantCents(t, res, 0, "revenue_cents", 30_000)
	if code, _ := cell(t, res, 0, "category_code").(string); code != f.tag+"-deposit proof cat" {
		t.Errorf("the one category is %q, want the goods' own", code)
	}

	// Margin by product: the same goods, once.
	res = run(t, svc, "margin_by_product", params)
	if len(res.Rows) != 1 {
		t.Fatalf("margin_by_product answered %d rows, want exactly the one product: %v", len(res.Rows), res.Rows)
	}
	wantCents(t, res, 0, "revenue_cents", 30_000)

	// Margin by category: the same again.
	res = run(t, svc, "margin_by_category", params)
	if len(res.Rows) != 1 {
		t.Fatalf("margin_by_category answered %d rows, want exactly the one category: %v", len(res.Rows), res.Rows)
	}
	wantCents(t, res, 0, "revenue_cents", 30_000)

	// Commission: the rep's revenue basis is the goods, and the deposit line is
	// not among the attributed lines.
	periods, err := svc.CommissionPreview(context.Background(), rep, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 {
		t.Fatalf("preview returned %d periods for one rep: %+v", len(periods), periods)
	}
	p := periods[0]
	if p.RevenueCents != 30_000 {
		t.Errorf("attributed revenue = %d, want 30000 (the goods once, the deposit document excluded)", p.RevenueCents)
	}
	if p.SaleCount != 2 {
		t.Errorf("attributed lines = %d, want the 2 goods lines (the deposit line excluded)", p.SaleCount)
	}
	if p.CommissionCents != 1_500 {
		t.Errorf("commission = %d, want 1500 (5%% of the goods, not of the deposit)", p.CommissionCents)
	}
}
