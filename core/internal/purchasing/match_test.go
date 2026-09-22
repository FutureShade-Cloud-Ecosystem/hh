package purchasing

// match_test.go, the three-way-match arithmetic and the journal it drives. Every number in here is a
// ruling: change one and a test names which ruling you changed.

import "testing"

// strictTol is the 0121 org default: no quantity slack, 2% or 500¢ on price (whichever is more forgiving).
var strictTol = Tolerance{QtyPct: 0, PricePct: 2, PriceAbsCents: 500}

// zeroTol removes even the absolute price floor, so a single cent of price drift is a VARIANCE. Used to pin
// the arithmetic itself rather than the tolerance policy.
var zeroTol = Tolerance{QtyPct: 0, PricePct: 0, PriceAbsCents: 0}

func TestEvaluateLine_MatchedWhenBilledEqualsReceivedAtTheAgreedPrice(t *testing.T) {
	res := EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 10, QtyBilledBase: 10,
		POUnitCostCents: 2500, BillUnitCostCents: 2500,
	}, zeroTol)
	if res.State != MatchMatched {
		t.Fatalf("state = %s, want MATCHED", res.State)
	}
	if res.QtyVarianceBase != 0 || res.PriceVarianceCents != 0 {
		t.Fatalf("a clean match must show zero variances, got qty=%v price=%d", res.QtyVarianceBase, res.PriceVarianceCents)
	}
}

// TestEvaluateLine_PriceVarianceArithmetic pins the formula: billed qty × (billed unit − PO unit).
func TestEvaluateLine_PriceVarianceArithmetic(t *testing.T) {
	cases := []struct {
		name             string
		qty              float64
		poCost, billCost int64
		want             int64
	}{
		{"charged 3¢ more on 10 units", 10, 2500, 2503, 30},
		{"charged 3¢ less on 10 units", 10, 2500, 2497, -30},
		{"fractional quantity rounds half away from zero", 2.5, 1000, 1001, 3},
		{"identical prices", 7, 999, 999, 0},
		{"large drift", 100, 5000, 5500, 50000},
	}
	for _, c := range cases {
		res := EvaluateLine(MatchInput{
			HasPOLine: true, QtyOrderedBase: c.qty, QtyReceivedBase: c.qty, QtyBilledBase: c.qty,
			POUnitCostCents: c.poCost, BillUnitCostCents: c.billCost,
		}, zeroTol)
		if res.PriceVarianceCents != c.want {
			t.Errorf("%s: price variance = %d, want %d", c.name, res.PriceVarianceCents, c.want)
		}
	}
}

// TestEvaluateLine_PriceVarianceIsOnTheBILLEDQuantity pins WHICH quantity the price variance multiplies.
// It is the quantity the vendor is charging for, not the quantity that landed and not the quantity ordered:
// the number being disputed is "what they are asking us to pay against what we agreed to pay for it". Keying
// it off the received quantity instead looks identical on every clean line, billed == received there, and
// only diverges on exactly the lines that matter, which is why this case exists separately from the table
// above. (Mutation-checked: swapping QtyBilledBase for QtyReceivedBase in EvaluateLine survives the table and
// is killed here.)
func TestEvaluateLine_PriceVarianceIsOnTheBILLEDQuantity(t *testing.T) {
	// 4 landed, the vendor billed 10, at 100¢/unit over the agreed price.
	res := EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 4, QtyBilledBase: 10,
		POUnitCostCents: 1000, BillUnitCostCents: 1100,
	}, zeroTol)
	if res.PriceVarianceCents != 1000 {
		t.Fatalf("price variance = %d, want 1000 (10 BILLED units × 100¢), not 400 (4 received)", res.PriceVarianceCents)
	}
	// And the mirror: more landed than billed.
	res = EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 10, QtyBilledBase: 3,
		POUnitCostCents: 1000, BillUnitCostCents: 1100,
	}, zeroTol)
	if res.PriceVarianceCents != 300 {
		t.Fatalf("price variance = %d, want 300 (3 BILLED units × 100¢), not 1000 (10 received)", res.PriceVarianceCents)
	}
	// A line with nothing received yet still prices the variance off what is being billed.
	res = EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 0, QtyBilledBase: 6,
		POUnitCostCents: 2000, BillUnitCostCents: 2500,
	}, zeroTol)
	if res.PriceVarianceCents != 3000 {
		t.Fatalf("price variance = %d, want 3000, a zero received quantity must not zero the variance", res.PriceVarianceCents)
	}
}

