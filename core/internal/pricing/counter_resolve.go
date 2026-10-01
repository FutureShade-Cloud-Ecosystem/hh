package pricing

import "context"

// counter_resolve.go, the COUNTER RESOLVE (D48 row 481, D49): the till's door onto the same
// waterfall, the margin gate step never asked.
//
// TWO KINDS OF SALE. A relationship sale is the quote flow, and the margin floor lives there
// alone: POST /api/v1/pricing/resolve and every quote line a salesperson prices below the price
// the customer's rules and tiers give still meet the gate, exactly as before. A counter checkout
// rings each line at the price the customer's pricing rules and tiers give, and that price is
// accepted: no manual price change, no margin floor, no percentage off and no keyed promotion at
// the till. This file is the till's half of that ruling: the full waterfall that picks the
// customer's rule and tier price, stopping before the floor question.
//
// SET ONLY BY SERVER CODE. There is no input field, no request body key and no HTTP route that
// selects this path. It is an exported method on the Service, and its only callers are server
// modules that reach it in Go (the till's sale and the till's pricing order). The generic
// resolve route and the quote flow cannot get here, which is what keeps their gate byte-identical.
//
// NO BYPASS ROW, EVER. The only audit writes in the waterfall live inside the margin gate step
// (the enforcement-disabled BYPASS row and the approved-override BYPASS row). The counter resolve
// returns before that step, so a till line can never write a BYPASS row: a below-floor price at
// the counter is the ruled price, not an exception to record. For the same reason the floor and
// margin figures on the breakdown stay unset here, the glance that shows margin at the till
// reads the branch's costs itself (pos/rev8.go) and owns its own visibility rule.

// ResolveCounterPrice runs the waterfall for one till line and returns the breakdown with the
// margin gate step skipped: never blocked, never a BYPASS row. A line that cannot be priced at
// all (no anchor, no uom conversion) is still an error, an unpriceable line is not a zero price.
func (s *Service) ResolveCounterPrice(ctx context.Context, in ResolveInput) (Breakdown, error) {
	return s.resolve(ctx, in, false, true)
}

// DeriveCounterLinePrice is the counter resolve projected onto the line trio a till line
// persists, the counter twin of DeriveLinePrice.
func (s *Service) DeriveCounterLinePrice(ctx context.Context, in ResolveInput) (LinePrice, error) {
	bd, err := s.ResolveCounterPrice(ctx, in)
	if err != nil {
		return LinePrice{}, err
	}
	return LinePrice{
		UnitPriceCents: bd.FinalCents,
		PriceUOM:       bd.PriceUOM,
		LineTotalCents: bd.LineTotalCents,
		QtyBase:        bd.QtyBase,
		Breakdown:      bd,
	}, nil
}
