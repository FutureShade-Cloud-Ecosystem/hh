package printing

// receipt_standard_rate_test.go, the rate a receipt's tax line names follows the dealer's standard rate
// on the sale date (item 3b), the letterhead's settings figure standing when no engine is wired or the
// read fails (a receipt without the figure beats no receipt).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/taxwords"
)

type fakeReceiptStandardRate struct {
	bps  int
	err  error
	asOf *string
}

func (f fakeReceiptStandardRate) StandardRateBps(_ context.Context, asOf string) (int, error) {
	if f.asOf != nil {
		*f.asOf = asOf
	}
	return f.bps, f.err
}

func TestReceipt_TaxLabelFollowsTheEnginesStandardRateOnTheSaleDate(t *testing.T) {
	var asked string
	svc, _ := receiptHarness(t)
	svc.WithStandardRate(fakeReceiptStandardRate{bps: 775, asOf: &asked})
	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.TaxLabel, "7.75%") {
		t.Fatalf("tax label %q, want the dealer's 7.75%%", rec.TaxLabel)
	}
	if asked != "2026-08-04" {
		t.Fatalf("the rate was read as of %q, want the sale day 2026-08-04", asked)
	}
}

func TestReceipt_TaxLabelKeepsTheLetterheadFigureWhenUnwiredOrTheReadFails(t *testing.T) {
	svc, _ := receiptHarness(t)
	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil || !strings.Contains(rec.TaxLabel, "13%") {
		t.Fatalf("unwired: %q %v", rec.TaxLabel, err)
	}
	svc2, _ := receiptHarness(t)
	svc2.WithStandardRate(fakeReceiptStandardRate{err: errors.New("down")})
	rec, err = svc2.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil || !strings.Contains(rec.TaxLabel, "13%") {
		t.Fatalf("read failed: %q %v", rec.TaxLabel, err)
	}
}

type fakeReceiptWords struct {
	w   string
	err error
}

func (f fakeReceiptWords) TaxLabel(context.Context) (string, error) { return f.w, f.err }

// The word on the tax line is the dealer's: HST exactly as today for an Ontario dealer (and unwired),
// "Sales tax" for a California one, the neutral "Tax" when the jurisdiction could not be read.
func TestReceipt_TaxLabelWordFollowsTheDealersJurisdiction(t *testing.T) {
	for _, c := range []struct {
		name string
		src  taxwords.Source
		want string
	}{
		{"unwired", nil, "HST (13%)"},
		{"ontario", fakeReceiptWords{w: "HST"}, "HST (13%)"},
		{"california", fakeReceiptWords{w: "Sales tax"}, "Sales tax (7.75%)"},
		{"failed read", fakeReceiptWords{err: errors.New("down")}, "Tax (13%)"},
	} {
		svc, _ := receiptHarness(t)
		if c.name == "california" {
			svc.WithStandardRate(fakeReceiptStandardRate{bps: 775})
		}
		if c.src != nil {
			svc.WithTaxLabel(c.src)
		}
		rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
		if err != nil || rec.TaxLabel != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, rec.TaxLabel, err, c.want)
		}
	}
}

func TestTaxLabelCarriesTheWordAndTheRate(t *testing.T) {
	for _, tc := range []struct {
		word string
		bps  int
		want string
	}{
		{"Sales tax", 775, "Sales tax (7.75%)"}, {"Sales tax", 600, "Sales tax (6%)"}, {"Sales tax", 0, "Sales tax"},
	} {
		if got := taxLabel(tc.word, tc.bps); got != tc.want {
			t.Errorf("taxLabel(%q,%d) = %q, want %q", tc.word, tc.bps, got, tc.want)
		}
	}
}
