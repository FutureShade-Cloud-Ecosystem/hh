package portal

// pay_expected_test.go: row 0.50. The portal pay route needs the amount the Pay button showed, and the
// refusal of a changed amount carries a machine code beside its words.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// countingQuoteStore counts every call that could precede a charge: a quote, a claim of the method,
// a settle. A refused request must leave all three at zero.
type countingQuoteStore struct {
	quoteStore
	quotes, methods, settles, claims int
}

// The intent seam the pay flow insists on; a claim here is a step toward a charge, so it is counted.
func (c *countingQuoteStore) ClaimIntent(context.Context, payments.IntentClaim) (payments.Intent, bool, error) {
	c.claims++
	return payments.Intent{ID: "intent-1"}, true, nil
}
func (c *countingQuoteStore) GetIntentByKey(context.Context, string) (payments.Intent, bool, error) {
	return payments.Intent{}, false, nil
}
func (c *countingQuoteStore) StampIntentRef(context.Context, string, string, string) error {
	return nil
}
func (c *countingQuoteStore) FailIntent(context.Context, string, string) error { return nil }
func (c *countingQuoteStore) PayWithIntent(context.Context, string, string, string, []payments.Allocation, string) (payments.Payment, error) {
	c.settles++
	return payments.Payment{}, nil
}
func (c *countingQuoteStore) GetPaymentByID(context.Context, string, string) (payments.Payment, bool, error) {
	return payments.Payment{}, false, nil
}

func (c *countingQuoteStore) PayableInvoices(ctx context.Context, cust string, ids []string) ([]payments.Allocation, error) {
	c.quotes++
	return c.quoteStore.PayableInvoices(ctx, cust, ids)
}

func (c *countingQuoteStore) GetMethod(ctx context.Context, cust, id string) (payments.PaymentMethod, error) {
	c.methods++
	return c.quoteStore.GetMethod(ctx, cust, id)
}

func (c *countingQuoteStore) Pay(ctx context.Context, a, b, d string, al []payments.Allocation) (payments.Payment, error) {
	c.settles++
	return c.quoteStore.Pay(ctx, a, b, d, al)
}

func payHandler(ps *countingQuoteStore) *Handler {
	return NewHandler(nil, nil).WithPayments(payments.NewService(ps, payments.NewStubGateway()))
}

func postPay(h *Handler, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.createPayment(w, reqWithCaller(http.MethodPost, "/api/v1/tenant/payments", body, tenantA))
	return w
}

// An omitted, zero or negative expected_cents is refused in words (400) before anything is quoted,
// claimed or charged.
func TestCreatePayment_TheShownAmountIsRequired(t *testing.T) {
	for name, body := range map[string]string{
		"omitted":  `{"invoice_ids":["inv-1"],"nonce":"n-1"}`,
		"zero":     `{"invoice_ids":["inv-1"],"nonce":"n-1","expected_cents":0}`,
		"negative": `{"invoice_ids":["inv-1"],"nonce":"n-1","expected_cents":-5}`,
	} {
		t.Run(name, func(t *testing.T) {
			ps := &countingQuoteStore{quoteStore: quoteStore{quote: []payments.Allocation{{
				InvoiceID: "inv-1", AmountCents: 100000, Status: "FINALIZED",
			}}}}
			w := postPay(payHandler(ps), body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("a pay without a positive expected_cents must be 400; got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "expected_cents") {
				t.Errorf("the refusal must say what is missing; got %s", w.Body.String())
			}
			if ps.quotes != 0 || ps.methods != 0 || ps.claims != 0 || ps.settles != 0 {
				t.Errorf("nothing may be quoted, claimed or settled; quotes %d methods %d claims %d settles %d", ps.quotes, ps.methods, ps.claims, ps.settles)
			}
		})
	}
}

// The 409 for a changed amount carries the machine code Pro Pay matches on, with the customer's words.
func TestCreatePayment_AChangedAmountCarriesAMachineCode(t *testing.T) {
	ps := &countingQuoteStore{quoteStore: quoteStore{quote: []payments.Allocation{{
		InvoiceID: "inv-1", AmountCents: 100000, Status: "FINALIZED",
	}}}}
	w := postPay(payHandler(ps), `{"invoice_ids":["inv-1"],"nonce":"n-1","expected_cents":98000}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("a changed amount is 409; got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "quote_changed" {
		t.Errorf(`the body must carry "code": "quote_changed"; got %v`, body)
	}
	if s, _ := body["error"].(string); !strings.Contains(s, "amount to pay has changed") {
		t.Errorf("the customer's words stay; got %v", body["error"])
	}
	if ps.claims != 0 || ps.settles != 0 {
		t.Errorf("nothing claimed or settled; claims %d settles %d", ps.claims, ps.settles)
	}
}
