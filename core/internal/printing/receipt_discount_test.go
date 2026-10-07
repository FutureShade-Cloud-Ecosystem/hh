package printing

import (
	"os"
	"path/filepath"
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
		for _, want := range []string{"Discount approved by Sales Manager", "$10.00 off", "Discount approved by Branch Manager", "-$7.00"} {
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

// The paper must add up for the person holding it: each line prints at its price before the ticket
// discount (a line's own discount shown on that line), then the ticket discount row, then a
// Subtotal equal to the lines less the ticket discount. Stored line totals are net of the ticket
// discount's share, so the service adds the share back for the printed line.
func TestReceipt_LinesAddUpToTheSubtotalBeforeAndAfterTheTicketDiscount(t *testing.T) {
	qs := &fakeQueueStore{markFind: true, markUpd: true}
	store := &fakeReceiptStore{found: true,
		sale: receiptSale{
			OrderID: saleOrderID, OrderNo: "#SO-9", BranchID: trentonID, InvoiceNo: "#INV-9",
			Tender: "CASH", SaleType: "COLLECT_NOW", Status: "COMMITTED",
			// 208 + 394 + 1453 = 2055 stored; the ticket discount of 300 was shared 20 + 40 + 240 = 300.
			// Lines before the ticket discount: 228 + 434 + 1693 = 2355; subtotal 2055.
			SubtotalCents: 2055, TaxCents: 267, TotalCents: 2322,
			SaleDiscountCents: 300, SaleDiscountLabel: "Branch Manager",
			SoldAt: time.Now(),
		},
		lines: []ReceiptLine{
			{LineNo: 1, Description: "A", Qty: 1, UOM: "PC", UnitPriceCents: 228, LineTotalCents: 208, SaleDiscountShareCents: 20},
			{LineNo: 2, Description: "B", Qty: 2, UOM: "PC", UnitPriceCents: 241, LineTotalCents: 394, SaleDiscountShareCents: 40,
				DiscountCents: 48, DiscountApprovedByLabel: "Sales Manager"},
			{LineNo: 3, Description: "C", Qty: 7, UOM: "PC", UnitPriceCents: 242, LineTotalCents: 1453, SaleDiscountShareCents: 240},
		}}
	svc := NewReceiptService(store, NewQueueService(qs))
	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	var lines int64
	for _, l := range rec.Lines {
		lines += l.LineTotalCents
	}
	if lines != 2355 {
		t.Fatalf("the printed lines must be priced before the ticket discount (2355), got %d", lines)
	}
	if lines-rec.SaleDiscountCents != rec.SubtotalCents {
		t.Fatalf("lines %d less the ticket discount %d must equal the subtotal %d", lines, rec.SaleDiscountCents, rec.SubtotalCents)
	}
	if rec.SubtotalCents+rec.TaxCents != rec.TotalCents {
		t.Fatalf("subtotal and tax must make the total")
	}
	// For the review: RECEIPT_HTML_DIR, when set, also writes the roll and the page document.
	if dir := os.Getenv("RECEIPT_HTML_DIR"); dir != "" {
		rec.Tenders = []ReceiptTender{{Label: "CASH", AmountCents: rec.TotalCents}}
		rec.TaxLabel, rec.OrgName, rec.BranchName = "HST (13%)", "Dibbits Landscape Supply", "Trenton"
		rec.Lines[0].Description, rec.Lines[1].Description, rec.Lines[2].Description = "Holland Stone 60 mm", "Edge Restraint 8 ft", "Polymeric Sand 50 lb"
		for name, render := range map[string]func(Receipt) (string, error){"roll": RenderReceiptHTML, "page": RenderReceiptPageHTML} {
			html, err := render(rec)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "receipt-"+name+".html"), []byte(html), 0o644); err != nil {
				t.Fatalf("%v", err)
			}
		}
	}
}
