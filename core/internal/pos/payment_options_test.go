package pos

import (
	"context"
	"errors"
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
			PaymentShapePayInFull, PaymentShapeDepositDueLater}},
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

// A cash on demand account never sees on account, its deposit leaves the
// balance due later.
func TestSalePreview_PaymentOptions_CashOnDemandCustomer(t *testing.T) {
	h := newHarness()
	h.store.customers["cust-abc"] = customerRec{ID: "cust-abc", CustomerType: "CONTRACTOR",
		PaymentTermsDays: 0, CreditLimitCents: 500_000}
	h.svc.WithHoldReader(fakeHoldReader{})
	got := shapeCodes(previewOptions(t, h, "cust-abc"))
	want := []string{PaymentShapePayInFull, PaymentShapeDepositDueLater}
	if !equalStrings(got, want) {
		t.Fatalf("the cash on demand customer's preview answered %v, want %v", got, want)
	}
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
// will not take.
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
