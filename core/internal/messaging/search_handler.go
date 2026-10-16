package messaging

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func (h *Handler) search(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok || caller.OrgID == "" {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	results, err := h.svc.Search(r.Context(), c, caller.OrgID,
		r.URL.Query().Get("q"), parseLimit(r.URL.Query().Get("limit")))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, results)
}
