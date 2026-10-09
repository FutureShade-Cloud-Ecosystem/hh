package pos

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// payment_options_test.go, item 9.1 (the row 1047 deposits work): the payment
// shapes the counter may offer, answered by the server so no screen decides a
// money rule. The derivation is a pure function over the account facts, and
// the preview carries it because the preview is already where the till reads
// its customer and its branch wall at the payment step.

// fakeHoldReader answers the holds seam (standing.go's HoldReader).
type fakeHoldReader struct {
	held bool
	err  error
}

func (f fakeHoldReader) HasActiveHold(context.Context, string) (bool, error) { return f.held, f.err }

// shapeCodes flattens the options to their codes, the shape an assertion reads.
func shapeCodes(opts []PaymentOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Shape)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// previewFor ringing harness: one line, the harness customer, walk-in when
// customerID is empty.
func previewOptions(t *testing.T, h *harness, customerID string) []PaymentOption {
	t.Helper()
	in := SalePreviewInput{
		BranchID: "branch-trt",
		Lines:    []LineInput{{ProductID: "prod-blu", QtyEntered: 100, UOMEntered: "SF"}},
	}
	if customerID != "" {
		in.CustomerID = customerID
	}
	out, err := h.svc.SalePreview(context.Background(), in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	return out.PaymentOptions
}

func TestPaymentOptionsFor_TheAccountTypeDecides(t *testing.T) {
	cases := []struct {
		name  string
		terms int
		limit int64
		held  bool
		want  []string
	}{
		{"credit customer", 30, 500_000, false, []string{
			PaymentShapePayInFull, PaymentShapeAllOnAccount, PaymentShapeDepositOnAccount}},
		{"credit customer on hold", 30, 500_000, true, []string{
			PaymentShapePayInFull, PaymentShapeDepositDueLater}},
		{"cash on demand customer (zero day terms with a line)", 0, 500_000, false, []string{
			PaymentShapePayInFull, PaymentShapePayBeforeRelease, PaymentShapeDepositDueLater}},
		{"no credit account (terms with no line)", 30, 0, false, []string{
			PaymentShapePayInFull, PaymentShapeDepositDueLater}},
		{"walk in (zero day terms, no line)", 0, 0, false, []string{
			PaymentShapePayInFull, PaymentShapeDepositDueLater}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shapeCodes(paymentOptionsFor(c.terms, c.limit, c.held))
			if !equalStrings(got, c.want) {
				t.Fatalf("paymentOptionsFor(%d, %d, %v) = %v, want %v", c.terms, c.limit, c.held, got, c.want)
			}
		})
	}
}

func TestPaymentOptionsFor_LineTwoWordsComeFromTheServer(t *testing.T) {
	for _, o := range paymentOptionsFor(30, 500_000, false) {
		switch o.Shape {
		case PaymentShapeDepositOnAccount:
			if o.LineTwoWords != LineTwoOnAccount {
				t.Errorf("the deposit shape for a credit customer says %q, want %q", o.LineTwoWords, LineTwoOnAccount)
			}
		case PaymentShapeAllOnAccount, PaymentShapePayInFull:
			if o.LineTwoWords != "" {
				t.Errorf("shape %s carries line two words %q, the words belong to the deposit shapes", o.Shape, o.LineTwoWords)
			}
		}
	}
	for _, o := range paymentOptionsFor(0, 0, false) {
		if o.Shape == PaymentShapeDepositDueLater && o.LineTwoWords != LineTwoDueLater {
			t.Errorf("the deposit shape for a pay before release account says %q, want %q", o.LineTwoWords, LineTwoDueLater)
		}
	}
}

// A credit customer's preview answers all three shapes, with the deposit's
// line two on account.
func TestSalePreview_PaymentOptions_CreditCustomer(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 30, CreditLimitCents: 500_000}
	h.svc.WithHoldReader(fakeHoldReader{})
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	want := []string{PaymentShapePayInFull, PaymentShapeAllOnAccount, PaymentShapeDepositOnAccount}
	if !equalStrings(got, want) {
		t.Fatalf("the credit customer's preview answered %v, want %v", got, want)
	}
}