// TestEvaluateLine_QtyVarianceIsAgainstReceivedNotOrdered, the disagreement being measured is between the
// vendor's bill and OUR COUNT of what came off the truck. The ordered quantity is not a party to it.
func TestEvaluateLine_QtyVarianceIsAgainstReceivedNotOrdered(t *testing.T) {
	res := EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 100, QtyReceivedBase: 40, QtyBilledBase: 40,
		POUnitCostCents: 1000, BillUnitCostCents: 1000,
	}, zeroTol)
	if res.QtyVarianceBase != 0 {
		t.Fatalf("billing exactly what was received on a part-delivered PO is not a variance, got %v", res.QtyVarianceBase)
	}
	if res.State != MatchMatched {
		t.Fatalf("state = %s, want MATCHED", res.State)
	}
}

// TestEvaluateLine_ShortBilled, fewer units billed than landed, at the agreed price. Nobody is overcharged,
// but GRNI stays accrued on the un-billed units, so it gets its own state and is not triaged as an overcharge.
func TestEvaluateLine_ShortBilled(t *testing.T) {
	res := EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 10, QtyBilledBase: 7,
		POUnitCostCents: 2500, BillUnitCostCents: 2500,
	}, strictTol)
	if res.State != MatchShortBilled {
		t.Fatalf("state = %s, want SHORT_BILLED", res.State)
	}
	if res.QtyVarianceBase != -3 {
		t.Fatalf("qty variance = %v, want -3", res.QtyVarianceBase)
	}
}

// TestEvaluateLine_OverBilledIsVarianceNotShortBilled, billing for MORE than landed costs money, so it is a
// VARIANCE. The sign of the quantity difference is the whole distinction between the two states.
func TestEvaluateLine_OverBilledIsVarianceNotShortBilled(t *testing.T) {
	res := EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 6, QtyBilledBase: 10,
		POUnitCostCents: 2500, BillUnitCostCents: 2500,
	}, strictTol)
	if res.State != MatchVariance {
		t.Fatalf("state = %s, want VARIANCE", res.State)
	}
	if res.QtyVarianceBase != 4 {
		t.Fatalf("qty variance = %v, want +4", res.QtyVarianceBase)
	}
}

// TestEvaluateLine_ShortBilledWithPriceDriftIsVariance, a short bill that ALSO overcharges is not a benign
// short bill. The price breach dominates.
func TestEvaluateLine_ShortBilledWithPriceDriftIsVariance(t *testing.T) {
	res := EvaluateLine(MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 10, QtyBilledBase: 7,
		POUnitCostCents: 2500, BillUnitCostCents: 3500,
	}, strictTol)
	if res.State != MatchVariance {
		t.Fatalf("state = %s, want VARIANCE", res.State)
	}
}

// TestEvaluateLine_PriorBillsCount, the match is against the RUNNING billed total. Without this a vendor
// could bill the same delivery three times and every individual bill would look perfectly matched.
func TestEvaluateLine_PriorBillsCount(t *testing.T) {
	in := MatchInput{
		HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 10, QtyBilledBase: 10,
		QtyBilledPriorBase: 10, POUnitCostCents: 2500, BillUnitCostCents: 2500,
	}
	res := EvaluateLine(in, strictTol)
	if res.State != MatchVariance {
		t.Fatalf("a second bill for an already-billed delivery must be a VARIANCE, got %s", res.State)
	}
	if res.QtyVarianceBase != 10 {
		t.Fatalf("qty variance = %v, want +10 (20 billed against 10 received)", res.QtyVarianceBase)
	}
}

