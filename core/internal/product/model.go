// Package product owns the products catalogue: faceted read + attribute edit.
// A-18 (W2) promotes the in-use BisTrack Product UDFs to first-class columns
// (see migrations/0018 + seed/udf_promote_list.csv). Module boundary: this
// module is the sole owner of the products table.
package product

// Product is the catalogue view of a products row (promoted attrs included).
type Product struct {
	ID                             string         `json:"id"`
	SKU                            string         `json:"sku"`
	Name                           string         `json:"name"`
	Description                    string         `json:"description"`
	Manufacturer                   string         `json:"manufacturer"`
	Collection                     string         `json:"collection"`
	Color                          string         `json:"color"`
	Finish                         string         `json:"finish"`
	Application                    string         `json:"application"`
	ColorFamily                    string         `json:"color_family"`
	Texture                        string         `json:"texture"`
	ProductSize                    string         `json:"product_size"`
	SoldBy                         string         `json:"sold_by"`
	BaseUOM                        string         `json:"base_uom"` // stocking unit, the POS fallback sell UoM when sold_by is unset
	DimensionsLWH                  string         `json:"dimensions_lwh"`
	IsFeatured                     bool           `json:"is_featured"`
	IsSpecialOrder                 bool           `json:"is_special_order"`
	ContractorPricingEventDiscount bool           `json:"contractor_pricing_event_discount"`
	FullSkidQuantity               *int           `json:"full_skid_quantity"`
	LayerQuantity                  *int           `json:"layer_quantity"`
	PalletCount                    *int           `json:"pallet_count"`
	MinimumOrderQty                *string        `json:"minimum_order_qty"` // DECIMAL as string
	IsActive                       bool           `json:"is_active"`
	Metadata                       map[string]any `json:"metadata"`

	// - DIB-404: per-row detail for the catalogue list -
	//
	// Both fields are ENRICHMENT, not columns of `products`: this module is the sole owner of the
	// products table and is specced never to SELECT stock_balances or pricing_list_prices, so they
	// are read through the BranchStockReader / ListPriceReader seams (the same zero-trust boundary
	// the in_stock_only filter already crosses) and attached to the row afterwards.
	//
	// Both are omitempty and nil-safe: a Service with no reader wired, or a product with no anchor
	// and no ledger row, simply does not carry them. The catalogue must render a row it cannot
	// price, not hide it.

	// OnHandByBranch is branch id → on-hand in base UOM. Populated on the LIST read only (the row
	// detail the catalogue shows before click-through); Get/Update are untouched.
	OnHandByBranch map[string]float64 `json:"on_hand_by_branch,omitempty"`
	// ListPriceCents is the retail anchor for this product, nil when none is set. A pointer, not a
	// zero int: "no list price on file" and "list price is $0.00" are different facts, and a
	// catalogue that shows $0.00 for the former invites someone to quote it.
	ListPriceCents *int64 `json:"list_price_cents,omitempty"`
	// ListPriceUOM is the unit ListPriceCents is denominated in, read from the anchor row itself
	// (DIB-437). Carried ONLY when ListPriceCents is, a unit with no price is noise, and a price
	// with no unit is a number a reader silently supplies the wrong unit for.
	//
	// NOT products.price_uom. That is a different column with a different job, and the two disagree
	// in real data; the anchor's own unit is the only one that describes the anchor's number.
	ListPriceUOM string `json:"list_price_uom,omitempty"`
}

// CatalogResult is the compact catalog hit the procurement line-add picker consumes
// (GET /catalog/search). It is a search projection of a products row -- not the full
// Product payload -- so the picker stays cheap (pg_trgm fuzzy on name + by-SKU prefix,
// migration 0028 / P4.1). category is the product's category code.
type CatalogResult struct {
	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	BaseUOM   string `json:"base_uom"`
}

// Filter is the faceted catalogue query. Empty fields are ignored (AND of the rest).
// M4 (scope reconciliation 2026-07-14, A-4) adds the collection/category_id/q/in_stock_only
// delta on top of A-18's 5 same-module facets.
type Filter struct {
	Finish       string
	ColorFamily  string
	Application  string
	Color        string
	Manufacturer string
	Collection   string // M4: same-module column facet
	CategoryID   string // M4: products.category_id (A-07 category registry)
	Q            string // M4: ILIKE text search over name+sku+description
	ActiveOnly   bool
	// InStockOnly narrows to products with on_hand_base > 0 in the stock ledger
	// (cross-module read, branch-scoped). It is resolved through a StockReader seam so
	// this module never SELECTs stock_balances directly (M4 zero-trust boundary).
	InStockOnly bool
	BranchID    string // scopes the in-stock read; "" = either branch (any on-hand)

	// Limit/Offset page the list (qol2 park P1). ZERO LIMIT MEANS NO LIMIT, and that is a
	// compatibility promise rather than an oversight: every caller that exists today -
	// pos-api.listProducts, the catalogue screen (which sorts and CSVs client-side over the rows it
	// holds) and the .xlsx catalogue export, asks for the whole catalogue and would lose rows
	// SILENTLY under a default page size, on a data set no test currently large enough to notice.
	// A caller that wants a page asks for one.
	//
	// Offset is honoured independently of Limit: "skip the first N" is a legitimate request and
	// OFFSET without LIMIT is valid SQL. Both are clamped in Service.List, never trusted raw.
	Limit  int
	Offset int
}

// Facet is one facet's distinct values with the count of products that carry each value,
// reflecting the currently-applied filter (M4 /facets endpoint, replaces the frontend
// "harvest distinct on first load" hack).
type Facet struct {
	Field  string       `json:"field"`
	Values []FacetValue `json:"values"`
}

// FacetValue is a single facet option and how many matching products carry it.
type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// facetFields is the allowlist of facet columns the /facets endpoint counts. Keys are the
// public facet name; values are the real products column. Kept as an allowlist so the
// column can never be caller-controlled (it is interpolated into GROUP BY / SELECT).
var facetFields = map[string]string{
	"manufacturer": "manufacturer",
	"collection":   "collection",
	"color":        "color",
	"finish":       "finish",
	"application":  "application",
	"color_family": "color_family",
	// category is projected as the category code (LEFT JOIN product_categories); handled specially.
	"category": "category",
}

// facetOrder fixes the response order so the frontend renders facets consistently.
var facetOrder = []string{"manufacturer", "collection", "color", "finish", "application", "color_family", "category"}

// editableColumns is the allowlist for PUT /products/{id}. Keys are real column
// names; values are the expected JSON kind for normalization. Anything not here
// is rejected (US-A18-4 / TestProduct_Update_RejectsUnknownColumn).
var editableColumns = map[string]string{
	"name":                              "text",
	"description":                       "text",
	"manufacturer":                      "text",
	"collection":                        "text",
	"color":                             "text",
	"finish":                            "text",
	"application":                       "text",
	"color_family":                      "text",
	"texture":                           "text",
	"product_size":                      "text",
	"sold_by":                           "text",
	"dimensions_lwh":                    "text",
	"is_featured":                       "bool",
	"is_special_order":                  "bool",
	"contractor_pricing_event_discount": "bool",
	"full_skid_quantity":                "int",
	"layer_quantity":                    "int",
	"pallet_count":                      "int",
	"minimum_order_qty":                 "decimal",
	"metadata":                          "json",
}
