package portal

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// registerSettingsRoutes wires self-service profile + password change. Any authenticated
// user may reach these (h.authn, token required, no capability), since they act only on
// the caller's own record.
func (h *Handler) registerSettingsRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/tenant/me", g.RequireTenant(h.tenant(h.getMe)))
	r.Handle("PUT /api/v1/tenant/me", g.RequireTenant(h.tenant(h.updateMe)))
	r.Handle("POST /api/v1/tenant/me/password", g.RequireTenant(h.tenant(h.changePassword)))
}

// MeResponse is the self-profile payload: the user record with the caller's RESOLVED CAPABILITIES
// attached (DIB-479, spec §1.3 recommendation 1).
//
// WHY THE CAPABILITIES SHIP WITH /me. A client that owns its own role→capability table is a second
// authority, and the day the two disagree the UI enables a button the server refuses, or, far
// worse, hides one the server would have allowed and the person concludes the feature is broken.
// HH Pro's `GRANTS` table is exactly such a copy. Serving the server's own answer here lets the
// client render permissions it can never get wrong, because it is not computing them.
//
// User is EMBEDDED, not nested. Go promotes an embedded struct's fields in JSON, so the response is
// the same object it always was with one new key beside it, every existing reader of GET /me keeps
// working, which is what makes this an extension rather than a break.
type MeResponse struct {
	User
	// Capabilities is CapabilitiesFor(caller.Role), literally the value the request guards
	// consulted to let this call through, not a re-derivation of it. See getMe.
	Capabilities Capabilities `json:"capabilities"`
}

// getMe returns the caller's own user record plus the capabilities the guards resolved for them.
//
// The capability set comes from callerFrom(ctx).Caps(), the SAME PortalContext.Caps() call every
// h.require guard makes on every request. Not CapabilitiesFor(u.Role) off the loaded row, even
// though today that yields the same answer: the moment those two expressions can differ (a
// suspended-but-active state, a per-account capability override), the honest answer is the one the
// gate actually used, and a client told anything else is being told what it may do by something
// that is not the thing deciding.
func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	caller := callerFrom(r.Context())
	u, err := h.svc.Me(r.Context(), caller)
	if err != nil {
		h.writeAccountErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, MeResponse{User: u, Capabilities: caller.Caps()})
}

// updateMe PARTIALLY edits the caller's name + notification_prefs (JSONB, added in 0027).
// The FE saves name and notification_prefs in SEPARATE single-field forms, so this must be a
// partial update: only the fields actually present in the body are written; an omitted field
// is left unchanged (a full-replace would let each save wipe the other).
func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	// Decode into a raw key map so an OMITTED field (preserve) is distinguishable from one
	// explicitly present (set): a typed *T cannot tell an absent key from a JSON null.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&raw); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	var name *string
	if v, ok := raw["name"]; ok && string(v) != "null" {
		var n string
		if err := json.Unmarshal(v, &n); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid name")
			return
		}
		name = &n
	}
	// notification_prefs present (object, {} or null) -> validate+normalize and set; a null/empty
	// value normalizes to "{}" so a client can reset prefs. Omitted -> left unchanged.
	var prefs *[]byte
	if v, ok := raw["notification_prefs"]; ok {
		normalized, err := normalizePrefs(v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		prefs = &normalized
	}
	u, err := h.svc.UpdateProfile(r.Context(), callerFrom(r.Context()), name, prefs)
	if err != nil {
		h.writeAccountErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u)
}

// changePassword sets a new password after verifying the current one; success revokes the
// account's other sessions (§4.6 review #2).
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := h.svc.ChangePassword(r.Context(), callerFrom(r.Context()), req.CurrentPassword, req.NewPassword); err != nil {
		h.writeAccountErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// normalizePrefs validates the notification_prefs payload: an absent/empty value becomes
// an empty JSON object, and anything present must be a JSON object (not an array/scalar) so
// the JSONB column only ever stores a prefs map.
func normalizePrefs(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return []byte("{}"), nil
	}
	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, errors.New("notification_prefs must be a JSON object")
	}
	return trimmed, nil
}

// writeAccountErr maps self-service (settings + password-reset) service errors to HTTP
// inline, without leaning on the auth handler's writeErr, and never leaks internal detail.
func (h *Handler) writeAccountErr(w http.ResponseWriter, r *http.Request, err error) {
	var bre httpx.BadRequestError
	switch {
	case errors.Is(err, errInvalidToken):
		httpx.Error(w, http.StatusBadRequest, "invalid or expired token")
	case errors.Is(err, errWrongPassword):
		httpx.Error(w, http.StatusForbidden, errWrongPassword.Error())
	case errors.Is(err, errNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.As(err, &bre):
		httpx.Error(w, http.StatusBadRequest, bre.Error())
	default:
		h.log.Error("portal: account error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
