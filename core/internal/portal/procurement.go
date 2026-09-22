package portal

import (
	"encoding/json"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/procurement"
)

// ProjectsResponse is the GET /procurement/projects envelope: a flat list of the tenant's
// non-terminal projects ({ "projects": [...] }); the board groups them by status client-side.
// This is intentionally NOT the httpx.Page envelope -- the board pulls the whole working set.
type ProjectsResponse struct {
	Projects []procurement.Project `json:"projects"`
}

// registerProcurementRoutes wires the Planning board (P4.2, M3). Projects carry indicative
// prices (financial intent), so every route sits behind SubmitRFQ -- the same gate quotes use
// (Owner/Buyer, not field_crew). Each handler is tenant-scoped by caller.CustomerID; a
// cross-tenant id resolves to 404 (IDOR-safe) in the procurement module.
func (h *Handler) registerProcurementRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/procurement/projects", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.listProjects)))
	r.Handle("POST /api/portal/v1/procurement/projects", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.createProject)))
	r.Handle("GET /api/portal/v1/procurement/projects/{id}", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.getProject)))
	r.Handle("PUT /api/portal/v1/procurement/projects/{id}", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.updateProject)))
	r.Handle("DELETE /api/portal/v1/procurement/projects/{id}", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.deleteProject)))
	r.Handle("POST /api/portal/v1/procurement/projects/{id}/lines", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.addProjectLine)))
	r.Handle("PUT /api/portal/v1/procurement/projects/{id}/lines/{lineId}", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.updateProjectLine)))
	r.Handle("DELETE /api/portal/v1/procurement/projects/{id}/lines/{lineId}", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.removeProjectLine)))
	r.Handle("POST /api/portal/v1/procurement/projects/{id}/submit-rfq", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.submitProjectRFQ)))
}

// listProjects returns the tenant's non-terminal projects for the board.
func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement projects not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	items, err := h.procurementSvc.ListProjects(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []procurement.Project{}
	}
	httpx.WriteJSON(w, http.StatusOK, ProjectsResponse{Projects: items})
}

// createProject opens a new PLANNING project, attributed to the calling portal user.
func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement create not implemented")
		return
	}
	var req procurement.CreateProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	p, err := h.procurementSvc.CreateProject(ctx, caller.CustomerID, caller.UserID, req.Name)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

// getProject returns one project with its lines, scoped to the tenant.
func (h *Handler) getProject(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement detail not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	d, err := h.procurementSvc.GetProject(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// updateProject renames and/or moves a project's lane (status transition validated).
func (h *Handler) updateProject(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement update not implemented")
		return
	}
	var req procurement.UpdateProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	p, err := h.procurementSvc.UpdateProject(ctx, caller.CustomerID, r.PathValue("id"), req.Name, req.Status)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// deleteProject removes a PLANNING project (409 in any other lane).
func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement delete not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	if err := h.procurementSvc.DeleteProject(ctx, caller.CustomerID, r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// addProjectLine appends a catalog line to a project.
func (h *Handler) addProjectLine(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement add-line not implemented")
		return
	}
	var req procurement.AddLineRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	ln, err := h.procurementSvc.AddLine(ctx, caller.CustomerID, r.PathValue("id"), req)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ln)
}

// updateProjectLine edits qty/uom/notes on a project line (partial update).
func (h *Handler) updateProjectLine(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement update-line not implemented")
		return
	}
	var req procurement.UpdateLineRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	ln, err := h.procurementSvc.UpdateLine(ctx, caller.CustomerID, r.PathValue("id"), r.PathValue("lineId"), req)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ln)
}

// removeProjectLine deletes one line from a project.
func (h *Handler) removeProjectLine(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement remove-line not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	if err := h.procurementSvc.RemoveLine(ctx, caller.CustomerID, r.PathValue("id"), r.PathValue("lineId")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// submitProjectRFQ transitions PLANNING -> QUOTING, pricing each line indicatively.
func (h *Handler) submitProjectRFQ(w http.ResponseWriter, r *http.Request) {
	if h.procurementSvc == nil {
		httpx.NotImplemented(w, "procurement submit-rfq not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	d, err := h.procurementSvc.SubmitRFQ(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}
