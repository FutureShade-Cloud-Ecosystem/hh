package tax

// snapshot_shape_test.go, the recompute parity leg: the W4 snapshot row the
// pos ring writes under an edited standard row must reconstruct the exact
// taxable subtotal through reconstructBase, the reader the W4 recompute and
// the HST to GL tie-out reason through. reconstructBase divides
// full_hst_cents by the CONSTANT standard rate because the column is ALWAYS
// stored at the standard rate by convention; a ring write that puts the
// assessed tax in the column (what main wrote beside its flat 1300 rate)
// breaks the convention and misreconstructs the base.

import "testing"

// ringReliefSnapshotShape mirrors, field for field, the W4 snapshot row the
// pos ring writes inside the Sale transaction's relief arm
// (core/internal/pos/service.go, the InsertTaxSnapshot under if in.Relief !=
// nil): rate_bps the assessed quote's rate, full_hst_cents the STANDARD-rate
// HST on the subtotal, net_collected_cents the assessed tax the ring
// collected. Keep it in step with that write: this leg exists so the shape
// the ring writes can never silently drift from the convention
// reconstructBase reads by.
func ringReliefSnapshotShape(subtotalCents int64, quoteRateBps int, assessedTax int64) Snapshot {
	return Snapshot{
		OrderID: "order-snapshot-shape", ReasonCode: ReasonONFNPos8, RuleVersion: ruleVersionW4,
		RateBps:       quoteRateBps,
		FullHSTCents:  hstOn(subtotalCents, stdRateBps), // the standard-rate HST the write mirrors (pos.flatHST13, bit-identical)
		OnCreditCents: 0, NetCollectedCents: assessedTax,
	}
}

func TestReconstructBase_RingReliefShape_RecoversSubtotal(t *testing.T) {
	const (
		subtotal  = int64(10000) // the exact-cents anchor shape the pos legs ring
		editedBps = 1500         // a standard row an admin edited away from 1300
	)
	assessed := hstOn(subtotal, editedBps) // what the ring collected at the edited row: 1500

	if got := reconstructBase(ringReliefSnapshotShape(subtotal, editedBps, assessed)); got != subtotal {
		t.Fatalf("the ring's W4 shape under an edited standard row misreconstructs the taxable base: got %d, want %d (full_hst must sit at the constant standard rate, not at the assessed rate)", got, subtotal)
	}
	// The canary, both directions: the assessed tax in the column is exactly
	// the misreconstruction the convention forbids, so the guard above can
	// never pass by accident.
	if recon := reconstructBase(Snapshot{FullHSTCents: assessed}); recon == subtotal {
		t.Fatalf("the assessed-rate shape must misreconstruct (%d back from %d), the guard would be vacuous", recon, assessed)
	}
}
