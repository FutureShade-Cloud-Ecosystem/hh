package reporting

// handler.go, the HTTP surface. Thin by design: parse, call, map the error. Every rule that decides
// anything lives in service.go or commission_service.go, so a second caller (a job, a CLI, a future BFF)
// gets the same answers without going through HTTP.
//
// -- GUARD LEVELS, AND WHY THEY DIFFER ----------------------------------------------------------------
//	staff    the catalogue and saved views. Seeing that "Margin by Category" exists is not a leak, and
//	         hiding it from a scoped user means they cannot discover a report they may run for their
//	         own branch.
//	manager  every RUN, EXPORT, PRINT, and every commission read. Reports are finance information; the
//	         branch fence inside the service narrows it further.
//	admin    writing a commission RULE. A rate is a pay decision, and a rep who could write their own
//	         rule could write themselves a raise.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
	"strconv"
	"strings"
)

// Handler exposes reporting over HTTP.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
}

// NewHandler wires the service.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes self-registers the reporting surface. Signature matches the house handler shape.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	// Catalogue.
	r.Handle("GET /api/v1/reporting/templates", g.RequireStaff(h.staff(h.listTemplates)))
	r.Handle("GET /api/v1/reporting/templates/{code}", g.RequireStaff(h.staff(h.getTemplate)))
	// Running, exporting, printing.
	r.Handle("GET /api/v1/reporting/templates/{code}/run", g.RequireManager(h.staff(h.runTemplate)))
	r.Handle("GET /api/v1/reporting/templates/{code}/export.csv", g.RequireManager(h.staff(h.exportTemplate)))
	r.Handle("POST /api/v1/reporting/templates/{code}/print", g.RequireManager(h.staff(h.printTemplate)))
	// Artefacts.
	r.Handle("GET /api/v1/reporting/runs", g.RequireManager(h.staff(h.listRuns)))
	r.Handle("GET /api/v1/reporting/runs/{id}", g.RequireManager(h.staff(h.getRun)))
	r.Handle("GET /api/v1/reporting/runs/{id}/document", g.RequireManager(h.staff(h.getRunDocument)))
	// Saved parameter sets.
	r.Handle("GET /api/v1/reporting/saved-views", g.RequireStaff(h.staff(h.listSavedViews)))
	r.Handle("POST /api/v1/reporting/saved-views", g.RequireStaff(h.staff(h.saveView)))
	r.Handle("DELETE /api/v1/reporting/saved-views/{id}", g.RequireStaff(h.staff(h.deleteView)))
	// Commissions.
	r.Handle("GET /api/v1/reporting/commission/rules", g.RequireManager(h.staff(h.listRules)))
	r.Handle("POST /api/v1/reporting/commission/rules", g.RequireAdmin(h.staff(h.createRule)))
	r.Handle("DELETE /api/v1/reporting/commission/rules/{id}", g.RequireAdmin(h.staff(h.retireRule)))
	r.Handle("GET /api/v1/reporting/commission/preview", g.RequireManager(h.staff(h.commissionPreview)))
	r.Handle("POST /api/v1/reporting/commission/runs", g.RequireManager(h.staff(h.commissionCompute)))
	r.Handle("GET /api/v1/reporting/commission/runs", g.RequireManager(h.staff(h.commissionRuns)))
	r.Handle("GET /api/v1/reporting/commission/runs/{id}", g.RequireManager(h.staff(h.commissionRun)))
	r.Handle("GET /api/v1/reporting/commission/runs/{id}/export.csv", g.RequireManager(h.staff(h.commissionExport)))
	r.Handle("POST /api/v1/reporting/commission/runs/{id}/print", g.RequireManager(h.staff(h.commissionPrint)))
	r.Handle("GET /api/v1/reporting/commission/accruals", g.RequireManager(h.staff(h.commissionAccruals)))
	r.Handle("GET /api/v1/reporting/commission/attribution-check", g.RequireManager(h.staff(h.attributionCheck)))
}

// -- catalogue ----------------------------------------------------------------

