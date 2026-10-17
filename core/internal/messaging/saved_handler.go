package messaging

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/google/uuid"
)

func (h *Handler) saveSaved(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		MessageID string `json:"message_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := uuid.Parse(req.MessageID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "message_id must be a valid UUID")
		return
	}
	item, err := h.svc.SaveMessage(r.Context(), c, req.MessageID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) listSaved(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := h.svc.ListSavedMessages(r.Context(), c, parseLimit(r.URL.Query().Get("limit")))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) unsaveSaved(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, ok := h.caller(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	messageID := r.PathValue("messageID")
	if _, err := uuid.Parse(messageID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "messageID must be a valid UUID")
		return
	}
	if err := h.svc.DeleteSavedMessage(r.Context(), c, messageID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
