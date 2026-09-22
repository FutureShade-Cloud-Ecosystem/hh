package tasks

// weight_handler.go, the DIB-324 round-trip surface, registered separately (RegisterWeightRoutes)
// for the reason RegisterYardRoutes was: the shipped route tables stay byte-identical and this
// surface can be wired, or not, on its own.
//
// The identity rule is the module's existing one, unchanged: the counter who requests and the picker
// who answers both come from the verified StaffContext the guard stamps, never from the body. A yard
// worker cannot record a weight "as" somebody else.

import (
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterWeightRoutes wires the NEED_WEIGHT round trip and the advance-rule read:
//
//	POST /api/v1/yard/weight-requests            the counter pushes a request to the yard
//	POST /api/v1/yard/picks/{id}/weight          the yard types the weight back
//	GET  /api/v1/yard/advance-rules/{taskType}   what the DIB-325 gate currently says (read-only)
//
// The advance rule is READ-ONLY over HTTP on purpose. Turning it on moves orders toward invoicing,
// which is a decision with money at the end of it; it is a deliberate write against the database with
// an attributable updated_by, not a toggle somebody finds in a settings screen. advance.go says why.
func (h *Handler) RegisterWeightRoutes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("POST /api/v1/yard/weight-requests", g.RequireStaff(h.staff(h.requestWeight)))
	mux.Handle("POST /api/v1/yard/picks/{id}/weight", g.RequireStaff(h.staff(h.submitWeight)))
	mux.Handle("GET /api/v1/yard/advance-rules/{taskType}", g.RequireStaff(h.staff(h.advanceRule)))
}

type weightRequestBody struct {
	OrderID     string `json:"order_id"`
	OrderLineNo int    `json:"order_line_no"`
	UOM         string `json:"uom"`
	Note        string `json:"note"`
}

func (h *Handler) requestWeight(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req weightRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := h.svc.RequestWeight(r.Context(), WeightRequestInput{
		OrderID:     req.OrderID,
		OrderLineNo: req.OrderLineNo,
		UOM:         req.UOM,
		Note:        req.Note,
		RequestedBy: identity.StaffUserIDFromContext(r.Context()),
	})
	if err != nil {
		h.writeWeightErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

type weightSubmitBody struct {
	Weight string `json:"weight"`
	UOM    string `json:"uom"`
	Note   string `json:"note"`
}

func (h *Handler) submitWeight(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req weightSubmitBody
	if !decodeJSON(w, r, &req) {
		return
	}
	// 200, not 202, even when the order line refused the weight: the WEIGHT WAS RECORDED, which is
	// what this endpoint promises. The refusal travels in the body (order_synced / order_error) so
	// the yard sees it on the screen they typed into rather than in a report next week.
	res, err := h.svc.SubmitWeight(r.Context(), WeightInput{
		TaskID:  r.PathValue("id"),
		ActorID: identity.StaffUserIDFromContext(r.Context()),
		Weight:  req.Weight,
		UOM:     req.UOM,
		Note:    req.Note,
	})
	if err != nil {
		h.writeWeightErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) advanceRule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rule, err := h.svc.AdvanceRuleFor(r.Context(), r.PathValue("taskType"))
	if err != nil {
		h.writeWeightErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rule)
}

// writeWeightErr adds the two W3 refusals to the yard taxonomy and defers to it.
//
// A UNIT MISMATCH IS 400, NOT 409. The world is fine, the request carried a number in the wrong
// unit, and the fix is to re-type it. That is exactly the distinction writeErr's header draws.
func (h *Handler) writeWeightErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotWeightTask):
		httpx.Error(w, http.StatusConflict, "this task is not a weight request")
	case errors.Is(err, ErrWeightUOMMismatch):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		h.writeYardErr(w, r, err)
	}
}
