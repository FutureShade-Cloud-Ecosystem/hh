package billing

// ar_landing_handler.go, the AR landing's HTTP surface: the cards read, the typed policy, the
// reminder send + history, the AP contact, the paper-path flag list and its resolution, and the two
// PUBLIC tracking endpoints that make open and click observable at all.
//
// GATING. Every staff read here exposes customer financial distress, so it follows the month-end
// handler's rule rather than inventing a looser one next door: manager for the reads and the acts,
// ADMIN for the policy write, the bucket edges and the tier are dealer configuration and a money
// threshold, which is the level the auto-hold switch already requires.
//
// THE TWO PUBLIC ROUTES ARE PUBLIC ON PURPOSE, and they are the narrowest public surface in this
// package. They accept a random 24-byte token and nothing else, they never answer differently for a
// known and an unknown token, they return no JSON and no body a scraper can read, and they can only
// ever append a row. A customer's mail client fetching a pixel cannot authenticate, which is the
// whole reason tracking works this way everywhere; what stops it being a hole is that the token is
// the only key and the response is constant.

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// ARLandingHandler serves the landing.
type ARLandingHandler struct {
	userLink  identity.UserIDLinker
	landing   *ARLandingService
	reminders *ARReminderService
	log       *slog.Logger
	zone      houseday.Source // the dealer's business day zone; nil = the Ontario dealer's
}

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (h *ARLandingHandler) WithZone(src houseday.Source) *ARLandingHandler { h.zone = src; return h }

// NewARLandingHandler wires the services. A nil reminder service is allowed: the landing renders
// without the machinery, and the reminder routes answer 503 in words rather than 404, an unwired
// seam is a DEPLOY fault an operator should be told about, not a missing feature they guess at.
func NewARLandingHandler(landing *ARLandingService, reminders *ARReminderService, log *slog.Logger) *ARLandingHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ARLandingHandler{landing: landing, reminders: reminders, log: log}
}

// RegisterRoutes self-registers under the existing /api/v1/ar namespace, one namespace for the AR
// surface, not two that split by which Go file happened to implement them (the month-end ruling).
func (h *ARLandingHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/ar/landing", g.RequireManager(h.staff(h.landingRead)))
	r.Handle("GET /api/v1/ar/landing/policy", g.RequireManager(h.staff(h.policyRead)))
	r.Handle("PUT /api/v1/ar/landing/policy", g.RequireAdmin(h.staff(h.policyWrite)))
	// The change history. ADMIN, matching the write directly above rather than the manager read:
	// whoever may move the escalation edge may see who moved it last, and the manager who may only
	// read the landing has no business in the dealer's configuration trail.
	r.Handle("GET /api/v1/ar/landing/policy/history", g.RequireAdmin(h.staff(h.policyHistory)))

	r.Handle("POST /api/v1/ar/reminders", g.RequireManager(h.staff(h.sendReminder)))
	r.Handle("GET /api/v1/ar/reminders", g.RequireManager(h.staff(h.reminderHistory)))

	r.Handle("GET /api/v1/ar/paper-path", g.RequireManager(h.staff(h.paperPath)))
	r.Handle("POST /api/v1/ar/paper-path/sweep", g.RequireManager(h.staff(h.paperPathSweep)))
	r.Handle("POST /api/v1/ar/paper-path/{id}/resolve", g.RequireManager(h.staff(h.paperPathResolve)))

	r.Handle("GET /api/v1/ar/customers/{customerID}/ap-contact", g.RequireManager(h.staff(h.apRead)))
	r.Handle("PUT /api/v1/ar/customers/{customerID}/ap-contact", g.RequireManager(h.staff(h.apWrite)))
	r.Handle("DELETE /api/v1/ar/customers/{customerID}/ap-contact", g.RequireManager(h.staff(h.apDelete)))

	// PUBLIC. See the file header for why, and for what makes it narrow.
	r.Handle("GET /api/public/v1/ar/track/{token}/open", http.HandlerFunc(h.trackOpen))
	r.Handle("GET /api/public/v1/ar/track/{token}/click", http.HandlerFunc(h.trackClick))
}

