package lien

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/customerwall"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// handler.go, the Lien Cockpit HTTP surface. Reads are RequireManager (Monday triage is manager-visible);
// the release route adds the in-service dual gate. Errors map through the clientError status taxonomy.

const maxBodyBytes = 1 << 20 // 1 MiB

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	if errors.As(err, &ce) {
		httpx.Error(w, ce.status, ce.msg)
		return
	}
	h.log.Error("lien: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
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

// - reads -

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	jobs, err := h.svc.ListJobs(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": jobs})
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	job, err := h.svc.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, job)
}

func (h *Handler) triage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.Triage(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) aging(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rows, err := h.svc.HoldbackAging(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	docs, err := h.svc.ListDocuments(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": docs})
}

func (h *Handler) documentVersions(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	docs, err := h.svc.DocumentVersions(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": docs})
}

// - mutations -

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in JobInput
	if !h.decode(w, r, &in) {
		return
	}
	if !customerwall.Fence(w, r, h.reach, in.CustomerID) {
		return
	}
	job, err := h.svc.CreateJob(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, job)
}

func (h *Handler) syncHoldbacks(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	res, err := h.svc.SyncHoldbacks(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) createDocument(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in DocumentInput
	if !h.decode(w, r, &in) {
		return
	}
	doc, err := h.svc.CreateDocument(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, doc)
}

func (h *Handler) release(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var appr ReleaseApproval
	// The co-signer body is optional to decode (a missing body → empty approval → the dual gate 403s).
	if r.ContentLength != 0 {
		if !h.decode(w, r, &appr) {
			return
		}
	}
	res, err := h.svc.ReleaseHoldbackApproved(r.Context(), r.PathValue("jobID"), appr)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
