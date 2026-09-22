package portal

import (
	"encoding/json"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// registerPaymentMethodRoutes wires stored-method CRUD, all gated on ManagePaymentMethods.
// Card/ACH data is captured only in Run hosted fields, these routes only ever see an
// opaque nonce/token, never a PAN (PCI SAQ A, §4.2).
func (h *Handler) registerPaymentMethodRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/payment-methods", g.RequireCapability(identity.CapManagePaymentMethods, h.tenant(h.listPaymentMethods)))
	r.Handle("POST /api/portal/v1/payment-methods", g.RequireCapability(identity.CapManagePaymentMethods, h.tenant(h.addPaymentMethod)))
	r.Handle("DELETE /api/portal/v1/payment-methods/{id}", g.RequireCapability(identity.CapManagePaymentMethods, h.tenant(h.removePaymentMethod)))
	r.Handle("PUT /api/portal/v1/payment-methods/{id}/default", g.RequireCapability(identity.CapManagePaymentMethods, h.tenant(h.setDefaultPaymentMethod)))
}

// listPaymentMethods returns the tenant's active stored methods (display metadata only).
func (h *Handler) listPaymentMethods(w http.ResponseWriter, r *http.Request) {
	if h.paymentSvc == nil {
		httpx.NotImplemented(w, "payment method list not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	ms, err := h.paymentSvc.ListMethods(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if ms == nil {
		ms = []payments.PaymentMethod{}
	}
	httpx.WriteJSON(w, http.StatusOK, ms)
}

// addPaymentMethod exchanges a one-time hosted-field nonce for a vaulted method. The nonce
// is never logged.
func (h *Handler) addPaymentMethod(w http.ResponseWriter, r *http.Request) {
	if h.paymentSvc == nil {
		httpx.NotImplemented(w, "payment method add not implemented")
		return
	}
	var req payments.AddMethodRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	m, err := h.paymentSvc.AddMethod(ctx, caller.CustomerID, req)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, m)
}

// setDefaultPaymentMethod promotes one stored method to the tenant's default (IDOR-checked:
// a cross-tenant or missing id is a 404, never a 403).
func (h *Handler) setDefaultPaymentMethod(w http.ResponseWriter, r *http.Request) {
	if h.paymentSvc == nil {
		httpx.NotImplemented(w, "payment method set-default not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	if err := h.paymentSvc.SetDefaultMethod(ctx, caller.CustomerID, r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// removePaymentMethod detaches at the processor then soft-deletes locally (IDOR-checked).
func (h *Handler) removePaymentMethod(w http.ResponseWriter, r *http.Request) {
	if h.paymentSvc == nil {
		httpx.NotImplemented(w, "payment method remove not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)
	if err := h.paymentSvc.RemoveMethod(ctx, caller.CustomerID, r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
