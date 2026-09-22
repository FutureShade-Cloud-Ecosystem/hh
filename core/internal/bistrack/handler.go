package bistrack

import (
	"log/slog"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// handler.go, DIB-53 phase 0 the read-only observability surface. Exactly ONE route:
//
//	GET /api/v1/bistrack/status  (staff-gated) → {configured:false, last_sync:null, discrepancies:0}
//
// There is NO POST / trigger / sync endpoint. That is not a convention to remember, it is enforced:
// routeDefs() is the single registration source, RegisterRoutes wires from it, and Routes() exposes it so
// structural_test.go can assert every registered bistrack route is a GET. Add a non-GET here and CI goes
// red before the W10 worker can smuggle a trigger in (supervisor C4).

// Route is the (method, pattern) of one registered route, the data the structural test inspects.
type Route struct {
	Method  string
	Pattern string
}

// routeDef is a route plus its handler (the internal registration record).
type routeDef struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// Handler is the bistrack HTTP surface.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

// NewHandler wires the status service.
func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// routeDefs is the SINGLE source of truth for what this module registers. Everything under
// /api/v1/bistrack is here; there is deliberately nothing but a GET.
func (h *Handler) routeDefs() []routeDef {
	return []routeDef{
		{http.MethodGet, "/api/v1/bistrack/status", h.status},
	}
}

// Routes exposes (method, pattern) for every registered route, the structural test asserts all are GET.
func (h *Handler) Routes() []Route {
	out := make([]Route, 0, len(h.routeDefs()))
	for _, rd := range h.routeDefs() {
		out = append(out, Route{Method: rd.method, Pattern: rd.pattern})
	}
	return out
}

// RegisterRoutes wires the module's routes, staff-gated, from routeDefs, the same source Routes()
// reports, so the two can never drift.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	for _, rd := range h.routeDefs() {
		handler := rd.handler
		r.Handle(rd.method+" "+rd.pattern, g.RequireStaff(h.staff(func(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
			handler(w, r)
		})))
	}
}

// status returns the read-only sync status. Read-only: it starts nothing.
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Status(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not read bistrack status")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}
