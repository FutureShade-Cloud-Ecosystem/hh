package pos

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// sale_deposit_test.go, item 9.2 (the row 1047 deposits work): the deposit as
// line one, the rest as line two. Each account type times each fulfilment the
// shape allows, the refusals, the declined card deposit, and the preview
// answering the same totals the ring binds.

// depositHarness is the sale harness with a customer of the given account
// shape planted and the hold seam wired clear.
func depositHarness(terms int, limit int64) *harness {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: terms, CreditLimitCents: limit}
	h.svc.WithHoldReader(fakeHoldReader{})
	return h
}

func depositSaleInput() SaleInput {
	in := baseInput()
	in.TillSessionID = "till-1"
	return in
}

var depositClock = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

// A credit customer's deposit books the rest on account: the invoice lands
// PARTIALLY_PAID with its due date from the terms, whatever fulfilment waits.
func TestSale_Deposit_CreditCustomer_BooksTheRestOnAccount(t *testing.T) {
	for _, st := range []string{SaleCollectLater, SaleDelivered} {
		t.Run(st, func(t *testing.T) {
			h := depositHarness(30, 500_000)
			in := depositSaleInput()
			in.SaleType = st
			in.Tender = TenderAccount
			in.DepositCents = 4000
			in.DepositMethod = TenderCash
			out, err := h.svc.Sale(context.Background(), in)
			if err != nil {
				t.Fatalf("sale: %v", err)
			}
			if out.DepositCents != 4000 {
				t.Fatalf("the answer carries deposit %d, want 4000", out.DepositCents)
			}
			if len(h.store.invoices) != 1 {
				t.Fatalf("want one invoice, got %d", len(h.store.invoices))
			}
			inv := h.store.invoices[0]
			if inv.Status != "PARTIALLY_PAID" || inv.BalanceCents != out.TotalCents-4000 {
				t.Fatalf("invoice %s balance %d, want PARTIALLY_PAID at %d", inv.Status, inv.BalanceCents, out.TotalCents-4000)
			}
			wantDue := depositClock.AddDate(0, 0, 30).Format("2006-01-02")
			if inv.DueDate != wantDue {
				t.Fatalf("due date %s, want the terms date %s", inv.DueDate, wantDue)
			}
			// Line one: one captured deposit leg through the till, the account leg captures nothing.
			if len(h.tender.takes) != 1 || h.tender.takes[0].Method != TenderCash || h.tender.takes[0].AmountCents != 4000 {
				t.Fatalf("the deposit leg: %+v", h.tender.takes)
			}
			if len(h.store.custTxns) != 2 || h.store.custTxns[0].TxnType != "INVOICE" || h.store.custTxns[1].TxnType != "PAYMENT" {
				t.Fatalf("the AR rows: %+v", h.store.custTxns)
			}
		})
	}
}

// A cash on demand customer's deposit books the rest due later: the order and
// its invoice number exist while unpaid, no terms date, pay before release.
func TestSale_Deposit_CashOnDemand_BooksTheRestDueLater(t *testing.T) {
	for _, f := range []string{"", "PICKUP", "WILL_CALL", "DELIVERY"} {
		t.Run("fulfilment_"+f, func(t *testing.T) {
			h := depositHarness(0, 500_000)
			in := depositSaleInput()
			in.SaleType = SaleCOD
			in.Fulfilment = f
			in.Tender = TenderCash
			in.DepositCents = 2500
			in.DepositMethod = TenderCard
			in.PaymentToken = "tok-x"
			out, err := h.svc.Sale(context.Background(), in)
			if err != nil {
				t.Fatalf("sale: %v", err)
			}
			inv := h.store.invoices[0]
			if inv.Status != "PARTIALLY_PAID" || inv.BalanceCents != out.TotalCents-2500 {
				t.Fatalf("invoice %s balance %d, want PARTIALLY_PAID at %d", inv.Status, inv.BalanceCents, out.TotalCents-2500)
			}
			// No due date beyond the issue day: due on demand, never the terms.
			wantDue := depositClock.Format("2006-01-02")
			if inv.DueDate != wantDue {
				t.Fatalf("due date %s, want the issue day %s (on demand)", inv.DueDate, wantDue)
			}
			// The card deposit went through the card charge rail.
			if len(h.tender.captureCalls) != 1 || h.tender.captureCalls[0].AmountCents != 2500 {
				t.Fatalf("the card deposit capture: %+v", h.tender.captureCalls)
			}
			if len(h.tender.takes) != 1 || h.tender.takes[0].Method != TenderCard {
				t.Fatalf("the deposit leg: %+v", h.tender.takes)
			}
			// The order carries the pay before release posture and the deposit facts.
			o := h.store.orders[0]
			if o.Metadata["cod"] != true {
				t.Fatalf("the order is not a pay before release sale: %v", o.Metadata)
			}
			if o.Metadata["deposit_cents"] != int64(2500) || o.Metadata["deposit_method"] != TenderCard {
				t.Fatalf("the order's deposit facts: %v", o.Metadata)
			}
		})
	}
}

