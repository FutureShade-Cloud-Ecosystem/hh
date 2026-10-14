package printing

// receipt_standard_rate_test.go, the rate a receipt's tax line names follows the dealer's standard rate
// on the sale date (item 3b), the letterhead's settings figure standing when no engine is wired or the
// read fails (a receipt without the figure beats no receipt).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
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

type receiptZoneSrc struct{ z houseday.Zone }

func (s receiptZoneSrc) BusinessZone(context.Context) (houseday.Zone, error) { return s.z, nil }

// The tax label's rate is read on the dealer's day of the sale (item 3b slice 3). A sale rung at 05:00
// UTC on the fourth is still the third in Los Angeles.
func TestReceipt_TaxLabelRateIsReadOnTheDealersDayOfTheSale(t *testing.T) {
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		src  houseday.Source
		want string
	}{
		{"no source, the Ontario dealer", nil, "2026-08-04"},
		{"Los Angeles", receiptZoneSrc{z: la}, "2026-08-03"},
		{"Toronto after Los Angeles", receiptZoneSrc{z: houseday.DefaultZone()}, "2026-08-04"},
	} {
		qs := &fakeQueueStore{markFind: true, markUpd: true}
		store := &fakeReceiptStore{found: true, sale: receiptSale{
			OrderID: saleOrderID, OrderNo: "#SO-42", BranchID: trentonID, InvoiceNo: "#INV-9",
			Tender: "CASH", SaleType: "COLLECT_NOW", Status: "COMMITTED",
			SubtotalCents: 12340, TaxCents: 1604, TotalCents: 13944,
			SoldAt: time.Date(2026, 8, 4, 5, 0, 0, 0, time.UTC),
		}}
		var asked string
		svc := NewReceiptService(store, NewQueueService(qs)).
			WithStandardRate(fakeReceiptStandardRate{bps: 775, asOf: &asked}).
			WithZone(tc.src)
		if _, err := svc.Receipt(staffFor(trentonID), saleOrderID); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if asked != tc.want {
			t.Fatalf("%s: the rate was read as of %q, want %s", tc.name, asked, tc.want)
		}
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

// Both features hold together for a California dealer: the tax line carries the dealer's word AND the
// rate read on the Pacific day of the sale (a sale rung at 05:00 UTC on the fourth is the third in Los Angeles).
func TestReceipt_CaliforniaDealerGetsTheWordAndThePacificDayRate(t *testing.T) {
	la, err := houseday.NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeReceiptStore{found: true, sale: receiptSale{
		OrderID: saleOrderID, OrderNo: "#SO-42", BranchID: trentonID, InvoiceNo: "#INV-9",
		Tender: "CASH", SaleType: "COLLECT_NOW", Status: "COMMITTED",
		SubtotalCents: 12340, TaxCents: 1604, TotalCents: 13944,
		SoldAt: time.Date(2026, 8, 4, 5, 0, 0, 0, time.UTC),
	}}
	var asked string
	svc := NewReceiptService(store, NewQueueService(&fakeQueueStore{markFind: true, markUpd: true})).
		WithStandardRate(fakeReceiptStandardRate{bps: 775, asOf: &asked}).
		WithZone(receiptZoneSrc{z: la}).
		WithTaxLabel(fakeReceiptWords{w: "Sales tax"})
	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.TaxLabel != "Sales tax (7.75%)" {
		t.Fatalf("tax label = %q, want the word and the rate", rec.TaxLabel)
	}
	if asked != "2026-08-03" {
		t.Fatalf("rate read as of %q, want the Pacific day 2026-08-03", asked)
	}
}
