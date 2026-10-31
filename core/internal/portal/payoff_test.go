package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// quoteStore answers the payable the pay flow charges on: the payoff, the discount and its day.
type quoteStore struct {
	fakePaymentStore
	quote []payments.Allocation
}

func (q *quoteStore) PayableInvoices(context.Context, string, []string) ([]payments.Allocation, error) {
	return q.quote, nil
}

// TestListInvoices_OpenRowsCarryThePayoffAndTheDiscount: the portal list shows, on an open invoice,
// what a payment today would charge, the discount inside it and the last day it runs (TM-2b). An
// invoice outside the window carries the payoff and no discount fields; a paid one carries none.
func TestListInvoices_OpenRowsCarryThePayoffAndTheDiscount(t *testing.T) {
	bs := &fakeBillingStore{invoice: billing.Invoice{ID: "inv-1", Status: "FINALIZED", TotalCents: 100000, BalanceCents: 100000}}
	ps := &quoteStore{quote: []payments.Allocation{{
		InvoiceID: "inv-1", AmountCents: 98000, DiscountCents: 2000, DiscountDay: "2026-03-10", Status: "FINALIZED",
	}}}
	h := NewHandler(nil, nil).WithBilling(billing.NewService(bs)).WithPayments(payments.NewService(ps, payments.NewStubGateway()))

	w := httptest.NewRecorder()
	h.listInvoices(w, reqWithCaller(http.MethodGet, "/api/v1/tenant/invoices", "", tenantA))
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", w.Code, w.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("decode the page: %v %s", err, w.Body.String())
	}
	row := page.Items[0]
	if row["payoff_cents"] != float64(98000) || row["discount_cents"] != float64(2000) || row["discount_by"] != "2026-03-10" {
		t.Errorf("want payoff 98000, discount 2000, by 2026-03-10; got %v", row)
	}

	// Outside the window: the payoff is the open amount, with no discount fields.
	ps.quote = []payments.Allocation{{InvoiceID: "inv-1", AmountCents: 100000, Status: "FINALIZED"}}
	w = httptest.NewRecorder()
	h.listInvoices(w, reqWithCaller(http.MethodGet, "/api/v1/tenant/invoices", "", tenantA))
	page.Items = nil
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	row = page.Items[0]
	if row["payoff_cents"] != float64(100000) {
		t.Errorf("outside the window the payoff is the open amount; got %v", row)
	}
	if _, has := row["discount_cents"]; has {
		t.Errorf("no discount_cents outside the window; got %v", row)
	}
	if _, has := row["discount_by"]; has {
		t.Errorf("no discount_by outside the window; got %v", row)
	}

	// A paid invoice is not quoted.
	bs.invoice.Status = "PAID"
	w = httptest.NewRecorder()
	h.listInvoices(w, reqWithCaller(http.MethodGet, "/api/v1/tenant/invoices", "", tenantA))
	page.Items = nil
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if _, has := page.Items[0]["payoff_cents"]; has {
		t.Errorf("a paid invoice carries no payoff; got %v", page.Items[0])
	}
}