// The walk in's deposit is the same pay before release shape, answered from
// the house row's own facts.
func TestSale_Deposit_WalkIn_PayBeforeRelease(t *testing.T) {
	h := depositHarness(0, 0)
	h.store.customers[h.store.walkInID] = customerRec{ID: h.store.walkInID, CustomerType: "RETAIL"}
	in := depositSaleInput()
	in.CustomerID = ""
	in.SaleType = SaleCOD
	in.Tender = TenderCash
	in.DepositCents = 1000
	in.DepositMethod = TenderCash
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if h.store.orders[0].CustomerID != h.store.walkInID {
		t.Fatalf("the sale did not resolve to the house walk in")
	}
	if h.store.invoices[0].BalanceCents != out.TotalCents-1000 {
		t.Fatalf("the walk in's balance is not the rest")
	}
}

// A deposit on a collect now sale is refused in words: the goods do not leave
// on a part payment. (The item's choice, refused rather than moved: the
// refusal keeps the customer's ask intact and names the two honest paths.)
func TestSale_DepositOnCollectNow_RefusedInWords(t *testing.T) {
	h := depositHarness(30, 500_000)
	in := depositSaleInput()
	in.Tender = TenderAccount
	in.DepositCents = 4000
	in.DepositMethod = TenderCash
	_, err := h.svc.Sale(context.Background(), in)
	var e ErrDepositNotCollectNow
	if !errors.As(err, &e) {
		t.Fatalf("want ErrDepositNotCollectNow, got %v", err)
	}
	if e.DepositCents != 4000 || e.TotalCents != 11300 {
		t.Fatalf("the refusal's fields: deposit %d total %d", e.DepositCents, e.TotalCents)
	}
	if msg := depositNotCollectNowMessage(e.DepositCents, e.TotalCents); msg == "" || strings.Contains(msg, "cents") {
		t.Fatalf("the refusal's message must speak dollars: %q", msg)
	}
	if len(h.store.orders) != 0 || h.tender.called {
		t.Fatalf("a refused sale wrote rows: orders %d tender %v", len(h.store.orders), h.tender.called)
	}
}

// A cash on demand account (zero day terms, with a line) cannot put the rest
// on account: the shape does not fit, whatever its limit says.
func TestSale_AccountShape_ZeroDayTerms_RefusedInWords(t *testing.T) {
	h := depositHarness(0, 500_000)
	in := depositSaleInput()
	in.SaleType = SaleCollectLater
	in.Tender = TenderAccount
	_, err := h.svc.Sale(context.Background(), in)
	var e ErrAccountShapeNotAvailable
	if !errors.As(err, &e) {
		t.Fatalf("want ErrAccountShapeNotAvailable, got %v", err)
	}
	if e.CreditLimitCents != 500_000 {
		t.Fatalf("the refusal names the limit %d, want 500000", e.CreditLimitCents)
	}
	if len(h.store.orders) != 0 {
		t.Fatalf("a refused sale wrote an order")
	}
}

