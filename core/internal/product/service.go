package product

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ErrNoConversion is returned by ConvertUOM when no factor exists for a product's
// UOM. Surfaced to the pricing module (A-07 / US-P06) so it can refuse to guess a
// price rather than silently convert with a wrong factor.
var ErrNoConversion = errors.New("product: no uom conversion factor")

// badRequestError marks a client input error (→ HTTP 400). Anything that is NOT a
// badRequestError or ErrNotFound is an internal error (→ 500, logged).
type badRequestError struct{ msg string }

func (e badRequestError) Error() string { return e.msg }

func badReq(format string, a ...any) error { return badRequestError{msg: fmt.Sprintf(format, a...)} }

// defaultSearchLimit / maxSearchLimit clamp the catalog search page so the line-add picker
// can never request an unbounded fuzzy scan.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// productStore is the persistence the Service needs. Real impl: Repository (pgx); tests use a fake.
type productStore interface {
	// List/Facets take the resolved in-stock id set (nil = no in-stock restriction) so the
	// product module never SELECTs the stock ledger itself (M4 zero-trust boundary).
	List(ctx context.Context, f Filter, inStockIDs []string) ([]Product, error)
	Facets(ctx context.Context, f Filter, inStockIDs []string) ([]Facet, error)
	Get(ctx context.Context, id string) (Product, error)
	Update(ctx context.Context, id string, attrs map[string]any) (Product, error)
	// Search is the pg_trgm fuzzy/by-SKU catalog seam for the procurement picker (P4.1).
	Search(ctx context.Context, query string, limit int) ([]CatalogResult, error)
	// Catalog read seam for pricing (A-07): the pricing repo never SELECTs
	// product_categories / product_uoms / products directly, it asks through these.
	GetCategory(ctx context.Context, productID string) (string, error)
	ConvertUOM(ctx context.Context, productID, fromUOM string) (float64, error)
	GetBaseUOM(ctx context.Context, productID string) (string, error)
}

// StockReader is the cross-module read seam for the M4 in_stock_only filter: it returns the
// product ids that currently have positive on-hand in the stock ledger, optionally
// branch-scoped. The product module is specced NEVER to touch a stock table directly, so the
// in-stock check goes through this interface (the default impl is the inventory/product
// repository; tests inject a fake). A nil StockReader means the module is running without a
// stock source, an in_stock_only request then returns no products (fail-closed, not silently
// unfiltered).
type StockReader interface {
	InStockProductIDs(ctx context.Context, branchID string) ([]string, error)
}

// BranchStockReader is the DIB-404 bulk on-hand seam: product ids → (branch id → on-hand base).
//
// Deliberately a BULK read, not the per-product pos/stock-glance the POS tiles use. The catalogue
// lists the whole filtered set, and one round trip per row is 30+ queries for a page nobody has
// clicked into yet, the shape that makes a list feel broken as the catalogue grows.
//
// Primitive-only signature so *inventory.Service satisfies it directly, with no adapter type and no
// new import edge in either direction.
type BranchStockReader interface {
	OnHandByBranch(ctx context.Context, productIDs []string) (map[string]map[string]float64, error)
}

// ListPriceReader is the DIB-404 retail-anchor seam: product ids → list price in cents, with ids
// that carry no anchor simply ABSENT from the map (not zero, see Product.ListPriceCents).
//
// pricing owns pricing_list_prices; this module never reads it. Primitive-only for the same reason
// as BranchStockReader: *pricing.Service satisfies it as-is.
type ListPriceReader interface {
	ListPriceCentsFor(ctx context.Context, productIDs []string) (map[string]int64, error)
}

// ListPriceUOMReader is the DIB-437 widening of ListPriceReader: the UNIT each anchor is
// denominated in.
//
// An OPTIONAL capability, asserted onto whatever ListPriceReader is wired rather than added to it,
// so every existing implementation and test fake keeps satisfying the seam untouched. A reader that
// does not implement it simply yields rows with no list_price_uom, and the catalogue prints the
// price without a unit rather than inventing one.
type ListPriceUOMReader interface {
	ListPriceUOMsFor(ctx context.Context, productIDs []string) (map[string]string, error)
}

