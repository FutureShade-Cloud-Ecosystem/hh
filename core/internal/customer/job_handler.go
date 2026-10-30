package customer

// job_handler.go, the job account doors under a customer (wave 3 JA-1, decision 1.1 A).
//
// The routes live under the customer because the job belongs to the customer:
//
//	GET  /api/v1/customers/{id}/jobs              the customer's jobs
//	POST /api/v1/customers/{id}/jobs              open a job
//	GET  /api/v1/customers/{id}/jobs/{jobId}      one job
//	PUT  /api/v1/customers/{id}/jobs/{jobId}      edit a job
//	POST /api/v1/customers/{id}/jobs/{jobId}/close close a job
//
// THE AUTHZ IS THE CUSTOMER RECORD'S OWN. The reads are staff reads and the writes are manager
// writes, the same guard the customer create and the customer address write carry, on the same
// surfaces: a job is part of the customer record, and the person who may open an account may open
// a job under it.
//
// THE FENCE IS THE BRANCH'S. A job of another branch is not found by a caller whose grants do not
// reach it, and the answer is 404, never a 403 that would confirm the job exists.

import (
	"encoding/json"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// registerJobRoutes registers the five job doors. They are registered beside the customer's own
// routes and share their guards and surfaces.
func (h *Handler) registerJobRoutes(r httpx.Registrar, g *guards.Guards) {
	// The reads are staff reads, the writes manager writes: the customer record's own authz.
	r.Handle("GET /api/v1/customers/{id}/jobs", g.RequireStaff(h.staff(h.listJobs)))
	r.Handle("GET /api/v1/customers/{id}/jobs/{jobId}", g.RequireStaff(h.staff(h.getJob)))
	r.Handle("POST /api/v1/customers/{id}/jobs", g.RequireManager(h.staff(h.createJob)))
	r.Handle("PUT /api/v1/customers/{id}/jobs/{jobId}", g.RequireManager(h.staff(h.editJob)))
	r.Handle("POST /api/v1/customers/{id}/jobs/{jobId}/close", g.RequireManager(h.staff(h.closeJob)))
}

// listJobs backs GET /api/v1/customers/{id}/jobs.
//
// The list is fenced: the customer is the first fence and the job's own branch is the second, so a
// job of a branch the caller cannot reach is not in the list. An empty list is an empty list, not
// a 404.
func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	jobs, err := h.svc.ListJobs(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.svc.JobsForWire(r.Context(), jobs))
}

// getJob backs GET /api/v1/customers/{id}/jobs/{jobId}.
func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	job, err := h.svc.GetJob(r.Context(), r.PathValue("id"), r.PathValue("jobId"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.svc.JobForWire(r.Context(), job))
}

// createJob backs POST /api/v1/customers/{id}/jobs.
//
//	{"job_no": "J-1", "name": "Riverside driveway", "po_reference": "PO-8891", ...}
//
// A job number already in use under this customer is a 409, and the job's own credit limit may
// never exceed the customer's.
func (h *Handler) createJob(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in JobCreateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	job, err := h.svc.CreateJob(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.svc.JobForWire(r.Context(), job))
}

// editJob backs PUT /api/v1/customers/{id}/jobs/{jobId}.
//
// A field left out keeps the job's own value, and an explicit null on the job's own credit limit
// or payment terms clears it, so the job shares the customer's again.
func (h *Handler) editJob(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in JobEditInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	job, err := h.svc.EditJob(r.Context(), r.PathValue("id"), r.PathValue("jobId"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.svc.JobForWire(r.Context(), job))
}

// closeJob backs POST /api/v1/customers/{id}/jobs/{jobId}/close.
//
// A closed job reads and reports, and it takes no new document. A job that is already closed is a
// 409: the caller asked for a close that has already happened and is told so.
func (h *Handler) closeJob(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	job, err := h.svc.CloseJob(r.Context(), r.PathValue("id"), r.PathValue("jobId"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.svc.JobForWire(r.Context(), job))
}