// A no credit account (terms, no line) is refused the same way, and so is the
// account leg of a split.
func TestSale_AccountShape_NoLine_AndSplitLeg_Refused(t *testing.T) {
	h := depositHarness(30, 0)
	in := depositSaleInput()
	in.Tender = TenderAccount
	if _, err := h.svc.Sale(context.Background(), in); !errors.As(err, &ErrAccountShapeNotAvailable{}) {
		t.Fatalf("the no line account rang on account: %v", err)
	}
	h2 := depositHarness(30, 0)
	in2 := depositSaleInput()
	in2.Tenders = []TenderLegInput{
		{Method: TenderCash, AmountCents: 5000},
		{Method: TenderAccount, AmountCents: 6300},
	}
	if _, err := h2.svc.Sale(context.Background(), in2); !errors.As(err, &ErrAccountShapeNotAvailable{}) {
		t.Fatalf("the split's account leg rang on a no line account: %v", err)
	}
	if len(h2.store.orders) != 0 {
		t.Fatalf("a refused split wrote an order")
	}
}

// A COD sale never takes an ACCOUNT tender: pay before release and on account
// are different line twos.
func TestSale_AccountShape_CODSale_Refused(t *testing.T) {
	h := depositHarness(30, 500_000)
	in := depositSaleInput()
	in.SaleType = SaleCOD
	in.Tender = TenderAccount
	if _, err := h.svc.Sale(context.Background(), in); !errors.As(err, &ErrAccountShapeNotAvailable{}) {
		t.Fatalf("a COD sale took an ACCOUNT tender: %v", err)
	}
}

// A declined card deposit writes nothing: no order, no tender, the capture
// refused before the transaction.
func TestSale_Deposit_CardDeclined_WritesNothing(t *testing.T) {
	h := depositHarness(0, 500_000)
	h.tender.captureErr = till.ErrCardDeclined
	in := depositSaleInput()
	in.SaleType = SaleCOD
	in.Tender = TenderCash
	in.DepositCents = 2500
	in.DepositMethod = TenderCard
	in.PaymentToken = "tok-x"
	_, err := h.svc.Sale(context.Background(), in)
	if !errors.Is(err, ErrCardChargeCardDeclined) {
		t.Fatalf("want the card decline answer, got %v", err)
	}
	if len(h.store.orders) != 0 || len(h.store.invoices) != 0 || len(h.store.posSales) != 0 {
		t.Fatalf("a declined deposit wrote rows: %+v", h.store.orders)
	}
	if len(h.tender.takes) != 0 {
		t.Fatalf("a declined deposit took a tender")
	}
	if len(h.tender.captureCalls) != 1 || h.tender.captureCalls[0].AmountCents != 2500 {
		t.Fatalf("the deposit leg never reached the card rail: %+v", h.tender.captureCalls)
	}
}

// The preview's totals are the ring's totals for a deposit body: the same
// resolver, the same cents, whatever tender the ring then takes.
func TestSale_Deposit_PreviewTotalsMatchTheRing(t *testing.T) {
	h := depositHarness(30, 500_000)
	in := depositSaleInput()
	in.SaleType = SaleCollectLater
	in.Tender = TenderAccount
	in.DepositCents = 4000
	in.DepositMethod = TenderCash
	ring, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	h2 := depositHarness(30, 500_000)
	preview, err := h2.svc.SalePreview(context.Background(), SalePreviewInput{
		BranchID:   "branch-trt",
		CustomerID: "cust-abc",
		SaleType:   SaleCollectLater,
		Lines:      []LineInput{{ProductID: "prod-blu", QtyEntered: 100, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.SubtotalCents != ring.SubtotalCents || preview.TaxCents != ring.TaxCents || preview.TotalCents != ring.TotalCents {
		t.Fatalf("preview (%d/%d/%d) is not the ring (%d/%d/%d)",
			preview.SubtotalCents, preview.TaxCents, preview.TotalCents,
			ring.SubtotalCents, ring.TaxCents, ring.TotalCents)
	}
}