// Service holds product business logic: faceted list, get, and validated attribute edit.
type Service struct {
	store      productStore
	stock      StockReader
	branchQty  BranchStockReader
	listPrices ListPriceReader
	images     imageStore // GABLE:product-images-ui, nil until WithImages is called
}

func NewService(store productStore) *Service { return &Service{store: store} }

// WithStock injects the cross-module in-stock reader (M4). Returns the Service for chaining
// (mirrors the pricing/portal builder convention).
func (s *Service) WithStock(sr StockReader) *Service { s.stock = sr; return s }

// WithBranchStock injects the DIB-404 bulk on-hand seam. Additive: unwired, the catalogue simply
// omits on_hand_by_branch, exactly as it did before the field existed.
func (s *Service) WithBranchStock(r BranchStockReader) *Service { s.branchQty = r; return s }

// WithListPrices injects the DIB-404 retail-anchor seam. Additive in the same way.
func (s *Service) WithListPrices(r ListPriceReader) *Service { s.listPrices = r; return s }

// resolveInStock returns the in-stock id set for the filter, or nil when the in_stock_only
// filter is off (no restriction). When it is on but no StockReader is wired, it returns an
// empty (non-nil) slice so the query fails closed (no products) rather than silently ignoring
// the filter.
func (s *Service) resolveInStock(ctx context.Context, f Filter) ([]string, error) {
	if !f.InStockOnly {
		return nil, nil
	}
	if s.stock == nil {
		return []string{}, nil
	}
	ids, err := s.stock.InStockProductIDs(ctx, f.BranchID)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{} // non-nil empty = "in-stock on, nothing in stock" (fail closed)
	}
	return ids, nil
}

// maxListLimit caps a requested catalogue page (qol2 park P1). A caller asking for more gets this
// many rather than a 400: an oversized limit is a differently-sized page of the RIGHT rows, which is
// the same reasoning inventory's browse limit uses. There is deliberately no DEFAULT limit beside it
// , see Filter.Limit for why zero must keep meaning "all".
const maxListLimit = 200

