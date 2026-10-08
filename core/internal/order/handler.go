// handler.go, DIB-191 order-fulfilment lifecycle: the MANUAL status-transition endpoints (PICKING,
// READY, CANCELLED). They complement the event-driven FULFILLED/INVOICED drivers (consumer.go) and the
// DIB-159 status-history read (history.go). Every transition funnels through the Service choke-point,
// which enforces the forward-only legality matrix atomically and writes the attributed timeline row.
//
//	POST /api/v1/orders/{id}/picking → CONFIRMED → PICKING   (active in-branch staff)
//	POST /api/v1/orders/{id}/ready   → PICKING   → READY     (active in-branch staff)
//	POST /api/v1/orders/{id}/cancel  → {open}    → CANCELLED (manager Tier ≤ 2; reason required)
//
// These methods hang off the SAME *Handler the history read uses (history.go), one order HTTP surface,
// registered in the composition root next to the history route.
package order

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxBodyBytes caps a transition request body (R3), following the house convention of a package-level
// bound fed to http.MaxBytesReader. Comfortably above a maxReasonLen reason plus its JSON envelope, so
// a legitimate request is never truncated.
const maxBodyBytes = 8 << 10

// RegisterTransitionRoutes wires the manual transition endpoints. PICKING/READY are open to any active
// in-branch staff (requireStaff); the per-order branch check is the horizontal boundary the service
// enforces on the resolved caller. CANCELLED is manager-gated at the route (requireManager, Tier ≤ 2)
// AND re-checked in the service (defence-in-depth), the service (manualRules) is the single source of
// authority truth, so the route guard is a coarse first line, not the only one.
func (h *Handler) RegisterTransitionRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/orders/{id}/confirm", g.RequireStaff(h.staff(h.confirm)))
	r.Handle("POST /api/v1/orders/{id}/picking", g.RequireStaff(h.staff(h.markPicking)))
	r.Handle("POST /api/v1/orders/{id}/ready", g.RequireStaff(h.staff(h.markReady)))
	r.Handle("POST /api/v1/orders/{id}/cancel", g.RequireManager(h.staff(h.cancel)))
}

// confirm applies the ruling-D2 DRAFT→CONFIRMED move: the on-ramp that puts a DRAFT-born order
// (quote-convert, portal reorder) onto the CONFIRMED→PICKING→READY runway. Open to any active
// in-branch staff, exactly like picking/ready.
func (h *Handler) confirm(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	h.transition(w, r, ActionConfirm, "")
}

func (h *Handler) markPicking(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	h.transition(w, r, ActionPicking, "")
}

func (h *Handler) markReady(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	h.transition(w, r, ActionReady, "")
}

// cancel reads the required cancellation reason, and the manager's optional
// keep-the-deposit override, from the JSON body, then applies the manager-gated
// CANCELLED transition. An absent/undecodable body leaves reason "", the service
// then rejects a blank reason with a 400, so a malformed body and a missing
// reason converge on the same clear error. keep_deposit true is the row 1193
// override: the order's supplier deposit is kept by the dealer rather than
// credited back, and the reason already mandatory for every cancel is the
// override's audited reason.
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		Reason      string `json:"reason"`
		KeepDeposit bool   `json:"keep_deposit"`
	}
	if r.Body != nil {
		// R3: cap the body with http.MaxBytesReader, the house convention (finstmt/handler.go:226,
		// lien/handler.go:27, portal/reorder.go:73), so an oversized payload is refused at the
		// boundary rather than buffered. The service independently bounds the trimmed reason.
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body)
	}
	newStatus, err := h.svc.CancelOrderManual(r.Context(), r.PathValue("id"), body.Reason, body.KeepDeposit)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"order_id": r.PathValue("id"), "status": newStatus})
}

// transition applies one manual action and maps the service's typed errors to HTTP:
//
//	BadRequest            → 400 (unknown action, blank required reason, bad id)
//	ErrUnauthorized       → 401 (no authenticated caller)
//	ErrForbidden          → 403 (inactive, cross-branch, or non-manager for a manager-gated action)
//	ErrNotFound           → 404 (missing order, never leaks existence across a branch boundary)
//	ErrIllegalTransition  → 409 (forward-only violation, e.g. READY→PICKING)
//	ErrNoTransition       → 409 (order already in the requested state, no move happened)
func (h *Handler) transition(w http.ResponseWriter, r *http.Request, action ManualAction, reason string) {
	id := r.PathValue("id")
	newStatus, err := h.svc.TransitionManual(r.Context(), id, action, reason)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"order_id": id, "status": newStatus})
}

// orderRead serves GET /api/v1/orders/{id} (B2/R6): the order's current status plus the manual actions
// THIS caller may take right now, computed server-side from the transition matrix. A client renders its
// controls from legal_actions rather than duplicating the matrix, so the UI can never offer a move the
// server would refuse and a matrix change needs no client redeploy.
func (h *Handler) orderRead(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	view, err := h.svc.OrderRead(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

// writeErr maps the order service's typed errors onto HTTP, shared by the read and the transitions so
// both answer identically for the same condition.
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	var br httpx.BadRequestError
	switch {
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.Msg)
	case errors.Is(err, ErrUnauthorized):
		httpx.Error(w, http.StatusUnauthorized, "unauthenticated")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "order not found")
	case errors.Is(err, ErrIllegalTransition):
		httpx.Error(w, http.StatusConflict, "illegal status transition")
	case errors.Is(err, ErrHeldFromPicking):
		// Item 9.3: the pay before release hold, a business refusal with the
		// stable word held_from_picking and the mandated sentence.
		httpx.ErrorWith(w, http.StatusConflict, "held_from_picking", map[string]any{
			"message": PickHoldWords,
		})
	case errors.Is(err, ErrPickHoldUnverifiable):
		// A wiring or read fault on the hold, fail closed: name it, never a
		// generic 500. Unreachable in a correct deploy (the composition root
		// asserts the wiring at boot).
		httpx.Error(w, http.StatusServiceUnavailable, ErrPickHoldUnverifiable.Error())
	case errors.Is(err, ErrNoTransition):
		httpx.Error(w, http.StatusConflict, "order is already in the requested state")
	case errors.Is(err, ErrCancelBlocked):
		// A legitimate business refusal, not a fault: money is already attached.
		httpx.Error(w, http.StatusConflict,
			"this order has a finalized invoice or a committed counter sale, use the POS returns flow to reverse it")
	case errors.Is(err, ErrCancelUnverifiable):
		// A server wiring fault, deliberately NOT a generic 500: name the unwired seam so the
		// operator knows exactly what to fix. Unreachable in a correct deploy (main.go asserts the
		// wiring at boot).
		httpx.Error(w, http.StatusServiceUnavailable,
			"cannot verify the order has no attached invoice or sale (cancel guards not wired), cancel refused")
	case errors.As(err, new(errDepositDefaultUnreadable)):
		// Row 1193: the dealer's deposit rule could not be read, so the
		// disposition a cancel would apply is unknown. Money, never guessed.
		httpx.Error(w, http.StatusServiceUnavailable,
			"the dealer's deposit rule could not be read, cancel refused")
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
