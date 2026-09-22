package product

// detail.go, DIB-479: the single-product detail read (one product, its per-branch position, and
// its per-branch lead time).
//
// WHY IT IS NOT `List` WITH A FILTER, AND NOT `Get`. List enriches rows in BULK because it renders
// the whole filtered catalogue; Get is the bare row and is deliberately unenriched (a page nobody
// clicked into should not pay for a stock read). A product DETAIL page is the third case: exactly
// one row, and the two facts the row alone cannot answer, "have you got it?" and "when can I have
// it?", are the entire reason somebody opened it.
//
// NO PRICE IS ATTACHED HERE, and that is a decision rather than an omission. The portal's product
// detail is specced price-free (HH Pro connection spec §2.2): a contractor's price comes from the
// pricing round-trip, which is a different stage with its own margin-stripping projection. A detail
// read that helpfully carried a list price would put a money field one struct-copy away from a
// contractor-facing response, and the one thing that can never leak across that counter is what
// the dealer's numbers are made of.
//
// EVERY READ HERE GOES THROUGH THE SEAMS THIS MODULE ALREADY HAS. This module is the sole owner of
// `products` and is specced never to SELECT stock_balances or product_reorder_points; both facts
// arrive through the reader injected with WithBranchStock. Nothing new is wired: the lead-time
// capability is ASSERTED onto that same reader, the way ListPriceUOMReader is asserted onto the
// list-price reader (DIB-437), so a reader that predates it simply yields no lead times and the
// page renders without them.

import "context"

// LeadTimeReader is the optional lead-time capability of whatever BranchStockReader is wired: for a
// product, branch id → days. A branch with no lead time on file is ABSENT from the map, never zero -
// "no policy on file" and "available today" are different facts and only one of them is a promise.
type LeadTimeReader interface {
	LeadTimeDaysByBranch(ctx context.Context, productID string) (map[string]int, error)
}

// Detail is one product plus the per-branch facts a detail page asks for. Product is embedded, so
// this is the catalogue row every other consumer already knows, with lead times beside it.
type Detail struct {
	Product
	// LeadTimeDaysByBranch is branch id → days until replenishment at that branch. omitempty and
	// nil-safe: absent when no reader is wired and absent for a product nobody manages.
	LeadTimeDaysByBranch map[string]int `json:"lead_time_days_by_branch,omitempty"`
}

// GetDetail returns one product with its per-branch on-hand and per-branch lead time.
//
// FAIL-SOFT ON THE ENRICHMENTS, FAIL-HARD ON THE PRODUCT. A missing product is an error the caller
// must see (a detail page for a SKU that does not exist is a 404, not a blank page). A stock or
// lead-time reader that errors costs the response those fields and nothing else, the same rule
// enrichRows already follows, and for the same reason: the catalogue must render a row it cannot
// enrich rather than hide it.
func (s *Service) GetDetail(ctx context.Context, id string) (Detail, error) {
	if id == "" {
		return Detail{}, badReq("product id is required")
	}
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Product: p}
	if s.branchQty != nil {
		if m, err := s.branchQty.OnHandByBranch(ctx, []string{p.ID}); err == nil {
			if q, ok := m[p.ID]; ok && len(q) > 0 {
				d.OnHandByBranch = q
			}
		}
		if ltr, ok := s.branchQty.(LeadTimeReader); ok {
			if lt, err := ltr.LeadTimeDaysByBranch(ctx, p.ID); err == nil && len(lt) > 0 {
				d.LeadTimeDaysByBranch = lt
			}
		}
	}
	return d, nil
}
