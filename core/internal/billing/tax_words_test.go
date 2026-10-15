package billing

// tax_words_test.go, the printed invoice's tax row and the credit memo's reversing tax leg name the
// dealer's tax in the dealer's word (item 3b): "HST" exactly as before for an Ontario dealer,
// "Sales tax" for a California one. Amounts, roles and sides do not move with the word.

import (
	"strings"
	"testing"
)

func TestInvoiceDocumentNamesTheTaxByTheDealersWord(t *testing.T) {
	d := InvoiceDetail{InvoiceNo: "INV-1"}
	d.Totals.TaxCents = 775
	if html := renderInvoiceHTML(d, "Yard"); !strings.Contains(html, "<tr><td>HST</td>") {
		t.Fatalf("the Ontario document lost its HST row")
	}
	html := renderInvoiceHTMLWithTax(d, "Yard", "Sales tax")
	if !strings.Contains(html, "<tr><td>Sales tax</td>") || strings.Contains(html, "<td>HST</td>") {
		t.Fatalf("the California document must name Sales tax and no HST row")
	}
}

func TestCreditMemoTaxLegMemoFollowsTheWord(t *testing.T) {
	j := CreditMemoJournal{RevenueCents: 10000, TaxCents: 775, ARCents: 10775}
	memo := func(legs []JournalLeg) string {
		for _, l := range legs {
			if l.Role == RoleTax {
				return l.Memo
			}
		}
		return ""
	}
	if got := memo(j.ReversingLegs()); got != "HST reversed" {
		t.Errorf("ontario memo %q", got)
	}
	cal := j.ReversingLegsFor("Sales tax")
	if got := memo(cal); got != "Sales tax reversed" {
		t.Errorf("california memo %q", got)
	}
	for _, l := range cal {
		if l.Role == RoleTax && (l.DebitCents != 775 || l.CreditCents != 0) {
			t.Errorf("the tax leg's amount moved with the word: %+v", l)
		}
	}
}
