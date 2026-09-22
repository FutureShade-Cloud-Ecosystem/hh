package pricing

// counter.go, the COUNTER PRICE PICTURE: retail alongside this customer's price, and the saving
// between them, answered by the pricing engine itself.
//
// WHY THIS LIVES IN pricing AND NOT IN pos. The client asked the counter to "display retail + the
// account specific pricing depending on the customer type, also display savings over retail for
// special pricing and bulk pricing". Every number in that sentence is a PRICE, and this package is
// the only thing allowed to decide a price. A caller that subtracted two numbers it was handed
// would be a second pricing implementation with no tests, no audit trail and no waterfall, and it
// would drift the first time a rule changed. So the SUBTRACTION happens here, beside the waterfall
// that produced both operands, and the counter renders what it is told.
//
// WHAT "RETAIL" IS. Breakdown.OriginalCents, the retail anchor the waterfall resolved, already
// converted into the result price_uom. It is the same anchor every PCT_OFF_LIST discount is taken
// off, so "retail" on the counter and "retail" in the engine are one number by construction rather
// than by agreement. One resolve therefore yields BOTH sides of the comparison; there is no second
// round-trip and no window in which the two could disagree.
//
// READ-ONLY BY CONSTRUCTION. Every resolve here goes through ResolvePricePreview (dryRun), so
// browsing a search result, painting a quick-pick tile or peeking at a bulk threshold can never
// append a pricing_audit_log row. Only ringing the sale prices for real (DeriveLinePrice).
//
// NO COST, EVER. A CounterQuote carries retail, the customer's price and the difference. Margin,
// floor, profit and cost are NOT on this struct, not gated, not omitempty, ABSENT. This is the
// customer's side of the counter; the dealer's cost is MarginGlance's business (pos/rev8.go), role
// -gated at its own marshal layer. Keeping them in different structs is what makes that a fact
// about the type rather than a promise about a code path.

import (
	"context"
	"sort"
)

// Basis names WHY this customer's price differs from retail, the counter's one-word explanation,
// chosen server-side so the wording cannot drift between screens. Empty means "no difference".
const (
	BasisContract = "CONTRACT" // a negotiated CUSTOMER_PRODUCT price (final; no break stacks on it)
	BasisAccount  = "ACCOUNT"  // this account's own or its tier's standing discount
	BasisPromo    = "PROMO"    // a promotion won base selection
	BasisBulk     = "BULK"     // a quantity break adjusted the base downward
)

// CounterQuote is one product's price picture for one customer at one quantity.
//
// RetailCents and CustomerCents are both denominated in PriceUOM. SavingCents is per PriceUOM and
// is set only when the customer is genuinely better off than retail; a surcharge (the partial-skid
// case) leaves it zero rather than reporting a negative saving, because "you save -$4.10" is not a
// thing a counter should ever say.
type CounterQuote struct {
	ProductID     string `json:"product_id"`
	PriceUOM      string `json:"price_uom"`
	RetailCents   int64  `json:"retail_cents"`
	CustomerCents int64  `json:"customer_cents"`

	// SavingCents / SavingBps, the server's subtraction, per PriceUOM. Absent when zero: a
	// walk-in, or an account whose price for this item is simply retail, must not be shown a
	// "you save $0.00" badge.
	SavingCents int64 `json:"saving_cents,omitempty"`
	SavingBps   int64 `json:"saving_bps,omitempty"`
	// Basis is set whenever SavingCents is, one of the Basis* words above.
	Basis string `json:"basis,omitempty"`

	// - the UNREACHED bulk break (the opportunity) -
	// A discount break the customer has NOT yet qualified for, at the nearest threshold above the
	// current quantity. Present only when one exists AND it actually beats the price they are
	// getting now. The counter shows it as an offer to make, never as a price already given.
	NextBreakQty       float64 `json:"next_break_qty,omitempty"`
	NextBreakUOM       string  `json:"next_break_uom,omitempty"`
	NextBreakUnitCents int64   `json:"next_break_unit_cents,omitempty"`
	NextBreakSaveCents int64   `json:"next_break_save_cents,omitempty"`

	// Unpriceable marks a product the waterfall could not price at all (no anchor, no cost to
	// derive one from, no UOM conversion). The counter renders no price rather than a $0.00 -
	// empty-never-error, and never a number somebody might sell at.
	Unpriceable bool `json:"unpriceable,omitempty"`
}

