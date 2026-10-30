package tax

import (
	"encoding/json"
	"errors"
	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
	"strconv"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// writeErr maps a domain error to its HTTP status: BadRequestError → 400, everything else → 500 (the message
// is logged, not leaked). This is the single error boundary for the tax-admin surface.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bad httpx.BadRequestError
	if errors.As(err, &bad) {
		httpx.Error(w, http.StatusBadRequest, bad.Msg)
		return
	}
	h.log.Error("tax: request failed", "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

// decode reads a JSON body (bounded to 1 MiB); on malformed input it writes a 400 and returns false.
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// -- rates ---------------------------------------------------------------------

func (h *Handler) listRates(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rows, err := h.svc.ListRates(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rates": rows})
}

func (h *Handler) createRate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in Rate
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.svc.CreateRate(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// -- profiles --------------------------------------------------------------------

func (h *Handler) listProfiles(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rows, err := h.svc.ListProfiles(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profiles": rows})
}

func (h *Handler) upsertProfile(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in StatusCardProfile
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.svc.UpsertProfile(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) reviewQueue(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !dealerWideReads(w, caller) {
		return
	}
	rows, err := h.svc.ListProfiles(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	pending := make([]StatusCardProfile, 0)
	for _, p := range rows {
		if p.Status == "PENDING" {
			pending = append(pending, p)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"pending_profiles": pending})
}

// -- config --------------------------------------------------------------------

func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, err := h.svc.GetConfig(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) updateConfig(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in Config
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.svc.UpdateConfig(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// -- evidence (masked-only) -------------------------------------------------------

func (h *Handler) listEvidence(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !dealerWideReads(w, caller) {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, err := h.svc.ListEvidence(r.Context(), EvidenceFilter{
		OrderID:   q.Get("order_id"),
		ProfileID: q.Get("profile_id"),
		Limit:     limit,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"evidence": rows})
}

func (h *Handler) captureEvidence(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in EvidenceCapture
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.svc.CaptureEvidence(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) viewEvidence(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !dealerWideReads(w, caller) {
		return
	}
	ev, ok, err := h.svc.ViewEvidence(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "evidence not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ev)
}

func (h *Handler) downloadEvidence(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !dealerWideReads(w, caller) {
		return
	}
	ev, ok, err := h.svc.DownloadEvidence(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "evidence not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ev)
}

func (h *Handler) validateEvidence(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.ValidateReliefEvidence(r.Context(), r.PathValue("id"), r.URL.Query().Get("as_of"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// -- recompute + recon ------------------------------------------------------------

func (h *Handler) recompute(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in struct {
		FromDate string `json:"from_date"`
		ToDate   string `json:"to_date"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.svc.RecomputeReport(r.Context(), in.FromDate, in.ToDate)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// dealerWideReads fences the finance reads whose rows carry no branch of their own: the exemption
// profiles and the evidence records name no branch column, so they are the dealer's whole certificate
// book, a dealer wide caller's read (615 round 2, P2-3). The fence answers before any existence read.
func dealerWideReads(w http.ResponseWriter, caller guards.StaffCaller) bool {
	if caller.Scope != auth.ScopeGlobal {
		httpx.Error(w, http.StatusForbidden, "tax: the exemption certificates and the review queue are the dealer's whole book, a dealer wide caller's read")
		return false
	}
	return true
}

func (h *Handler) recon(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	year, _ := strconv.Atoi(q.Get("year"))
	month, _ := strconv.Atoi(q.Get("month"))
	if year == 0 {
		httpx.Error(w, http.StatusBadRequest, "year required")
		return
	}
	// FW1-10: the reconcile is fenced to the caller's branch reach, the rule the fenced GL reads
	// carry (ListJournals, DIB-392). A named branch must be reachable, and the consolidated figure
	// (no branch named) is the whole company's books, a dealer wide caller's read.
	branchID := q.Get("branch_id")
	if (branchID == "" && caller.Scope != auth.ScopeGlobal) || (branchID != "" && !caller.CanAccessBranch(branchID)) {
		httpx.Error(w, http.StatusForbidden, "tax: caller cannot access this branch")
		return
	}
	out, err := h.svc.ReconcileReport(r.Context(), year, month, branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) worksheet(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	branchID := q.Get("branch_id")
	if (branchID == "" && caller.Scope != auth.ScopeGlobal) || (branchID != "" && !caller.CanAccessBranch(branchID)) {
		httpx.Error(w, http.StatusForbidden, "tax: caller cannot access this branch")
		return
	}
	year, _ := strconv.Atoi(q.Get("year"))
	quarter, _ := strconv.Atoi(q.Get("quarter"))
	month, _ := strconv.Atoi(q.Get("month"))
	if year == 0 {
		httpx.Error(w, http.StatusBadRequest, "year required")
		return
	}
	out, err := h.svc.WorksheetReport(r.Context(), WorksheetPeriod{
		Year:     year,
		Quarter:  quarter,
		Month:    month,
		BranchID: branchID,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
