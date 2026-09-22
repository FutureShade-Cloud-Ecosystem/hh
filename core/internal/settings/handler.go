package settings

// handler.go, the kit's HTTP surface (spec §4.5), registered by a CONSUMING module's own route
// registration (the quote pilot mounts it from quote.RegisterStaffRoutes) so no cmd/* wiring is
// needed. Every route is staff-gated at the door; the dealer-curation write applies the Tier-1
// GLOBAL admin gate IN THE SERVICE (the acceptcontrols precedent, that is where the caller is
// actually resolved).

import (
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"

	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Handler serves the surface-config routes.
type Handler struct {
	svc      *Service
	userLink identity.UserIDLinker
}

// NewHandler builds the handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes wires the kit's routes. Idempotent-by-construction is NOT promised: register once,
// from one module (the pilot's quote handler); a second registration panics in ServeMux exactly as
// any duplicated route would.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("settings: guards are required")
	}
	r.Handle("GET /api/v1/surfaces/{surface}/config", g.RequireStaff(h.staff(h.config)))
	r.Handle("PUT /api/v1/surfaces/{surface}/items", g.RequireStaff(h.staff(h.putRoleItems)))
	r.Handle("DELETE /api/v1/surfaces/{surface}/items", g.RequireStaff(h.staff(h.resetRoleItems)))
	r.Handle("PUT /api/v1/surfaces/{surface}/my-items", g.RequireStaff(h.staff(h.putMyItems)))
	r.Handle("DELETE /api/v1/surfaces/{surface}/my-items", g.RequireStaff(h.staff(h.resetMyItems)))
	r.Handle("PUT /api/v1/surfaces/{surface}/my-workspace", g.RequireStaff(h.staff(h.putMyWorkspace)))
	r.Handle("DELETE /api/v1/surfaces/{surface}/my-workspace", g.RequireStaff(h.staff(h.resetMyWorkspace)))

	// D-111, the platform roster. NOT under /surfaces/{surface}: a surface's config is one
	// dealer's curation of one screen, and this is the deployment's floor beneath every dealer.
	// Filing it at the same path would have made "which level is this" a question about a query
	// parameter. The READ is staff-wide (a dealer's admin must be able to see what their override
	// is overriding); the WRITE is the platform operator's alone, gated IN THE SERVICE where the
	// caller is actually resolved, the same placement PutRoleItems' admin gate takes.
	r.Handle("GET /api/v1/platform/hotkeys", g.RequireStaff(h.staff(h.platformHotkeys)))
	r.Handle("PUT /api/v1/platform/hotkeys/{surface}", g.RequireStaff(h.staff(h.putPlatformHotkeys)))
}

// answer maps kit errors onto the wire: StatusError speaks its own status, the two refusal types
// are 403s with their sentence, anything else is an opaque 500.
//
// The two refusals are listed separately on purpose. NotAdminError means "you are not an
// administrator"; NotPlatformAdminError means "you are not the platform operator", which a General
// Manager unambiguously is not while being unambiguously an administrator. One arm for both would
// have to pick one of those sentences and tell somebody the wrong thing.
func answer(w http.ResponseWriter, err error) {
	var se *StatusError
	var na *NotAdminError
	var np *NotPlatformAdminError
	switch {
	case errors.As(err, &se):
		httpx.Error(w, se.Status, se.Msg)
	case errors.As(err, &na):
		httpx.Error(w, http.StatusForbidden, na.Error())
	case errors.As(err, &np):
		httpx.Error(w, http.StatusForbidden, np.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "settings: internal error")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) config(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	// ?profile= is passed through ONLY when present, so a request that does not ask for one is
	// byte-identical to what it was before the widening.
	var opts []ConfigOpt
	if p := r.URL.Query().Get("profile"); p != "" {
		opts = append(opts, ForProfile(p))
	}
	cfg, err := h.svc.Config(r.Context(), r.PathValue("surface"), r.URL.Query().Get("branch_id"), opts...)
	if err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, cfg)
}

type itemsBody struct {
	Profile string `json:"profile,omitempty"`
	Items   []Item `json:"items"`
}

func (h *Handler) putRoleItems(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var b itemsBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "settings: body must be JSON with profile and items")
		return
	}
	if err := h.svc.PutRoleItems(r.Context(), r.PathValue("surface"), b.Profile, b.Items); err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, map[string]bool{"saved": true})
}

// resetRoleItems clears the dealer's curation of one kind, so the level beneath answers again.
// profile and kind are query parameters rather than body fields because a DELETE with a body is a
// shape half the HTTP stack in front of us is entitled to drop.
func (h *Handler) resetRoleItems(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	if err := h.svc.ResetRoleItems(r.Context(), r.PathValue("surface"), q.Get("profile"), q.Get("kind")); err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, map[string]bool{"reset": true})
}

// platformHotkeys serves every surface's platform roster plus this caller's can_edit answer.
func (h *Handler) platformHotkeys(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	view, err := h.svc.PlatformHotkeys(r.Context())
	if err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, view)
}

type platformRosterBody struct {
	Keys []PlatformHotkey `json:"keys"`
}

func (h *Handler) putPlatformHotkeys(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var b platformRosterBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "settings: body must be JSON with a list of keys")
		return
	}
	if err := h.svc.PutPlatformHotkeys(r.Context(), r.PathValue("surface"), b.Keys); err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, map[string]bool{"saved": true})
}

func (h *Handler) putMyItems(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var b itemsBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "settings: body must be JSON with items")
		return
	}
	if err := h.svc.PutUserItems(r.Context(), r.PathValue("surface"), b.Items); err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, map[string]bool{"saved": true})
}

func (h *Handler) resetMyItems(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if err := h.svc.ResetUserItems(r.Context(), r.PathValue("surface")); err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, map[string]bool{"reset": true})
}

func (h *Handler) putMyWorkspace(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in WorkspaceSaveInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "settings: body must be JSON")
		return
	}
	v, err := h.svc.SaveWorkspace(r.Context(), r.PathValue("surface"), in)
	if err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) resetMyWorkspace(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if err := h.svc.ResetWorkspace(r.Context(), r.PathValue("surface")); err != nil {
		answer(w, err)
		return
	}
	writeJSON(w, map[string]bool{"reset": true})
}
