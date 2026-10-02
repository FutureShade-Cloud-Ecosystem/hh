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

// The simulated card word: a card sale rung on the demo box's simulated rail must not print paper
// that reads like a live card charge. The till's capture stub mints gateway refs with a
// recognizable prefix, the repository stamps ReceiptTender.Simulated from the row's own ref, and
// the template carries the word once, inside the tender line. The guard legs hold the other
// direction: a live gateway card charge prints no simulated word anywhere, and the word is the
// ONLY delta between the two documents, so a receipt with no simulated card rows renders
// byte-identically to the flag-less document.
func TestRenderReceiptHTML_SimulatedCardWord(t *testing.T) {
	receipt := func(simulated bool) Receipt {
		return Receipt{
			OrderNo:   "#SO-42",
			TaxLabel:  "HST (13%)",
			SoldAt:    time.Now(),
			PrintedAt: time.Now(),
			Tenders: []ReceiptTender{{
				Method: "CARD", Label: "CARD · VISA", AmountCents: 13944,
				Ref:       "PMT-000007",
				Simulated: simulated,
			}},
		}
	}
	const word = "Simulated card: nothing reached a processor."
	const addition = ` <span class="muted">` + word + `</span>`

	simHTML, err := RenderReceiptHTML(receipt(true))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	liveHTML, err := RenderReceiptHTML(receipt(false))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if n := strings.Count(simHTML, word); n != 1 {
		t.Fatalf("the simulated card sale's receipt must carry the word exactly once, got %d", n)
	}
	// Inside the tender line, not a page-level banner: the word belongs to the row it describes.
	table := strings.Index(simHTML, `<table class="tenders">`)
	if table < 0 {
		t.Fatal("the tenders table is missing from the document")
	}
	end := strings.Index(simHTML[table:], "</table>") + table
	at := strings.Index(simHTML, word)
	if at < table || at > end {
		t.Fatalf("the word must render inside the tender line, found at %d, the tenders table spans %d..%d", at, table, end)
	}
	if !strings.Contains(simHTML, addition) {
		t.Fatalf("the word must render in the house muted span idiom beside the label, %q not found", addition)
	}
	if strings.Contains(liveHTML, "Simulated card") {
		t.Fatal("a live gateway card charge must print no simulated word anywhere on the receipt")
	}
	// The strongest form of "no flag, no word": strip the word span from the simulated document and
	// the bytes must equal the live document exactly.
	if strings.Replace(simHTML, addition, "", 1) != liveHTML {
		t.Fatal("the word must be the only delta: the same receipt without the flag must render byte-identically to the live document")
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
