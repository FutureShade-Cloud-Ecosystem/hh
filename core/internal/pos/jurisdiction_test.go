package pos

// jurisdiction_test.go, the ring and the reads bind by the dealer's own jurisdiction at the unit seam
// (no database): the assessor's quote carries the reason it bound, the jurisdiction and the standard
// rate, and every record the ring writes, the sale, the preview, the relief snapshot, the relief
// assessment and the counter's rate read, follows the quote and not a literal. An Ontario dealer, and
// an assessor that answers none of the new fields, keep the Ontario words and figures exactly.

import (
	"context"
	"testing"
)

// californiaAssessor binds the San Diego County combined rate the way the wired engine does for a
// California dealer: the neutral standard reason, the dealer's jurisdiction, and its standard rate.
// A relief reason without evidence (the adapter pins none) binds the standard, reason retained.
type californiaAssessor struct{}

func (californiaAssessor) AssessSale(_ context.Context, in TaxQuoteInput) (QuoteResult, error) {
	reason := in.ReasonCode
	if reason == "" || reason == TaxStdON13 || reason == TaxStdSales {
		reason = TaxStdSales
	}
	return QuoteResult{
		TaxCents:        (in.SubtotalCents*775 + 5000) / 10000,
		RateBps:         775,
		ReasonCode:      reason,
		Jurisdiction:    "CA",
		StandardRateBps: 775,
	}, nil
}

func TestJurisdiction_CaliforniaRingRecordsTheNeutralReasonAndTheDealersRate(t *testing.T) {
	h := newHarness()
	h.svc.WithTaxAssessor(californiaAssessor{})
	out, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	// 100.00 at 7.75 percent is 7.75.
	if out.TaxCents != 775 {
		t.Fatalf("california tax: %d", out.TaxCents)
	}
	if out.TaxReasonCode != TaxStdSales {
		t.Fatalf("the ring recorded reason %q, want %s", out.TaxReasonCode, TaxStdSales)
	}
	if len(h.store.posSales) != 1 || h.store.posSales[0].TaxReasonCode != TaxStdSales {
		t.Fatalf("pos_sale record: %+v", h.store.posSales)
	}
}

func TestJurisdiction_CaliforniaReliefSnapshotCarriesTheStandardRate(t *testing.T) {
	h := newHarness()
	h.svc.WithTaxAssessor(californiaAssessor{})
	in := baseInput()
	in.Relief = &ReliefDecision{ReasonCode: TaxONFNPos8, EvidenceDocID: "doc-1"}
	if _, err := h.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("sale: %v", err)
	}
	if len(h.store.snapshots) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(h.store.snapshots))
	}
	row := h.store.snapshots[0]
	// The full tax is the STANDARD rate on the subtotal (775 bps), not the Ontario 13 percent literal,
	// and the row names the standard rate it was computed at so the recompute can divide by it.
	if row.StandardRateBps != 775 || row.FullHSTCents != (10000*775+5000)/10000 {
		t.Fatalf("snapshot standard figures: %+v", row)
	}
}

func TestJurisdiction_CaliforniaPreviewAndRateRead(t *testing.T) {
	h := newHarness()
	h.svc.WithTaxAssessor(californiaAssessor{})
	got, err := h.svc.TaxRate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Jurisdiction != "CA" || got.ReasonCode != TaxStdSales || got.RateBps != 775 {
		t.Fatalf("the counter's rate read: %+v", got)
	}
	pv, err := h.svc.SalePreview(context.Background(), SalePreviewInput{BranchID: "branch-trt", CustomerID: "cust-abc", SaleType: SaleCollectNow, Lines: baseInput().Lines})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if pv.TaxReasonCode != TaxStdSales || len(pv.TaxLines) != 1 || pv.TaxLines[0].ReasonCode != TaxStdSales {
		t.Fatalf("the preview's reason: %+v", pv)
	}
}

func TestJurisdiction_CaliforniaReliefAssessmentSpeaksTheDealersStandard(t *testing.T) {
	h := newHarness()
	h.svc.WithTaxAssessor(californiaAssessor{})
	h.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", OntarioEligibility: "NONE", MaskedIdentifier: "•••• 1"}
	out, err := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", OrderID: "order-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
	})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if out.ReasonCode != TaxStdSales || out.Outcome != "7.75%" || out.RateBps != 775 {
		t.Fatalf("standard outcome: %+v", out)
	}
	if len(h.store.snapshots) != 1 || h.store.snapshots[0].TaxReasonCode != TaxStdSales || h.store.snapshots[0].StandardRateBps != 775 {
		t.Fatalf("snapshot: %+v", h.store.snapshots)
	}
}

