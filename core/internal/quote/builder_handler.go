package quote

// builder_handler.go, DIB-506 routes + the DIB-507 kit mount.
//
// Registered from RegisterStaffRoutes (one added call there, no cmd/* wiring): the quote module
// is the settings kit's pilot consumer, so it is the module that mounts the kit's generic
// /api/v1/surfaces/… routes, contributing its own quotes.builder hooks. All routes staff-gated at
// the door; the two dealer-curation writes (quote policy, role items) apply the Tier-1 GLOBAL
// admin gate IN THE SERVICE (the acceptcontrols precedent).

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// registerBuilderRoutes wires the DIB-506 work-area endpoints + mounts the shared settings kit.
func (h *StaffHandler) registerBuilderRoutes(mux *http.ServeMux, g *guards.Guards) {
	// The literal "policy" segment binds ahead of the {id} wildcard by ServeMux precedence.
	mux.Handle("GET /api/v1/quotes/policy", g.RequireStaff(h.staff(h.readQuotePolicy)))
	mux.Handle("PUT /api/v1/quotes/policy", g.RequireStaff(h.staff(h.putQuotePolicy)))
	mux.Handle("GET /api/v1/quotes/{id}/terms", g.RequireStaff(h.staff(h.readTerms)))
	mux.Handle("PUT /api/v1/quotes/{id}/terms", g.RequireStaff(h.staff(h.putTerms)))
	mux.Handle("GET /api/v1/quotes/{id}/margin-glance", g.RequireStaff(h.staff(h.marginGlance)))

	// The DIB-507 kit: mounted here because the pilot consumer is the module that proves the kit
	// (spec §7). A scaffold store (no pool) simply does not mount, surfaces degrade to defaults.
	if ksvc := h.svc.settingsKitService(); ksvc != nil {
		settings.NewHandler(ksvc).WithUserLinker(h.userLink).RegisterRoutes(mux, g)
	}
}

// writeBuilderErr maps builder errors: the kit's StatusError speaks its own status (the
// 422-in-words posture), BadTermsRefError is a 422, everything else falls through writeErr.
func (h *StaffHandler) writeBuilderErr(w http.ResponseWriter, err error) {
	var se *settings.StatusError
	var badRef *BadTermsRefError
	switch {
	case errors.As(err, &se):
		httpx.Error(w, se.Status, se.Msg)
	case errors.As(err, &badRef):
		httpx.Error(w, http.StatusUnprocessableEntity, badRef.Error())
	default:
		h.writeErr(w, err)
	}
}

func (h *StaffHandler) readQuotePolicy(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	p, err := h.svc.QuotePolicyFor(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeBuilderErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *StaffHandler) putQuotePolicy(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p QuotePolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "quote: policy body must be JSON")
		return
	}
	out, err := h.svc.UpdateQuotePolicy(r.Context(), p)
	if err != nil {
		h.writeBuilderErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *StaffHandler) readTerms(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	t, err := h.svc.Terms(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeBuilderErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *StaffHandler) putTerms(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in TermsInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "quote: terms body must be JSON")
		return
	}
	t, err := h.svc.UpdateTerms(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeBuilderErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *StaffHandler) marginGlance(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	m, err := h.svc.MarginGlance(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeBuilderErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

// setLineDiscount serves the disc_pct arm of the line PATCH (called from editLineQty when the
// body carries disc_pct). Split out so the shipped qty path stays byte-identical when absent.
func (h *StaffHandler) setLineDiscount(w http.ResponseWriter, r *http.Request, quoteID string, lineNo int, discPct int) bool {
	res, err := h.svc.SetLineDiscount(r.Context(), quoteID, lineNo, discPct)
	if err != nil {
		h.writeBuilderErr(w, err)
		return false
	}
	httpx.WriteJSON(w, http.StatusOK, res)
	return true
}

// parseDiscPct parses an optional disc_pct raw JSON number/string ("5", 5) to a bounded int.
func parseDiscPct(raw json.RawMessage) (int, error) {
	s := string(raw)
	if len(s) >= 2 && s[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return 0, err
		}
		s = str
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, settings.Errf(http.StatusUnprocessableEntity, "quote: disc_pct must be a whole percent from 0 to 100")
	}
	return n, nil
}
