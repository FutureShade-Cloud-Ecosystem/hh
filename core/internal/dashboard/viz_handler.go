package dashboard

// viz_handler.go, the two routes the composed-metric catalog needs, and the ONE decision the
// handler makes that nothing below it may second-guess: whether this caller is allowed cost.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
)

// maxVizTiles bounds one composed-tile read. A home screen holds a handful of charts; this refuses
// a URL built to make the server measure a hundred of them.
const maxVizTiles = 24

// RegisterVizRoutes wires the composer.
//
//	GET /api/v1/dashboard/metrics          staff, the catalog THIS caller may compose from
//	GET /api/v1/dashboard/viz?tile=…&tile=… staff, the series for the composed tiles named
//
// Both are staff-gated at the door and cost-gated in the payload. There is deliberately NO write
// route here: a composed tile is stored through the shared presentation substrate
// (PUT /api/v1/surfaces/home.dashboard/my-items, the 0172 kit), because a second persistence path
// for the same kind of fact is how "reset" comes to mean two things.
func (h *Handler) RegisterVizRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/dashboard/metrics", g.RequireStaff(h.staff(h.metricCatalog)))
	r.Handle("GET /api/v1/dashboard/viz", g.RequireStaff(h.staff(h.viz)))
}

// vizScopeFrom is scopeFrom plus the cost decision.
//
// MANAGER-UP, FROM THE CALLER'S OWN TIER, RESOLVED BY THE AUTH GUARD. Not from a query parameter,
// not from a header, and not from anything a dealer can curate, curation decides which metrics are
// OFFERED, and being offered a metric is not the same as being allowed one. The rule is
// caller.IsManager(), which is the same test quote's costAllowed and pos's margin gate apply, so
// there is one answer in the product to "may this person see cost".
func (h *Handler) vizScopeFrom(r *http.Request) Scope {
	s := h.scopeFrom(r)
	caller, ok := identity.StaffContextFromContext(r.Context())
	s.CostAllowed = ok && caller.IsManager()
	return s
}

func (h *Handler) metricCatalog(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	scope := h.vizScopeFrom(r)
	metrics := h.svc.MetricCatalogFor(r.Context(), scope)
	if metrics == nil {
		metrics = []MetricMeta{} // an empty catalog is an empty ARRAY, never null: the composer
		// renders an empty state from a list it can iterate, and `null` is the shape that makes a
		// client invent an error surface for a legitimate empty.
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"metrics":    metrics,
		"dimensions": []string{DimBranch, DimStatus, DimBucket},
		"shapes":     []string{ShapeBar, ShapeSplit, ShapeList},
		"profile":    h.svc.ProfileFor(r.Context(), scope.RoleCode),
	})
}

func (h *Handler) viz(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	ids := r.URL.Query()["tile"]
	if len(ids) > maxVizTiles {
		ids = ids[:maxVizTiles]
	}
	tiles := h.svc.ResolveViz(r.Context(), h.vizScopeFrom(r), ids)
	if tiles == nil {
		tiles = []VizTile{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tiles": tiles})
}
