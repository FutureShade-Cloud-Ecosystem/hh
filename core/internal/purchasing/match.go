package purchasing

// match.go, the three-way-match arithmetic, as pure functions over numbers. No DB, no context, no clock:
// every rule below is a table test, and every constant in it is a ruling somebody can argue with.
//
// The three numbers (migration 0121 states the doctrine):
//   ORDERED   what we agreed to buy, and at what unit cost
//   RECEIVED  what landed on the yard
//   BILLED    what the vendor is asking to be paid for
//
// Two independent comparisons, because they fail differently and get triaged by different people:
//   QUANTITY  billed vs received   → the receiving clerk's disagreement with the vendor
//   PRICE     billed unit cost vs the PO's unit cost → the buyer's disagreement with the vendor

import "math"

// MatchInput is one bill line measured against the PO line it claims to bill. All money is CAD cents; the
// USD→CAD conversion happens before anything reaches here (DX-19), so a foreign amount never enters the match.
type MatchInput struct {
	// HasPOLine=false is a charge with no purchase order behind it. It has nothing to match against, so it
	// can never be MATCHED, it is UNMATCHED and it lands wholly on the variance account.
	HasPOLine       bool
	QtyOrderedBase  float64
	QtyReceivedBase float64
	// QtyBilledBase is THIS bill line's quantity; QtyBilledPriorBase is what other POSTED bills already
	// claimed against the same PO line. The match is against the RUNNING total, otherwise a vendor could bill
	// the same delivery three times and every individual bill would look perfectly matched.
	QtyBilledBase      float64
	QtyBilledPriorBase float64
	POUnitCostCents    int64
	BillUnitCostCents  int64
}

// MatchResult is the verdict plus the two signed variances. The signs are the diagnosis:
// QtyVarianceBase > 0 means billed for more than landed; PriceVarianceCents > 0 means charged above contract.
type MatchResult struct {
	State              string
	QtyVarianceBase    float64
	PriceVarianceCents int64
}

// EvaluateLine is the verdict for one bill line.
//
// PRICE TOLERANCE IS THE MORE FORGIVING OF PERCENT AND CENTS, deliberately. A 2% tolerance on a $3 pallet
// deposit is six cents, and pulling a manager off the counter to accept a six-cent variance is how a control
// gets switched off entirely. A flat floor keeps the percentage meaningful on the lines where percentages
// mean something.
//
// QUANTITY TOLERANCE IS A PERCENTAGE OF WHAT WAS RECEIVED, not of what was ordered. The disagreement is
// between the vendor's bill and our count of what came off the truck; the ordered quantity is not a party to
// it. (It defaults to 0%, see 0121.)
func EvaluateLine(in MatchInput, tol Tolerance) MatchResult {
	res := MatchResult{State: MatchUnmatched}
	if !in.HasPOLine {
		// The whole billed amount is unbacked, so it is all variance, that is what will hit 5250 when the
		// bill posts, and stating it here keeps the number the operator reads identical to the number posted.
		res.PriceVarianceCents = round(in.QtyBilledBase * float64(in.BillUnitCostCents))
		return res
	}

	billedTotal := in.QtyBilledPriorBase + in.QtyBilledBase
	res.QtyVarianceBase = billedTotal - in.QtyReceivedBase
	res.PriceVarianceCents = round(in.QtyBilledBase * float64(in.BillUnitCostCents-in.POUnitCostCents))

	qtyAllowance := math.Abs(in.QtyReceivedBase) * tol.QtyPct / 100
	expected := math.Abs(in.QtyBilledBase * float64(in.POUnitCostCents))
	priceAllowance := round(expected * tol.PricePct / 100)
	if tol.PriceAbsCents > priceAllowance {
		priceAllowance = tol.PriceAbsCents
	}

	qtyOK := math.Abs(res.QtyVarianceBase) <= qtyAllowance+qtyEpsilon
	priceOK := abs64(res.PriceVarianceCents) <= priceAllowance

	switch {
	case qtyOK && priceOK:
		res.State = MatchMatched
	case priceOK && res.QtyVarianceBase < 0:
		// Billed for FEWER units than landed, at the agreed price. Nobody is being overcharged, but GRNI
		// stays accrued on the un-billed units, so this must be visible and it must NOT be triaged as an
		// overcharge. That is the whole reason SHORT_BILLED is its own state.
		res.State = MatchShortBilled
	default:
		res.State = MatchVariance
	}
	return res
}

