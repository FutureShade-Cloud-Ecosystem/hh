package warranty

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// handler_agreement.go, the walk change 6 vendor agreement HTTP surface, the policy handlers' road:
// the read is staff (Dealer Admin's Warranty page and Manager Home's Vendors and credits board read
// the agreements), the write is admin (Dealer Admin sets them), the counter policy's split.

func (h *Handler) listAgreements(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rows, err := h.svc.Agreements(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rows == nil {
		rows = []VendorAgreement{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) putAgreement(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	vendorID := r.PathValue("vendorID")
	var in AgreementInput
	if !h.decode(w, r, &in) {
		return
	}
	saved, err := h.svc.UpdateAgreement(r.Context(), vendorID, in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, saved)
}