func (h *ARLandingHandler) landingRead(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	asOf, err := parseAsOfFor(r.Context(), h.zone, r.URL.Query().Get("as_of"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	view, err := h.landing.Landing(r.Context(), r.URL.Query().Get("branch_id"), asOf)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *ARLandingHandler) policyRead(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	httpx.WriteJSON(w, http.StatusOK, h.landing.Policy(r.Context(), r.URL.Query().Get("branch_id")))
}

func (h *ARLandingHandler) policyWrite(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	// The CURRENT policy is the base, so a PUT that carries one knob does not silently reset five.
	p := h.landing.Policy(r.Context(), r.URL.Query().Get("branch_id"))
	if err := decodeJSON(w, r, &p); err != nil {
		h.writeErr(w, r, err)
		return
	}
	p.BranchID = r.URL.Query().Get("branch_id")
	saved, err := h.landing.SetPolicy(r.Context(), p)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, saved)
}

// policyHistory serves the receivables settings change trail, newest first.
//
// The envelope is {"items": [...]} rather than a bare array, the shape every other list read on this
// surface uses, and the one that leaves room for a cursor later without breaking a client.
func (h *ARLandingHandler) policyHistory(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := h.landing.PolicyHistory(r.Context(), r.URL.Query().Get("branch_id"), limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *ARLandingHandler) sendReminder(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if h.reminders == nil {
		httpx.Error(w, http.StatusServiceUnavailable,
			"reminders are not configured on this server, the AR send seam is not wired")
		return
	}
	// THE CAPABILITY GATE ON THE ACT, not only on the payload. A base-tier caller who hand-crafts
	// this POST is refused here, because a gate that only omits fields from a GET is a gate on the
	// display and not on the power.
	if !h.landing.AdvancedAllowed(r.Context(), "") {
		httpx.Error(w, http.StatusForbidden,
			"reminders are part of accelerated AR, this account is on the base plan")
		return
	}
	var in SendReminderInput
	if err := decodeJSON(w, r, &in); err != nil {
		h.writeErr(w, r, err)
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	sent, err := h.reminders.Send(r.Context(), in, caller.UserID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sent)
}

func (h *ARLandingHandler) reminderHistory(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if h.reminders == nil {
		httpx.Error(w, http.StatusServiceUnavailable,
			"reminders are not configured on this server, the AR send seam is not wired")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := h.reminders.History(r.Context(), r.URL.Query().Get("customer_id"), limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *ARLandingHandler) paperPath(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	view, err := h.landing.Landing(r.Context(), r.URL.Query().Get("branch_id"), time.Time{})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": view.PaperPath})
}

func (h *ARLandingHandler) paperPathSweep(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if h.reminders == nil {
		httpx.Error(w, http.StatusServiceUnavailable,
			"reminders are not configured on this server, the AR send seam is not wired")
		return
	}
	evaluated, raised, err := h.reminders.SweepPaperPath(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"evaluated": evaluated, "raised": raised})
}

type paperPathResolveRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (h *ARLandingHandler) paperPathResolve(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if h.reminders == nil {
		httpx.Error(w, http.StatusServiceUnavailable,
			"reminders are not configured on this server, the AR send seam is not wired")
		return
	}
	var req paperPathResolveRequest
	if err := decodeJSON(w, r, &req); err != nil {
		h.writeErr(w, r, err)
		return
	}
	caller, _ := identity.StaffContextFromContext(r.Context())
	if err := h.reminders.ResolvePaperPathFlag(r.Context(), r.PathValue("id"), req.Status, req.Note, caller.UserID); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *ARLandingHandler) apRead(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if h.reminders == nil {
		httpx.Error(w, http.StatusServiceUnavailable,
			"reminders are not configured on this server, the AR send seam is not wired")
		return
	}
	c, ok, err := h.reminders.APContact(r.Context(), r.PathValue("customerID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !ok {
		// NOT a 404. "Nobody has tagged an AP contact" is a true, ordinary answer about an account
		// that exists, and turning it into a not-found would make every client write a 404 branch to
		// mean "empty", which is the shape that eventually renders an error box over an empty state.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"tagged": false})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tagged": true, "contact": c})
}

func (h *ARLandingHandler) apWrite(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	if h.reminders == nil {
		httpx.Error(w, http.StatusServiceUnavailable,
			"reminders are not configured on this server, the AR send seam is not wired")
		return
	}
	var c APContact
	if err := decodeJSON(w, r, &c); err != nil {
		h.writeErr(w, r, err)
		return
	}
	c.CustomerID = r.PathValue("customerID")
	caller, _ := identity.StaffContextFromContext(r.Context())
	saved, err := h.reminders.TagAPContact(r.Context(), c, caller.UserID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tagged": true, "contact": saved})
}

func (h *ARLandingHandler) apDelete(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if h.reminders == nil {
		httpx.Error(w, http.StatusServiceUnavailable,
			"reminders are not configured on this server, the AR send seam is not wired")
		return
	}
	if err := h.reminders.UntagAPContact(r.Context(), r.PathValue("customerID")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tagged": false})
}

// trackingPixel is a 1×1 transparent GIF, 43 bytes, the smallest thing a mail client will fetch
// and render without leaving a broken-image box in the middle of an invoice.
var trackingPixel = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00,
	0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02,
	0x44, 0x01, 0x00, 0x3b,
}

// trackOpen records an open and always returns the same pixel. See the file header: a public
// endpoint that answered differently for an unknown token would be an enumeration oracle.
func (h *ARLandingHandler) trackOpen(w http.ResponseWriter, r *http.Request) {
	if h.reminders != nil {
		h.reminders.Track(r.Context(), r.PathValue("token"), EventOpen, r.UserAgent())
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(trackingPixel)
}

// trackClick records a click and returns 204.
//
// IT DOES NOT REDIRECT, and that is the deliberate choice. A tracking endpoint that takes a
// destination and forwards to it is an open redirect, and an open redirect on a domain a customer
// trusts is a phishing tool somebody else will use. The link in the message points at the portal
// directly; this endpoint is fetched alongside it and answers with nothing.
func (h *ARLandingHandler) trackClick(w http.ResponseWriter, r *http.Request) {
	if h.reminders != nil {
		h.reminders.Track(r.Context(), r.PathValue("token"), EventClick, r.UserAgent())
	}
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.WriteHeader(http.StatusNoContent)
}

// writeErr maps the kit's StatusError onto its status and echoes the sentence, because every
// refusal this surface produces is written for a person to read.
func (h *ARLandingHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var se *settings.StatusError
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &se):
		httpx.Error(w, se.Status, se.Msg)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrBranchForbidden):
		httpx.Error(w, http.StatusForbidden, BranchForbiddenWords)
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Error())
	default:
		h.log.Warn("ar landing request failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusBadRequest, err.Error())
	}
}
