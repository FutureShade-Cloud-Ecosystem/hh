package dispatch

// handler_w3.go, the Wave 3 routes. Thin by the same policy handler.go states: decode, delegate,
// map. Every authority decision is in service_w3.go.
//
// REGISTERED SEPARATELY (RegisterW3Routes, called from the composition root next to RegisterRoutes)
// so the Wave 2 route table is byte-identical and the Wave 3 surface can be wired, or not, on its own
//, the RegisterYardRoutes precedent from Wave 2's tasks module.

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterW3Routes wires the follow-through surface:
//
//	POST /api/v1/dispatch/stops/{id}/mto              classify a load (DIB-63)
//	POST /api/v1/dispatch/stops/{id}/attach-photos    fold late photos into the POD record (DIB-63)
//	POST /api/v1/dispatch/stops/{id}/reschedule       move ONE unassigned stop's date (DIB-382)
//	GET  /api/v1/dispatch/stops/{id}/reschedules      that stop's move trail
//	GET  /api/v1/dispatch/stops/{id}/payment-hold     the driver's PAYMENT DUE banner (B's seam)
//	POST /api/v1/dispatch/runs/{id}/reschedule        move a PLANNED run + its stops (DIB-382)
//	GET  /api/v1/dispatch/runs/{id}/reschedules       that run's move trail
//	GET  /api/v1/dispatch/runs/{id}/payment-holds     one banner set for a whole day (B's seam)
//	POST /api/v1/dispatch/scans                       record a pallet/load checkpoint (DIB-75)
//	GET  /api/v1/dispatch/scan-trail                  ?order_id=, the trail both surfaces render
//
// ALL RequireStaff, matching RegisterRoutes' gating rule exactly: the people who run a delivery day
// are frontline roles, and the branch fence inside the service is what makes that safe. Nothing here
// registers a manager-only write, because nothing here outlives the day the way a truck row does.
func (h *Handler) RegisterW3Routes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("POST /api/v1/dispatch/stops/{id}/mto", g.RequireStaff(h.staff(h.setStopMTO)))
	mux.Handle("POST /api/v1/dispatch/stops/{id}/attach-photos", g.RequireStaff(h.staff(h.attachPhotos)))
	mux.Handle("POST /api/v1/dispatch/stops/{id}/reschedule", g.RequireStaff(h.staff(h.rescheduleStop)))
	mux.Handle("GET /api/v1/dispatch/stops/{id}/reschedules", g.RequireStaff(h.staff(h.stopReschedules)))
	mux.Handle("GET /api/v1/dispatch/stops/{id}/payment-hold", g.RequireStaff(h.staff(h.stopPaymentHold)))

	mux.Handle("POST /api/v1/dispatch/runs/{id}/reschedule", g.RequireStaff(h.staff(h.rescheduleRun)))
	mux.Handle("GET /api/v1/dispatch/runs/{id}/reschedules", g.RequireStaff(h.staff(h.runReschedules)))
	mux.Handle("GET /api/v1/dispatch/runs/{id}/payment-holds", g.RequireStaff(h.staff(h.runPaymentHolds)))

	mux.Handle("POST /api/v1/dispatch/scans", g.RequireStaff(h.staff(h.recordScan)))
	// The trail read hangs off its OWN top-level path with the order in the QUERY rather than under
	// /orders/{id}/, for the routing reason RegisterRoutes' run-by-no comment records: dispatch does
	// not own an /orders/ path space, and inventing one here would put this module's routes in
	// another module's namespace.
	mux.Handle("GET /api/v1/dispatch/scan-trail", g.RequireStaff(h.staff(h.scanTrail)))
}

// - DIB-63 -

func (h *Handler) setStopMTO(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in MTOInput
	if !decodeJSON(w, r, &in) {
		return
	}
	d, err := h.svc.SetStopMTO(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) attachPhotos(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	d, err := h.svc.AttachStopPhotos(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// - DIB-382 -

func (h *Handler) rescheduleStop(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in RescheduleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := h.svc.RescheduleStop(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) rescheduleRun(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in RescheduleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := h.svc.RescheduleRun(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) stopReschedules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.RescheduleHistory(r.Context(), RescheduleEntityStop, r.PathValue("id"))
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) runReschedules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.RescheduleHistory(r.Context(), RescheduleEntityRun, r.PathValue("id"))
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// - DIB-75 -

func (h *Handler) recordScan(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ScanInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := h.svc.RecordScan(r.Context(), in)
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) scanTrail(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	orderID := r.URL.Query().Get("order_id")
	if orderID == "" {
		httpx.Error(w, http.StatusBadRequest, "order_id is required")
		return
	}
	t, err := h.svc.ScanTrailForOrder(r.Context(), orderID)
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// - the AR gate's driver-facing read -

func (h *Handler) stopPaymentHold(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	hold, err := h.svc.PaymentHoldForStop(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hold)
}

func (h *Handler) runPaymentHolds(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.PaymentHoldsForRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeW3Err(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// writeW3Err adds the Wave 3 errors to the shipped taxonomy and then defers to it. Both new errors
// are 409 for the reason writeErr's header states: the request was well-formed and the WORLD was in
// the wrong state, so the client must fix the world, not the request.
func (h *Handler) writeW3Err(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrBackwardReschedule), errors.Is(err, ErrBackwardScan),
		errors.Is(err, ErrPaymentHold):
		httpx.Error(w, http.StatusConflict, err.Error())
	default:
		h.writeErr(w, r, err)
	}
}