// matchRank orders the verdicts by how much they want a human. A bill's state is its worst line's state:
// one over-charged line makes the whole bill a VARIANCE no matter how many lines matched, because the
// document is paid as one.
var matchRank = map[string]int{
	MatchMatched:     0,
	MatchUnmatched:   1,
	MatchShortBilled: 2,
	MatchVariance:    3,
}

// RollUp is a bill's header state: the worst of its line states. An empty bill is UNMATCHED, there is
// nothing to have matched.
func RollUp(states []string) string {
	if len(states) == 0 {
		return MatchUnmatched
	}
	worst := MatchMatched
	for _, s := range states {
		if matchRank[s] > matchRank[worst] {
			worst = s
		}
	}
	return worst
}

// - the journal a posted bill writes -

// PostingLine is one bill line's inputs to the journal build.
type PostingLine struct {
	HasPOLine       bool
	QtyBilledBase   float64
	QtyReceivedBase float64
	// QtyBilledPriorBase is what earlier POSTED bills already relieved from GRNI on this PO line. Without it a
	// second bill for the same line would relieve the accrual twice and drive 2150 negative.
	QtyBilledPriorBase float64
	POUnitCostCents    int64
	LineTotalCents     int64
}

// JournalLegs is the per-account roll-up of a bill's journal.
//
//	DR 2150 GRNIReliefCents   the accrual receiving raised, now relieved
//	DR 2200 TaxCents          recoverable input tax
//	DR 5250 VarianceCents     signed, a negative value posts as a CREDIT to the same account
//	CR 2100 APCents           what the vendor is owed
//
// The identity that keeps it balanced, and that TestBuildJournalLegs_AlwaysBalances asserts over a spread of
// shapes: Σ relief + Σ variance ≡ Σ line totals ≡ subtotal, so relief + variance + tax ≡ subtotal + tax ≡ AP.
type JournalLegs struct {
	GRNIReliefCents int64
	TaxCents        int64
	VarianceCents   int64
	APCents         int64
}

// BuildJournalLegs computes the four legs.
//
// GRNI IS RELIEVED AT THE PO's PRICE, NOT THE BILL's, and only for units that were actually received and not
// already billed. That is the entire point of the account: receiving credited 2150 with received-qty ×
// PO-cost, so relieving anything else would leave a residue in 2150 that no reconciliation could explain. The
// gap between what the vendor charged and what we relieved IS the variance, by construction, which is why
// the variance leg is a subtraction and not a second opinion.
func BuildJournalLegs(lines []PostingLine, taxCents int64) JournalLegs {
	var legs JournalLegs
	legs.TaxCents = taxCents
	for _, l := range lines {
		relief := int64(0)
		if l.HasPOLine {
			reliefQty := l.QtyReceivedBase - l.QtyBilledPriorBase
			if reliefQty > l.QtyBilledBase {
				reliefQty = l.QtyBilledBase
			}
			if reliefQty > 0 {
				relief = round(reliefQty * float64(l.POUnitCostCents))
			}
		}
		legs.GRNIReliefCents += relief
		legs.VarianceCents += l.LineTotalCents - relief
		legs.APCents += l.LineTotalCents
	}
	legs.APCents += taxCents
	return legs
}

// - small numeric helpers (same rounding rule as inventory/landedcost) -

// round is half-away-from-zero to whole cents, the same rule internal/inventory uses, so a value computed
// here and a value computed there for the same quantity × price agree to the cent.
func round(f float64) int64 { return int64(math.Round(f)) }

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
