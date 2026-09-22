package invcount

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// listSheets returns count sheets, optionally filtered by ?branch_id and ?status.
func (h *Handler) listSheets(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sheets, err := h.svc.ListSheets(r.Context(), r.URL.Query().Get("branch_id"), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if sheets == nil {
		sheets = []Sheet{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": sheets})
}

// createSheet opens a new OPEN count sheet.
func (h *Handler) createSheet(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CreateSheetRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	sheet, err := h.svc.CreateSheet(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sheet)
}

// sheetDetail returns a sheet, its lines, and the variance summary.
func (h *Handler) sheetDetail(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	detail, err := h.svc.SheetDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

// freezeSheet captures the snapshot on-hand basis onto the sheet and flips it FROZEN.
func (h *Handler) freezeSheet(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sheet, err := h.svc.FreezeSheet(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sheet)
}

// submitCounts records the maker's counted quantities + reasons and flips the sheet COUNTED.
func (h *Handler) submitCounts(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SubmitCountsRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	sheet, err := h.svc.SubmitCounts(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sheet)
}

// approveSheet records the checker's approval (the maker-checker gate).
func (h *Handler) approveSheet(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sheet, err := h.svc.ApproveSheet(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sheet)
}

// postSheet posts the counted variances (stock moves + reason-coded GL journal), idempotently.
func (h *Handler) postSheet(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.PostSheet(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	detail, err := h.svc.SheetDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

// cancelSheet abandons a sheet before post.
func (h *Handler) cancelSheet(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.CancelSheet(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": StatusCancelled})
}

// reasonCodes returns the per-reason GL account mapping (the reason picker).
func (h *Handler) reasonCodes(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rcs, err := h.svc.ReasonCodes(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rcs == nil {
		rcs = []ReasonCode{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rcs})
}

// negativeStock returns products carrying by-design negative on-hand at ?branch_id.
func (h *Handler) negativeStock(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rows, err := h.svc.NegativeStock(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

// recon lazily runs the current-month inventory→GL reconciliation (run-once) on screen load and returns the
// period run plus the branch's recon history. ?branch_id required; ?year & ?month override the current period.
func (h *Handler) recon(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	branchID := r.URL.Query().Get("branch_id")
	year, month := currentYearMonth(time.Now())
	if v := r.URL.Query().Get("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			year = n
		}
	}
	if v := r.URL.Query().Get("month"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 12 {
			month = n
		}
	}
	if _, err := h.svc.ReconcileMonth(r.Context(), year, month, branchID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	current, _, err := h.svc.ReconRunFor(r.Context(), year, month, branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	history, err := h.svc.ListReconRuns(r.Context(), branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if history == nil {
		history = []ReconRun{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"current": current, "history": history})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the invcount error taxonomy to HTTP.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	if errors.As(err, &ce) {
		httpx.Error(w, ce.status, ce.msg)
		return
	}
	h.log.Error("invcount: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}
