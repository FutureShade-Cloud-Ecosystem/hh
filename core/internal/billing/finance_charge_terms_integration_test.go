//go:build integration

// finance_charge_terms_integration_test.go, wave 3 FC-1 fix round (the Opus read of PR 748).
package billing

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// P3-1: a charge for a customer on 2/10 Net 30 carries no discount date, and is due on issue.
func TestIntegration_W3FC1Fix_AChargeCarriesNoEarlyPaymentDiscount(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "DISC")
	r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "DISC")
	w := NewInvoiceWriter(r.pool).WithTerms(termsReaderStub{term: customer.Terms{
		Code: customer.Terms2PctNet30, Name: "2/10 net 30", NetDays: 30, DiscountBps: 200, DiscountDays: 10,
	}})
	chargeID := r.chargeFor(w, cust)

	var discount, termsCode *string
	var issue, due string
	if err := r.pool.QueryRow(r.ctx, `
		SELECT discount_date::text, terms_code, issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
		chargeID).Scan(&discount, &termsCode, &issue, &due); err != nil {
		t.Fatalf("read the charge document: %v", err)
	}
	if discount != nil {
		t.Fatalf("the charge carries a discount date %s, want none", *discount)
	}
	if issue != due {
		t.Fatalf("the charge is issued %s and due %s, want due on issue", issue, due)
	}
	if termsCode == nil || *termsCode != customer.TermsReceipt {
		t.Fatalf("the charge carries terms code %v, want %s", termsCode, customer.TermsReceipt)
	}
}
