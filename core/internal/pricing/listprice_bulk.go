package pricing

import (
	"context"
	"errors"
)

// listprice_bulk.go, DIB-404: the bulk retail-anchor read the product catalogue list needs.
//
// The catalogue shows a list price per row so a counter user can judge a product before clicking
// into it. pricing owns pricing_list_prices; product never reads it. This file is that seam's
// pricing-side half, *Service satisfies product.ListPriceReader directly, with a primitive-only
// signature, so no adapter type and no import edge between the two modules is needed.
//
// NOT a pricing decision. A list price is the retail ANCHOR, not what a customer pays: that comes
// from DeriveLinePrice, which layers customer overrides, category policy, quantity breaks and the
// margin gate on top. The catalogue column is labelled "List price" for exactly that reason, and
// nothing here may be used to quote from.

// errBulkListPriceUnavailable is returned when the wired store predates this read (a fake in a unit
// test, say). The caller, product.Service.enrichRows, treats an error as "no detail available"
// and ships the rows without the column, which is the honest degradation.
var errBulkListPriceUnavailable = errors.New("pricing: store does not support bulk list-price reads")

// listPriceBulkStore is asserted onto the existing store rather than added to pricingStore, so the
// several fake stores in this package's unit tests keep compiling unchanged. The real Repository
// implements it.
type listPriceBulkStore interface {
	ListPricesFor(ctx context.Context, productIDs []string) (map[string]int64, error)
}

// ListPriceCentsFor returns product id → list price in cents for the ids that HAVE a retail anchor.
//
// Ids with no anchor are absent from the map, never present as 0. "No list price on file" and "the
// list price is zero" are different facts about a product, and a catalogue that collapses them shows
// a $0.00 someone may try to sell at. Callers must treat absence as "unknown".
func (s *Service) ListPriceCentsFor(ctx context.Context, productIDs []string) (map[string]int64, error) {
	if len(productIDs) == 0 {
		return map[string]int64{}, nil
	}
	bulk, ok := s.store.(listPriceBulkStore)
	if !ok {
		return nil, errBulkListPriceUnavailable
	}
	return bulk.ListPricesFor(ctx, productIDs)
}

// - DIB-437: the anchor's UNIT, alongside the anchor -

// listPriceUOMBulkStore is asserted separately from listPriceBulkStore for the same reason that one
// is asserted rather than added to pricingStore: the several fake stores in this package's unit
// tests keep compiling unchanged, and a store that predates this read degrades to "no unit known"
// instead of failing the catalogue.
type listPriceUOMBulkStore interface {
	ListPriceUOMsFor(ctx context.Context, productIDs []string) (map[string]string, error)
}

// ListPriceUOMsFor returns product id → the UOM the anchor is DENOMINATED IN (pricing_list_prices
// .price_uom, BisTrack PricePerID).
//
// This exists because "$22.00" is not a price, "$22.00 / PC" is. The catalogue prints the anchor
// next to an on-hand count, and a reader who assumes the wrong unit misquotes by whatever the
// conversion factor happens to be.
//
// It must be READ FROM THE ANCHOR ROW and may not be inferred from the product. products.price_uom
// is a different column with a different job and the two genuinely disagree, in the seeded demo
// alone they differ for 20 of 30 anchored products (e.g. Banas coping: products.price_uom = LF,
// anchor = PC). Whoever set the anchor chose its unit; that choice is the only one that describes
// the number being shown.
//
// Ids with no anchor row are ABSENT from the map, mirroring ListPriceCentsFor: no anchor, no unit.
func (s *Service) ListPriceUOMsFor(ctx context.Context, productIDs []string) (map[string]string, error) {
	if len(productIDs) == 0 {
		return map[string]string{}, nil
	}
	bulk, ok := s.store.(listPriceUOMBulkStore)
	if !ok {
		return nil, errBulkListPriceUnavailable
	}
	return bulk.ListPriceUOMsFor(ctx, productIDs)
}

// ListPriceUOMsFor is the pgx read, one query over the whole id set, same shape as ListPricesFor.
//
// Kept as its own query rather than widened into ListPricesFor's result type: that method's
// signature is the published product.ListPriceReader seam, and changing it to carry a struct would
// churn every caller and fake for a field only the catalogue list needs.
func (r *Repository) ListPriceUOMsFor(ctx context.Context, productIDs []string) (map[string]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(productIDs) == 0 {
		return map[string]string{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT product_id::text, price_uom::text FROM pricing_list_prices WHERE product_id = ANY($1::uuid[])`,
		productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(productIDs))
	for rows.Next() {
		var id, uom string
		if err := rows.Scan(&id, &uom); err != nil {
			return nil, err
		}
		out[id] = uom
	}
	return out, rows.Err()
}

// ListPricesFor is the pgx read: one query over the whole requested id set.
//
// Deliberately NOT a loop over ListPrice(). The catalogue lists the full filtered set, so a per-row
// round trip is one query per product on a page nobody has clicked into yet.
func (r *Repository) ListPricesFor(ctx context.Context, productIDs []string) (map[string]int64, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(productIDs) == 0 {
		return map[string]int64{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT product_id::text, list_price_cents FROM pricing_list_prices WHERE product_id = ANY($1::uuid[])`,
		productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64, len(productIDs))
	for rows.Next() {
		var id string
		var cents int64
		if err := rows.Scan(&id, &cents); err != nil {
			return nil, err
		}
		out[id] = cents
	}
	return out, rows.Err()
}
