package tour

// handler.go, the module's five routes, self-registered from one wiring block in
// cmd/server/main.go.
//
// EVERY ROUTE IS STAFF-GATED AT THE DOOR AND THE PLATFORM GATE LIVES IN THE SERVICE. That split is
// the shipped house posture (settings' putPlatformHotkeys, quote's acceptcontrols, dispatch's
// SavePolicy) and the argument for it is one sentence: the mux guard is the coarse first line, and
// the caller is only actually RESOLVED in the service, so an authority that lived only in a route
// registration is an authority one refactor away from gone.
//
// THE TWO PLATFORM ROUTES ARE NOT UNDER /api/v1/tour. A dealer's walkthrough and the deployment's
// copy of it are two tiers, and filing the second inside the first would make "which level is this"
// a question about a path segment. Same shape D-111 chose for /api/v1/platform/hotkeys and the same
// reason.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
)

// maxBodyBytes bounds every write body. A step's copy is 72 characters and a bookmark is two ids;
// 64 KiB is four orders of magnitude of headroom and still refuses a body nobody typed.
const maxBodyBytes = 64 << 10

// Handler serves the walkthrough routes.
type Handler struct {
	svc      *Service
	userLink identity.UserIDLinker
}

// NewHandler builds the handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes wires the five routes.
//
// requireStaff is REQUIRED and its absence panics at boot rather than registering an open route.
// A nil guard would produce a mux that serves the platform copy editor to the internet and would do
// it silently, at a time when nobody is looking, settings.RegisterRoutes takes the same posture.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("tour.RegisterRoutes: guards are required")
	}
	r.Handle("GET /api/v1/tour", g.RequireStaff(h.staff(h.tour)))
	r.Handle("PUT /api/v1/tour/progress", g.RequireStaff(h.staff(h.putProgress)))
	r.Handle("DELETE /api/v1/tour/progress/{chapter_key}", g.RequireStaff(h.staff(h.restartChapter)))

	// The platform tier. Staff at the door, IsSuperAdmin in the service.
	r.Handle("PUT /api/v1/platform/tour/steps", g.RequireStaff(h.staff(h.putStepCopy)))
	r.Handle("PUT /api/v1/platform/tour/policy", g.RequireStaff(h.staff(h.putPolicy)))
}

// answer maps this module's errors onto the wire: StatusError speaks its own status (the
// 422-in-words posture), the platform refusal is a 403 carrying its own sentence, anything else is
// an opaque 500, a client never learns the text of a database error.
func (h *Handler) answer(w http.ResponseWriter, err error) {
	var se *StatusError
	var np *NotPlatformAdminError
	switch {
	case errors.As(err, &se):
		httpx.Error(w, se.Status, se.Msg)
	case errors.As(err, &np):
		httpx.Error(w, http.StatusForbidden, np.Error())
	default:
		httpx.Error(w, http.StatusInternalServerError, "tour: that could not be saved")
	}
}

// decode reads a bounded JSON body, answering 400 in the walkthrough voice on garbage.
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "tour: that body is not JSON this route can read")
		return false
	}
	return true
}

// tour serves the whole walkthrough for the calling person, or the two bytes `{}`.
//
// THERE IS NO ERROR BRANCH HERE AND THERE CANNOT BE ONE, because Service.Tour returns no error.
// That is the acceptance clause made structural: this handler has no reachable path that writes a
// status other than 200, so no failure anywhere beneath it can put a fault on the app's first
// screen, and no future edit can add one without changing the service's signature first.
func (h *Handler) tour(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	httpx.WriteJSON(w, http.StatusOK, h.svc.Tour(r.Context(), r.URL.Query().Get("branch_id")))
}

func (h *Handler) putProgress(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ProgressEdit
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.svc.PutProgress(r.Context(), in); err != nil {
		h.answer(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// restartChapter takes the chapter from the PATH and nothing from a body.
//
// A DELETE WITH A BODY IS A SHAPE HALF THE HTTP STACK IN FRONT OF US IS ENTITLED TO DROP, which is
// why settings' own resets read their arguments from the URL too. The caller is not in the path
// either, it is the authenticated caller, always, and there is no spelling of this route that
// names somebody else's row.
func (h *Handler) restartChapter(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.RestartChapter(r.Context(), r.PathValue("chapter_key")); err != nil {
		h.answer(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"restarted": true})
}

func (h *Handler) putStepCopy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in StepEdit
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.svc.PutStepCopy(r.Context(), in); err != nil {
		h.answer(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PolicyEdit
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.svc.PutPolicy(r.Context(), in); err != nil {
		h.answer(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"saved": true})
}
