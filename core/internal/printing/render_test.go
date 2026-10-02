package printing

import (
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
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

// The sale type words: the customer's paper is not the codebase's vocabulary. Each pos sale type
// (core/internal/pos/model.go) renders as the plain words true to what that sale is, inside the
// same muted line, and the raw constant never prints. The words carry no payment claim (an ACCOUNT
// tender can ride any sale type, so "paid" is not the paper's to assert) and no time-relative word
// (a reprint must read as true as the first copy).
func TestRenderReceiptHTML_SaleTypeRendersPlainWords(t *testing.T) {
	for _, tc := range []struct {
		saleType string
		words    string
	}{
		{pos.SaleCollectNow, "Collected at the counter"},
		{pos.SaleCollectLater, "For later collection"},
		{pos.SaleDelivered, "Delivery"},
		{pos.SaleCOD, "Invoice issued, payment before fulfilment"},
	} {
		html, err := RenderReceiptHTML(Receipt{
			OrderNo: "#SO-1", SaleType: tc.saleType,
			TaxLabel: "HST (13%)", SoldAt: time.Now(), PrintedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("render %q: %v", tc.saleType, err)
		}
		if !strings.Contains(html, `<div class="muted">`+tc.words+`</div>`) {
			t.Fatalf("sale type %q must render the plain words in the muted line, %q not found", tc.saleType, tc.words)
		}
		if strings.Contains(html, tc.saleType) {
			t.Fatalf("sale type %q must never print the raw constant on the receipt", tc.saleType)
		}
	}
}

// An unknown sale type renders the raw value itself, in the muted line. Nothing
// is hidden and nothing is guessed, a value the mapping does not know is the customer's own data on
// the paper, not a blank and not invented words.
func TestRenderReceiptHTML_UnknownSaleTypeRendersRaw(t *testing.T) {
	html, err := RenderReceiptHTML(Receipt{
		OrderNo: "#SO-2", SaleType: "TELEPORT",
		TaxLabel: "HST (13%)", SoldAt: time.Now(), PrintedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, `<div class="muted">TELEPORT</div>`) {
		t.Fatal("an unknown sale type must render the raw value, muted, verbatim")
	}
}

// An empty sale type renders no line at all: the template's if stands, so no empty muted div and
// no placeholder words are invented for a sale that carries no sale type.
func TestRenderReceiptHTML_EmptySaleTypeRendersNoLine(t *testing.T) {
	html, err := RenderReceiptHTML(Receipt{
		OrderNo: "#SO-3", SaleType: "",
		TaxLabel: "HST (13%)", SoldAt: time.Now(), PrintedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, word := range []string{
		`<div class="muted"></div>`,
		"Collected at the counter", "For later collection", "Delivery", "Invoice issued, payment before fulfilment",
	} {
		if strings.Contains(html, word) {
			t.Fatalf("a receipt with no sale type must render no sale type line at all, found %q", word)
		}
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

// THE FULFILMENT IN WORDS (row 666 item 1): the paper speaks to the customer, so the sale type
// renders as the plain words a counter person says, never as the raw constant the wire carries.
// The map is keyed on pos's own constants, so a constant rename cannot leave a stale word behind:
// the four known values map, an unknown value passes through verbatim (a future sale type must not
// print as an empty line), and an empty value renders no line at all.
func TestRenderReceiptHTML_FulfilmentInWords(t *testing.T) {
	for _, tc := range []struct {
		saleType string
		want     string
		notWant  string
	}{
		{"COLLECT_NOW", "Collected at the counter", "COLLECT_NOW"},
		{"COLLECT_LATER", "For later collection", "COLLECT_LATER"},
		{"DELIVERED", "Delivery", "DELIVERED"},
		{"COD", "Invoice issued, payment before fulfilment", "COD"},
	} {
		html, err := RenderReceiptHTML(Receipt{
			OrderNo: "#SO-1", SaleType: tc.saleType, TaxLabel: "HST (13%)",
			SoldAt: time.Now(), PrintedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("render %s: %v", tc.saleType, err)
		}
		if !strings.Contains(html, tc.want) {
			t.Errorf("sale type %s must print as %q, the words are not on the page", tc.saleType, tc.want)
		}
		if strings.Contains(html, tc.notWant) {
			t.Errorf("the raw constant %q must never reach the paper", tc.notWant)
		}
	}

	// An unknown value passes through verbatim: a sale type this build does not know must still
	// print as itself rather than silently vanish.
	unknown := "WILL_CALL_GATE_2"
	html, err := RenderReceiptHTML(Receipt{
		OrderNo: "#SO-2", SaleType: unknown, SoldAt: time.Now(), PrintedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("render unknown: %v", err)
	}
	if !strings.Contains(html, unknown) {
		t.Errorf("an unknown sale type must pass through verbatim, %q is not on the page", unknown)
	}

	// An empty sale type renders no fulfilment line: the absence is quiet, never a placeholder.
	empty, err := RenderReceiptHTML(Receipt{
		OrderNo: "#SO-3", SaleType: "", SoldAt: time.Now(), PrintedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("render empty: %v", err)
	}
	if strings.Contains(empty, "Collected at the counter") || strings.Contains(empty, "fulfilment") {
		t.Error("an empty sale type must render no fulfilment line at all")
	}
}

// THE CHANGE DUE ROW (row 666 item 1): a cash over-tender's change is on the paper, carried by the
// committed sale (the order's own cash-tendered fact), and the row is absent when no change is due:
// a zero or a negative must not print a row that tells the cashier to hand money back.
func TestRenderReceiptHTML_ChangeDueRow(t *testing.T) {
	over := Receipt{
		OrderNo: "#SO-10", TaxLabel: "HST (13%)", SoldAt: time.Now(), PrintedAt: time.Now(),
		ChangeDueCents: 1056,
		Tenders:        []ReceiptTender{{Method: "CASH", Label: "CASH", AmountCents: 15000, Ref: "PMT-1"}},
	}
	html, err := RenderReceiptHTML(over)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "Change due") {
		t.Fatal("an over-tendered cash sale must carry the Change due row")
	}
	if !strings.Contains(html, Money(1056)) {
		t.Fatalf("the change due figure %s must be on the page", Money(1056))
	}
	// The row lives in the tenders table, the cash conversation's last word, not as a stray figure.
	table := strings.Index(html, `<table class="tenders">`)
	end := strings.Index(html[table:], "</table>") + table
	at := strings.Index(html, "Change due")
	if at < table || at > end {
		t.Fatalf("the Change due row must sit in the tenders table (spans %d..%d), found at %d", table, end, at)
	}

	exact := over
	exact.ChangeDueCents = 0
	exactHTML, err := RenderReceiptHTML(exact)
	if err != nil {
		t.Fatalf("render exact: %v", err)
	}
	if strings.Contains(exactHTML, "Change due") {
		t.Fatal("a tendered-exact sale must print no Change due row")
	}
}

// THE DEPOSIT AND BALANCE DUE ROWS (row 666 item 1): an unpaid-at-ring sale taken with a deposit
// states the deposit taken at the counter and the balance that remains, both absent when they do
// not apply: a fully paid sale prints neither, and an unsettled account sale prints the balance
// alone (no deposit was taken, and a invented one would be a lie on paper).
func TestRenderReceiptHTML_DepositAndBalanceRows(t *testing.T) {
	deposit := Receipt{
		OrderNo: "#SO-11", TaxLabel: "HST (13%)", SoldAt: time.Now(), PrintedAt: time.Now(),
		TotalCents:   10000,
		DepositCents: 4000,
		BalanceCents: 6000,
		Tenders:      []ReceiptTender{{Method: "CASH", Label: "CASH", AmountCents: 4000, Ref: "PMT-2"}},
	}
	html, err := RenderReceiptHTML(deposit)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "Deposit") {
		t.Fatal("a sale taken with a deposit must carry the Deposit row")
	}
	if !strings.Contains(html, Money(4000)) {
		t.Fatalf("the deposit figure %s must be on the page", Money(4000))
	}
	if !strings.Contains(html, "Balance due") || !strings.Contains(html, Money(6000)) {
		t.Fatal("the balance due row must carry the invoice's own balance")
	}
	// The paper reads in one line: of the total, the deposit came off it, the balance remains.
	if strings.Index(html, "Deposit") > strings.Index(html, "Balance due") {
		t.Error("the Deposit row must sit above the Balance due row: total, deposit, balance is the readable order")
	}

	paid := deposit
	paid.DepositCents = 0
	paid.BalanceCents = 0
	paidHTML, err := RenderReceiptHTML(paid)
	if err != nil {
		t.Fatalf("render paid: %v", err)
	}
	if strings.Contains(paidHTML, "Deposit") || strings.Contains(paidHTML, "Balance due") {
		t.Fatal("a fully paid sale must print neither a Deposit nor a Balance due row")
	}

	unsettled := deposit
	unsettled.DepositCents = 0
	unsettledHTML, err := RenderReceiptHTML(unsettled)
	if err != nil {
		t.Fatalf("render unsettled: %v", err)
	}
	if strings.Contains(unsettledHTML, "Deposit") {
		t.Fatal("an account sale with no deposit taken must print no Deposit row")
	}
	if !strings.Contains(unsettledHTML, "Balance due") {
		t.Fatal("an unsettled account sale still carries its Balance due row")
	}
}