// A cash on demand account never sees on account; its no deposit shape is the
// pay before release ring (the COD tile's sale, row 1214) and its deposit
// leaves the balance due later.
func TestSalePreview_PaymentOptions_CashOnDemandCustomer(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 0, CreditLimitCents: 500_000}
	h.svc.WithHoldReader(fakeHoldReader{})
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	want := []string{PaymentShapePayInFull, PaymentShapePayBeforeRelease, PaymentShapeDepositDueLater}
	if !equalStrings(got, want) {
		t.Fatalf("the cash on demand customer's preview answered %v, want %v", got, want)
	}
}

// The ruling (row 1214): the pay before release option is the Cash On Demand
// account's alone. A terms account never sees it (its sale books on account or
// paid in full, the ruling 11 refusal), and a no credit retail account never
// sees it either (no line, the walk in's own facts): the shape is the
// ACCOUNT'S payment posture (row 999), zero day terms with a line, never the
// customer type's label.
func TestSalePreview_PaymentOptions_PayBeforeReleaseIsTheCashOnDemandAccountsAlone(t *testing.T) {
	cases := []struct {
		name  string
		cust  customerRec
		want  bool
		reason string
	}{
		{"cash on demand account (zero day terms with a line)", customerRec{CustomerType: "CONTRACTOR",
			PaymentTermsDays: 0, CreditLimitCents: 500_000}, true, "the posture's own account"},
		{"terms account with a credit line", customerRec{CustomerType: "CONTRACTOR",
			PaymentTermsDays: 30, CreditLimitCents: 2_500_000}, false, "ruling 11: not a pay before release account"},
		{"retail account with no line", customerRec{CustomerType: "RETAIL",
			PaymentTermsDays: 0, CreditLimitCents: 0}, false, "no line is not the posture"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness()
			c.cust.ID = "cust-abc"
			h.store.customers["cust-abc"] = c.cust
			h.svc.WithHoldReader(fakeHoldReader{})
			got := shapeCodes(previewOptions(t, h, "cust-abc"))
			for _, shape := range got {
				if shape == PaymentShapePayBeforeRelease && !c.want {
					t.Fatalf("%s: the preview offered %s, the shape is the Cash On Demand account's alone (%s)", c.name, shape, c.reason)
				}
			}
			if c.want && !containsShape(got, PaymentShapePayBeforeRelease) {
				t.Fatalf("%s: the preview did not offer %s, want it offered (%s)", c.name, PaymentShapePayBeforeRelease, c.reason)
			}
		})
	}
}

func containsShape(shapes []string, shape string) bool {
	for _, s := range shapes {
		if s == shape {
			return true
		}
	}
	return false
}

// The walk in (no customer named) resolves to the house row and is answered
// from its own facts.
func TestSalePreview_PaymentOptions_WalkIn(t *testing.T) {
	h := newHarness()
	h.store.customers[h.store.walkInID] = customerRec{ID: h.store.walkInID, CustomerType: "RETAIL",
		PaymentTermsDays: 0, CreditLimitCents: 0}
	got := shapeCodes(previewOptions(t, h, ""))
	want := []string{PaymentShapePayInFull, PaymentShapeDepositDueLater}
	if !equalStrings(got, want) {
		t.Fatalf("the walk in's preview answered %v, want %v", got, want)
	}
}

// Item 9 Opus P2-3: the due later deposit rides the COD sale type, which the
// branch's policy can turn off (0043). With Cash On Demand off at the branch
// the menu drops it and offers pay in full now only, the same fact the ring's
// own errCODDisabled refuses on: the menu must not promise a shape the ring
// will not take. The pay before release option rides the same sale type and
// drops with it.
func TestSalePreview_PaymentOptions_CodOffDropsTheDueLaterDeposit(t *testing.T) {
	for _, account := range []struct {
		name string
		cust customerRec
	}{
		{"walk in", customerRec{CustomerType: "RETAIL", PaymentTermsDays: 0, CreditLimitCents: 0}},
		{"cash on demand customer", customerRec{CustomerType: "CONTRACTOR", PaymentTermsDays: 0, CreditLimitCents: 500_000}},
		{"no credit account", customerRec{CustomerType: "CONTRACTOR", PaymentTermsDays: 30, CreditLimitCents: 0}},
	} {
		t.Run(account.name, func(t *testing.T) {
			h := newHarness()
			account.cust.ID = "cust-abc"
			h.store.customers["cust-abc"] = account.cust
			h.store.customers[h.store.walkInID] = customerRec{ID: h.store.walkInID, CustomerType: "RETAIL",
				PaymentTermsDays: 0, CreditLimitCents: 0}
			h.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", CODEnabled: false}
			got := shapeCodes(previewOptions(t, h, "cust-abc"))
			want := []string{PaymentShapePayInFull}
			if !equalStrings(got, want) {
				t.Fatalf("with COD off the preview answered %v, want %v (no due later deposit the ring would refuse)", got, want)
			}
		})
	}
}

