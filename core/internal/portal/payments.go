package portal

import (
	"encoding/json"
	"net/http"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// portalAllocation is the TENANT-SAFE projection of one payment→invoice application. It deliberately
// omits the staff attribution (allocated_by / allocated_by_name, internal user UUIDs + display names
// are staff data; the Q-044 attribution surface is the staff-gated /api/v1/ar reads, never the portal).
type portalAllocation struct {
	InvoiceID   string    `json:"invoice_id"`
	AmountCents int64     `json:"amount_cents"`
	CreatedAt   time.Time `json:"created_at"`
}

// portalPayment is the tenant-safe payment shape the portal serializes (payments.Payment minus the
// staff-attributed allocation fields).
type portalPayment struct {
	ID          string             `json:"id"`
	PaymentNo   string             `json:"payment_no"`
	CustomerID  string             `json:"customer_id"`
	Method      string             `json:"method"`
	AmountCents int64              `json:"amount_cents"`
	Status      string             `json:"status"`
	GatewayRef  string             `json:"gateway_ref"`
	ReceivedAt  time.Time          `json:"received_at"`
	Allocations []portalAllocation `json:"allocations"`
}

// toPortalPayment projects one payments.Payment to its tenant-safe shape.
func toPortalPayment(p payments.Payment) portalPayment {
	out := portalPayment{
		ID: p.ID, PaymentNo: p.PaymentNo, CustomerID: p.CustomerID, Method: p.Method,
		AmountCents: p.AmountCents, Status: p.Status, GatewayRef: p.GatewayRef, ReceivedAt: p.ReceivedAt,
		Allocations: make([]portalAllocation, 0, len(p.Allocations)),
	}
	for _, a := range p.Allocations {
		out.Allocations = append(out.Allocations, portalAllocation{
			InvoiceID: a.InvoiceID, AmountCents: a.AmountCents, CreatedAt: a.CreatedAt,
		})
	}
	return out
}

// registerPaymentRoutes wires payment history (ViewBilling, it is AR data) and the
// multi-invoice pay action (PayInvoices). The summed amount and Run customer are derived
// server-side from the session, never trusted from the client (§4.2).
func (h *Handler) registerPaymentRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/payments", g.RequireCapability(identity.CapViewBilling, h.tenant(h.listPayments)))
	r.Handle("POST /api/portal/v1/payments", g.RequireCapability(identity.CapPayInvoices, h.tenant(h.createPayment)))
}

// listPayments returns the tenant's payment history with allocations, projected to the portal-safe
// shape (staff attribution never leaves the building).
func (h *Handler) listPayments(w http.ResponseWriter, r *http.Request) {
	if h.paymentSvc == nil {
		httpx.NotImplemented(w, "payment history not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	ps, err := h.paymentSvc.ListPayments(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	out := make([]portalPayment, 0, len(ps))
	for _, p := range ps {
		out = append(out, toPortalPayment(p))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// createPayment runs the multi-invoice pay flow: ONE charge for the summed balance of the
// selected invoices, split atomically across allocations. The summed amount, the Run
// customer, and the idempotency key are all derived server-side from the session; the
// client value is advisory only.
func (h *Handler) createPayment(w http.ResponseWriter, r *http.Request) {
	if h.paymentSvc == nil {
		httpx.NotImplemented(w, "invoice payment not implemented")
		return
	}
	var req payments.PayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	p, err := h.paymentSvc.Pay(ctx, caller.CustomerID, req)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// Same tenant-safe projection as the list (a portal payment carries no staff attribution today,
	// but the shape guarantees it structurally).
	httpx.WriteJSON(w, http.StatusCreated, toPortalPayment(p))
}
