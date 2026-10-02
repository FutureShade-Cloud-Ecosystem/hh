package pos

// snapshot_credit_test.go, the relief snapshot's credit leg at the unit seam
// (no database): a relief sale whose injected TaxAssessor binds the Ontario
// path must leave the bound ON credit in the W4 snapshot row, and a quote
// carrying no credit must leave the column zero. The assessor arrives through
// WithTaxAssessor (the same seam cmd/core wires the real engine over) and the
// snapshot row is read back through the harness store, so the leg exercises
// exactly the ring's snapshot write and nothing else.
//
// The leg binds the credit through the SEAM: the injected assessor's
// QuoteResult carries OnCreditCents and the row must carry that value, the
// same carriage the posTaxAdapter mapping gives the real engine's quote
// (decision log row 697's blessed field). Commit 1 carried this leg without
// the field by demanding the engine's own half-up 8 percent arithmetic; the
// fix commit completes the carriage, and the demand now reads straight off
// the quote the assessor returned.

import (
	"context"
	"testing"
)

// reliefAssessor is the test-injected TaxAssessor: it answers the bound quote
// the leg names, the way the engine answers a relief whose gate has passed.
type reliefAssessor struct {
	taxCents int64
	rateBps  int
	credit   int64 // the ON credit the quote carries across the seam
}

func (a reliefAssessor) AssessSale(context.Context, TaxQuoteInput) (QuoteResult, error) {
	return QuoteResult{TaxCents: a.taxCents, RateBps: a.rateBps, OnCreditCents: a.credit}, nil
}

// The bound Ontario quote on the harness's 100.00 subtotal: the 5 percent net
// (500 cents at 500 bps) with the engine's half-up 8 percent credit beside it
// (800 cents). The snapshot row must carry the QUOTE's credit: on the defect
// the ring writes the column never, so the FN audit trail loses the credit
// every bound relief earned.
func TestSale_ReliefSnapshot_RecordsTheBoundOnCredit(t *testing.T) {
	h := newHarness()
	bound := reliefAssessor{taxCents: 500, rateBps: 500, credit: 800}
	h.svc.WithTaxAssessor(bound)
	in := baseInput()
	in.Relief = &ReliefDecision{ReasonCode: TaxONFNPos8, EvidenceDocID: "doc-unit-1"}
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("relief sale: %v", err)
	}
	if out.TaxCents != 500 {
		t.Fatalf("the ring must collect the bound net (500), got %d", out.TaxCents)
	}
	if len(h.store.snapshots) != 1 {
		t.Fatalf("want 1 tax_decision_snapshot, got %d", len(h.store.snapshots))
	}
	row := h.store.snapshots[0]
	if row.TaxReasonCode != TaxONFNPos8 || row.NetCollectedCents != 500 {
		t.Fatalf("the row must be the relief's own snapshot at the bound net: %+v", row)
	}
	if row.OnCreditCents != bound.credit {
		t.Fatalf("the relief snapshot must carry the quote's ON credit %d, got %d: the ring writes the column never, so the audit trail loses the credit", bound.credit, row.OnCreditCents)
	}
}

// The zero arm of the credit leg: a quote carrying no credit leaves the
// column zero. On the defect this passes trivially (the ring wrote the column
// never); after the fix it is the freeze half, the ring must not invent a
// credit the quote did not bind.
func TestSale_ReliefSnapshot_NoBoundCredit_StaysZero(t *testing.T) {
	h := newHarness()
	h.svc.WithTaxAssessor(reliefAssessor{taxCents: 500, rateBps: 500, credit: 0})
	in := baseInput()
	in.Relief = &ReliefDecision{ReasonCode: TaxONFNPos8, EvidenceDocID: "doc-unit-1"}
	if _, err := h.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("relief sale: %v", err)
	}
	if len(h.store.snapshots) != 1 {
		t.Fatalf("want 1 tax_decision_snapshot, got %d", len(h.store.snapshots))
	}
	if row := h.store.snapshots[0]; row.OnCreditCents != 0 {
		t.Fatalf("a quote with no credit must leave on_credit_cents zero, got %d", row.OnCreditCents)
	}
}