// A policy read that cannot be made fails closed the same way: no due later
// deposit the ring might refuse.
func TestSalePreview_PaymentOptions_UnreadablePolicyDropsTheDueLaterDeposit(t *testing.T) {
	h := newHarness()
	h.store.customers[h.store.walkInID] = customerRec{ID: h.store.walkInID, CustomerType: "RETAIL",
		PaymentTermsDays: 0, CreditLimitCents: 0}
	h.store.policyErr = errors.New("policy unreadable")
	got := shapeCodes(previewOptions(t, h, ""))
	want := []string{PaymentShapePayInFull}
	if !equalStrings(got, want) {
		t.Fatalf("with the policy unreadable the preview answered %v, want %v", got, want)
	}
}

// A credit customer's menu is untouched by the switch: its deposit rides on
// account, not the COD sale type.
func TestSalePreview_PaymentOptions_CodOffLeavesTheCreditShapes(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 30, CreditLimitCents: 500_000}
	h.store.policies["branch-trt"] = PosPolicy{BranchID: "branch-trt", CODEnabled: false}
	h.svc.WithHoldReader(fakeHoldReader{})
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	want := []string{PaymentShapePayInFull, PaymentShapeAllOnAccount, PaymentShapeDepositOnAccount}
	if !equalStrings(got, want) {
		t.Fatalf("with COD off the credit customer's preview answered %v, want %v", got, want)
	}
}

// Terms without a credit line is a no credit account: never on account.
func TestSalePreview_PaymentOptions_NoCreditAccount(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 30, CreditLimitCents: 0}
	h.svc.WithHoldReader(fakeHoldReader{})
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	want := []string{PaymentShapePayInFull, PaymentShapeDepositDueLater}
	if !equalStrings(got, want) {
		t.Fatalf("the no credit account's preview answered %v, want %v", got, want)
	}
}

// The existing hold rule (till/holds.go): a credit customer on hold loses on
// account, cash or card only.
func TestSalePreview_PaymentOptions_CreditCustomerOnHold(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 30, CreditLimitCents: 500_000}
	h.svc.WithHoldReader(fakeHoldReader{held: true})
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	want := []string{PaymentShapePayInFull, PaymentShapeDepositDueLater}
	if !equalStrings(got, want) {
		t.Fatalf("the held credit customer's preview answered %v, want %v (no on account under a hold)", got, want)
	}
}

// A hold read that cannot be made fails closed on the on account shapes: the
// preview still answers its totals, but it never offers credit it could not
// prove is clear. The ring's own gate stays the enforcement, this is what the
// counter is told it may offer.
func TestSalePreview_PaymentOptions_HoldReadUnwiredOffersNoCredit(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 30, CreditLimitCents: 500_000}
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	want := []string{PaymentShapePayInFull, PaymentShapeDepositDueLater}
	if !equalStrings(got, want) {
		t.Fatalf("with no hold seam the preview answered %v, want %v (fail closed on on account)", got, want)
	}
}

// The seam is standing.go's HoldReader: the wired repository satisfies it (the
// compile proof the composition root relies on).
var _ HoldReader = &till.Repository{}

