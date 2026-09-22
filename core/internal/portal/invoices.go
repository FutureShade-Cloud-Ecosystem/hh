package portal

import (
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// registerInvoiceRoutes wires invoice list + detail. Both require ViewBilling.
func (h *Handler) registerInvoiceRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/invoices", g.RequireCapability(identity.CapViewBilling, h.tenant(h.listInvoices)))
	r.Handle("GET /api/portal/v1/invoices/{id}", g.RequireCapability(identity.CapViewBilling, h.tenant(h.getInvoice)))
}

// listInvoices returns a paginated, optionally status-filtered page of the tenant's
// invoices. A client-supplied status outside the invoices CHECK set is a 400.
func (h *Handler) listInvoices(w http.ResponseWriter, r *http.Request) {
	if h.billingSvc == nil {
		httpx.NotImplemented(w, "invoice list not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	// Cursor pagination (HH-3.2 fold), the tenant invoice list. Stable-ordering
	// basis: issue_date DESC, invoice_no (unique). Fetch limit+1, drop the extra
	// row, mint next_cursor from the last kept row.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		h.writeErr(w, r, httpx.BadRequestError{Msg: "cursor is malformed"})
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validInvoiceStatus(status) {
		h.writeErr(w, r, httpx.BadRequestf("unknown invoice status %q", status))
		return
	}
	issue, invNo, ok := parseInvoiceCursor(p.Cursor)
	if !ok {
		h.writeErr(w, r, httpx.BadRequestError{Msg: "cursor is malformed"})
		return
	}
	f := billing.Filter{Status: status, Limit: p.Limit + 1, AfterCursor: p.Cursor != "",
		CursorIssueDate: issue, CursorInvoiceNo: invNo}
	items, err := h.billingSvc.ListInvoices(ctx, caller.CustomerID, f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		next = items[len(items)-1].IssueDate.Format("2006-01-02") + "/" + items[len(items)-1].InvoiceNo
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// parseInvoiceCursor decodes a minted tenant invoice-list key (issue_date /
// invoice_no); ok is false for an empty cursor (first page) or one that does
// not carry the pair shape.
func parseInvoiceCursor(key string) (issue, invNo string, ok bool) {
	if key == "" {
		return "", "", true
	}
	i := strings.LastIndex(key, "/")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// getInvoice returns one invoice with lines, scoped to the tenant (cross-tenant -> 404).
func (h *Handler) getInvoice(w http.ResponseWriter, r *http.Request) {
	if h.billingSvc == nil {
		httpx.NotImplemented(w, "invoice detail not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	inv, err := h.billingSvc.GetInvoice(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, inv)
}

// validInvoiceStatus mirrors the invoices CHECK set (migration 0009) so the BFF can
// reject a bad filter as a 400 before delegating. The billing module is the authority;
// this is a thin client-input guard.
func validInvoiceStatus(status string) bool {
	switch status {
	case "DRAFT", "FINALIZED", "PARTIALLY_PAID", "PAID", "VOID":
		return true
	default:
		return false
	}
}
