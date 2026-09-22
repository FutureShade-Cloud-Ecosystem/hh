package compliance

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
	"strconv"
)

// handler.go, the compliance-calendar HTTP surface + the shared review-status banner endpoint. Reads are
// RequireManager; the review-status upsert is manager-gated.

const maxBodyBytes = 1 << 20

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	if errors.As(err, &ce) {
		httpx.Error(w, ce.status, ce.msg)
		return
	}
	h.log.Error("compliance: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

func (h *Handler) calendar(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.Calendar(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) upcoming(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	horizon := defaultRuleDays
	if v := r.URL.Query().Get("horizon_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			horizon = n
		}
	}
	items, err := h.svc.Upcoming(r.Context(), horizon)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) reviewStatus(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rs, err := h.svc.ReviewStatus(r.Context(), r.URL.Query().Get("subject_type"), r.URL.Query().Get("subject_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rs)
}

func (h *Handler) upsertReviewStatus(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReviewStatusInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	rs, err := h.svc.UpsertReviewStatus(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rs)
}
