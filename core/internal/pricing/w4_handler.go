package pricing

// w4_handler.go, the REST surface for the three W4-A features.
//
// GUARD DISCIPLINE, unchanged from RegisterRoutes: reads are Sales+ (readGuard), writes are
// Manager+ (writeGuard). Three of these deserve a word on WHY they sit where they do:
//
//   * GET /pricing/clearance is a READ for a counter clerk. Clearance is the one piece of pricing
//     config a Sales user genuinely needs to see, it is what they tell a customer about, so it
//     is readGuard, while every mutation of it is Manager+.
//   * POST /pricing/uom/plan is a READ despite being a POST. It computes and returns; it writes
//     nothing. POST because the input is a structured body (product + qty + uom + target units),
//     the same reason /pricing/resolve is a POST.
//   * POST /pricing/rebates/accrue WRITES the claim ledger and is Manager+. It is the manual /
//     backfill door; the automatic on-sale call is a PARK (see REPORT-W4-A.md) because the POS
//     service would have to declare the seam and internal/pos is outside this swarm's fence.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterW4Routes registers the DIB-70 / DIB-69 / M2-uom surface. It is a SEPARATE method rather
// than more lines inside RegisterRoutes so the wave's additions are one call site to find, one
// block to review, and one line to remove if the wave is reverted.
func (h *Handler) RegisterW4Routes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("pricing.RegisterW4Routes: guards are required (routes must be auth-gated)")
	}
	// DIB-70 promotions.
	r.Handle("GET /api/v1/pricing/promotions", g.RequireStaff(h.staff(h.listPromotions)))
	r.Handle("POST /api/v1/pricing/promotions", g.RequireManager(h.staff(h.createPromotion)))
	r.Handle("PUT /api/v1/pricing/promotions/{id}", g.RequireManager(h.staff(h.updatePromotion)))
	r.Handle("DELETE /api/v1/pricing/promotions/{id}", g.RequireManager(h.staff(h.deletePromotion)))

	// DIB-154 clearance.
	r.Handle("GET /api/v1/pricing/clearance", g.RequireStaff(h.staff(h.listClearance)))
	r.Handle("PUT /api/v1/pricing/clearance/{productId}", g.RequireManager(h.staff(h.upsertClearance)))
	r.Handle("DELETE /api/v1/pricing/clearance/{productId}", g.RequireManager(h.staff(h.deleteClearance)))

	// DIB-69 rebate core.
	r.Handle("GET /api/v1/pricing/rebates/programs", g.RequireStaff(h.staff(h.listRebatePrograms)))
	r.Handle("POST /api/v1/pricing/rebates/programs", g.RequireManager(h.staff(h.createRebateProgram)))
	r.Handle("PUT /api/v1/pricing/rebates/programs/{id}", g.RequireManager(h.staff(h.updateRebateProgram)))
	r.Handle("DELETE /api/v1/pricing/rebates/programs/{id}", g.RequireManager(h.staff(h.deleteRebateProgram)))
	r.Handle("GET /api/v1/pricing/rebates/vendors/{vendorId}/ledger", g.RequireStaff(h.staff(h.vendorLedger)))
	r.Handle("POST /api/v1/pricing/rebates/accrue", g.RequireManager(h.staff(h.accrueRebates)))

	// M2-uom conversions.
	r.Handle("POST /api/v1/pricing/uom/plan", g.RequireStaff(h.staff(h.planUOM)))
	r.Handle("GET /api/v1/pricing/uom/conversions", g.RequireStaff(h.staff(h.listConversions)))
	r.Handle("PUT /api/v1/pricing/uom/conversions", g.RequireManager(h.staff(h.upsertConversion)))
	r.Handle("DELETE /api/v1/pricing/uom/conversions/{id}", g.RequireManager(h.staff(h.deleteConversion)))
}

// - promotions -

