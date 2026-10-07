package printing

import (
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

func noticeReceipt(notice string) Receipt {
	return Receipt{
		OrderNo:        "#SO-1",
		Lines:          []ReceiptLine{{LineNo: 1, Description: "Paver", Qty: 2, UOM: "EA", LineTotalCents: 100}},
		Tenders:        []ReceiptTender{{Label: "CASH", AmountCents: 100}},
		TaxLabel:       "HST (13%)",
		SoldAt:         time.Now(),
		PrintedAt:      time.Now(),
		QuantityNotice: notice,
	}
}

// Every order's printed receipt, the roll and the full page, carries the dealer's quantity notice after
// the lines and before the totals; a notice with markup is escaped, and none given prints none.
func TestReceiptsCarryTheQuantityNotice(t *testing.T) {
	const words = "Quantities are yours to confirm & check."
	for name, render := range map[string]func(Receipt) (string, error){
		"roll": RenderReceiptHTML,
		"page": RenderReceiptPageHTML,
	} {
		t.Run(name, func(t *testing.T) {
			html, err := render(noticeReceipt(words))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(html, "Quantities are yours to confirm &amp; check.") {
				t.Fatalf("the notice must be on the page, escaped")
			}
			at, sub := strings.Index(html, `class="qty-notice"`), strings.Index(html, "Subtotal")
			if at < 0 || sub < 0 || at > sub {
				t.Errorf("the notice must sit after the lines and before the totals (notice %d, subtotal %d)", at, sub)
			}
			bare, err := render(noticeReceipt(""))
			if err != nil {
				t.Fatalf("render bare: %v", err)
			}
			if strings.Contains(bare, `class="qty-notice"`) {
				t.Error("a receipt with no notice must print none")
			}
		})
	}
}

// The printing module's fallback is the quote module's house wording, one sentence.
func TestDefaultQuantityNoticeIsTheQuoteModulesWording(t *testing.T) {
	if defaultQuantityNotice != quote.DefaultQuantityNotice {
		t.Errorf("printing default %q differs from quote's %q", defaultQuantityNotice, quote.DefaultQuantityNotice)
	}
}

// A long unbroken notice (a 500 character run, a URL) must wrap inside the paper, never widen it.
func TestQuantityNoticeWrapsLongUnbrokenText(t *testing.T) {
	for name, render := range map[string]func(Receipt) (string, error){
		"roll": RenderReceiptHTML,
		"page": RenderReceiptPageHTML,
	} {
		t.Run(name, func(t *testing.T) {
			html, err := render(noticeReceipt(strings.Repeat("x", 500)))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			i := strings.Index(html, ".qty-notice {")
			if i < 0 {
				t.Fatal("no .qty-notice rule")
			}
			rule := html[i : i+strings.Index(html[i:], "}")]
			if !strings.Contains(rule, "overflow-wrap: anywhere") {
				t.Errorf("the notice rule must wrap anywhere: %q", rule)
			}
		})
	}
}