// clampPage applies the page rules: a limit above the cap is capped, a negative limit or offset is
// floored at zero (a negative OFFSET is a Postgres error, and a negative limit read as "unlimited"
// would be a caller asking to be bounded and silently not being).
func clampPage(f Filter) Filter {
	if f.Limit < 0 {
		f.Limit = 0
	}
	if f.Limit > maxListLimit {
		f.Limit = maxListLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return f
}

// List runs the faceted catalogue query. When in_stock_only is set it first resolves the
// branch-scoped in-stock id set through the StockReader seam, then passes it to the store.
//
// Paging (qol2 P1) is opt-in: f.Limit == 0 returns the whole filtered catalogue, exactly as this
// method has always done, because the .xlsx export and the POS product grid both depend on it.
func (s *Service) List(ctx context.Context, f Filter) ([]Product, error) {
	f = clampPage(f)
	inStock, err := s.resolveInStock(ctx, f)
	if err != nil {
		return nil, err
	}
	list, err := s.store.List(ctx, f, inStock)
	if err != nil {
		return nil, err
	}
	return s.enrichRows(ctx, list), nil
}

// productCounter is the pre-limit total behind a paged List (qol2 P1).
//
// An OPTIONAL capability, asserted onto whatever store is wired rather than added to productStore,
// for the same reason ListPriceUOMReader is (see above): productStore is an unexported interface that
// other packages' test fakes satisfy structurally, internal/portal has one, and widening it would
// break them from outside their own fence. The real *Repository implements it, asserted at compile
// time below, so the production path is checked rather than hoped for.
type productCounter interface {
	Count(ctx context.Context, f Filter, inStockIDs []string) (int, error)
}

// compile-time assertion: the real store can count. A fake that cannot simply yields no total, and
// the handler omits the header.
var _ productCounter = (*Repository)(nil)

// errNoCount means the wired store cannot produce a total. Not an error the CALLER did anything to
// deserve: the page it asked for is fine, only the denominator is missing.
var errNoCount = errors.New("product: store does not support counting")

// Count returns how many products match the filter ignoring Limit/Offset, the total behind a paged
// List. Kept separate from List rather than folded into a result envelope so that List's signature,
// and therefore the unpaged read every existing caller makes, does not change and does not pay for a
// count nobody asked for.
func (s *Service) Count(ctx context.Context, f Filter) (int, error) {
	counter, ok := s.store.(productCounter)
	if !ok {
		return 0, errNoCount
	}
	inStock, err := s.resolveInStock(ctx, f)
	if err != nil {
		return 0, err
	}
	return counter.Count(ctx, f, inStock)
}

// enrichRows attaches the DIB-404 row detail (per-branch on-hand, list price) to a catalogue page.
//
// FAIL-SOFT BY DESIGN, and this is the whole judgement in the function: a catalogue is a browsing
// surface, and neither number is authoritative here, on-hand belongs to inventory, the price a
// customer actually pays comes from pricing.DeriveLinePrice against their contract. If a seam is
// unwired or errors, the rows go out WITHOUT the extra detail rather than failing the list. The
// alternative, a 500 on the catalogue because the stock ledger hiccuped, trades a page that is
// merely less detailed for one that does not exist.
//
// What it must NOT do is guess: an id absent from either map carries no field at all, so the
// frontend renders "-" rather than "0 on hand" or "$0.00".
func (s *Service) enrichRows(ctx context.Context, list []Product) []Product {
	if len(list) == 0 {
		return list
	}
	ids := make([]string, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.ID)
	}
	var onHand map[string]map[string]float64
	if s.branchQty != nil {
		if m, err := s.branchQty.OnHandByBranch(ctx, ids); err == nil {
			onHand = m
		}
	}
	var prices map[string]int64
	var priceUOMs map[string]string
	if s.listPrices != nil {
		if m, err := s.listPrices.ListPriceCentsFor(ctx, ids); err == nil {
			prices = m
			// DIB-437: the unit is only asked for once there is a price to denominate, and only from
			// a reader that actually carries it. Same fail-soft rule as the price itself, an error
			// here costs the row its unit, never the page.
			if uomReader, ok := s.listPrices.(ListPriceUOMReader); ok && len(prices) > 0 {
				if u, err := uomReader.ListPriceUOMsFor(ctx, ids); err == nil {
					priceUOMs = u
				}
			}
		}
	}
	if onHand == nil && prices == nil {
		return list
	}
	for i := range list {
		if q, ok := onHand[list[i].ID]; ok && len(q) > 0 {
			list[i].OnHandByBranch = q
		}
		if c, ok := prices[list[i].ID]; ok {
			cents := c
			list[i].ListPriceCents = &cents
			// The unit rides ONLY with a price. A unit on a row with no anchor would render as a
			// bare "/ PC" beside an em dash, a unit for a price nobody has set.
			if u, ok := priceUOMs[list[i].ID]; ok {
				list[i].ListPriceUOM = u
			}
		}
	}
	return list
}

// Facets returns per-facet distinct values + counts honoring the current filter (M4 /facets).
// It resolves the same in-stock id set as List so counts stay consistent with the results.
func (s *Service) Facets(ctx context.Context, f Filter) ([]Facet, error) {
	inStock, err := s.resolveInStock(ctx, f)
	if err != nil {
		return nil, err
	}
	return s.store.Facets(ctx, f, inStock)
}

func (s *Service) Get(ctx context.Context, id string) (Product, error) {
	if id == "" {
		return Product{}, badReq("product id is required")
	}
	return s.store.Get(ctx, id)
}

// Update validates every key against the editable-column allowlist and normalizes
// each value to the column's type before persisting. Unknown columns are rejected
// (no silent drops); type mismatches and out-of-range values are rejected too.
func (s *Service) Update(ctx context.Context, id string, raw map[string]any) (Product, error) {
	if id == "" {
		return Product{}, badReq("product id is required")
	}
	if len(raw) == 0 {
		return Product{}, badReq("no attributes to update")
	}
	attrs := make(map[string]any, len(raw))
	for k, v := range raw {
		kind, ok := editableColumns[k]
		if !ok {
			return Product{}, badReq("unknown column %q", k)
		}
		nv, err := normalize(k, kind, v)
		if err != nil {
			return Product{}, err
		}
		attrs[k] = nv
	}
	return s.store.Update(ctx, id, attrs)
}