// TestEvaluateLine_ToleranceBands pins both tolerance dimensions at their boundaries.
func TestEvaluateLine_ToleranceBands(t *testing.T) {
	// Price: 2% of 10 × 10000¢ = 2000¢, which beats the 500¢ floor. 2000 passes, 2001 does not.
	base := MatchInput{HasPOLine: true, QtyOrderedBase: 10, QtyReceivedBase: 10, QtyBilledBase: 10, POUnitCostCents: 10000}
	within := base
	within.BillUnitCostCents = 10200 // +200¢/unit × 10 = 2000¢ exactly
	if got := EvaluateLine(within, strictTol); got.State != MatchMatched {
		t.Fatalf("a variance exactly ON the tolerance must pass, got %s (%d¢)", got.State, got.PriceVarianceCents)
	}
	over := base
	over.BillUnitCostCents = 10201 // 2010¢
	if got := EvaluateLine(over, strictTol); got.State != MatchVariance {
		t.Fatalf("a variance above the tolerance must fail, got %s (%d¢)", got.State, got.PriceVarianceCents)
	}

	// The absolute floor is the MORE FORGIVING of the two: 2% of a $3 line is 6¢, but 500¢ is allowed.
	tiny := MatchInput{HasPOLine: true, QtyOrderedBase: 1, QtyReceivedBase: 1, QtyBilledBase: 1,
		POUnitCostCents: 300, BillUnitCostCents: 700}
	if got := EvaluateLine(tiny, strictTol); got.State != MatchMatched {
		t.Fatalf("the absolute floor must forgive a 400¢ drift on a $3 line, got %s", got.State)
	}
	tiny.BillUnitCostCents = 801 // 501¢ over, past the floor
	if got := EvaluateLine(tiny, strictTol); got.State != MatchVariance {
		t.Fatalf("past the absolute floor must be a VARIANCE, got %s", got.State)
	}

	// Quantity: 5% of 20 received = 1.0 unit.
	qtyTol := Tolerance{QtyPct: 5, PricePct: 2, PriceAbsCents: 500}
	q := MatchInput{HasPOLine: true, QtyOrderedBase: 20, QtyReceivedBase: 20, QtyBilledBase: 21,
		POUnitCostCents: 100, BillUnitCostCents: 100}
	if got := EvaluateLine(q, qtyTol); got.State != MatchMatched {
		t.Fatalf("1 unit over on a 5%%-of-20 tolerance must pass, got %s", got.State)
	}
	q.QtyBilledBase = 21.5
	if got := EvaluateLine(q, qtyTol); got.State != MatchVariance {
		t.Fatalf("1.5 units over a 1.0 tolerance must be a VARIANCE, got %s", got.State)
	}
}

// TestEvaluateLine_NoPOLineIsUnmatchedAndAllVariance, a charge with no purchase order behind it (a fuel
// surcharge, a pallet deposit) has nothing to match against, and the whole amount is what will hit 5250.
func TestEvaluateLine_NoPOLineIsUnmatchedAndAllVariance(t *testing.T) {
	res := EvaluateLine(MatchInput{HasPOLine: false, QtyBilledBase: 1, BillUnitCostCents: 7500}, strictTol)
	if res.State != MatchUnmatched {
		t.Fatalf("state = %s, want UNMATCHED", res.State)
	}
	if res.PriceVarianceCents != 7500 {
		t.Fatalf("an unbacked charge is variance in full: got %d, want 7500", res.PriceVarianceCents)
	}
}

