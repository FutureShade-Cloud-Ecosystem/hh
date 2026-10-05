// branchprice.go: the branch price editor surface and the resolver's
// branch-aware anchor step (HH-LOAD-1 LD-7, plan CP0-5 answer A). A price per
// branch beside the dealer-wide one: at a branch the resolver starts from that
// branch's price where one is set and from the dealer-wide anchor where not,
// and every percent rule, quantity break and promotion works from that start
// as it does today. Money is integer cents, never a float; a non whole cent
// cannot be expressed in the type and a negative is refused.
package pricing

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// branchPriceSyncScope is the per-scope sequence counter (scope_sequences, the
// 0213 convention) every branch price write draws from, the price_matrix
// allocator's sibling. The price_matrix scope keeps serving the dealer-wide
// anchor only (reading 10.8): a branch price reaches a device through the
// server's resolve, never through that scope's pull.
const branchPriceSyncScope = "branch_prices"

// BranchPrice is one pricing_branch_prices row: the list price for one
// product at one branch, beside the dealer-wide anchor.
type BranchPrice struct {
	ProductID      string    `json:"product_id"`
	BranchID       string    `json:"branch_id"`
	ListPriceCents int64     `json:"list_price_cents"`
	PriceUOM       string    `json:"price_uom"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// BranchPriceFilter narrows the branch price list. AfterKey is the resume
// key the handler mints (the row's product/branch pair), "" on the first
// page; Limit arrives already clamped by the handler's page parse.
type BranchPriceFilter struct {
	ProductID string
	BranchID  string
	AfterKey  string
	Limit     int
}

// BranchPriceWrite is the editor's set body: the money and the unit it is per.
// The unit is the caller's to name (a branch price may be per a larger unit),
// checked against the uoms catalogue by the table's own FK.
type BranchPriceWrite struct {
	ListPriceCents int64
	PriceUOM       string
}

// resolveAnchor reads the line's retail anchor: the branch price at the
// line's branch when one is loaded, else the dealer-wide anchor. The bool
// answers "the branch row won" so a LIST breakdown can name it. With no
// branch named on the line, or no branch row loaded, the dealer-wide read
// answers alone and this step is exactly the pre-LD-7 behaviour.
func (s *Service) resolveAnchor(ctx context.Context, in ResolveInput) (*ListPrice, bool, error) {
	if in.BranchID != "" {
		bp, err := s.store.BranchListPrice(ctx, in.ProductID, in.BranchID)
		if err != nil {
			return nil, false, fmt.Errorf("branch list price: %w", err)
		}
		if bp != nil {
			return bp, true, nil
		}
	}
	lp, err := s.store.ListPrice(ctx, in.ProductID)
	if err != nil {
		return nil, false, err
	}
	return lp, false, nil
}

// ListBranchPrices is the editors' read of the branch price table, live rows
// only (a tombstone is a deleted price whose trail the change log keeps).
// The limit arrives clamped from the handler's page parse; a defensive upper
// bound stays so no caller of the service can pull an unbounded page.
func (s *Service) ListBranchPrices(ctx context.Context, f BranchPriceFilter) ([]BranchPrice, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	return s.store.ListBranchPrices(ctx, f)
}

// PutBranchPrice sets one product's price at one branch. A create and an
// update are the same act (the set), the write is idempotent, and an
// identical re-set moves nothing: no new trail row, no fresh seq, the same
// discipline the sync write's value-equal arm keeps. Every change files its
// row in the price change log with the branch named, before and after.
func (s *Service) PutBranchPrice(ctx context.Context, productID, branchID string, in BranchPriceWrite, userID string) (BranchPrice, error) {
	if productID == "" {
		return BranchPrice{}, badReq("product_id is required")
	}
	if branchID == "" {
		return BranchPrice{}, badReq("branch_id is required")
	}
	if in.PriceUOM == "" {
		return BranchPrice{}, badReq("price_uom is required")
	}
	if in.ListPriceCents < 0 {
		return BranchPrice{}, unprocessable("list_price_cents must not be negative")
	}
	// The unit must be one the product's own set can convert: the resolver
	// prices every line of this product at this branch through the same
	// conversion, so a price per a unit outside the set would strand the
	// ring, the quote lines and the order lines until it was taken back down.
	// A conversion that fails because the PRODUCT is unknown is left to the
	// store's foreign-key refusal, which names the reference.
	if _, err := s.uomFactorFor(ctx, productID, in.PriceUOM); err != nil {
		if errors.Is(err, errNoConversion) && s.productKnown(ctx, productID) {
			return BranchPrice{}, unprocessable("price_uom does not convert for this product")
		}
		if !errors.Is(err, errNoConversion) {
			return BranchPrice{}, err
		}
	}
	out, err := s.store.PutBranchPrice(ctx, productID, branchID, in, userID)
	if err != nil {
		return BranchPrice{}, err // repo maps the FK violation to errFKViolation (422)
	}
	return out, nil
}

// productKnown answers whether the catalogue can resolve the product at all:
// the convertibility guard only speaks for a product that exists, an unknown
// one belongs to the foreign-key refusal.
func (s *Service) productKnown(ctx context.Context, productID string) bool {
	if s.cat == nil {
		return false
	}
	_, err := s.cat.GetBaseUOM(ctx, productID)
	return err == nil
}

// DeleteBranchPrice removes one product's branch price: a tombstone with a
// fresh seq (the module's delete shape), the trail row carrying the price it
// took away. An absent row is the 404.
func (s *Service) DeleteBranchPrice(ctx context.Context, productID, branchID, userID string) error {
	if productID == "" {
		return badReq("product_id is required")
	}
	if branchID == "" {
		return badReq("branch_id is required")
	}
	return s.store.DeleteBranchPrice(ctx, productID, branchID, userID)
}
