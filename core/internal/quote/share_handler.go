// share_handler.go, DIB-57 HTTP surface: staff link management + the PUBLIC customer view.
//
// HOW A ROUTE BECOMES PUBLIC HERE. There is no auth middleware to exclude a route from: this codebase
// guards per-route by wrapping the handler (identity/handler.go), so a route is public simply by being
// registered bare. That makes public-ness an explicit, visible act rather than the absence of one,
// which is the property worth having, but it also means a bare registration must never be accidental.
// Hence the separate registration method with a name that says so out loud.
package quote

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterShareRoutes wires the STAFF share-link management endpoints (all staff-gated, per R1).
func (h *StaffHandler) RegisterShareRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("quote: guards are required")
	}
	r.Handle("POST /api/v1/quotes/{id}/share-links", g.RequireStaff(h.staff(h.mintShareLink)))
	r.Handle("GET /api/v1/quotes/{id}/share-links", g.RequireStaff(h.staff(h.listShareLinks)))
	r.Handle("DELETE /api/v1/quotes/{id}/share-links/{linkId}", g.RequireStaff(h.staff(h.revokeShareLink)))
}

// RegisterPublicRoutes wires the UNAUTHENTICATED customer endpoints. The token in the path IS the
// credential (R4), there is no session, no cookie and no staff context on these two routes.
//
// What keeps that safe is enforced in the service and the repository, not here: the token grants
// exactly one quote, the payload is a hand-built allowlist that cannot leak a field added upstream,
// a dead link answers 410 rather than revealing whether the quote exists, and every use is logged.
func (h *StaffHandler) RegisterPublicRoutes(r httpx.Registrar) {
	r.Handle("GET /api/public/v1/quotes/{token}", http.HandlerFunc(h.publicQuote))
	r.Handle("POST /api/public/v1/quotes/{token}/accept", http.HandlerFunc(h.publicAccept))
}

func (h *StaffHandler) mintShareLink(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		// Days overrides the default 30-day life. Optional; a per-link value is stored either way, so
		// changing the default later never touches a live link.
		Days int `json:"days"`
	}
	// An absent body is fine here, minting with defaults is the common case.
	_ = decodeBody(w, r, &body)
	var ttl time.Duration
	if body.Days > 0 {
		ttl = time.Duration(body.Days) * 24 * time.Hour
	}
	out, err := h.svc.MintShareLink(r.Context(), r.PathValue("id"), ttl)
	if err != nil {
		h.writeShareErr(w, err)
		return
	}
	// 201 with the raw token, the ONLY time it is ever returned. It is unrecoverable afterwards.
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *StaffHandler) listShareLinks(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	links, err := h.svc.ListShareLinks(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeShareErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": links})
}

func (h *StaffHandler) revokeShareLink(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if err := h.svc.RevokeShareLink(r.Context(), r.PathValue("id"), r.PathValue("linkId")); err != nil {
		h.writeShareErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *StaffHandler) publicQuote(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.PublicQuoteByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		h.writeShareErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *StaffHandler) publicAccept(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TypedName string `json:"typed_name"`
		Accepted  bool   `json:"accepted"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		h.writeShareErr(w, err)
		return
	}
	res, err := h.svc.AcceptByTokenKeyed(r.Context(), r.PathValue("token"), body.TypedName, body.Accepted, r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.writeShareErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// writeShareErr adds the link-lifecycle answers on top of the shared quote mapping.
func (h *StaffHandler) writeShareErr(w http.ResponseWriter, err error) {
	var notEditable *NotEditableError
	switch {
	case errors.Is(err, ErrLinkGone):
		// 410 GONE, deliberately not 404. The link EXISTED and died, the person holding it should
		// learn that the link is dead and ask for a new one, not be told the quote never existed and
		// conclude someone made it up.
		httpx.Error(w, http.StatusGone,
			"this link is no longer valid, ask for a new link to view this quote")
	case errors.Is(err, ErrLinkNotFound):
		// A token that never existed. 404 with no detail: an unauthenticated caller learns nothing.
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrNotAcceptable):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrEmptyQuote):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.As(err, &notEditable):
		httpx.Error(w, http.StatusConflict, notEditable.Error())
	default:
		h.writeErr(w, err)
	}
}
