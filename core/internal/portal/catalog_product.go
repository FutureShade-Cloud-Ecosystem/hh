package portal

// catalog_product.go, GET /api/v1/tenant/catalog/products/{id} (DIB-479, spec §2.2 gap 3).
//
// THIS FILE IS A COUNTER. Everything the ERP knows about a product is on one side of it; what a
// contractor may see is on the other, and the only way across is a field explicitly written out
// below. That is why CatalogProduct is a hand-copied struct and not `product.Product` with a few
// json:"-" tags: a deny-list is a promise that every future column added upstream will be
// remembered by somebody, and it will not be. An allow-list forgets in the safe direction, a new
// products column simply does not appear here until a person decides it should.
//
// WHAT IS DELIBERATELY NOT HERE, and why each one:
//
//	metadata           , a free JSONB bag. Today it is import residue; it is also exactly where a
//	                      BisTrack sync would drop a cost, a vendor code or a margin band. A
//	                      contractor-facing response must never carry a field whose CONTENTS are
//	                      not enumerable at review time.
//	list_price_cents /
//	price_uom          , no price crosses on this route AT ALL (spec §2.2: "price-free product").
//	                      A contractor's price is a pricing round-trip (Stage 2) with its own
//	                      margin-stripped projection. Shipping a list price here would put a money
//	                      field in the same struct as the availability read and invite the next
//	                      person to add the "real" one beside it.
//	avg cost / margin /
//	supplier / vendor  , not in this struct, not in the read behind it, not in the SQL under that.
//	                      Dealer margin is the one thing that can never leak (spec §2.4), and the
//	                      cheapest way to guarantee that is to never load it.
//	reserved / soft-held
//	stock detail       , a contractor gets "is it there and how much", not the yard's reservation
//	                      ledger. Who else is holding stock is somebody else's business.
//
// The leak test (TestCatalogProduct_ProjectionCarriesNoMoneyOrMarginFields) marshals this type with
// every field populated and asserts the forbidden names are absent from the JSON, so the promise
// above is enforced by the suite rather than by this comment.

