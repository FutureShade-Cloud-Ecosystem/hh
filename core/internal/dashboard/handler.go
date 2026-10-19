package dashboard

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
	"sort"
)

// maxBodyBytes bounds a layout PUT. Ten tiles of three fields is a few hundred bytes; 32 KiB is
// four orders of magnitude of headroom and still refuses a body meant to exhaust memory.
const maxBodyBytes = 32 << 10

// Handler exposes the cockpit surface.
//
// internal/identity is the ONE peer this package imports, and only in this file: the handler has
// to know who is asking. The service and the resolver below it never see an identity type, Scope
// carries strings, so the seam that matters (dashboard imports no MODULE) is intact.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// RegisterRoutes wires the cockpit behind injected guards, matching the sibling-module pattern.
//
//	GET    /api/v1/dashboard                        staff , my home: layout + measurements
//	GET    /api/v1/dashboard/catalog                staff , the full tile catalog + wiring state
//	PUT    /api/v1/dashboard/layout                 staff , save MY override
//	DELETE /api/v1/dashboard/layout                 staff , reset MY layout to the role default
//	GET    /api/v1/dashboard/profiles               ADMIN , every profile + its role codes
//	GET    /api/v1/dashboard/profiles/{profile}     ADMIN , one role default + its home config
//	PUT    /api/v1/dashboard/profiles/{profile}     ADMIN , edit that role default
//	PUT    /api/v1/dashboard/profiles/{profile}/home ADMIN, edit that role's landing/takeover
//
// The split is the whole of DIB-353's authority model: MY layout is mine (any staff member may
// reorder their own home), a ROLE's layout belongs to an admin. A counter clerk rearranging their
// own tiles is housekeeping; a counter clerk rearranging every counter clerk's tiles is not.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/dashboard", g.RequireStaff(h.staff(h.myHome)))
	r.Handle("GET /api/v1/dashboard/catalog", g.RequireStaff(h.staff(h.catalog)))
	r.Handle("PUT /api/v1/dashboard/layout", g.RequireStaff(h.staff(h.saveMyLayout)))
	r.Handle("DELETE /api/v1/dashboard/layout", g.RequireStaff(h.staff(h.resetMyLayout)))
	// -- DIB-433 pinned tabs. Same authority split as the tile half, for the same reason: MY strip
	// is mine, a ROLE's strip belongs to an admin. --
	r.Handle("PUT /api/v1/dashboard/tabs", g.RequireStaff(h.staff(h.saveMyTabs)))
	r.Handle("DELETE /api/v1/dashboard/tabs", g.RequireStaff(h.staff(h.resetMyTabs)))
	r.Handle("GET /api/v1/dashboard/profiles/{profile}/tabs", g.RequireAdmin(h.staff(h.getProfileTabs)))
	r.Handle("PUT /api/v1/dashboard/profiles/{profile}/tabs", g.RequireAdmin(h.staff(h.putProfileTabs)))
	r.Handle("GET /api/v1/dashboard/profiles", g.RequireAdmin(h.staff(h.listProfiles)))
	r.Handle("GET /api/v1/dashboard/profiles/{profile}", g.RequireAdmin(h.staff(h.getProfile)))
	r.Handle("PUT /api/v1/dashboard/profiles/{profile}", g.RequireAdmin(h.staff(h.putProfile)))
	r.Handle("PUT /api/v1/dashboard/profiles/{profile}/home", g.RequireAdmin(h.staff(h.putProfileHome)))
}

// scopeFrom turns the guard-resolved caller into the Scope providers receive.
//
// The branch list comes from the caller's GRANTS, never from a query parameter. A dashboard that
// let you ask for another branch's numbers by editing a URL would be a nicer-looking version of
// the thing branch scoping exists to prevent.
func (h *Handler) scopeFrom(r *http.Request) Scope {
	caller, _ := identity.StaffContextFromContext(r.Context())
	s := Scope{UserID: caller.UserID, RoleCode: caller.RoleCode}
	if caller.Scope == identity.ScopeGlobal {
		s.AllBranches = true
		return s
	}
	for id, ok := range caller.Branches {
		if ok {
			s.BranchIDs = append(s.BranchIDs, id)
		}
	}
	// Deterministic order so a provider's SQL plan and any test assertion are stable.
	sort.Strings(s.BranchIDs)
	return s
}

// scopeFor is scopeFrom plus the caller's platform identity and org, which a tile's access rule asks the
// role gate with. The routes that save or measure tiles use it; the identity comes from the guard, never
// from the request.
func (h *Handler) scopeFor(r *http.Request, sc guards.StaffCaller) Scope {
	s := h.scopeFrom(r)
	s.PlatformUserID, s.OrgID = sc.PlatformUserID, sc.OrgID
	return s
}

func (h *Handler) myHome(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	layout, err := h.svc.Resolve(r.Context(), h.scopeFor(r, caller))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, layout)
}

// catalogEntry is a catalog tile plus whether anything is actually measuring it. The admin editor
// shows the difference so nobody pins an em-dash to the top of a role's home screen by accident.
type catalogEntry struct {
	TileMeta
	Wired bool `json:"wired"`
}

