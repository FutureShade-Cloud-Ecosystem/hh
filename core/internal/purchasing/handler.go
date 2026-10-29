package purchasing

// handler.go, the purchasing HTTP surface. The guards are threaded in by RegisterRoutes (module.go); this
// file only decodes, delegates, and maps the error taxonomy to HTTP.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const maxBodyBytes = 1 << 20 // 1 MiB

func (h *Handler) listVendors(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ListVendors(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// createVendor is the staff writer for the vendor record (vendor_master.go). The org stamp comes
// from the guard resolved scope on the caller, never from a header or the body.
func (h *Handler) createVendor(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CreateVendorRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := h.svc.CreateVendor(r.Context(), caller.OrgID, in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, v)
}

// setVendor corrects the fields the body names on one vendor, the same PUT shape the expected-date
// and delivery-method corrections carry.
func (h *Handler) setVendor(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in UpdateVendorRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := h.svc.UpdateVendor(r.Context(), caller.OrgID, r.PathValue("vendorID"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listPOs(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ListPOs(r.Context(), r.URL.Query().Get("branch_id"), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []PO{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handler) createPO(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CreatePORequest
	if !decodeJSON(w, r, &in) {
		return
	}
	po, err := h.svc.CreatePO(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, po)
}

func (h *Handler) getPO(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	po, err := h.svc.GetPO(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

func (h *Handler) addLine(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in AddLineParams
	if !decodeJSON(w, r, &in) {
		return
	}
	po, err := h.svc.AddLine(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

func (h *Handler) removeLine(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	po, err := h.svc.RemoveLine(r.Context(), r.PathValue("id"), r.PathValue("lineID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

// setLine corrects the quantity or the cost of one line on a draft, the same PUT shape the other
// correction routes carry (vendor_master.go, the expected date, the delivery method).
func (h *Handler) setLine(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in UpdateLineParams
	if !decodeJSON(w, r, &in) {
		return
	}
	po, err := h.svc.UpdateLine(r.Context(), r.PathValue("id"), r.PathValue("lineID"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

func (h *Handler) transition(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in TransitionRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	po, err := h.svc.Transition(r.Context(), r.PathValue("id"), in.To)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

func (h *Handler) receive(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ReceiveRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	rc, err := h.svc.Receive(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rc)
}

// GET /api/v1/purchasing/incoming?product_id=&branch_id=, DIB-460.
//
// The read a product surface asks: what is on order for THIS product, and when is it expected.
// branch_id is optional and NARROWS; without it the answer covers every branch the caller may see.
func (h *Handler) incoming(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	lines, err := h.svc.IncomingForProduct(r.Context(),
		r.URL.Query().Get("product_id"), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// `[]`, never null: "nothing on order" is an ordinary answer and must not arrive looking like a
	// field that failed to populate (the DIB-452 rule, same reason).
	if lines == nil {
		lines = []IncomingPOLine{}
	}
	// The contract's page (PurchasingIncomingLinePage): the list under `items`, the cursor key
	// present and null, the same envelope every list carries. The ERP wrote it under `lines`.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": lines, "next_cursor": nil})
}

// PUT /api/v1/purchasing/pos/{id}/expected-date, DIB-460's edit path.
//
// A PUT because it SETS one field to what the body says, including setting it to nothing; a POST
// action verb would suggest an event ("promise this date") when what happens is a correction.
func (h *Handler) setExpectedDate(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SetExpectedDateRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	po, err := h.svc.SetExpectedDate(r.Context(), r.PathValue("id"), in.ExpectedAt)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

// PUT /api/v1/purchasing/pos/{id}/delivery-method, DIB-572's correction path (ported from the ERP).
//
// The same shape and the same verb as the expected-date handler above, because it is the same kind
// of thing: one field, set to what the body says. It exists because the column could be WRITTEN
// only at create time, which made a mistyped arrival permanent and, in the ERP, made the
// receiving worklist print a fact nobody could put right. (Core's worklist does not render the
// column yet; the DIB-573 read half is a recorded residual.)
func (h *Handler) setDeliveryMethod(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SetDeliveryMethodRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	po, err := h.svc.SetDeliveryMethod(r.Context(), r.PathValue("id"), in.DeliveryMethod)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

// POST /api/v1/purchasing/pos/{id}/apply-warranty-credit
func (h *Handler) applyWarrantyCredit(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ApplyWarrantyCreditRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &in) {
			return
		}
	}
	po, err := h.svc.ApplyWarrantyCredit(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

func (h *Handler) worklist(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.Worklist(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []WorklistItem{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handler) getReceipt(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rc, err := h.svc.GetReceipt(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rc)
}

func (h *Handler) attachLandedCost(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in LandedCostRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	rc, err := h.svc.AttachLandedCost(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rc)
}

func (h *Handler) listBills(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	items, err := h.svc.ListBills(r.Context(), q.Get("branch_id"), q.Get("status"), q.Get("match_state"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createBill(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in CreateBillRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	b, err := h.svc.CreateBill(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) getBill(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	b, err := h.svc.GetBill(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) postBill(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PostBillRequest
	// An empty body is a legitimate "post it as it is", only a MALFORMED body is a 400.
	if r.ContentLength > 0 && !decodeJSON(w, r, &in) {
		return
	}
	b, err := h.svc.PostBill(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

// payBill records a supplier payment against one posted bill. The replay key
// rides the Idempotency-Key header the way the AR payment doors carry it: one
// key per press, judged server side, a re-press answering the first payment.
func (h *Handler) payBill(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in PayBillRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.IdempotencyKey = r.Header.Get("Idempotency-Key")
	b, err := h.svc.PayBill(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) apAging(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	page, err := h.svc.APAgingForCaller(r.Context(), q.Get("as_of"), q.Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) getTolerance(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	t, err := h.svc.GetTolerance(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) setTolerance(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SetToleranceRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	t, err := h.svc.SetTolerance(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the purchasing error taxonomy to HTTP. The seam refusals carry their own 503 status through
// clientError, so an unwired deployment reads as a configuration fault rather than a caller error.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	var br httpx.BadRequestError
	switch {
	case errors.As(err, &ce):
		httpx.Error(w, ce.status, ce.msg)
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.Msg)
	default:
		h.log.Error("purchasing: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
