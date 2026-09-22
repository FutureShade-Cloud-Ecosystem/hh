package portal

// reorder.go, CP-04: the contractor "Reorder" surface. GET .../reorder-preview grades
// every line of a past order (today's price + availability); POST .../reorder creates a
// DRAFT order from the selected lines, repriced at today's rates. The BFF resolves the
// caller's account (customer type drives the pricing tier) and delegates to the order
// module, which owns the pricing/availability composition and the tenant-scoped write.
// IDOR (US-RO-04): both routes are keyed by the JWT's customer_id, a foreign order id
// answers 404, exactly like the orders.go reads.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxReorderLines bounds the selection payload (an order never has anywhere near this
// many lines; this only guards the decoder).
const maxReorderLines = 500

// ReorderRequest is the POST body: the SOURCE order line numbers to reorder plus the
// client-generated idempotency key (a retried POST returns the same draft).
type ReorderRequest struct {
	Lines          []int  `json:"lines"`
	IdempotencyKey string `json:"idempotency_key"`
}

// registerReorderRoutes wires the reorder pair. Preview is a read → ViewOrdersDeliveries
// (same gate as the order detail it decorates); the POST creates an order → CreateOrders,
// so field_crew can see the preview but never lands a draft.
func (h *Handler) registerReorderRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/tenant/orders/{id}/reorder-preview", g.RequireCapability(identity.CapViewOrdersDeliveries, h.tenant(h.reorderPreview)))
	r.Handle("POST /api/v1/tenant/orders/{id}/reorder", g.RequireCapability(identity.CapCreateOrders, h.tenant(h.createReorder)))
}

// reorderPreview returns every source line priced at today's rates with availability
// flags, the line picker's working set.
func (h *Handler) reorderPreview(w http.ResponseWriter, r *http.Request) {
	if h.orderSvc == nil || h.customerSvc == nil {
		httpx.NotImplemented(w, "reorder not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	rc, err := h.reorderContext(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	prev, err := h.orderSvc.PreviewReorder(ctx, caller.CustomerID, r.PathValue("id"), rc)
	if err != nil {
		h.writeReorderErr(w, r, err)
		return
	}
	if prev.Lines == nil {
		prev.Lines = []order.ReorderLine{}
	}
	httpx.WriteJSON(w, http.StatusOK, prev)
}

// createReorder creates the DRAFT order from the selected lines. 201 with the new
// order id/no; 409 with per-line reasons when a selected line is no longer orderable.
func (h *Handler) createReorder(w http.ResponseWriter, r *http.Request) {
	if h.orderSvc == nil || h.customerSvc == nil {
		httpx.NotImplemented(w, "reorder not implemented")
		return
	}
	var req ReorderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(req.Lines) > maxReorderLines {
		h.writeErr(w, r, httpx.BadRequestf("too many lines selected"))
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	rc, err := h.reorderContext(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	res, err := h.orderSvc.CreateReorder(ctx, caller.CustomerID, r.PathValue("id"), rc, req.Lines, req.IdempotencyKey)
	if err != nil {
		h.writeReorderErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

// reorderContext resolves the caller's account facts the pricing tier needs (customer
// type, exactly what the POS ring reads before pricing). A missing account propagates
// (customer.ErrNotFound → 404 via writeErr).
func (h *Handler) reorderContext(ctx context.Context, customerID string) (order.ReorderContext, error) {
	acct, err := h.customerSvc.ResolveAccount(ctx, customerID)
	if err != nil {
		return order.ReorderContext{}, err
	}
	return order.ReorderContext{CustomerType: acct.CustomerType}, nil
}

// writeReorderErr maps the reorder-specific errors before falling back to the shared
// writeErr (which handler.go owns, kept untouched): the unavailable-lines rejection is
// a 409 carrying the per-line reasons; unconfigured seams answer 501 like every other
// scaffold-stage BFF feature.
func (h *Handler) writeReorderErr(w http.ResponseWriter, r *http.Request, err error) {
	var ua *order.ReorderUnavailableError
	switch {
	case errors.As(err, &ua):
		httpx.ErrorWith(w, http.StatusConflict,
			"some selected lines cannot be reordered",
			map[string]any{"lines": ua.Lines})
	case errors.Is(err, order.ErrReorderNotConfigured):
		httpx.NotImplemented(w, "reorder not implemented")
	default:
		h.writeErr(w, r, err)
	}
}
