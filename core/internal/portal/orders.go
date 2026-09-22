package portal

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxDeliveryInstructionsLen bounds the free-text delivery note the BFF accepts, so an
// over-long note is rejected as a 400 before delegating. Mirrors the order module's own
// cap (it is the authority); this is a thin client-input guard.
const maxDeliveryInstructionsLen = 2000

// registerOrderRoutes wires order list/detail (ViewOrdersDeliveries) plus the delivery-
// instructions write (EditDeliveryInstructions). field_crew holds both caps, so the crew
// can read orders and annotate a delivery, but never reaches financials.
func (h *Handler) registerOrderRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/orders", g.RequireCapability(identity.CapViewOrdersDeliveries, h.tenant(h.listOrders)))
	r.Handle("GET /api/portal/v1/orders/{id}", g.RequireCapability(identity.CapViewOrdersDeliveries, h.tenant(h.getOrder)))
	r.Handle("PUT /api/portal/v1/orders/{id}/delivery-instructions", g.RequireCapability(identity.CapEditDeliveryInstructions, h.tenant(h.setDeliveryInstructions)))
}

// listOrders returns a paginated, optionally status-filtered page of the tenant's orders.
func (h *Handler) listOrders(w http.ResponseWriter, r *http.Request) {
	if h.orderSvc == nil {
		httpx.NotImplemented(w, "order list not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	// Cursor pagination (HH-3.2 fold), the tenant order list. Stable-ordering
	// basis: created_at DESC, order_no (unique). Fetch limit+1, drop the extra
	// row, mint next_cursor from the last kept row.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		h.writeErr(w, r, httpx.BadRequestError{Msg: "cursor is malformed"})
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validOrderStatus(status) {
		h.writeErr(w, r, httpx.BadRequestf("unknown order status %q", status))
		return
	}
	created, ordNo, ok := parseOrderListCursor(p.Cursor)
	if !ok {
		h.writeErr(w, r, httpx.BadRequestError{Msg: "cursor is malformed"})
		return
	}
	f := order.Filter{Status: status, Limit: p.Limit + 1, AfterCursor: p.Cursor != "",
		CursorCreated: created, CursorOrderNo: ordNo}
	items, err := h.orderSvc.ListOrders(ctx, caller.CustomerID, f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		next = items[len(items)-1].CreatedAt.UTC().Format(time.RFC3339Nano) + "/" + items[len(items)-1].OrderNo
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// parseOrderListCursor decodes a minted tenant order-list key (created_at /
// order_no); ok is false for an empty cursor (first page) or one that does
// not carry the pair shape.
func parseOrderListCursor(key string) (created, ordNo string, ok bool) {
	if key == "" {
		return "", "", true
	}
	i := strings.LastIndex(key, "/")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// getOrder returns one order with lines + delivery instructions, scoped to the tenant.
func (h *Handler) getOrder(w http.ResponseWriter, r *http.Request) {
	if h.orderSvc == nil {
		httpx.NotImplemented(w, "order detail not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	d, err := h.orderSvc.GetOrder(ctx, caller.CustomerID, r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// setDeliveryInstructions attaches/updates the contractor's note for a scheduled delivery.
func (h *Handler) setDeliveryInstructions(w http.ResponseWriter, r *http.Request) {
	if h.orderSvc == nil {
		httpx.NotImplemented(w, "delivery instructions not implemented")
		return
	}
	var req struct {
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(req.Instructions) > maxDeliveryInstructionsLen {
		h.writeErr(w, r, httpx.BadRequestf("delivery instructions exceed %d characters", maxDeliveryInstructionsLen))
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	if err := h.orderSvc.SetDeliveryInstructions(ctx, caller.CustomerID, r.PathValue("id"), req.Instructions); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// validOrderStatus mirrors the orders CHECK set (migration 0008) so the BFF can reject a
// bad filter as a 400 before delegating. The order module is the authority.
func validOrderStatus(status string) bool {
	switch status {
	case "DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED",
		"FULFILLED", "INVOICED", "CLOSED", "CANCELLED":
		return true
	default:
		return false
	}
}
