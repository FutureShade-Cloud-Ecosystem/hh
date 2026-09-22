package pricing

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// A-08 REST surface (registered by registerBatchRoutes, called from RegisterRoutes). Reads are
// Sales+ (readGuard); writes, create/import/approve/apply, are Manager+ (writeGuard). The
// separation-of-duties invariant (approver ≠ maker) is enforced in the SERVICE, not by RBAC, so
// two Managers are required for a batch to go live. Manufacturer/vendor names surface here on
// staff screens only (rule 1); no A-08 route is portal-reachable.
func (h *Handler) registerBatchRoutes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("GET /api/v1/pricing/batches", g.RequireStaff(h.staff(h.listBatches)))
	mux.Handle("POST /api/v1/pricing/batches", g.RequireManager(h.staff(h.createBatch)))
	mux.Handle("GET /api/v1/pricing/batches/{id}", g.RequireStaff(h.staff(h.getBatch)))
	mux.Handle("GET /api/v1/pricing/batches/{id}/lines", g.RequireStaff(h.staff(h.listBatchLines)))
	// Import accepts the raw CSV body (text/csv). Filename rides on ?source_ref=.
	mux.Handle("POST /api/v1/pricing/batches/{id}/import", g.RequireManager(h.staff(h.importBatch)))
	mux.Handle("POST /api/v1/pricing/batches/{id}/approve", g.RequireManager(h.staff(h.approveBatch)))
	mux.Handle("POST /api/v1/pricing/batches/{id}/apply", g.RequireManager(h.staff(h.applyBatch)))
	mux.Handle("POST /api/v1/pricing/batches/apply-due", g.RequireManager(h.staff(h.applyDueBatches)))
	// Import profiles (mapping editor), the seeded V-* manufacturer column mappings.
	mux.Handle("GET /api/v1/pricing/import-profiles", g.RequireStaff(h.staff(h.listImportProfiles)))
	mux.Handle("PUT /api/v1/pricing/import-profiles/{id}", g.RequireManager(h.staff(h.updateImportProfileMapping)))
	// Append-only change-log viewer (who/when/before/after).
	mux.Handle("GET /api/v1/pricing/change-log", g.RequireStaff(h.staff(h.changeLog)))
}

func (h *Handler) listImportProfiles(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.ListImportProfiles(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []ImportProfile{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type mappingPayload struct {
	Mapping map[string]string `json:"mapping"`
}

func (h *Handler) updateImportProfileMapping(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	var p mappingPayload
	if !decode(w, r, &p) {
		return
	}
	out, err := h.svc.UpdateImportProfileMapping(r.Context(), id, p.Mapping)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createBatchPayload struct {
	BatchNo       string `json:"batch_no"`
	VendorID      string `json:"vendor_id"`
	ProfileID     string `json:"profile_id"`
	EffectiveDate string `json:"effective_date"` // YYYY-MM-DD (optional)
	SourceRef     string `json:"source_ref"`
}

func (h *Handler) createBatch(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p createBatchPayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.VendorID, "vendor_id") || !optionalBodyUUID(w, p.ProfileID, "profile_id") {
		return
	}
	b := PriceBatch{
		BatchNo: p.BatchNo, VendorID: p.VendorID, ProfileID: p.ProfileID, SourceRef: p.SourceRef,
	}
	if p.EffectiveDate != "" {
		t, err := time.Parse("2006-01-02", p.EffectiveDate)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "effective_date must be YYYY-MM-DD")
			return
		}
		b.EffectiveDate = &t
	}
	out, err := h.svc.CreateBatch(r.Context(), b, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) getBatch(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	out, err := h.svc.GetBatch(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listBatches(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	vendorID := q.Get("vendor_id")
	if vendorID != "" && !uuidRe.MatchString(vendorID) {
		httpx.Error(w, http.StatusBadRequest, "vendor_id must be a uuid")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := h.svc.ListBatches(r.Context(), BatchFilter{Status: q.Get("status"), VendorID: vendorID, Limit: limit})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []PriceBatch{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listBatchLines(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	out, err := h.svc.ListBatchLines(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []PriceChangeLine{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// importBatch reads the raw CSV request body and stages the derived lines. Capped at 8 MiB.
func (h *Handler) importBatch(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	sourceRef := r.URL.Query().Get("source_ref")
	body := http.MaxBytesReader(w, r.Body, 8<<20)
	defer body.Close()
	out, err := h.svc.ImportBatch(r.Context(), id, body, sourceRef)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) approveBatch(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	out, err := h.svc.ApproveBatch(r.Context(), id, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) applyBatch(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	b, applied, err := h.svc.ApplyBatch(r.Context(), id, staffUserID(r), time.Now())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"batch": b, "applied": applied})
}

func (h *Handler) applyDueBatches(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	n, err := h.svc.ApplyDueBatches(r.Context(), staffUserID(r), time.Now())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"applied": n})
}

func (h *Handler) changeLog(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	productID := q.Get("product_id")
	if productID != "" && !uuidRe.MatchString(productID) {
		httpx.Error(w, http.StatusBadRequest, "product_id must be a uuid")
		return
	}
	batchID := q.Get("batch_id")
	if batchID != "" && !uuidRe.MatchString(batchID) {
		httpx.Error(w, http.StatusBadRequest, "batch_id must be a uuid")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := h.svc.ChangeLog(r.Context(), ChangeLogFilter{ProductID: productID, BatchID: batchID, Limit: limit})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []ChangeLogRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
