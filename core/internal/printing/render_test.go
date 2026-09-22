package printing

import (
	"strings"
	"testing"
	"time"
)

func TestMoney(t *testing.T) {
	for _, tc := range []struct {
		cents int64
		want  string
	}{
		{0, "$0.00"}, {5, "$0.05"}, {1234, "$12.34"}, {100000, "$1,000.00"},
		{123456789, "$1,234,567.89"}, {-1250, "-$12.50"},
	} {
		if got := Money(tc.cents); got != tc.want {
			t.Errorf("Money(%d) = %q, want %q", tc.cents, got, tc.want)
		}
	}
}

func TestQty_NoTrailingZeroNoise(t *testing.T) {
	for _, tc := range []struct {
		q    float64
		want string
	}{{10, "10"}, {2.5, "2.5"}, {0.125, "0.125"}} {
		if got := Qty(tc.q); got != tc.want {
			t.Errorf("Qty(%v) = %q, want %q", tc.q, got, tc.want)
		}
	}
}

// The receipt is rendered with html/template, not string concatenation: a product description or a
// customer name carrying markup is customer-supplied text landing inside a document, and it must be
// escaped rather than executed.
func TestRenderReceiptHTML_EscapesSuppliedText(t *testing.T) {
	rec := Receipt{
		OrderNo:      "#SO-1",
		CustomerName: `<script>alert(1)</script>`,
		Lines:        []ReceiptLine{{LineNo: 1, Description: `Paver "A" & <b>B</b>`, Qty: 1, UOM: "EA", LineTotalCents: 100}},
		Tenders:      []ReceiptTender{{Label: "CASH", AmountCents: 100}},
		TaxLabel:     "HST (13%)",
		SoldAt:       time.Now(),
		PrintedAt:    time.Now(),
	}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("supplied text must be escaped, never rendered as markup")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("the escaped form must still be visible on the page")
	}
	if strings.Contains(html, "<b>B</b>") {
		t.Fatal("a line description must not inject markup")
	}
}

// An original receipt carries NO reprint marker, stamping REPRINT on the customer's first copy is
// as wrong as omitting it on their second.
func TestRenderReceiptHTML_OriginalHasNoReprintMarker(t *testing.T) {
	rec := Receipt{OrderNo: "#SO-7", TaxLabel: "HST (13%)", SoldAt: time.Now(), PrintedAt: time.Now()}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "REPRINT") {
		t.Fatal("an original receipt must not carry the REPRINT marker")
	}
	if !strings.Contains(html, "#SO-7") {
		t.Fatal("the #SO number is the receipt's headline, it must be on the page")
	}
	if !strings.Contains(html, "@page") || !strings.Contains(html, "@media print") {
		t.Fatal("the receipt must ship its own print CSS, it is printed, not browsed")
	}
}

// DIB-456 / D-078, the receipt is a printed artifact that leaves the building, so the product mark
// on it is checked rather than assumed.
//
// The two assertions here are a pair and neither is redundant: the mark must be PRESENT (a rebrand
// that misses the receipt is a rebrand a customer catches), and the DEALER's name must still be the
// header (a rebrand that replaces the dealer's name with the software's is worse than doing
// nothing). The receipt says who sold you the stone; it mentions who wrote the till in a footnote.
func TestRenderReceiptHTML_CarriesTheProductMarkWithoutDisplacingTheDealer(t *testing.T) {
	// The timestamps are the real clock and not a pinned date on purpose: nothing here asserts
	// anything about time, and a fixed calendar date in a file that also reads time.Now() is the
	// dated-time-bomb shape internal/testaudit/dated_tests_test.go (DIB-427) exists to refuse.
	html, err := RenderReceiptHTML(Receipt{
		OrgName:    "Dibbits Landscape Supply",
		BranchName: "Trenton",
		OrderNo:    "SO-TRT-4182",
		SoldAt:     time.Now(),
		PrintedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(html, "HH Yard · a Hardscape House product") {
		t.Error("the receipt no longer carries the HH Yard product mark in its foot")
	}
	if strings.Contains(html, "HardscapeOS") {
		t.Error("the receipt still prints the old product name")
	}

	// The dealer owns the top of the page. Asserting the ORDER, not just the presence, is what makes
	// this a statement about the document's hierarchy rather than about two strings both existing.
	dealer := strings.Index(html, "Dibbits Landscape Supply")
	mark := strings.Index(html, "HH Yard · a Hardscape House product")
	if dealer < 0 {
		t.Fatal("the receipt no longer names the dealer at all")
	}
	if mark < dealer {
		t.Error("the product mark is printing ABOVE the dealer's name, the header belongs to the dealer")
	}
}
