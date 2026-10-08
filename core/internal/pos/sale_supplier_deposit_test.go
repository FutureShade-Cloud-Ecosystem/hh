package pos

// sale_supplier_deposit_test.go, sub item 9.5 (the row 1047 deposits work):
// the supplier's demanded deposit on a special order, billed to a credit
// customer's account. Unit legs over the harness; the SQL (the two invoices,
// the GL journal, the subledger) is proven against the real database in
// sale_supplier_deposit_integration_test.go and gl's own suite.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// supplierHarness is one credit customer's on-account ring: the shape the
// deposit may ride. The harness's one-line ticket prices at a 10000 subtotal
// and a 1300 tax (11300 total).
func supplierHarness() *harness {
	return depositHarness(30, 500_000)
}

func supplierInput() SaleInput {
	in := depositSaleInput()
	in.SaleType = SaleCollectLater
	in.Tender = TenderAccount
	in.SupplierDepositCents = 4000
	return in
}

// Default no deposit: a plain credit sale answers no supplier deposit fields
// and writes ONE invoice, exactly as before.
func TestSale_SupplierDeposit_DefaultIsNone(t *testing.T) {
	h := supplierHarness()
	in := supplierInput()
	in.SupplierDepositCents = 0
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.SupplierDepositCents != 0 || out.SupplierDepositInvoiceID != "" || out.SupplierDepositInvoiceNo != "" {
		t.Fatalf("a plain sale answered supplier deposit fields: %+v", out)
	}
	if len(h.store.invoices) != 1 {
		t.Fatalf("a plain sale wrote %d invoices, want one", len(h.store.invoices))
	}
}

