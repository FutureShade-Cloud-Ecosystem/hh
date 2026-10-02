package pos

// snapshot_credit_test.go, the relief snapshot's credit leg at the unit seam
// (no database): a relief sale whose injected TaxAssessor binds the Ontario
// path must leave the bound ON credit in the W4 snapshot row, and a quote
// carrying no credit must leave the column zero. The assessor arrives through
// WithTaxAssessor (the same seam cmd/core wires the real engine over) and the
// snapshot row is read back through the harness store, so the leg exercises
// exactly the ring's snapshot write and nothing else.
//
// Commit 1 carries this leg without the seam field: pos.QuoteResult cannot
// carry a credit yet, so the injected assessor binds the relief by its own
// rate shape and the leg demands the engine's credit arithmetic in the row
// (the half-up 8 percent the engine hangs beside a bound ON quote). The fix
// commit completes the carriage on the seam (OnCreditCents on QuoteResult,
// decision log row 697) and the leg then reads the credit straight off the
// quote the assessor returned. The lane's compile-order split; the leg is
// fully asserted at head.

import (
	"context"
	"testing"
)

// reliefAssessor is the test-injected TaxAssessor: it answers the bound quote
// the leg names, the way the engine answers a relief whose gate has passed.
type reliefAssessor struct {
	taxCents int64
	rateBps  int
}

func (a reliefAssessor) AssessSale(context.Context, TaxQuoteInput) (QuoteResult, error) {
	return QuoteResult{TaxCents: a.taxCents, RateBps: a.rateBps}, nil
}

// The bound Ontario quote on the harness's 100.00 subtotal: the 5 percent net
// (500 cents at 500 bps), and beside it the credit the engine computes for a
// bound ON_FN_POS_8, the half-up 8 percent (800 cents). The snapshot row must
// carry that credit: on the defect the ring writes the column never, so the
// FN audit trail loses the credit every bound relief earned.
func TestSale_ReliefSnapshot_RecordsTheBoundOnCredit(t *testing.T) {
	h := newHarness()
	h.svc.WithTaxAssessor(reliefAssessor{taxCents: 500, rateBps: 500})
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
	const wantCredit = int64(800) // the engine's half-up 8% on the 100.00 subtotal
	if row.OnCreditCents != wantCredit {
		t.Fatalf("the relief snapshot must carry the bound ON credit %d, got %d: the ring writes the column never, so the audit trail loses the credit", wantCredit, row.OnCreditCents)
	}
}