func TestRollUp(t *testing.T) {
	cases := []struct {
		name   string
		states []string
		want   string
	}{
		{"empty", nil, MatchUnmatched},
		{"all matched", []string{MatchMatched, MatchMatched}, MatchMatched},
		{"one variance dominates", []string{MatchMatched, MatchShortBilled, MatchVariance}, MatchVariance},
		{"short-billed beats unmatched", []string{MatchUnmatched, MatchShortBilled}, MatchShortBilled},
		{"unmatched beats matched", []string{MatchMatched, MatchUnmatched}, MatchUnmatched},
	}
	for _, c := range cases {
		if got := RollUp(c.states); got != c.want {
			t.Errorf("%s: RollUp = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestBuildJournalLegs_ReliefIsAtThePOPrice, receiving credited 2150 with received-qty × PO-cost, so
// relieving anything else would leave a residue in 2150 no reconciliation could explain.
func TestBuildJournalLegs_ReliefIsAtThePOPrice(t *testing.T) {
	legs := BuildJournalLegs([]PostingLine{{
		HasPOLine: true, QtyBilledBase: 10, QtyReceivedBase: 10, POUnitCostCents: 2500,
		LineTotalCents: 25300, // vendor charged 2530¢/unit
	}}, 0)
	if legs.GRNIReliefCents != 25000 {
		t.Fatalf("GRNI relief = %d, want 25000 (10 × the PO's 2500)", legs.GRNIReliefCents)
	}
	if legs.VarianceCents != 300 {
		t.Fatalf("variance = %d, want 300 (the 30¢/unit overcharge)", legs.VarianceCents)
	}
	if legs.APCents != 25300 {
		t.Fatalf("AP = %d, want 25300", legs.APCents)
	}
}

// TestBuildJournalLegs_ReliefCappedAtWhatWasReceived, billing 10 against 6 received relieves only the 6
// units of accrual that exist; the other 4 units' worth is variance, which is exactly why an over-billed
// line shows up in the ledger and not only on a screen.
func TestBuildJournalLegs_ReliefCappedAtWhatWasReceived(t *testing.T) {
	legs := BuildJournalLegs([]PostingLine{{
		HasPOLine: true, QtyBilledBase: 10, QtyReceivedBase: 6, POUnitCostCents: 1000, LineTotalCents: 10000,
	}}, 0)
	if legs.GRNIReliefCents != 6000 {
		t.Fatalf("GRNI relief = %d, want 6000 (only 6 units were accrued)", legs.GRNIReliefCents)
	}
	if legs.VarianceCents != 4000 {
		t.Fatalf("variance = %d, want 4000", legs.VarianceCents)
	}
}

// TestBuildJournalLegs_PriorBillsDoNotRelieveTwice, a second bill against a line already billed in full
// relieves nothing, so 2150 cannot be driven negative by a duplicate invoice.
func TestBuildJournalLegs_PriorBillsDoNotRelieveTwice(t *testing.T) {
	legs := BuildJournalLegs([]PostingLine{{
		HasPOLine: true, QtyBilledBase: 10, QtyReceivedBase: 10, QtyBilledPriorBase: 10,
		POUnitCostCents: 1000, LineTotalCents: 10000,
	}}, 0)
	if legs.GRNIReliefCents != 0 {
		t.Fatalf("GRNI relief = %d, want 0, the accrual was already relieved", legs.GRNIReliefCents)
	}
	if legs.VarianceCents != 10000 {
		t.Fatalf("variance = %d, want the whole 10000", legs.VarianceCents)
	}
}

func TestBuildJournalLegs_ShortBillLeavesGRNIAccrued(t *testing.T) {
	legs := BuildJournalLegs([]PostingLine{{
		HasPOLine: true, QtyBilledBase: 7, QtyReceivedBase: 10, POUnitCostCents: 1000, LineTotalCents: 7000,
	}}, 0)
	if legs.GRNIReliefCents != 7000 {
		t.Fatalf("GRNI relief = %d, want 7000 (only what was billed)", legs.GRNIReliefCents)
	}
	if legs.VarianceCents != 0 {
		t.Fatalf("a short bill at the agreed price has no price variance, got %d", legs.VarianceCents)
	}
	// The remaining 3000¢ of accrual stays in 2150, that IS the point of the SHORT_BILLED state.
}

// TestBuildJournalLegs_AlwaysBalances is the property that keeps the journal postable at all:
// relief + tax + variance ≡ AP, for every shape.
func TestBuildJournalLegs_AlwaysBalances(t *testing.T) {
	shapes := [][]PostingLine{
		{{HasPOLine: true, QtyBilledBase: 10, QtyReceivedBase: 10, POUnitCostCents: 2500, LineTotalCents: 25000}},
		{{HasPOLine: true, QtyBilledBase: 3.25, QtyReceivedBase: 3.25, POUnitCostCents: 999, LineTotalCents: 3247}},
		{{HasPOLine: false, QtyBilledBase: 1, LineTotalCents: 7500}},
		{
			{HasPOLine: true, QtyBilledBase: 10, QtyReceivedBase: 6, POUnitCostCents: 1000, LineTotalCents: 10000},
			{HasPOLine: true, QtyBilledBase: 2, QtyReceivedBase: 5, POUnitCostCents: 4000, LineTotalCents: 7500},
			{HasPOLine: false, QtyBilledBase: 1, LineTotalCents: 2500},
		},
		{{HasPOLine: true, QtyBilledBase: 5, QtyReceivedBase: 5, POUnitCostCents: 2000, LineTotalCents: 9000}}, // billed BELOW cost
	}
	for _, taxCents := range []int64{0, 1300, 99} {
		for i, lines := range shapes {
			legs := BuildJournalLegs(lines, taxCents)
			if got := legs.GRNIReliefCents + legs.TaxCents + legs.VarianceCents; got != legs.APCents {
				t.Errorf("shape %d tax %d: legs do not balance, DR %d ≠ CR %d", i, taxCents, got, legs.APCents)
			}
			if legs.GRNIReliefCents < 0 {
				t.Errorf("shape %d: GRNI relief must never be negative, got %d", i, legs.GRNIReliefCents)
			}
		}
	}
}

func TestCheckInstalments(t *testing.T) {
	if err := checkInstalments(nil, 10000); err != nil {
		t.Fatalf("an empty schedule is legal (due in full): %v", err)
	}
	ok := []InstalmentParams{{DueDate: "2026-09-01", AmountCents: 4000}, {DueDate: "2026-10-01", AmountCents: 6000}}
	if err := checkInstalments(ok, 10000); err != nil {
		t.Fatalf("a schedule that adds up must be accepted: %v", err)
	}
	if err := checkInstalments(ok, 10001); err == nil {
		t.Fatal("a schedule that does not add up must be refused")
	}
	bad := []InstalmentParams{{DueDate: "2026-09-01", AmountCents: 0}}
	if err := checkInstalments(bad, 0); err == nil {
		t.Fatal("a zero instalment must be refused")
	}
	noDate := []InstalmentParams{{AmountCents: 10000}}
	if err := checkInstalments(noDate, 10000); err == nil {
		t.Fatal("an instalment with no due date must be refused")
	}
}

// TestNormalizeCurrency guards the ONE place a foreign currency still enters this module: the landed-cost
// document (landed_costs is allowlisted for DX-19; purchase orders and bills are CAD-only, see service.go).
// A USD document with no rate must be refused rather than silently treated as par, a freight invoice
// capitalized at 1.00 when the rate is 1.37 understates inventory by 27%.
func TestNormalizeCurrency(t *testing.T) {
	if c, fx, err := normalizeCurrency("", 0); err != nil || c != "CAD" || fx != 1.0 {
		t.Fatalf("an empty currency defaults to CAD/1.0, got %q/%v/%v", c, fx, err)
	}
	if c, fx, err := normalizeCurrency("cad", 1.37); err != nil || c != "CAD" || fx != 1.0 {
		t.Fatalf("a CAD document never carries a manual fx, got %q/%v/%v", c, fx, err)
	}
	if c, fx, err := normalizeCurrency("usd", 1.37); err != nil || c != "USD" || fx != 1.37 {
		t.Fatalf("USD/1.37 must pass through, got %q/%v/%v", c, fx, err)
	}
	if _, _, err := normalizeCurrency("USD", 0); err == nil {
		t.Fatal("a USD document with no fx rate must be refused, not treated as par")
	}
	if _, _, err := normalizeCurrency("EUR", 1.5); err == nil {
		t.Fatal("an unsupported currency must be refused")
	}
}
