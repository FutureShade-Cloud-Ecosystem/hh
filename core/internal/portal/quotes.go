package portal

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// registerQuoteRoutes wires quote read + the contractor state machine. Quotes carry
// prices (financial), so view/accept/reject sit behind SubmitRFQ (Owner/Buyer, not
// field_crew); convert additionally commits an order, so it requires CreateOrders.
func (h *Handler) registerQuoteRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/tenant/quotes", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.listQuotes)))
	r.Handle("GET /api/v1/tenant/quotes/{id}", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.getQuote)))
	r.Handle("POST /api/v1/tenant/quotes/{id}/accept", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.acceptQuote)))
	r.Handle("POST /api/v1/tenant/quotes/{id}/reject", g.RequireCapability(identity.CapSubmitRFQ, h.tenant(h.rejectQuote)))
	r.Handle("POST /api/v1/tenant/quotes/{id}/convert", g.RequireCapability(identity.CapCreateOrders, h.tenant(h.convertQuote)))
}

// listQuotes returns a paginated, optionally status-filtered page of the tenant's quotes.
func (h *Handler) listQuotes(w http.ResponseWriter, r *http.Request) {
	if h.quoteSvc == nil {
		httpx.NotImplemented(w, "quote list not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	// Cursor pagination (HH-3.2 fold), the tenant quote list. Stable-ordering
	// basis: created_at DESC, quote_no (unique), the same basis the staff list
	// carries. Fetch limit+1, drop the extra row, mint next_cursor from the
	// last kept row.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		h.writeErr(w, r, httpx.BadRequestError{Msg: "cursor is malformed"})
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validQuoteStatus(status) {
		h.writeErr(w, r, httpx.BadRequestf("unknown quote status %q", status))
		return
	}
	created, quoteNo, ok := parseQuoteListCursor(p.Cursor)
	if !ok {
		h.writeErr(w, r, httpx.BadRequestError{Msg: "cursor is malformed"})
		return
	}
	f := quote.Filter{Status: status, Limit: p.Limit + 1, AfterCursor: p.Cursor != "",
		CursorCreated: created, CursorQuoteNo: quoteNo}
	items, err := h.quoteSvc.ListQuotes(ctx, caller.CustomerID, f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		next = items[len(items)-1].CreatedAt.UTC().Format(time.RFC3339Nano) + "/" + items[len(items)-1].QuoteNo
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// parseQuoteListCursor decodes a minted tenant quote-list key (created_at /
// quote_no); ok is false for an empty cursor (first page) or one that does
// not carry the pair shape.
func parseQuoteListCursor(key string) (created, quoteNo string, ok bool) {
	if key == "" {
		return "", "", true
	}
	i := strings.LastIndex(key, "/")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// getQuote returns one quote with priced lines, scoped to the tenant.
func (h *Handler) getQuote(w http.ResponseWriter, r *http.Request) {
	if h.quoteSvc == nil {
		httpx.NotImplemented(w, "quote detail not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	d, err := h.quoteSvc.GetQuote(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// acceptQuote marks a SENT quote ACCEPTED.
func (h *Handler) acceptQuote(w http.ResponseWriter, r *http.Request) {
	if h.quoteSvc == nil {
		httpx.NotImplemented(w, "quote accept not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	q, err := h.quoteSvc.Accept(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, q)
}

// rejectQuote marks a SENT/ACCEPTED quote LOST.
func (h *Handler) rejectQuote(w http.ResponseWriter, r *http.Request) {
	if h.quoteSvc == nil {
		httpx.NotImplemented(w, "quote reject not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	q, err := h.quoteSvc.Reject(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, q)
}

// convertQuote converts an ACCEPTED quote into an order (gated on credit/hold upstream).
//
// DIB-483 (D-090): the body gains ONE optional field, `fulfillment`, the convert extension spec'd in
// the HH Pro connection spec. PICKUP or WILL_CALL; anything else is a 400 that names the two.
//
// It is optional in the strongest sense: a request with NO BODY AT ALL, which is what every shipped
// caller sends and what the contractor app sends today, converts exactly as it did before this change
// , the field arrives as "", the service treats that as unspecified, and the INSERT never names the
// column (quote/convert.go). An integrator who has not read the extension is not broken by it.
func (h *Handler) convertQuote(w http.ResponseWriter, r *http.Request) {
	if h.quoteSvc == nil {
		httpx.NotImplemented(w, "quote convert not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	var body struct {
		Fulfillment string `json:"fulfillment"`
	}
	if r.ContentLength > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			h.writeErr(w, r, httpx.BadRequestf("malformed request body"))
			return
		}
	}
	res, err := h.quoteSvc.Convert(ctx, caller.CustomerID, r.PathValue("id"),
		quote.ConvertOptions{Fulfillment: body.Fulfillment})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// validQuoteStatus mirrors the quotes CHECK set (migration 0008) so the BFF can reject a
// bad filter as a 400 before delegating. The quote module is the authority.
func validQuoteStatus(status string) bool {
	switch status {
	case "DRAFT", "SENT", "ACCEPTED", "CONVERTED", "EXPIRED", "LOST":
		return true
	default:
		return false
	}
}
