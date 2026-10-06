package billing

import (
	"strings"
	"testing"
)

// TestUnitPriceLabel (HH-206): the printed unit price names its unit exactly when the line was priced
// in a different unit from the one it was sold in. Unit level on purpose: the integration test that
// also covers it lives in a package the CI integration job does not run.
func TestUnitPriceLabel(t *testing.T) {
	for _, tc := range []struct {
		name       string
		uom, price string
		cents      int64
		want       string
	}{
		{"priced per SF, sold in PC", "PC", "SF", 1476, "$14.76 / SF"},
		{"same unit", "SF", "SF", 1476, "$14.76"},
		{"units differ only in case", "pc", "PC", 1476, "$14.76"},
		{"empty price unit is a guard, not a label", "PC", "", 1476, "$14.76"},
		{"priced per M2, sold in SF", "SF", "M2", 99, "$0.99 / M2"},
	} {
		got := unitPriceLabel(DetailLine{UOMEntered: tc.uom, PriceUOM: tc.price, UnitPriceCents: tc.cents})
		if got != tc.want {
			t.Errorf("%s: label = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRenderInvoiceHTML_PrintsThePriceUnitAndEscapesIt (HH-206): the rendered invoice uses the label
// rule at the unit price cell, and the label goes through the HTML escape. A line priced per SF and
// sold in PC must not read as a per-piece price; a price unit carrying markup must not inject it.
func TestRenderInvoiceHTML_PrintsThePriceUnitAndEscapesIt(t *testing.T) {
	d := InvoiceDetail{
		InvoiceNo: "INV-206",
		Customer:  CustomerBlock{Name: "Quinte", AccountNo: "CUST-1"},
		Lines: []DetailLine{
			{LineNo: 1, SKU: "A", Description: "Slab", QtyEntered: "25", UOMEntered: "PC", PriceUOM: "SF", UnitPriceCents: 1476, LineTotalCents: 36900},
			{LineNo: 2, SKU: "B", Description: "Stone", QtyEntered: "2", UOMEntered: "T", PriceUOM: "T", UnitPriceCents: 1800, LineTotalCents: 3600},
			{LineNo: 3, SKU: "C", Description: "Odd", QtyEntered: "1", UOMEntered: "EA", PriceUOM: "<b>", UnitPriceCents: 500, LineTotalCents: 500},
		},
	}
	html := renderInvoiceHTML(d, "x")
	if !strings.Contains(html, "$14.76 / SF") {
		t.Fatalf("the per-SF unit price on a line sold in PC must print its unit; got:\n%s", html)
	}
	if strings.Contains(html, "$18.00 /") {
		t.Fatal("a line priced and sold in the same unit must print its price without a unit suffix")
	}
	if !strings.Contains(html, "$5.00 / &lt;b&gt;") || strings.Contains(html, "<b>") {
		t.Fatal("the price unit must be HTML escaped, never injected")
	}
}