// The marked deposit is billed for a credit customer: its own FINALIZED
// invoice for the deposit amount, the sale's own invoice for the rest (the
// goods lines plus the line striking the deposit off, summing to the rest's
// subtotal), the subledger's two INVOICE rows, the order's metadata, and the
// ar.invoice.finalized event for BOTH documents.
func TestSale_SupplierDeposit_BilledForACreditCustomer(t *testing.T) {
	h := supplierHarness()
	out, err := h.svc.Sale(context.Background(), supplierInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.SupplierDepositCents != 4000 || out.SupplierDepositInvoiceID == "" || out.SupplierDepositInvoiceNo == "" {
		t.Fatalf("the answer's supplier deposit facts: %+v", out)
	}
	if len(h.store.invoices) != 2 {
		t.Fatalf("the sale wrote %d invoices, want the rest's and the deposit's", len(h.store.invoices))
	}
	var restInv, depInv invoiceRow
	for _, inv := range h.store.invoices {
		switch inv.SourceKind {
		case SourceSupplierDeposit:
			depInv = inv
		default:
			restInv = inv
		}
	}
	if depInv.Status != "FINALIZED" || depInv.TotalCents != 4000 || depInv.BalanceCents != 4000 || depInv.SubtotalCents != 4000 {
		t.Fatalf("the deposit invoice: %+v", depInv)
	}
	total := out.TotalCents
	if restInv.TotalCents != total-4000 || restInv.SubtotalCents != 10000-4000 || restInv.BalanceCents != total-4000 {
		t.Fatalf("the rest's invoice: %+v (sale total %d)", restInv, total)
	}
	// The subledger: the rest's INVOICE row then the deposit's, summing to the
	// sale's whole total.
	var invoiced int64
	var invoiceRows int
	for _, r := range h.store.custTxns {
		if r.TxnType == "INVOICE" {
			invoiceRows++
			invoiced += r.AmountCents
		}
	}
	if invoiceRows != 2 || invoiced != total {
		t.Fatalf("the subledger carries %d INVOICE rows summing %d, want two summing %d",
			invoiceRows, invoiced, total)
	}
	// Both documents tell the GL consumer they finalized.
	var finalized int
	for _, topic := range h.store.events {
		if topic == "ar.invoice.finalized" {
			finalized++
		}
	}
	if finalized != 2 {
		t.Fatalf("the sale emitted %d ar.invoice.finalized events, want one per document", finalized)
	}
	// The order carries the deposit fact.
	if h.store.orders[0].Metadata["supplier_deposit_cents"] != int64(4000) {
		t.Fatalf("the order's metadata: %v", h.store.orders[0].Metadata)
	}
}

// Refused for the shapes that are not a credit account's on-account ring, in
// words with a stable code: Cash On Demand (zero day terms), the walk in (no
// line), a pay before release ring, a counter deposit beside it, a split.
func TestSale_SupplierDeposit_RefusedForNonCreditShapes(t *testing.T) {
	cases := []struct {
		name string
		set  func(*SaleInput, *harness)
	}{
		{"a cash on demand account", func(in *SaleInput, h *harness) {
			h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", PaymentTermsDays: 0, CreditLimitCents: 500000}
		}},
		{"the walk in", func(in *SaleInput, h *harness) {
			in.CustomerID = ""
		}},
		{"a pay before release ring", func(in *SaleInput, h *harness) {
			h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR", PaymentTermsDays: 0, CreditLimitCents: 500000}
			in.SaleType = SaleCOD
			in.Fulfilment = "WILL_CALL"
			in.Tender = TenderCash
		}},
		{"a counter deposit beside it", func(in *SaleInput, h *harness) {
			in.DepositCents = 1000
			in.DepositMethod = TenderCash
		}},
		{"a split", func(in *SaleInput, h *harness) {
			in.Tenders = []TenderLegInput{{Method: TenderCash, AmountCents: 4000}, {Method: TenderAccount, AmountCents: 7300}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := supplierHarness()
			in := supplierInput()
			tc.set(&in, h)
			_, err := h.svc.Sale(context.Background(), in)
			var e ErrSupplierDepositNotCredit
			if !errors.As(err, &e) {
				t.Fatalf("want ErrSupplierDepositNotCredit, got %v", err)
			}
			if msg := supplierDepositNotCreditMessage(e); msg == "" || strings.Contains(msg, "cents") {
				t.Fatalf("the refusal's message must be plain words: %q", msg)
			}
			if len(h.store.orders) != 0 {
				t.Fatalf("a refused sale wrote an order")
			}
		})
	}
}

// Refused above the sale's pre-tax amount: the rest's invoice would carry a
// negative subtotal, and the deposit never exceeds the goods it secures.
func TestSale_SupplierDeposit_RefusedAboveThePreTaxAmount(t *testing.T) {
	h := supplierHarness()
	in := supplierInput()
	in.SupplierDepositCents = 10001 // the ticket's pre-tax amount is 10000
	_, err := h.svc.Sale(context.Background(), in)
	var e ErrSupplierDepositOverPretax
	if !errors.As(err, &e) {
		t.Fatalf("want ErrSupplierDepositOverPretax, got %v", err)
	}
	if e.DepositCents != 10001 || e.SubtotalCents != 10000 {
		t.Fatalf("the refusal's fields: %+v", e)
	}
	if msg := supplierDepositOverPretaxMessage(e); msg == "" || strings.Contains(msg, "cents") {
		t.Fatalf("the refusal's message must speak dollars: %q", msg)
	}
	if len(h.store.orders) != 0 {
		t.Fatalf("a refused sale wrote an order")
	}
}

// The preview says whether the counter may mark one, with the words its line
// prints: allowed for a clear credit account, never for the pay before
// release shapes, a held credit account (cash or card only) or the walk in.
func TestSalePreview_SupplierDepositAllowance(t *testing.T) {
	h := supplierHarness()
	in := SalePreviewInput{BranchID: "branch-trt", CustomerID: "cust-abc", Lines: depositSaleInput().Lines}
	out, err := h.svc.SalePreview(context.Background(), in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.SupplierDeposit == nil || !out.SupplierDeposit.Allowed || out.SupplierDeposit.Words != SupplierDepositWords {
		t.Fatalf("the credit account's preview: %+v", out.SupplierDeposit)
	}
	for _, tc := range []struct {
		name string
		cust customerRec
	}{
		{"a cash on demand account", customerRec{ID: "cust-2", CustomerType: "CONTRACTOR", PaymentTermsDays: 0, CreditLimitCents: 500000}},
		{"a no line account", customerRec{ID: "cust-3", CustomerType: "CONTRACTOR", PaymentTermsDays: 30, CreditLimitCents: 0}},
		{"the walk in", customerRec{ID: "cust-4", CustomerType: "RETAIL", PaymentTermsDays: 0, CreditLimitCents: 0}},
	} {
		h2 := supplierHarness()
		h2.store.customers["cust-abc"] = tc.cust
		h2.store.customers[tc.cust.ID] = tc.cust
		in2 := SalePreviewInput{BranchID: "branch-trt", CustomerID: tc.cust.ID, Lines: depositSaleInput().Lines}
		out2, err := h2.svc.SalePreview(context.Background(), in2)
		if err != nil {
			t.Fatalf("%s: preview: %v", tc.name, err)
		}
		if out2.SupplierDeposit == nil || out2.SupplierDeposit.Allowed {
			t.Fatalf("%s: the preview allows a supplier deposit: %+v", tc.name, out2.SupplierDeposit)
		}
	}
}