func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	cats, err := h.svc.Categories(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	t, err := h.svc.Template(r.Context(), r.PathValue("code"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// -- running ------------------------------------------------------------------

// queryParams turns the URL query into the parameter map. ONLY the first value of each key is read -
// a repeated parameter is a caller confusion, not a list, and taking the first is the behaviour every
// other surface in the app has. The map is handed to bindParams, which refuses anything unknown.
func queryParams(r *http.Request) map[string]string {
	out := map[string]string{}
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 && vs[0] != "" {
			out[k] = vs[0]
		}
	}
	// print_branch_id is a TRANSPORT concern (where the paper comes out), not a report parameter, so it
	// is stripped here rather than being allowed to trip the unknown-parameter refusal.
	delete(out, "print_branch_id")
	return out
}

func (h *Handler) runTemplate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	res, err := h.svc.Run(r.Context(), r.PathValue("code"), queryParams(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) exportTemplate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	res, body, err := h.svc.Export(r.Context(), r.PathValue("code"), queryParams(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+csvFilename(res)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// csvFilename builds a safe download name from the report code and its dates. No user string reaches it.
func csvFilename(res Result) string {
	name := res.Code
	if from := res.Params["from_date"]; from != "" {
		name += "_" + from
	}
	if to := res.Params["to_date"]; to != "" {
		name += "_" + to
	}
	return sanitizeFilename(name) + ".csv"
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

type printRequest struct {
	Params        map[string]string `json:"params"`
	PrintBranchID string            `json:"print_branch_id"`
}

func (h *Handler) printTemplate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req printRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if req.Params == nil {
		req.Params = queryParams(r)
	}
	run, err := h.svc.Print(r.Context(), r.PathValue("code"), req.Params, req.PrintBranchID)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, run)
}

// -- artefacts ----------------------------------------------------------------

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := h.svc.Runs(r.Context(), limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": runs})
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	run, err := h.svc.RunArtefact(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

// getRunDocument serves the FROZEN html of an artefact as a document, this is what "print" opens.
// Served with a restrictive CSP because the body is stored HTML: it is generated by renderHTML with
// every cell escaped, and the header makes that guarantee enforced by the browser rather than by
// trust in a code path that could change.
func (h *Handler) getRunDocument(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	run, err := h.svc.RunArtefact(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox allow-modals")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(run.HTML))
}

// -- saved views --------------------------------------------------------------

func (h *Handler) listSavedViews(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	views, err := h.svc.SavedViews(r.Context(), r.URL.Query().Get("template_code"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": views})
}

type saveViewRequest struct {
	TemplateCode string            `json:"template_code"`
	Name         string            `json:"name"`
	Params       map[string]string `json:"params"`
	IsShared     bool              `json:"is_shared"`
}

func (h *Handler) saveView(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req saveViewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	v, err := h.svc.SaveView(r.Context(), req.TemplateCode, req.Name, req.Params, req.IsShared)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) deleteView(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.DeleteView(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- commissions --------------------------------------------------------------

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rules, err := h.svc.CommissionRules(r.Context(), r.URL.Query().Get("rep_user_id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rules})
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CommissionRule
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := h.svc.CreateCommissionRule(r.Context(), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) retireRule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.RetireCommissionRule(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) commissionPreview(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	periods, err := h.svc.CommissionPreview(r.Context(),
		q.Get("rep_user_id"), q.Get("from"), q.Get("to"), q.Get("branch_id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": periods, "posts_to_gl": false, "note": AccrualNote})
}

type computeRequest struct {
	RepUserID string `json:"rep_user_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	BranchID  string `json:"branch_id"`
}

func (h *Handler) commissionCompute(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req computeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := h.svc.ComputeAndFreeze(r.Context(), req.RepUserID, req.From, req.To, req.BranchID)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handler) commissionRuns(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	runs, err := h.svc.CommissionRuns(r.Context(), q.Get("from"), q.Get("to"), q.Get("rep_user_id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": runs, "posts_to_gl": false})
}

func (h *Handler) commissionRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.CommissionRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) commissionExport(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, body, err := h.svc.ExportCommissionRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+
		sanitizeFilename("commission_"+p.PeriodStart+"_"+p.PeriodEnd)+`.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Handler) commissionPrint(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req printRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	run, err := h.svc.PrintCommissionRun(r.Context(), r.PathValue("id"), req.PrintBranchID)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, run)
}

// attributionCheck is the operator tool over the RepAssignmentReader seam: "does this report agree
// with who the system says owns each account?" An unwired seam answers 200 with bound=false and an
// explanation, not an error, the check is a cross-check, and its absence is a missing safety net
// rather than a broken feature.
func (h *Handler) attributionCheck(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	res, err := h.svc.AttributionCheck(r.Context(), q.Get("from"), q.Get("to"), q.Get("branch_id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) commissionAccruals(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	acc, err := h.svc.CommissionAccruals(r.Context(), q.Get("from"), q.Get("to"))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, acc)
}

// -- error mapping ------------------------------------------------------------

// fail is the ONE place a domain error becomes a status code. Written once so a new route cannot
// invent a mapping, and ordered so the most specific match wins.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Msg)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrUnauthorized):
		httpx.Error(w, http.StatusUnauthorized, "authentication required")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "this report is outside your branch scope")
	case errors.Is(err, ErrNoDatabase):
		httpx.Error(w, http.StatusServiceUnavailable, "reporting is unavailable (no database)")
	case errors.Is(err, ErrColumnDrift):
		// A development error reaching a caller. 500 is right, nothing the caller did caused it and
		// nothing they can change will fix it, and the message names the report so the log is findable.
		httpx.Error(w, http.StatusInternalServerError, err.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "reporting error")
	}
}