// Round 15 finding (a): the sentence under the two lines is the server's too, and it
// differs by reason. A Cash On Demand account hears the pay before release words, a credit
// customer hears that the rest rides the account, and every other pay before release
// account (a no credit retail account such as a gate refused one, a walk in, a credit
// customer on hold) is told what it is: this account cannot carry the sale on account.
func TestPaymentOptionsFor_LineTwoSentenceFollowsTheReason(t *testing.T) {
	sentenceOf := func(terms int, limit int64, held bool) string {
		for _, o := range paymentOptionsFor(terms, limit, held) {
			if o.Shape == PaymentShapeDepositOnAccount || o.Shape == PaymentShapeDepositDueLater {
				return o.LineTwoSentence
			}
		}
		return ""
	}
	cases := []struct {
		name  string
		terms int
		limit int64
		held  bool
		want  string
	}{
		{"credit customer", 30, 500_000, false, SentenceOnAccount},
		{"cash on demand account", 0, 500_000, false, SentenceCashOnDemand},
		{"no credit retail account", 0, 0, false, SentenceCannotCarry},
		{"terms with no line", 30, 0, false, SentenceCannotCarry},
		{"credit customer on hold", 30, 500_000, true, SentenceCannotCarry},
	}
	for _, c := range cases {
		if got := sentenceOf(c.terms, c.limit, c.held); got != c.want {
			t.Errorf("%s: sentence %q, want %q", c.name, got, c.want)
		}
	}
	if SentenceCannotCarry == SentenceCashOnDemand || SentenceCannotCarry == "" {
		t.Fatalf("the cannot carry sentence must be its own words, got %q", SentenceCannotCarry)
	}
	if strings.Contains(SentenceCannotCarry, "Cash On Demand") {
		t.Errorf("a no credit account is not a Cash On Demand account, yet the sentence says so: %q", SentenceCannotCarry)
	}
}

// The ruling's parity leg (row 1214): ringing the offered pay before release
// option gives exactly the order state the removed fulfilment tile's COD ring
// gave, which TestSale_COD_UnpaidAtRing_InvoiceFinalized_FulfilmentLocked pins
// shape for shape (service_w7_test.go). Here the ring is driven by the menu:
// the preview offers the shape to the Cash On Demand account, and the body the
// shape implies (the COD sale type, the fulfilment of the chosen tile, no
// deposit, the settlement tender) commits to the same invoice, the same due
// date and the same locked posture.
func TestSale_PayBeforeReleaseOption_RingsTheCODOrderState(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 0, CreditLimitCents: 500_000}
	h.svc.WithHoldReader(fakeHoldReader{})
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	if !containsShape(got, PaymentShapePayBeforeRelease) {
		t.Fatalf("the preview did not offer %s, want it offered to the Cash On Demand account", PaymentShapePayBeforeRelease)
	}
	in := baseInput()
	in.SaleType = SaleCOD
	in.Fulfilment = "WILL_CALL" // the Will-call tile's fulfilment
	in.Tender = TenderCash      // the settlement method collected later
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("the offered shape's ring: %v", err)
	}
	if out.SaleType != SaleCOD {
		t.Fatalf("the ring's sale type = %q, want %q", out.SaleType, SaleCOD)
	}
	if h.tender.called {
		t.Fatal("the shape is unpaid at ring, TakeTender must NOT be called")
	}
	inv := h.store.invoices[0]
	if inv.Status != "FINALIZED" || inv.BalanceCents != out.TotalCents {
		t.Fatalf("the shape's invoice must FINALIZE for the full balance: %+v", inv)
	}
	if inv.DueDate != "2026-06-29" {
		t.Fatalf("the shape's due date must be today, got %s", inv.DueDate)
	}
	if out.InvoiceNo == "" {
		t.Fatal("the shape's ring must surface the invoice number")
	}
	if h.store.orders[0].Fulfillment != "WILL_CALL" {
		t.Fatalf("the shape keeps the chosen tile's fulfilment: %q", h.store.orders[0].Fulfillment)
	}
	order := h.store.orders[0]
	if m, ok := order.Metadata["cod"]; !ok || m != true {
		t.Fatalf("the shape's order must carry the cod fact: %+v", order.Metadata)
	}
	if m, ok := order.Metadata["cod_posture"]; !ok || m != "AWAITING_PAYMENT" {
		t.Fatalf("the shape's order must carry the AWAITING_PAYMENT posture: %+v", order.Metadata)
	}
	for _, ev := range h.store.events {
		if ev == "order.fulfilled" {
			t.Fatal("the shape must NOT fulfil at ring")
		}
	}
}