// CounterQuoteInput asks for one product's picture. CustomerID empty = the walk-in case: retail
// stands alone, and the result carries no saving and no basis by construction.
type CounterQuoteInput struct {
	CustomerID   string
	CustomerType string
	ProductID    string
	BranchID     string
	QtyEntered   float64
	UOMEntered   string
}

// CounterQuotes answers a whole screen's worth of products in one call, the search results, the
// quick-pick grid, or the ticket's lines. One resolve per product (the waterfall is a single query
// set per product and already caches its UOM factors); an item that cannot be priced comes back
// marked Unpriceable rather than failing the batch, because one bad SKU must never blank a
// counter screen.
func (s *Service) CounterQuotes(ctx context.Context, ins []CounterQuoteInput) ([]CounterQuote, error) {
	out := make([]CounterQuote, 0, len(ins))
	for _, in := range ins {
		q, err := s.CounterQuote(ctx, in)
		if err != nil {
			out = append(out, CounterQuote{ProductID: in.ProductID, Unpriceable: true})
			continue
		}
		out = append(out, q)
	}
	return out, nil
}

// CounterQuote answers one product's picture: retail, this customer's price, the saving between
// them, and the nearest bulk threshold they have not reached yet.
func (s *Service) CounterQuote(ctx context.Context, in CounterQuoteInput) (CounterQuote, error) {
	qty := in.QtyEntered
	if qty <= 0 {
		qty = 1
	}
	custType := in.CustomerType
	if custType == "" {
		custType = "RETAIL"
	}
	base := ResolveInput{
		CustomerID:   in.CustomerID,
		CustomerType: custType,
		ProductID:    in.ProductID,
		BranchID:     in.BranchID,
		QtyEntered:   qty,
		UOMEntered:   in.UOMEntered,
	}
	bd, err := s.ResolvePricePreview(ctx, base)
	if err != nil {
		return CounterQuote{}, err
	}
	q := CounterQuote{
		ProductID:     in.ProductID,
		PriceUOM:      bd.PriceUOM,
		RetailCents:   bd.OriginalCents,
		CustomerCents: bd.FinalCents,
	}
	// THE SAVING, computed here, next to the waterfall that produced both numbers. Only a real
	// advantage counts: an uplift (partial-skid surcharge) or an equal price yields nothing.
	if bd.OriginalCents > bd.FinalCents && bd.FinalCents > 0 {
		q.SavingCents = bd.OriginalCents - bd.FinalCents
		q.SavingBps = bpsOff(bd.OriginalCents, bd.FinalCents)
		q.Basis = counterBasis(ctx, s, base, bd)
	}
	// The unreached threshold. Best-effort by design: a product whose breaks cannot be read still
	// has a perfectly good price picture, and losing the whole answer to an optional upsell would
	// be the worse trade.
	s.attachNextBreak(ctx, base, bd, &q)
	return q, nil
}

// counterBasis picks the ONE word that explains the gap. A contract price is final and cannot have
// a break on it, so it is answered first and without a second lookup. Otherwise BULK wins over the
// standing account discount when a discount break actually applied, because the break is the part
// of the price the customer can change by buying more, the actionable half of the explanation.
func counterBasis(ctx context.Context, s *Service, in ResolveInput, bd Breakdown) string {
	switch bd.Source {
	case SourceCustomerProduct:
		return BasisContract
	case SourcePromotion:
		return BasisPromo
	}
	if b, _ := s.appliedBreak(ctx, in); b != nil && !isSurchargeBreak(b.ValueType) {
		return BasisBulk
	}
	return BasisAccount
}

