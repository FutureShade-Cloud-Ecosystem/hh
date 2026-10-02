//go:build integration

// HH-206: an invoice line priced per square foot and sold in pieces printed its unit price with no
// price unit, so "CA$14.76" sat beside a quantity in PC and read as a per-piece price. The stored
// line is right (unit price per SF, total per quantity); the printed document must carry the unit.
package billing

import (
	"context"
	"strings"
	"testing"
)

func TestIntegration_Billing_InvoiceHTML_CarriesPriceUnitWhenSoldInOtherUnit(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)

	cust := seedCustomer(t, pool, n+"-A")
	inv := seedInvoice(t, pool, cust, n, invSpec{no: "U1", status: "FINALIZED", subtotal: 9000, tax: 1170, total: 10170, balance: 10170, finalized: true})
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered,
		                            qty_base, unit_price_cents, price_uom, line_total_cents)
		 VALUES ($1,1,'Brussels Block','25.0000','PC','25.0000',1476,'SF',9000)`, inv); err != nil {
		t.Fatalf("seed line: %v", err)
	}

	lines, err := repo.DetailLines(ctx, inv)
	if err != nil || len(lines) != 1 {
		t.Fatalf("detail lines: %v (%d)", err, len(lines))
	}
	html := renderInvoiceHTML(InvoiceDetail{InvoiceNo: "INV-U1", Lines: lines}, "Branch")
	if !strings.Contains(html, "CA$14.76 / SF") {
		t.Fatalf("the unit price per SF on a line sold in PC must print its unit, got:\n%s", html)
	}
}
