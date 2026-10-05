package pricing

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// registerBranchPriceRoutes mounts the branch price editor surface (HH-LOAD-1
// LD-7, plan CP0-5 answer A). The list is a staff read on the dealer-admin
// surface; both writes sit behind the manager guard, and the contract names
// the pricing_editors list beside the level, as every price write does.
func (h *Handler) registerBranchPriceRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/pricing/branch-prices", g.RequireStaff(h.staff(h.listBranchPrices)))
	r.Handle("PUT /api/v1/pricing/branch-prices/{productId}/{branchId}", g.RequireManager(h.staff(h.putBranchPrice)))
	r.Handle("DELETE /api/v1/pricing/branch-prices/{productId}/{branchId}", g.RequireManager(h.staff(h.deleteBranchPrice)))
}

// branchPricePayload is the set body: the money as integer cents (a JSON
// number with a fraction does not decode into an int64, so a non whole cent
// is refused at the decode) and the unit the price is per.
type branchPricePayload struct {
	ListPriceCents int64  `json:"list_price_cents"`
	PriceUOM       string `json:"price_uom"`
}

func (h *Handler) listBranchPrices(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	q := r.URL.Query()
	productID := q.Get("product_id")
	if productID != "" && !uuidRe.MatchString(productID) {
		httpx.Error(w, http.StatusBadRequest, "product_id must be a uuid")
		return
	}
	branchID := q.Get("branch_id")
	if branchID != "" && !uuidRe.MatchString(branchID) {
		httpx.Error(w, http.StatusBadRequest, "branch_id must be a uuid")
		return
	}
	// Cursor pagination: (product_id, branch_id) is the list's stable
	// ordering key, so fetch limit+1, drop the extra row, mint next_cursor
	// from the last kept row's pair.
	items, err := h.svc.ListBranchPrices(r.Context(), BranchPriceFilter{
		ProductID: productID, BranchID: branchID, AfterKey: p.Cursor, Limit: p.Limit + 1,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		next = last.ProductID + "/" + last.BranchID
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

func (h *Handler) putBranchPrice(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID := r.PathValue("productId")
	if !requirePathUUID(w, productID, "productId") {
		return
	}
	branchID := r.PathValue("branchId")
	if !requirePathUUID(w, branchID, "branchId") {
		return
	}
	var p branchPricePayload
	if !decode(w, r, &p) {
		return
	}
	out, err := h.svc.PutBranchPrice(r.Context(), productID, branchID,
		BranchPriceWrite{ListPriceCents: p.ListPriceCents, PriceUOM: p.PriceUOM}, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteBranchPrice(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID := r.PathValue("productId")
	if !requirePathUUID(w, productID, "productId") {
		return
	}
	branchID := r.PathValue("branchId")
	if !requirePathUUID(w, branchID, "branchId") {
		return
	}
	if err := h.svc.DeleteBranchPrice(r.Context(), productID, branchID, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