// appliedBreak re-selects the quantity break the waterfall applied for this line, using the SAME
// store read and the SAME selectBreak the resolver used. Re-selecting rather than plumbing the
// answer out of Breakdown keeps the shipped Breakdown contract byte-identical, no consumer of the
// resolver has to learn a new field to keep working.
//
// A CUSTOMER_PRODUCT contract price never has a break on it (step 4 of the waterfall), so callers
// must not reach here on that path; the guard below makes that safe rather than merely documented.
func (s *Service) appliedBreak(ctx context.Context, in ResolveInput) (*QuantityBreak, error) {
	breaks, factor, err := s.breakContext(ctx, &in)
	if err != nil || len(breaks) == 0 {
		return nil, err
	}
	return s.selectBreak(breaks, in, factor)
}

// breakContext loads the applicable breaks for a line plus the UOM factor function the threshold
// comparison needs, the resolver's own two ingredients, borrowed. It resolves CategoryID through
// the catalog seam when the caller left it empty, exactly as resolve() does, so a category-scoped
// break is found for a caller that only knows the product.
func (s *Service) breakContext(ctx context.Context, in *ResolveInput) ([]QuantityBreak, func(string) (float64, error), error) {
	if in.CategoryID == "" && s.cat != nil {
		cid, err := s.cat.GetCategory(ctx, in.ProductID)
		if err != nil {
			return nil, nil, err
		}
		in.CategoryID = cid
	}
	breaks, err := s.store.QuantityBreaksFor(ctx, in.ProductID, in.CategoryID)
	if err != nil {
		return nil, nil, err
	}
	fcache := map[string]float64{}
	productID := in.ProductID
	factor := func(uom string) (float64, error) {
		if v, ok := fcache[uom]; ok {
			return v, nil
		}
		f, ferr := s.uomFactorFor(ctx, productID, uom)
		if ferr != nil {
			return 0, ferr
		}
		fcache[uom] = f
		return f, nil
	}
	return breaks, factor, nil
}

// attachNextBreak finds the nearest DISCOUNT threshold above the current quantity and prices the
// line AT that threshold through the ordinary waterfall, so the offered number is a real resolved
// price and not an extrapolation.
//
// A contract-priced line is skipped: breaks do not stack on a negotiated price, so telling that
// customer "buy a pallet and save" would be a promise the engine would refuse to keep.
// SURCHARGE tiers are skipped too, crossing INTO a surcharge is not an opportunity.
func (s *Service) attachNextBreak(ctx context.Context, in ResolveInput, bd Breakdown, q *CounterQuote) {
	if bd.Source == SourceCustomerProduct || q.Unpriceable {
		return
	}
	probe := in
	breaks, factor, err := s.breakContext(ctx, &probe)
	if err != nil || len(breaks) == 0 {
		return
	}
	// Candidate thresholds: discount tiers whose FromQty sits ABOVE where this line already is,
	// measured in each tier's own threshold UOM.
	type cand struct {
		fromQty float64
		uom     string
		entered float64 // the same threshold expressed in the line's entered UOM
	}
	var cands []cand
	for i := range breaks {
		b := breaks[i]
		if isSurchargeBreak(b.ValueType) || b.FromQty <= 0 {
			continue
		}
		fEnt, e1 := factor(probe.UOMEntered)
		fThr, e2 := factor(b.ThresholdUOM)
		if e1 != nil || e2 != nil || fThr == 0 || fEnt == 0 {
			continue
		}
		here := probe.QtyEntered * fEnt / fThr // the line's qty in the tier's threshold UOM
		if here >= b.FromQty {
			continue // already qualified, this is not an opportunity, it is the current price
		}
		cands = append(cands, cand{fromQty: b.FromQty, uom: b.ThresholdUOM, entered: b.FromQty * fThr / fEnt})
	}
	if len(cands) == 0 {
		return
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].fromQty < cands[j].fromQty })
	best := cands[0]
	at := probe
	at.QtyEntered = best.entered
	atBd, err := s.ResolvePricePreview(ctx, at)
	if err != nil {
		return
	}
	// Only an offer that actually beats what they are paying now is worth a pixel.
	if atBd.FinalCents <= 0 || atBd.FinalCents >= bd.FinalCents {
		return
	}
	q.NextBreakQty = best.fromQty
	q.NextBreakUOM = best.uom
	q.NextBreakUnitCents = atBd.FinalCents
	q.NextBreakSaveCents = bd.FinalCents - atBd.FinalCents
}
