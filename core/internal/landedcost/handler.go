package landedcost

// handler.go, the landed-cost HTTP surface. Reads are RequireStaff; create/apply/retro/void are
// RequireManager (the guards are threaded in by RegisterRoutes in module.go). Money is integer cents; the
// error taxonomy maps to HTTP via writeErr.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const maxBodyBytes = 1 << 20 // 1 MiB

func (h *Handler) list(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.List(r.Context(), r.URL.Query().Get("branch_id"), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := h.svc.Create(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": id, "status": StatusDraft})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	detail, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.Preview(r.Context(), r.PathValue("id"), r.URL.Query().Get("basis"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.Apply(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": StatusPosted})
}

func (h *Handler) retro(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.RetroAdjust(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": StatusRetroAdjusted})
}

func (h *Handler) settle(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SettleInput
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &in) {
			return
		}
	}
	if err := h.svc.Settle(r.Context(), r.PathValue("id"), in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": StatusPosted})
}

func (h *Handler) void(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.Void(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": StatusVoided})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the landedcost error taxonomy to HTTP.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	var br httpx.BadRequestError
	switch {
	case errors.As(err, &ce):
		httpx.Error(w, ce.status, ce.msg)
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.Msg)
	default:
		h.log.Error("landedcost: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
