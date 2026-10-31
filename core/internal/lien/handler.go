package lien

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
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

// - holdback rules (RT-1) -

// overrideBody is the body of an override write: a percent in basis points, or null to clear. The field
// must be present, so an empty body is never read as a clear.
type overrideBody struct {
	HoldbackPctBps customer.OptionalValue[int] `json:"holdback_pct_bps"`
}

func (h *Handler) decodeOverride(w http.ResponseWriter, r *http.Request) (*int, bool) {
	var b overrideBody
	if !h.decode(w, r, &b) {
		return nil, false
	}
	if !b.HoldbackPctBps.Set() {
		httpx.Error(w, http.StatusUnprocessableEntity, "lien: holdback_pct_bps is required (basis points, or null to clear the override)")
		return nil, false
	}
	return b.HoldbackPctBps.Value, true
}

func (h *Handler) provinceRules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ProvinceRules(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) jobRule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rule, err := h.svc.HoldbackRuleForJob(r.Context(), r.PathValue("customerJobID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rule)
}

func (h *Handler) setDealerOverride(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	pct, ok := h.decodeOverride(w, r)
	if !ok {
		return
	}
	d, err := h.svc.SetDealerOverride(r.Context(), pct)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) setJobOverride(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	pct, ok := h.decodeOverride(w, r)
	if !ok {
		return
	}
	id := r.PathValue("customerJobID")
	if err := h.svc.SetJobOverride(r.Context(), id, pct); err != nil {
		h.writeErr(w, r, err)
		return
	}
	rule, err := h.svc.HoldbackRuleForJob(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rule)
}

// linkBody is the body of a link write: the customer job's id, or null to unlink.
type linkBody struct {
	CustomerJobID customer.OptionalValue[string] `json:"customer_job_id"`
}

func (h *Handler) linkCustomerJob(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b linkBody
	if !h.decode(w, r, &b) {
		return
	}
	if !b.CustomerJobID.Set() {
		httpx.Error(w, http.StatusUnprocessableEntity, "lien: customer_job_id is required (a customer job id, or null to unlink)")
		return
	}
	id := ""
	if b.CustomerJobID.Value != nil {
		id = *b.CustomerJobID.Value
	}
	job, err := h.svc.LinkCustomerJob(r.Context(), r.PathValue("id"), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, job)
}

// preservedLienBody is the body of a preserve or a discharge: the date and the note.
type preservedLienBody struct {
	PreservedOn  string `json:"preserved_on"`
	DischargedOn string `json:"discharged_on"`
	Note         string `json:"note"`
}

func (h *Handler) preserveLien(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b preservedLienBody
	if !h.decode(w, r, &b) {
		return
	}
	job, err := h.svc.RecordLienPreserved(r.Context(), r.PathValue("id"), b.PreservedOn, b.Note)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, job)
}

func (h *Handler) dischargeLien(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b preservedLienBody
	if !h.decode(w, r, &b) {
		return
	}
	job, err := h.svc.DischargeLien(r.Context(), r.PathValue("id"), b.DischargedOn, b.Note)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, job)
}