func (h *Handler) listPromotions(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.ListPromotions(r.Context(), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []Promotion{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// promoPayload takes the date bounds as RFC3339 STRINGS rather than time.Time so a malformed date
// is a 400 with a sentence about the format, not encoding/json's "parsing time ... as ..." noise
// leaking into an operator's screen.
type promoPayload struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	ProductID     string `json:"product_id"`
	CategoryID    string `json:"category_id"`
	BranchID      string `json:"branch_id"`
	StartsAt      string `json:"starts_at"`
	EndsAt        string `json:"ends_at"`
	CalcType      string `json:"calc_type"`
	DiscountBps   *int64 `json:"discount_bps"`
	DiscountCents *int64 `json:"discount_cents"`
	PriceCents    *int64 `json:"price_cents"`
	PriceUOM      string `json:"price_uom"`
	Priority      int64  `json:"priority"`
	DetailText    string `json:"detail_text"`
	IsActive      *bool  `json:"is_active"`
	Version       int64  `json:"version"`
}

func (p promoPayload) toPromotion(w http.ResponseWriter) (Promotion, bool) {
	starts, ok := parseRFC3339(w, p.StartsAt, "starts_at")
	if !ok {
		return Promotion{}, false
	}
	ends, ok := parseRFC3339(w, p.EndsAt, "ends_at")
	if !ok {
		return Promotion{}, false
	}
	out := Promotion{
		Code: p.Code, Name: p.Name, ProductID: p.ProductID, CategoryID: p.CategoryID,
		BranchID: p.BranchID, StartsAt: starts, EndsAt: ends, CalcType: p.CalcType,
		DiscountBps: p.DiscountBps, DiscountCents: p.DiscountCents, PriceCents: p.PriceCents,
		PriceUOM: p.PriceUOM, Priority: p.Priority, DetailText: p.DetailText, IsActive: true,
	}
	if p.IsActive != nil {
		out.IsActive = *p.IsActive
	}
	return out, true
}

func parseRFC3339(w http.ResponseWriter, v, field string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true // absence is caught by validation with a domain sentence
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, field+" must be an RFC3339 timestamp (e.g. 2026-06-01T00:00:00Z)")
		return time.Time{}, false
	}
	return t, true
}

