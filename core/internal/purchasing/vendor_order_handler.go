package purchasing

// vendor_order_handler.go, the HTTP surface for the outbound purchase order. Like handler.go, this
// file only decodes, delegates and maps the error taxonomy; the guards are threaded in by
// RegisterRoutes and every policy decision lives in the service.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - the document -

// GET /api/v1/purchasing/pos/{id}/document, the print page.
//
// text/html with Cache-Control: no-store, copied from internal/printing's receipt handler and for a
// sharper version of its reason: this page is rendered THROUGH A ROLE GATE, so a cached copy is a
// manager's columns waiting in a proxy for the next clerk who opens the same URL.
func (h *Handler) poDocumentHTML(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	doc, err := h.svc.PODocumentFor(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	page, err := RenderPODocumentHTML(doc)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(page))
}

// GET /api/v1/purchasing/pos/{id}/document.csv, the same document, the other encoder.
func (h *Handler) poDocumentCSV(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	doc, err := h.svc.PODocumentFor(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	body, err := EncodePODocumentCSV(doc)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// The filename is the PO number, so a buyer with six of these in a downloads folder can tell
	// them apart without opening any of them.
	w.Header().Set("Content-Disposition", `attachment; filename="`+doc.FileName()+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// GET /api/v1/purchasing/pos/{id}/costs, the gated money payload on its own.
//
// A surface asks this BEFORE it draws a cost column, so "you may not see cost" is an answer it
// receives rather than a conclusion it draws from a missing key. For a refused caller the body is
// exactly `{}`.
func (h *Handler) poCosts(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, err := h.svc.POCostsFor(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

// - the send -

// POST /api/v1/purchasing/pos/{id}/send, send the order to the tagged ordering desk.
func (h *Handler) sendPO(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SendPORequest
	// An empty body is a legitimate "send it as it is", only a MALFORMED body is a 400. The same
	// posture postBill already takes.
	if r.ContentLength > 0 && !decodeJSON(w, r, &in) {
		return
	}
	send, err := h.svc.SendPO(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// 200 AND NOT AN ERROR, EVEN THOUGH NOTHING WAS DELIVERED. The row exists, the recipient was
	// resolved, and the outcome is on the record: that is a successful REQUEST whose SEND failed,
	// and the two are different facts. The surface reads `status` and `detail` and says so in words.
	httpx.WriteJSON(w, http.StatusOK, send)
}

// GET /api/v1/purchasing/pos/{id}/sends, one order's send history.
func (h *Handler) listPOSends(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sends, err := h.svc.ListPOSends(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// `[]`, never null: "never sent" is an ordinary answer and must not arrive looking like a field
	// that failed to populate (the DIB-452 rule, same reason the incoming read gives).
	if sends == nil {
		sends = []POSend{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": sends})
}

// - the ordering contact -

func (h *Handler) getVendorContact(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c, found, err := h.svc.GetVendorContact(r.Context(), r.PathValue("vendorID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// NOT-FOUND IS 200 WITH `tagged:false`, not a 404. "Nobody has tagged a desk yet" is the
	// ordinary state of every vendor in this deployment, and a surface that had to catch a 404 to
	// draw an empty state would treat the normal case as an error.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tagged": found, "contact": c})
}

func (h *Handler) setVendorContact(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in VendorContact
	if !decodeJSON(w, r, &in) {
		return
	}
	in.VendorID = r.PathValue("vendorID")
	c, err := h.svc.SetVendorContact(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tagged": true, "contact": c})
}

func (h *Handler) deleteVendorContact(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.DeleteVendorContact(r.Context(), r.PathValue("vendorID")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tagged": false, "contact": VendorContact{}})
}

// - the vendor's catalogue vocabulary -

func (h *Handler) listVendorSKUs(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	items, err := h.svc.ListVendorSKUs(r.Context(), r.PathValue("vendorID"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if items == nil {
		items = []VendorSKU{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) setVendorSKU(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in VendorSKU
	if !decodeJSON(w, r, &in) {
		return
	}
	in.VendorID = r.PathValue("vendorID")
	m, err := h.svc.SetVendorSKU(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) deleteVendorSKU(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.DeleteVendorSKU(r.Context(), r.PathValue("vendorID"), r.PathValue("productID")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// - the echoed reference -

// GET /api/v1/purchasing/vendor-ref?ref=, find the order a vendor quoted back.
//
// A QUERY PARAMETER RATHER THAN A PATH SEGMENT, because what an AP clerk types is whatever the
// vendor wrote, "#PO-TRT-4", "PO TRT 4", "po/trt/4", and half of those are not path-safe. It is
// also its own route rather than /pos/{something}: an id and a human's transcription are different
// kinds of thing and should not share a slot.
func (h *Handler) vendorRef(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	po, err := h.svc.ResolveEchoedRef(r.Context(), r.URL.Query().Get("ref"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, po)
}

// - policy -

func (h *Handler) getPurchasingPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, err := h.svc.GetPolicy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) setPurchasingPolicy(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in SetPolicyRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := h.svc.SetPolicy(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
