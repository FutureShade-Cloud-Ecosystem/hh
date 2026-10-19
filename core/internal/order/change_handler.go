package order

// change_handler.go, HTTP for SCOPE:F10 (change orders), DIB-78 (multi-ship) and the W5 seam-3
// reservation read. All three hang off the SAME *Handler the transition/edit/history routes use, so
// the order module keeps ONE HTTP surface registered from one place in the composition root.
//
//	GET    /api/v1/orders/{id}/reservations                       active in-branch staff
//	POST   /api/v1/orders/{id}/reservations/resync                manager
//
//	GET    /api/v1/orders/{id}/changes                            active in-branch staff
//	POST   /api/v1/orders/{id}/changes                            active in-branch staff
//	GET    /api/v1/order-changes                                  active in-branch staff  (change_queue.go)
//	GET    /api/v1/order-changes/{changeId}                       active in-branch staff
//	POST   /api/v1/order-changes/{changeId}/yard-ack              active in-branch staff  ← the yard
//	POST   /api/v1/order-changes/{changeId}/finance-approve       manager + named approver
//	POST   /api/v1/order-changes/{changeId}/reject                active in-branch staff
//	POST   /api/v1/order-changes/{changeId}/cancel                active in-branch staff
//	POST   /api/v1/order-changes/{changeId}/apply                 manager
//	POST   /api/v1/order-changes/{changeId}/sign-apply            manager + named approver, Idempotency-Key
//
//	GET    /api/v1/orders/{id}/shipments                          active in-branch staff
//	PUT    /api/v1/orders/{id}/shipments                          active in-branch staff
//	POST   /api/v1/orders/{id}/shipments/invoice-policy           manager
//	POST   /api/v1/orders/{id}/shipments/invoice                  manager
//	POST   /api/v1/shipment-groups/{groupId}/status               active in-branch staff
//
// THE YARD ACK IS requireStaff, NOT requireManager, and that is the whole point of the round trip:
// the person who can answer "can we still do this with a half-loaded truck" is the person holding
// the forks. Route guards are the coarse first line only, the service re-checks tier, branch and
// the ladder on the resolved caller, which is the single source of authority truth.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterChangeRoutes wires the W5 order-depth surface.
func (h *Handler) RegisterChangeRoutes(r httpx.Registrar, g *guards.Guards) {
	// Reservations (W4 seam 3 landing).
	r.Handle("GET /api/v1/orders/{id}/reservations", g.RequireStaff(h.staff(h.orderReservations)))
	r.Handle("POST /api/v1/orders/{id}/reservations/resync", g.RequireManager(h.staff(h.resyncReservations)))

	// Change orders (SCOPE:F10).
	r.Handle("GET /api/v1/orders/{id}/changes", g.RequireStaff(h.staff(h.listChanges)))
	r.Handle("POST /api/v1/orders/{id}/changes", g.RequireStaff(h.staff(h.raiseChange)))
	r.Handle("GET /api/v1/order-changes", g.RequireStaff(h.staff(h.listChangeQueue)))
	r.Handle("GET /api/v1/order-changes/{changeId}", g.RequireStaff(h.staff(h.getChange)))
	r.Handle("POST /api/v1/order-changes/{changeId}/yard-ack", g.RequireStaff(h.staff(h.yardAck)))
	r.Handle("POST /api/v1/order-changes/{changeId}/finance-approve", g.RequireManager(h.staff(h.financeApprove)))
	r.Handle("POST /api/v1/order-changes/{changeId}/reject", g.RequireStaff(h.staff(h.rejectChange)))
	r.Handle("POST /api/v1/order-changes/{changeId}/cancel", g.RequireStaff(h.staff(h.cancelChange)))
	r.Handle("POST /api/v1/order-changes/{changeId}/apply", g.RequireManager(h.staff(h.applyChange)))
	r.Handle("POST /api/v1/order-changes/{changeId}/sign-apply", g.RequireManager(h.staff(h.signApplyChange)))

	// Multi-ship (DIB-78).
	r.Handle("GET /api/v1/orders/{id}/shipments", g.RequireStaff(h.staff(h.getShipmentPlan)))
	r.Handle("PUT /api/v1/orders/{id}/shipments", g.RequireStaff(h.staff(h.planShipments)))
	r.Handle("POST /api/v1/orders/{id}/shipments/invoice-policy", g.RequireManager(h.staff(h.setInvoicePolicy)))
	r.Handle("POST /api/v1/orders/{id}/shipments/invoice", g.RequireManager(h.staff(h.invoiceShipments)))
	r.Handle("POST /api/v1/shipment-groups/{groupId}/status", g.RequireStaff(h.staff(h.transitionGroup)))
}

// - reservations -

func (h *Handler) orderReservations(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	// The branch/authority check rides on the same context resolution every other order surface
	// uses, so a cross-branch id answers 404 here exactly as it does everywhere else, and it
	// resolves an order_no to the real id for the holds read below.
	view, err := h.svc.OrderRead(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	holds, err := h.svc.OrderReservations(r.Context(), view.ID)
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	if holds == nil {
		holds = []OrderHold{}
	}
	var heldBase float64
	for _, hd := range holds {
		heldBase += hd.QtyBase
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order_id":     view.ID,
		"status":       view.Status,
		"seam_wired":   h.svc.StockSeamWired(),
		"reservations": holds,
		"held_base":    heldBase,
	})
}

