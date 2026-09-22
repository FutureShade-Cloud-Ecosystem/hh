package portal

import (
	"net/http"
	"strconv"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// CatalogSearchResponse is the GET /catalog/search envelope: ranked catalog hits for the
// procurement line-add picker ({ "items": [...] }).
type CatalogSearchResponse struct {
	Items []product.CatalogResult `json:"items"`
}

// registerCatalogRoutes wires the catalog fuzzy/by-SKU search the Planning line picker uses.
// Gated on SubmitRFQ (it is part of the procurement compose surface, not the public A-18
// catalogue), and tenant-agnostic: catalog data is shared, but the picker is a procurement tool.
//
// DIB-479 adds the detail read behind the picker's click-through, on the SAME gate for the same
// reason: it is the second half of one act (find the thing, then look at the thing).
func (h *Handler) registerCatalogRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/catalog/search", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.searchCatalog)))
	r.Handle("GET /api/portal/v1/catalog/products/{id}", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.getCatalogProduct)))
}

// searchCatalog runs a fuzzy (pg_trgm) name + by-SKU search. Query is `q`; `limit` is optional
// (the service clamps it). Returns compact CatalogResult hits.
func (h *Handler) searchCatalog(w http.ResponseWriter, r *http.Request) {
	if h.productSvc == nil {
		httpx.NotImplemented(w, "catalog search not implemented")
		return
	}
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	items, err := h.productSvc.SearchProducts(r.Context(), q.Get("q"), limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []product.CatalogResult{}
	}
	httpx.WriteJSON(w, http.StatusOK, CatalogSearchResponse{Items: items})
}
