package quote

// reserve_handler.go, HTTP for the quote-side W5 seam-3 surface. Hangs off the SAME *StaffHandler
// the quote builder uses, so this module keeps ONE HTTP surface registered from one place.
//
//	GET  /api/v1/quotes/{id}/availability                 per-line branch availability chips
//	GET  /api/v1/quotes/{id}/holds                        this quote's ACTIVE soft holds
//	GET  /api/v1/quotes/{id}/hold-expiry                  which of this quote's holds have LAPSED
//	POST /api/v1/quotes/{id}/holds/resync                 re-derive the holds from the lines
//	POST /api/v1/quotes/{id}/holds/release                close the holds (lost / withdrawn)
//	GET  /api/v1/quote-availability/{productId}/branches  the cross-branch peek
//
// Every route is requireStaff, matching the rest of this surface: a quote moves no money, and the
// availability read is exactly the information a counter clerk needs BEFORE promising anything.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterAvailabilityRoutes wires the at-quote availability + hold endpoints.
func (h *StaffHandler) RegisterAvailabilityRoutes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("GET /api/v1/quotes/{id}/availability", g.RequireStaff(h.staff(h.quoteAvailability)))
	mux.Handle("GET /api/v1/quotes/{id}/holds", g.RequireStaff(h.staff(h.quoteHolds)))
	mux.Handle("GET /api/v1/quotes/{id}/hold-expiry", g.RequireStaff(h.staff(h.quoteHoldExpiry)))
	mux.Handle("POST /api/v1/quotes/{id}/holds/resync", g.RequireStaff(h.staff(h.resyncHolds)))
	mux.Handle("POST /api/v1/quotes/{id}/holds/release", g.RequireStaff(h.staff(h.releaseHolds)))
	mux.Handle("GET /api/v1/quote-availability/{productId}/branches", g.RequireStaff(h.staff(h.crossBranchPeek)))
}

func (h *StaffHandler) quoteAvailability(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lines, wired, err := h.svc.QuoteAvailability(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// seam_wired rides in the envelope so the client can tell "not reported" from "none in stock".
	// A grey chip and a red chip are different answers and must not collapse into one.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"lines": lines, "seam_wired": wired})
}

func (h *StaffHandler) quoteHolds(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	holds, err := h.svc.QuoteHolds(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if holds == nil {
		holds = []QuoteHold{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"holds": holds, "seam_wired": h.svc.StockSeamWired(),
	})
}

// quoteHoldExpiry, DIB-428. Which of this quote's soft holds have lapsed, in the shape the banner
// renders. seam_wired rides in the envelope for the same reason it does on /availability: a screen
// that shows nothing because nobody asked and a screen that shows nothing because nothing expired
// are different answers, and only one of them is worth a deployment note.
func (h *StaffHandler) quoteHoldExpiry(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	st, wired, err := h.svc.SoftHoldExpiry(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"expiry": st, "seam_wired": wired})
}

func (h *StaffHandler) resyncHolds(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	n, err := h.svc.ResyncQuoteHolds(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"holds_applied": n})
}

func (h *StaffHandler) releaseHolds(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	// The only terminal status this endpoint offers is RELEASED. CONSUMED is written by the
	// conversion path and nowhere else, a hold that "was taken up by an order" must be provable by
	// pointing at the order, and an HTTP caller cannot provide that proof.
	n, err := h.svc.ReleaseQuoteHolds(r.Context(), r.PathValue("id"), ResReleased)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"holds_released": n})
}

func (h *StaffHandler) crossBranchPeek(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	rows, err := h.svc.CrossBranchPeek(r.Context(), r.PathValue("productId"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if rows == nil {
		rows = []BranchStock{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"branches": rows, "seam_wired": h.svc.AvailabilitySeamWired(),
	})
}
