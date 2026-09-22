package warranty

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// handler.go, the warranty HTTP surface. Registry reads are staff-visible, registry config is admin; claim
// capture + lifecycle are staff; settlement is manager (PRD §4). Errors map through the clientError taxonomy.

const maxBodyBytes = 1 << 20 // 1 MiB

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	if errors.As(err, &ce) {
		httpx.Error(w, ce.status, ce.msg)
		return
	}
	h.log.Error("warranty: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// - FR-1 vendor registry -

func (h *Handler) listVendorProfiles(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ListVendorProfiles(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) getVendorProfile(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.GetVendorProfile(r.Context(), r.PathValue("vendorID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) upsertVendorProfile(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in VendorProfileInput
	if !h.decode(w, r, &in) {
		return
	}
	in.VendorID = r.PathValue("vendorID") // the path is the source of truth for which vendor
	p, err := h.svc.UpsertVendorProfile(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) addOverride(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ProductOverrideInput
	if !h.decode(w, r, &in) {
		return
	}
	// Resolve the profile from the vendor in the path, so the caller supplies only product + terms.
	p, err := h.svc.GetVendorProfile(r.Context(), r.PathValue("vendorID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	in.ProfileID = p.ID
	ov, err := h.svc.AddProductOverride(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ov)
}

// - FR-2 claims -

func (h *Handler) listClaims(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	claims, err := h.svc.ListClaims(r.Context(), ClaimFilter{
		BranchID: q.Get("branch_id"), Phase: q.Get("phase"), VendorID: q.Get("vendor_id"),
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": claims})
}

func (h *Handler) getClaim(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, err := h.svc.GetClaim(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) createClaim(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ClaimInput
	if !h.decode(w, r, &in) {
		return
	}
	c, err := h.svc.CreateClaim(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) assignClaim(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in struct {
		VendorID string `json:"vendor_id"`
		OwnerID  string `json:"owner_id"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	c, err := h.svc.AssignClaim(r.Context(), r.PathValue("id"), in.VendorID, in.OwnerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

// - FR-3 lifecycle -

func (h *Handler) claimEvents(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	events, err := h.svc.ClaimEvents(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": events})
}

func (h *Handler) claimChecklist(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.Checklist(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) tickChecklist(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ChecklistTickInput
	if !h.decode(w, r, &in) {
		return
	}
	st, err := h.svc.TickChecklist(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) advancePhase(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PhaseTransitionInput
	if !h.decode(w, r, &in) {
		return
	}
	c, err := h.svc.AdvancePhase(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) attachEvidence(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in EvidenceInput
	if !h.decode(w, r, &in) {
		return
	}
	ev, err := h.svc.AttachEvidence(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ev)
}

// - FR-5 settlement -

func (h *Handler) settleClaim(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SettlementInput
	if !h.decode(w, r, &in) {
		return
	}
	res, err := h.svc.RecordSettlement(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// - FR-7 reporting + board -

func (h *Handler) reports(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	bundle, err := h.svc.Reports(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bundle)
}

// board returns the Kanban columns (the confirmed phases + exits) with their claims, a one-call board load.
func (h *Handler) board(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	claims, err := h.svc.ListClaims(r.Context(), ClaimFilter{BranchID: q.Get("branch_id"), VendorID: q.Get("vendor_id")})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	byPhase := map[string][]Claim{}
	for _, c := range claims {
		byPhase[c.Phase] = append(byPhase[c.Phase], c)
	}
	cols := make([]map[string]any, 0, len(KanbanColumns()))
	for _, ph := range KanbanColumns() {
		list := byPhase[ph]
		if list == nil {
			list = []Claim{}
		}
		cols = append(cols, map[string]any{"phase": ph, "claims": list})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"columns": cols})
}
