// followup_handler.go, the automation HTTP surface: SMS quote delivery (DIB-77) and the follow-up
// cadence's history + one-click switch (DIB-58).
//
// All four routes are requireStaff and every one of them resolves the quote through StaffService, so
// branch reach is proved by the same code that proves it for a line edit. There is no public route
// here: a customer's only automation-adjacent surface is the share link they were sent.
package quote

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// AutomationHandler serves the quote automation surface.
type AutomationHandler struct {
	svc      *AutomationService
	userLink identity.UserIDLinker
}

// NewAutomationHandler builds the handler.
func NewAutomationHandler(svc *AutomationService) *AutomationHandler {
	return &AutomationHandler{svc: svc}
}

// RegisterAutomationRoutes wires the staff automation endpoints.
//
// It is a SEPARATE registration from RegisterShareRoutes so a deployment can carry quote sharing
// without carrying an outbound-SMS surface, and so the Wave 2 registration lines stay untouched.
func (h *AutomationHandler) RegisterAutomationRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("quote: guards are required")
	}
	r.Handle("POST /api/v1/quotes/{id}/sms-share", g.RequireStaff(h.staff(h.shareBySMS)))
	r.Handle("GET /api/v1/quotes/{id}/followups", g.RequireStaff(h.staff(h.history)))
	r.Handle("POST /api/v1/quotes/{id}/followups/disable", g.RequireStaff(h.staff(h.disable)))
	r.Handle("POST /api/v1/quotes/{id}/followups/enable", g.RequireStaff(h.staff(h.enable)))
}

// shareBySMS texts the customer this quote's share link.
func (h *AutomationHandler) shareBySMS(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Days int `json:"days"` // optional link lifetime override, as on the mint route
	}
	_ = decodeBody(w, r, &body)
	var ttl time.Duration
	if body.Days > 0 {
		ttl = time.Duration(body.Days) * 24 * time.Hour
	}
	res, err := h.svc.ShareBySMS(r.Context(), r.PathValue("id"), ttl)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// 202 ACCEPTED, not 200. The message is QUEUED, the provider is the stub by default and nothing
	// has left the building. A 200 here would tell a salesperson their customer has the quote.
	httpx.WriteJSON(w, http.StatusAccepted, res)
}

func (h *AutomationHandler) history(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.History(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *AutomationHandler) disable(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	h.setEnabled(w, r, false)
}

func (h *AutomationHandler) enable(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	h.setEnabled(w, r, true)
}

func (h *AutomationHandler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	out, err := h.svc.SetEnabled(r.Context(), r.PathValue("id"), enabled)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// writeErr maps the automation errors on top of the shared quote mapping.
func (h *AutomationHandler) writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNoSMSConsent):
		// 409, not 403 and not 500. The request was well formed, the caller was entitled to make it,
		// and the answer is a business fact the salesperson needs stated plainly, so the message says
		// what to do next rather than just refusing.
		httpx.Error(w, http.StatusConflict,
			"this customer has not agreed to receive texts (or has opted out), send the link by email, or ask them to opt in")
	case errors.Is(err, ErrNoticeQueueUnwired), errors.Is(err, ErrFollowUpNotWired):
		// 503: the feature is not configured on this deployment. Not a 404 (the quote exists) and not
		// a 500 (nothing failed), an operator reading this log needs "you did not wire it".
		httpx.Error(w, http.StatusServiceUnavailable,
			"quote automation is not configured on this server")
	default:
		// Everything else, not found, forbidden, expired, not shareable, is already mapped by the
		// quote surface's own error writer, and it must stay mapped in ONE place.
		(&StaffHandler{}).writeErr(w, err)
	}
}
