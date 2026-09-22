package dashboard

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
)

// quickaction_handler.go, DIB-434. Two surfaces over one registry: what I may see, and what an
// admin may change about what everyone sees.

type QuickActionHandler struct {
	userLink identity.UserIDLinker
	svc      *QuickActionService
	log      *slog.Logger
}

func NewQuickActionHandler(svc *QuickActionService, log *slog.Logger) *QuickActionHandler {
	return &QuickActionHandler{svc: svc, log: log}
}

// RegisterRoutes wires the quick-action surface behind injected guards.
//
//	GET /api/v1/quick-actions                  staff , the actions I may see, resolved
//	GET /api/v1/quick-actions/config           ADMIN , every action + switch + per-profile matrix
//	PUT /api/v1/quick-actions/config/{action}  ADMIN , edit one action's visibility
//
// THE GATE IS HERE, NOT IN THE CLIENT. GET /quick-actions returns only what this caller may see;
// there is no payload of hidden actions for a client to filter, because a filter the client applies
// is a filter the client can be made to skip. The admin routes are behind requireAdmin for the same
// reason the cockpit's profile routes are: MY chrome is mine, EVERYONE's chrome is an admin's.
func (h *QuickActionHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/quick-actions", g.RequireStaff(h.staff(h.mine)))
	r.Handle("GET /api/v1/quick-actions/config", g.RequireAdmin(h.staff(h.config)))
	r.Handle("PUT /api/v1/quick-actions/config/{action}", g.RequireAdmin(h.staff(h.putConfig)))
}

func (h *QuickActionHandler) mine(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	caller, _ := identity.StaffContextFromContext(r.Context())
	profile, actions := h.svc.VisibleFor(r.Context(), caller.RoleCode)
	// Never nil: an empty ARRAY is the honest wire answer for "you see none of these", and it saves
	// every client from having to distinguish [] from null before iterating.
	if actions == nil {
		actions = []ActionMeta{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profile": profile, "actions": actions})
}

func (h *QuickActionHandler) config(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"actions":  h.svc.AdminView(r.Context()),
		"profiles": Profiles,
	})
}

type quickActionConfigBody struct {
	Enabled  bool            `json:"enabled"`
	Profiles map[string]bool `json:"profiles"`
}

func (h *QuickActionHandler) putConfig(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body quickActionConfigBody
	if !decode(w, r, &body) {
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	eff, err := h.svc.SaveConfig(r.Context(), r.PathValue("action"), body.Enabled, body.Profiles, caller.UserID)
	if err != nil {
		if errors.Is(err, ErrUnknownAction) {
			httpx.Error(w, http.StatusNotFound, err.Error())
			return
		}
		if h.log != nil {
			h.log.Error("quick actions: save failed", "action", r.PathValue("action"), "err", err)
		}
		httpx.Error(w, http.StatusInternalServerError, "quick actions unavailable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, eff)
}