// SearchProducts runs a fuzzy/by-SKU catalog search for the procurement line-add picker
// (P4.1). The query is trimmed and required; limit is clamped to [1, maxSearchLimit] with a
// default when unset. Returns compact CatalogResult hits, never the full Product payload.
func (s *Service) SearchProducts(ctx context.Context, query string, limit int) ([]CatalogResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		// httpx.BadRequestError (not the module-local badReq) so the portal BFF, which is
		// the only caller of this seam, maps an empty query to 400 (it catches httpx's type).
		return nil, httpx.BadRequestf("search query is required")
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	return s.store.Search(ctx, query, limit)
}

// - Catalog read seam for the pricing module (A-07 / W3) -
// These satisfy pricing.CategoryResolver / pricing.UOMConverter. The narrow
// interface keeps zero-trust intact: pricing reads category + UOM factors through
// the catalog (this module) instead of SELECTing product_categories / product_uoms.

// GetCategory returns the product's category_id (or "" when the product is
// uncategorised). ErrNotFound when the product does not exist.
func (s *Service) GetCategory(ctx context.Context, productID string) (string, error) {
	if productID == "" {
		return "", badReq("product id is required")
	}
	return s.store.GetCategory(ctx, productID)
}

// ConvertUOM returns how many base units one `fromUOM` represents for the product
// (product_uoms.qty_in_base; the base UOM itself is 1). ErrNoConversion when no
// factor is recorded, the pricing resolver turns this into a typed error rather
// than guessing (US-P06: never a silent wrong price).
func (s *Service) ConvertUOM(ctx context.Context, productID, fromUOM string) (float64, error) {
	if productID == "" {
		return 0, badReq("product id is required")
	}
	if fromUOM == "" {
		return 0, badReq("uom is required")
	}
	return s.store.ConvertUOM(ctx, productID, fromUOM)
}

// GetBaseUOM returns the product's base unit-of-measure (products.base_uom). Catalog
// read seam for the pricing module (A-07): the pricing resolver denominates a DERIVED
// (cost-based) retail anchor in this base UOM, so Breakdown.PriceUOM carries the product's
// real base unit instead of "". ErrNotFound when the product does not exist (the resolver
// maps it to its own 404 path, same as GetCategory).
func (s *Service) GetBaseUOM(ctx context.Context, productID string) (string, error) {
	if productID == "" {
		return "", badReq("product id is required")
	}
	return s.store.GetBaseUOM(ctx, productID)
}

// normalize coerces a JSON-decoded value to the column's expected Go type, or errors.
func normalize(col, kind string, v any) (any, error) {
	switch kind {
	case "text":
		s, ok := v.(string)
		if !ok {
			return nil, badReq("column %q expects a string", col)
		}
		return s, nil
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, badReq("column %q expects a boolean", col)
		}
		return b, nil
	case "int":
		if v == nil {
			return nil, nil // JSON null clears a nullable int
		}
		f, ok := v.(float64) // JSON numbers decode to float64
		if !ok || f != math.Trunc(f) {
			return nil, badReq("column %q expects an integer", col)
		}
		// products INTEGER columns are 32-bit; reject before a silent overflow/DB error.
		if f < math.MinInt32 || f > math.MaxInt32 {
			return nil, badReq("column %q out of range", col)
		}
		return int(f), nil
	case "decimal":
		if v == nil {
			return nil, nil
		}
		switch n := v.(type) {
		case float64:
			return n, nil
		case string: // a decimal string preserves precision (DECIMAL(19,4))
			return n, nil
		default:
			return nil, badReq("column %q expects a number", col)
		}
	case "json":
		m, ok := v.(map[string]any)
		if !ok {
			return nil, badReq("column %q expects an object", col)
		}
		return m, nil
	default:
		return nil, badReq("column %q has no normalizer", col)
	}
}