func (h *Handler) catalog(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	wired := map[string]bool{}
	for _, id := range h.svc.WiredTiles() {
		wired[id] = true
	}
	out := make([]catalogEntry, len(Catalog))
	for i, t := range Catalog {
		out[i] = catalogEntry{TileMeta: t, Wired: wired[t.ID]}
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"tiles":    out,
		"profiles": Profiles,
		"profile":  h.svc.ProfileFor(r.Context(), caller.RoleCode),
	})
}

type layoutBody struct {
	Tiles []LayoutRow `json:"tiles"`
}

func (h *Handler) saveMyLayout(w http.ResponseWriter, r *http.Request, sc guards.StaffCaller) {
	var body layoutBody
	if !decode(w, r, &body) {
		return
	}
	rows, err := h.svc.SaveMyLayout(r.Context(), h.scopeFor(r, sc), body.Tiles)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, layoutBody{Tiles: rows})
}

func (h *Handler) resetMyLayout(w http.ResponseWriter, r *http.Request, sc guards.StaffCaller) {
	caller, _ := identity.StaffContextFromContext(r.Context())
	n, err := h.svc.ResetMyLayout(r.Context(), caller.UserID)
	if err != nil {
		h.fail(w, err)
		return
	}
	// Answer with the layout the reset LANDED ON, not just a count. The client's whole reason for
	// calling this is "put me back on the role default"; making it re-fetch to find out what that
	// is invites a flicker of the old layout in between.
	layout, err := h.svc.Resolve(r.Context(), h.scopeFor(r, sc))
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cleared": n, "layout": layout})
}

type tabsBody struct {
	Pins []TabPin `json:"pins"`
}

func (h *Handler) saveMyTabs(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body tabsBody
	if !decode(w, r, &body) {
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	pins, err := h.svc.SaveMyTabs(r.Context(), caller.UserID, body.Pins)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, TabStrip{Source: SourceUser, Pins: pins})
}

// resetMyTabs answers with the strip the reset LANDED ON, not just a count, the same choice
// resetMyLayout makes, and for the same reason: the caller's whole purpose is "put me back on the
// role default", and making them re-fetch to find out what that is invites a flash of the old strip
// in between.
func (h *Handler) resetMyTabs(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	caller, _ := identity.StaffContextFromContext(r.Context())
	n, err := h.svc.ResetMyTabs(r.Context(), caller.UserID)
	if err != nil {
		h.fail(w, err)
		return
	}
	strip, err := h.svc.TabsFor(r.Context(), h.svc.ProfileFor(r.Context(), caller.RoleCode), caller.UserID)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cleared": n, "tabs": strip})
}

func (h *Handler) getProfileTabs(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	profile := r.PathValue("profile")
	pins, err := h.svc.ProfileTabsFor(r.Context(), profile)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profile": profile, "pins": pins})
}

func (h *Handler) putProfileTabs(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body tabsBody
	if !decode(w, r, &body) {
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	pins, err := h.svc.SaveProfileTabs(r.Context(), r.PathValue("profile"), body.Pins, caller.UserID)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tabsBody{Pins: pins})
}

func (h *Handler) listProfiles(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out := make([]map[string]any, 0, len(Profiles))
	for _, p := range Profiles {
		home, err := h.svc.HomeFor(r.Context(), p)
		if err != nil {
			h.fail(w, err)
			return
		}
		out = append(out, map[string]any{"profile": p, "home": home})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	profile := r.PathValue("profile")
	slots, home, err := h.svc.ProfileLayout(r.Context(), profile)
	if err != nil {
		h.fail(w, err)
		return
	}
	rows := make([]LayoutRow, len(slots))
	for i, s := range slots {
		rows[i] = LayoutRow{TileID: s.Meta.ID, Position: s.Position, Visible: s.Visible}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"profile": profile, "tiles": rows, "home": home,
	})
}

func (h *Handler) putProfile(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body layoutBody
	if !decode(w, r, &body) {
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	rows, err := h.svc.SaveProfileLayout(r.Context(), r.PathValue("profile"), body.Tiles, caller.UserID)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, layoutBody{Tiles: rows})
}

func (h *Handler) putProfileHome(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body HomeConfig
	if !decode(w, r, &body) {
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	profile := r.PathValue("profile")
	if err := h.svc.SaveProfileHome(r.Context(), profile, body, caller.UserID); err != nil {
		h.fail(w, err)
		return
	}
	home, err := h.svc.HomeFor(r.Context(), profile)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, home)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnknownProfile) {
		httpx.Error(w, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, ErrEmptyLayout) || errors.Is(err, ErrEmptyTabs) ||
		errors.Is(err, ErrNoLanding) || errors.Is(err, ErrNoCaller) {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var bad httpx.BadRequestError
	if errors.As(err, &bad) {
		httpx.Error(w, http.StatusBadRequest, bad.Error())
		return
	}
	if h.log != nil {
		h.log.Error("dashboard: request failed", "err", err)
	}
	httpx.Error(w, http.StatusInternalServerError, "dashboard unavailable")
}
