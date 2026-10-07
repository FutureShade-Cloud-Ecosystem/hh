package printing

import (
	"strings"
	"testing"
	"time"
)

// A discounted sale's receipt, roll and page both, says who approved each discount by the role
// label of that login, never a name, and a sale with no discount prints no such row.
func TestReceipt_NamesTheRoleThatApprovedEachDiscount(t *testing.T) {
	rec := Receipt{
		OrderNo: "#SO-9", TaxLabel: "HST (13%)", SoldAt: time.Now(), PrintedAt: time.Now(),
		Lines: []ReceiptLine{
			{LineNo: 1, Description: "Paver", Qty: 100, UOM: "SF", LineTotalCents: 9000,
				DiscountCents: 1000, DiscountApprovedByLabel: "Sales Manager"},
			{LineNo: 2, Description: "Sand", Qty: 1, UOM: "EA", LineTotalCents: 5000},
		},
		SaleDiscountCents: 700, SaleDiscountApprovedByLabel: "Branch Manager",
		SubtotalCents: 13300, TaxCents: 1729, TotalCents: 15029,
		Tenders: []ReceiptTender{{Label: "CASH", AmountCents: 15029}},
	}
	for name, render := range map[string]func(Receipt) (string, error){"roll": RenderReceiptHTML, "page": RenderReceiptPageHTML} {
		html, err := render(rec)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, want := range []string{"Discount approved by Sales Manager", "$10.00 off", "Discount approved by Branch Manager", "$7.00 off"} {
			if !strings.Contains(html, want) {
				t.Errorf("%s receipt lacks %q", name, want)
			}
		}
		if got := strings.Count(html, "Discount approved by"); got != 2 {
			t.Errorf("%s receipt prints %d approval rows, want 2", name, got)
		}
	}
	rec.Lines[0].DiscountCents, rec.Lines[0].DiscountApprovedByLabel = 0, ""
	rec.SaleDiscountCents, rec.SaleDiscountApprovedByLabel = 0, ""
	html, _ := RenderReceiptHTML(rec)
	if strings.Contains(html, "Discount approved by") {
		t.Error("a sale with no discount must print no approval row")
	}
}
