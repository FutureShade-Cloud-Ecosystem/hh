package printing

// receipt_standard_rate_test.go, the rate a receipt's tax line names follows the dealer's standard rate
// on the sale date (item 3b), the letterhead's settings figure standing when no engine is wired or the
// read fails (a receipt without the figure beats no receipt).

import (
	"context"
	"errors"
	"strings"
	"testing"
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