func (h *Handler) createPromotion(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p promoPayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.ProductID, "product_id") || !optionalBodyUUID(w, p.CategoryID, "category_id") ||
		!optionalBodyUUID(w, p.BranchID, "branch_id") {
		return
	}
	promo, ok := p.toPromotion(w)
	if !ok {
		return
	}
	out, err := h.svc.CreatePromotion(r.Context(), promo, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) updatePromotion(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	var p promoPayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.ProductID, "product_id") || !optionalBodyUUID(w, p.CategoryID, "category_id") ||
		!optionalBodyUUID(w, p.BranchID, "branch_id") {
		return
	}
	promo, ok := p.toPromotion(w)
	if !ok {
		return
	}
	out, err := h.svc.UpdatePromotion(r.Context(), id, promo, p.Version, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deletePromotion(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	version, ok := parseVersion(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeletePromotion(r.Context(), id, version, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// - clearance -

func (h *Handler) listClearance(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.ListClearance(r.Context(), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []ClearanceItem{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type clearancePayload struct {
	Reason              string `json:"reason"`
	ClearancePriceCents *int64 `json:"clearance_price_cents"`
	PriceUOM            string `json:"price_uom"`
	StartedAt           string `json:"started_at"`
	EndsAt              string `json:"ends_at"`
	IsActive            *bool  `json:"is_active"`
}

func (h *Handler) upsertClearance(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID := r.PathValue("productId")
	if !requirePathUUID(w, productID, "productId") {
		return
	}
	var p clearancePayload
	if !decode(w, r, &p) {
		return
	}
	started, ok := parseRFC3339(w, p.StartedAt, "started_at")
	if !ok {
		return
	}
	item := ClearanceItem{
		ProductID: productID, Reason: p.Reason, ClearancePriceCents: p.ClearancePriceCents,
		PriceUOM: p.PriceUOM, StartedAt: started, IsActive: true,
	}
	if p.EndsAt != "" {
		ends, ok := parseRFC3339(w, p.EndsAt, "ends_at")
		if !ok {
			return
		}
		item.EndsAt = &ends
	}
	if p.IsActive != nil {
		item.IsActive = *p.IsActive
	}
	out, err := h.svc.UpsertClearance(r.Context(), item, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteClearance(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID := r.PathValue("productId")
	if !requirePathUUID(w, productID, "productId") {
		return
	}
	if err := h.svc.DeleteClearance(r.Context(), productID, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// - rebates -

func (h *Handler) listRebatePrograms(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	vendorID := r.URL.Query().Get("vendor_id")
	if !optionalBodyUUID(w, vendorID, "vendor_id") {
		return
	}
	out, err := h.svc.ListRebatePrograms(r.Context(), vendorID, r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []RebateProgram{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type rebatePayload struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	VendorID         string `json:"vendor_id"`
	ProductID        string `json:"product_id"`
	CategoryID       string `json:"category_id"`
	Basis            string `json:"basis"`
	RateBps          *int64 `json:"rate_bps"`
	RateCentsPerUnit *int64 `json:"rate_cents_per_unit"`
	StartsAt         string `json:"starts_at"`
	EndsAt           string `json:"ends_at"`
	Notes            string `json:"notes"`
	IsActive         *bool  `json:"is_active"`
	Version          int64  `json:"version"`
}

func (p rebatePayload) toProgram(w http.ResponseWriter) (RebateProgram, bool) {
	starts, ok := parseRFC3339(w, p.StartsAt, "starts_at")
	if !ok {
		return RebateProgram{}, false
	}
	out := RebateProgram{
		Code: p.Code, Name: p.Name, VendorID: p.VendorID, ProductID: p.ProductID,
		CategoryID: p.CategoryID, Basis: p.Basis, RateBps: p.RateBps,
		RateCentsPerUnit: p.RateCentsPerUnit, StartsAt: starts, Notes: p.Notes, IsActive: true,
	}
	if p.EndsAt != "" {
		ends, ok := parseRFC3339(w, p.EndsAt, "ends_at")
		if !ok {
			return RebateProgram{}, false
		}
		out.EndsAt = &ends
	}
	if p.IsActive != nil {
		out.IsActive = *p.IsActive
	}
	return out, true
}

func (h *Handler) createRebateProgram(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p rebatePayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.VendorID, "vendor_id") || !optionalBodyUUID(w, p.ProductID, "product_id") ||
		!optionalBodyUUID(w, p.CategoryID, "category_id") {
		return
	}
	prog, ok := p.toProgram(w)
	if !ok {
		return
	}
	out, err := h.svc.CreateRebateProgram(r.Context(), prog, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) updateRebateProgram(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	var p rebatePayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.ProductID, "product_id") || !optionalBodyUUID(w, p.CategoryID, "category_id") {
		return
	}
	prog, ok := p.toProgram(w)
	if !ok {
		return
	}
	out, err := h.svc.UpdateRebateProgram(r.Context(), id, prog, p.Version, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteRebateProgram(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	version, ok := parseVersion(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteRebateProgram(r.Context(), id, version, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) vendorLedger(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	vendorID := r.PathValue("vendorId")
	if !requirePathUUID(w, vendorID, "vendorId") {
		return
	}
	var from, to *time.Time
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		t, ok := parseRFC3339(w, v, "from")
		if !ok {
			return
		}
		from = &t
	}
	if v := q.Get("to"); v != "" {
		t, ok := parseRFC3339(w, v, "to")
		if !ok {
			return
		}
		to = &t
	}
	out, err := h.svc.VendorRebateLedger(r.Context(), vendorID, from, to)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out.Rows == nil {
		out.Rows = []RebateAccrual{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type accruePayload struct {
	SourceType string           `json:"source_type"`
	SourceID   string           `json:"source_id"`
	OccurredAt string           `json:"occurred_at"`
	Lines      []RebateSaleLine `json:"lines"`
}

func (h *Handler) accrueRebates(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p accruePayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.SourceID, "source_id") {
		return
	}
	for _, l := range p.Lines {
		if !optionalBodyUUID(w, l.ProductID, "lines[].product_id") ||
			!optionalBodyUUID(w, l.CategoryID, "lines[].category_id") ||
			!optionalBodyUUID(w, l.VendorID, "lines[].vendor_id") ||
			!optionalBodyUUID(w, l.BranchID, "lines[].branch_id") {
			return
		}
	}
	occurred, ok := parseRFC3339(w, p.OccurredAt, "occurred_at")
	if !ok {
		return
	}
	out, err := h.svc.AccrueSale(r.Context(), RebateSaleEvent{
		SourceType: p.SourceType, SourceID: p.SourceID, OccurredAt: occurred,
		Lines: p.Lines, UserID: staffUserID(r),
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out.Accrued == nil {
		out.Accrued = []RebateAccrual{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// - uom -

func (h *Handler) planUOM(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in UOMPlanInput
	if !decode(w, r, &in) {
		return
	}
	if !optionalBodyUUID(w, in.ProductID, "product_id") || !optionalBodyUUID(w, in.CategoryID, "category_id") {
		return
	}
	out, err := h.svc.PlanUOM(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listConversions(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID := r.URL.Query().Get("product_id")
	if !optionalBodyUUID(w, productID, "product_id") {
		return
	}
	out, err := h.svc.ListConversions(r.Context(), productID, r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []UOMConversion{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) upsertConversion(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var c UOMConversion
	if !decode(w, r, &c) {
		return
	}
	if !optionalBodyUUID(w, c.ProductID, "product_id") || !optionalBodyUUID(w, c.CategoryID, "category_id") {
		return
	}
	out, err := h.svc.UpsertConversion(r.Context(), c, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteConversion(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	if err := h.svc.DeleteConversion(r.Context(), id, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