func (h *Handler) resyncReservations(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	view, err := h.svc.OrderRead(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	n, err := h.svc.ResyncReservations(r.Context(), view.ID, view.BranchID, "")
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order_id": view.ID, "reservations_applied": n})
}

// - change orders -

func (h *Handler) listChanges(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.ListChanges(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	if out == nil {
		out = []ChangeOrder{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"changes": out})
}

func (h *Handler) raiseChange(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body ChangeRequestInput
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeChangeErr(w, err)
		return
	}
	out, err := h.svc.RaiseChange(r.Context(), r.PathValue("id"), body)
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) getChange(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.GetChange(r.Context(), r.PathValue("changeId"))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// decodeNote reads the optional {"note":"…"} body every decision endpoint accepts. A body is
// OPTIONAL here (unlike an edit): a yard ACK with nothing to add is a legitimate ACK, and forcing a
// note would produce a column full of ".".
func decodeNote(w http.ResponseWriter, r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body)
	return body.Note
}

func (h *Handler) yardAck(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.YardAck(r.Context(), r.PathValue("changeId"), decodeNote(w, r))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) financeApprove(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.FinanceApprove(r.Context(), r.PathValue("changeId"), decodeNote(w, r))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) rejectChange(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.RejectChange(r.Context(), r.PathValue("changeId"), decodeNote(w, r))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) cancelChange(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.CancelChange(r.Context(), r.PathValue("changeId"), decodeNote(w, r))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) applyChange(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.ApplyChange(r.Context(), r.PathValue("changeId"))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// signApplyChange is the one tap: sign when a signature is owed, then apply, once. The Idempotency-Key
// header is required; a replay of the same key answers 200 with the first answer.
func (h *Handler) signApplyChange(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.SignApplyChange(r.Context(), r.PathValue("changeId"),
		r.Header.Get("Idempotency-Key"), decodeNote(w, r))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// - multi-ship -

func (h *Handler) getShipmentPlan(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.GetShipmentPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) planShipments(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Groups []ShipmentGroupInput `json:"groups"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeChangeErr(w, err)
		return
	}
	out, err := h.svc.PlanShipment(r.Context(), r.PathValue("id"), body.Groups)
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) setInvoicePolicy(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Policy string `json:"policy"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeChangeErr(w, err)
		return
	}
	out, err := h.svc.SetInvoicePolicy(r.Context(), r.PathValue("id"), body.Policy)
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) invoiceShipments(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	readiness, invID, invNo, err := h.svc.InvoiceShipments(r.Context(), r.PathValue("id"))
	if errors.Is(err, ErrInvoicerUnwired) {
		// 503 WITH the readiness payload, the surface shows exactly what WOULD be invoiced rather
		// than an empty error toast. Same posture as inventory's StubPOCreator.
		httpx.ErrorWith(w, http.StatusServiceUnavailable,
			"multi-ship invoicing is not enabled, no invoice writer is bound",
			map[string]any{"readiness": readiness})
		return
	}
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"invoice_id": invID, "invoice_no": invNo, "readiness": readiness,
	})
}

func (h *Handler) transitionGroup(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Status string `json:"status"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeChangeErr(w, err)
		return
	}
	out, err := h.svc.TransitionGroup(r.Context(), r.PathValue("groupId"), body.Status)
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// - errors -

// writeChangeErr maps the W5 errors, then falls back to writeEditErr (which itself falls back to the
// shared writeErr), so authority and not-found answers stay byte-identical to every other order
// endpoint for the same condition. Each 409 carries its OWN message: a caller must be able to tell
// "the yard has not acked" from "finance has not signed" from "already applied".
func (h *Handler) writeChangeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrChangeNotFound), errors.Is(err, ErrGroupNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrChangeNotPostPick):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrChangeOpenExists):
		httpx.Error(w, http.StatusConflict,
			"this order already has an open change request, decide it before raising another")
	case errors.Is(err, ErrChangeYardAckRequired):
		httpx.Error(w, http.StatusConflict, "the yard has not acknowledged this change yet")
	case errors.Is(err, ErrChangeFinanceRequired):
		httpx.Error(w, http.StatusConflict,
			"this change is over the finance threshold and has not been approved")
	case errors.Is(err, ErrChangeNotApproved), errors.Is(err, ErrChangeTerminal), errors.Is(err, ErrChangeKeyReused),
		errors.Is(err, ErrGroupIllegalTransition), errors.Is(err, ErrShipPlanIncomplete),
		errors.Is(err, ErrInvoicePolicyFrozen):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrChangeNoLines):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrStockSeamUnwired), errors.Is(err, ErrChangeNotConfigured),
		errors.Is(err, ErrShipNotConfigured), errors.Is(err, ErrInvoicerUnwired):
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
	default:
		h.writeEditErr(w, err)
	}
}
