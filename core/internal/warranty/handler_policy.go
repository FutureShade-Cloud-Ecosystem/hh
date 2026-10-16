package warranty

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// handler_policy.go, the walk change 6 policy HTTP surface. The read is staff (the till and Manager
// Home read the threshold; the board tiles and the return sentence answer from it), the write is
// admin (Dealer Admin's Warranty page), the same split the counter policy surface takes.

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.Policy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PolicyInput
	if !h.decode(w, r, &in) {
		return
	}
	p, err := h.svc.UpdatePolicy(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