import (
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// BranchAvailability is one yard's position on one product, contractor-safe.
type BranchAvailability struct {
	BranchID string `json:"branch_id"`
	// OnHandBase is the physical balance in the product's BASE unit. It is the same number the
	// counter would read off a screen for somebody standing in front of them.
	OnHandBase float64 `json:"on_hand_base"`
	InStock    bool    `json:"in_stock"`
	// LeadTimeDays is this branch's replenishment lead time, nil when no policy is on file. NEVER
	// zero-as-unknown: zero means "today" and is a promise.
	LeadTimeDays *int `json:"lead_time_days,omitempty"`
}

// CatalogProduct is the portal-safe product detail. Every field here was chosen; see the file
// header for what was refused and why.
type CatalogProduct struct {
	ProductID    string `json:"product_id"`
	SKU          string `json:"sku"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Manufacturer string `json:"manufacturer"`
	Collection   string `json:"collection"`
	Color        string `json:"color"`
	ColorFamily  string `json:"color_family"`
	Finish       string `json:"finish"`
	Texture      string `json:"texture"`
	Application  string `json:"application"`
	ProductSize  string `json:"product_size"`
	// SoldBy/BaseUOM are units, not money: a contractor doing takeoff math needs to know the thing
	// is sold by the square foot and stocked by the pallet.
	SoldBy        string `json:"sold_by"`
	BaseUOM       string `json:"base_uom"`
	DimensionsLWH string `json:"dimensions_lwh"`
	// Packaging quantities, how many come on a skid/layer/pallet. Pointers because "unknown" and
	// "zero per skid" are different facts, and the second one is never true.
	FullSkidQuantity *int   `json:"full_skid_quantity,omitempty"`
	LayerQuantity    *int   `json:"layer_quantity,omitempty"`
	PalletCount      *int   `json:"pallet_count,omitempty"`
	MinimumOrderQty  string `json:"minimum_order_qty,omitempty"`
	IsSpecialOrder   bool   `json:"is_special_order"`
	// IsActive false = discontinued. It is REPORTED rather than hidden: a discontinued product
	// still sits on a pallet in the yard and still appears on last year's order, and answering
	// "no such product" for something a contractor is looking at is the DIB-443 failure again.
	IsActive bool `json:"is_active"`
	// LeadTimeDays is the headline answer to "when can I have it?", resolved across branches:
	// 0 when it is on hand somewhere, otherwise the SHORTEST lead time on file, nil when nobody
	// has one on file. This is the number HH Pro's Product.leadTimeDays means.
	LeadTimeDays *int                 `json:"lead_time_days,omitempty"`
	Availability []BranchAvailability `json:"availability"`
}

// getCatalogProduct returns one product's contractor-safe detail.
//
// NO TENANT FENCE, AND THAT IS CORRECT. The catalogue is shared dealer data, not customer data -
// every contractor sees the same products, exactly as GET /catalog/search already does. The fence
// that matters here is the CAPABILITY gate on the route and the field allow-list in the projection;
// pretending a shared catalogue is tenant-scoped would be security theatre that hides neither of
// the two things this route could actually leak (margin, and another yard's internals).
//
// An unknown OR malformed id is a 404, never a 500 and never a 403.
func (h *Handler) getCatalogProduct(w http.ResponseWriter, r *http.Request) {
	if h.productSvc == nil {
		httpx.NotImplemented(w, "catalog product detail not implemented")
		return
	}
	d, err := h.productSvc.GetDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeCatalogErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, projectCatalogProduct(d))
}

// projectCatalogProduct is the counter itself: the ONLY function that turns an internal product
// row into a contractor-facing one. Field by field, on purpose.
func projectCatalogProduct(d product.Detail) CatalogProduct {
	out := CatalogProduct{
		ProductID:        d.ID,
		SKU:              d.SKU,
		Name:             d.Name,
		Description:      d.Description,
		Manufacturer:     d.Manufacturer,
		Collection:       d.Collection,
		Color:            d.Color,
		ColorFamily:      d.ColorFamily,
		Finish:           d.Finish,
		Texture:          d.Texture,
		Application:      d.Application,
		ProductSize:      d.ProductSize,
		SoldBy:           d.SoldBy,
		BaseUOM:          d.BaseUOM,
		DimensionsLWH:    d.DimensionsLWH,
		FullSkidQuantity: d.FullSkidQuantity,
		LayerQuantity:    d.LayerQuantity,
		PalletCount:      d.PalletCount,
		IsSpecialOrder:   d.IsSpecialOrder,
		IsActive:         d.IsActive,
		Availability:     []BranchAvailability{},
	}
	if d.MinimumOrderQty != nil {
		out.MinimumOrderQty = *d.MinimumOrderQty
	}

	// One row per branch that has EITHER a stock record or a lead-time policy. A branch with
	// neither is absent rather than reported as an authoritative zero, see the DIB-443 note in
	// inventory/stock_lookup.go: "no record" and "none on hand" are different sentences.
	branches := make(map[string]struct{}, len(d.OnHandByBranch)+len(d.LeadTimeDaysByBranch))
	for b := range d.OnHandByBranch {
		branches[b] = struct{}{}
	}
	for b := range d.LeadTimeDaysByBranch {
		branches[b] = struct{}{}
	}
	inStockSomewhere := false
	shortest := -1
	for b := range branches {
		qty := d.OnHandByBranch[b]
		row := BranchAvailability{BranchID: b, OnHandBase: qty, InStock: qty > 0}
		if days, ok := d.LeadTimeDaysByBranch[b]; ok {
			dd := days
			row.LeadTimeDays = &dd
			if shortest < 0 || days < shortest {
				shortest = days
			}
		}
		if row.InStock {
			inStockSomewhere = true
		}
		out.Availability = append(out.Availability, row)
	}
	// Deterministic order: in-stock branches first (by descending quantity), then the rest by
	// branch id. A response whose row order depends on Go's map iteration is a response that
	// differs between two identical requests, and a client that sorts it anyway still deserves a
	// stable one to diff against.
	sortAvailability(out.Availability)

	switch {
	case inStockSomewhere:
		zero := 0
		out.LeadTimeDays = &zero // it is here; "lead time" is nil days of waiting
	case shortest >= 0:
		s := shortest
		out.LeadTimeDays = &s
	}
	return out
}

// sortAvailability orders branch rows: stocked first (most stock first), then unstocked by branch
// id. Small n (a yard count), so an insertion sort keeps the file free of a sort import for the
// sake of two comparisons.
func sortAvailability(rows []BranchAvailability) {
	less := func(a, b BranchAvailability) bool {
		if a.InStock != b.InStock {
			return a.InStock
		}
		if a.InStock && a.OnHandBase != b.OnHandBase {
			return a.OnHandBase > b.OnHandBase
		}
		return a.BranchID < b.BranchID
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && less(rows[j], rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// writeCatalogErr maps the catalog read's errors. A missing or malformed product id is a 404 (the
// product module already normalises a non-UUID id to ErrNotFound); client input is a 400; anything
// else is a generic 500 with the real error logged server-side.
func (h *Handler) writeCatalogErr(w http.ResponseWriter, r *http.Request, err error) {
	var bre httpx.BadRequestError
	switch {
	case errors.Is(err, product.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.As(err, &bre):
		httpx.Error(w, http.StatusBadRequest, bre.Error())
	default:
		h.log.Error("portal: catalog detail error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