// The Ontario words and figures are pinned: the flat default assessor, and an assessor that answers
// none of the new fields, both keep STD_ON_HST_13, the ON jurisdiction and the 13 percent outcome.
func TestJurisdiction_OntarioWordsAndFiguresUnchanged(t *testing.T) {
	h := newHarness()
	out, err := h.svc.Sale(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.TaxReasonCode != TaxStdON13 || out.TaxCents != 1300 {
		t.Fatalf("ontario ring: %s %d", out.TaxReasonCode, out.TaxCents)
	}
	h.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", OntarioEligibility: "NONE", MaskedIdentifier: "•••• 1"}
	rel, err := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
		ProfileID: "prof-1", OrderID: "order-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
	})
	if err != nil || rel.Outcome != "13%" || rel.ReasonCode != TaxStdON13 {
		t.Fatalf("ontario relief outcome: %+v %v", rel, err)
	}
	// An assessor that answers only the old fields (the older fakes) is the Ontario dealer.
	h.svc.WithTaxAssessor(fixedBpsAssessor{bps: 800})
	rate, _ := h.svc.TaxRate(context.Background())
	if rate.Jurisdiction != "ON" || rate.ReasonCode != TaxStdON13 {
		t.Fatalf("an old assessor's read: %+v", rate)
	}
}

// Two services, two dealers, one process: neither reads the other's jurisdiction.
func TestJurisdiction_TwoDealersInOneProcessDoNotLeak(t *testing.T) {
	ca, on := newHarness(), newHarness()
	ca.svc.WithTaxAssessor(californiaAssessor{})
	for i := 0; i < 2; i++ {
		a, _ := ca.svc.TaxRate(context.Background())
		b, _ := on.svc.TaxRate(context.Background())
		if a.Jurisdiction != "CA" || b.Jurisdiction != "ON" {
			t.Fatalf("leak: %+v %+v", a, b)
		}
	}
}

// The Ontario 5 percent relief is Ontario's alone: a California dealer with an Ontario resident or
// Akwesasne profile and personal use rings a pickup at its own standard, with no 8 percent credit, no
// 5 percent figure and no Ontario relief reason on the snapshot. An Ontario dealer keeps the 5 percent.
func TestJurisdiction_OntarioReliefOnlyForAnOntarioDealer(t *testing.T) {
	for _, elig := range []string{"ON_RESIDENT", "AKWESASNE"} {
		h := newHarness()
		h.svc.WithTaxAssessor(californiaAssessor{})
		h.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", OntarioEligibility: elig, MaskedIdentifier: "•••• 1"}
		out, err := h.svc.AssessRelief(context.Background(), ReliefAssessInput{
			ProfileID: "prof-1", OrderID: "order-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
		})
		if err != nil {
			t.Fatalf("%s: %v", elig, err)
		}
		if out.ReasonCode != TaxStdSales || out.Outcome != "7.75%" || out.RateBps != 775 ||
			out.OnCreditCents != 0 || out.NetCollectedCents != 775 {
			t.Fatalf("%s california outcome: %+v", elig, out)
		}
		if len(h.store.snapshots) != 1 || h.store.snapshots[0].OnCreditCents != 0 || h.store.snapshots[0].TaxReasonCode != TaxStdSales {
			t.Fatalf("%s snapshot: %+v", elig, h.store.snapshots)
		}

		on := newHarness()
		on.store.profiles["prof-1"] = eligibilityMasked{ProfileID: "prof-1", OntarioEligibility: elig, MaskedIdentifier: "•••• 1"}
		got, err := on.svc.AssessRelief(context.Background(), ReliefAssessInput{
			ProfileID: "prof-1", OrderID: "order-1", DeclaredUse: "personal", Fulfilment: "pickup", SubtotalCents: 10000,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Outcome != "5%" || got.ReasonCode != TaxONFNPos8 || got.OnCreditCents != 800 || got.NetCollectedCents != 500 {
			t.Fatalf("%s ontario outcome: %+v", elig, got)
		}
	}
}
