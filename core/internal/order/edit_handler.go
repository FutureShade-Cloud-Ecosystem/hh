// edit_handler.go, DIB-175 HTTP for the staff order-edit surface. These hang off the SAME *Handler
// the transition endpoints and the history reads use (history.go), so the order module keeps ONE HTTP
// surface registered from one place in the composition root.
//
//	PATCH  /api/v1/orders/{id}/lines/{lineNo}  {"qty_entered":"12.5"}                manager (Tier ≤ 2)
//	POST   /api/v1/orders/{id}/lines           {"product_id","qty_entered","uom_entered","description"}
//	DELETE /api/v1/orders/{id}/lines/{lineNo}                                        manager (Tier ≤ 2)
//	PATCH  /api/v1/orders/{id}                 {"requested_date":"2026-08-15"}       active in-branch staff
//	GET    /api/v1/orders/{id}/edits                                                 active in-branch staff
//
// The route guards are the COARSE first line only: the service re-checks tier, branch and the edit
// matrix on the resolved caller (edit.go), which is the single source of authority truth. Same
// defence-in-depth posture the transition routes use.
package order

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterEditRoutes wires the DIB-175 staff order-edit endpoints. Line edits move money, so they are
// manager-gated at the route (requireManager, Tier ≤ 2) exactly like cancel; the requested-date edit
// and the change-history read are open to any active in-branch staff (requireStaff).
func (h *Handler) RegisterEditRoutes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("PATCH /api/v1/orders/{id}/lines/{lineNo}", g.RequireManager(h.staff(h.editLineQty)))
	mux.Handle("POST /api/v1/orders/{id}/lines", g.RequireManager(h.staff(h.addLine)))
	mux.Handle("DELETE /api/v1/orders/{id}/lines/{lineNo}", g.RequireManager(h.staff(h.removeLine)))
	mux.Handle("PATCH /api/v1/orders/{id}", g.RequireStaff(h.staff(h.editOrderHeader)))
	mux.Handle("GET /api/v1/orders/{id}/edits", g.RequireStaff(h.staff(h.orderEditHistory)))
}

// decodeEditBody reads a bounded JSON body. The bound is the same package-level maxBodyBytes the
// transition endpoints use, applied through http.MaxBytesReader so an oversized payload is refused at
// the boundary rather than buffered. A malformed body is a 400, never a silent zero-value edit, an
// edit that writes nothing because the JSON did not parse would be indistinguishable from a no-op.
func decodeEditBody(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return httpx.BadRequestf("a JSON body is required")
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(dst); err != nil {
		return httpx.BadRequestf("malformed JSON body")
	}
	return nil
}

// lineNoParam parses the {lineNo} path segment. order_lines.line_no is a positive integer within one
// order, so anything else is a 400 rather than a 404, the request is malformed, not merely missing.
func lineNoParam(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("lineNo"))
	if err != nil || n <= 0 {
		return 0, httpx.BadRequestf("line number must be a positive integer")
	}
	return n, nil
}

func (h *Handler) editLineQty(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lineNo, err := lineNoParam(r)
	if err != nil {
		h.writeEditErr(w, err)
		return
	}
	var body struct {
		QtyEntered string `json:"qty_entered"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeEditErr(w, err)
		return
	}
	res, err := h.svc.EditLineQty(r.Context(), r.PathValue("id"), lineNo, body.QtyEntered)
	if err != nil {
		h.writeEditErr(w, err)
		return
	}
	h.writeEditResult(w, r.PathValue("id"), res)
}

func (h *Handler) addLine(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		ProductID   string `json:"product_id"`
		QtyEntered  string `json:"qty_entered"`
		UOMEntered  string `json:"uom_entered"`
		Description string `json:"description"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeEditErr(w, err)
		return
	}
	res, err := h.svc.AddLine(r.Context(), r.PathValue("id"),
		body.ProductID, body.QtyEntered, body.UOMEntered, body.Description)
	if err != nil {
		h.writeEditErr(w, err)
		return
	}
	h.writeEditResult(w, r.PathValue("id"), res)
}

func (h *Handler) removeLine(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lineNo, err := lineNoParam(r)
	if err != nil {
		h.writeEditErr(w, err)
		return
	}
	res, err := h.svc.RemoveLine(r.Context(), r.PathValue("id"), lineNo)
	if err != nil {
		h.writeEditErr(w, err)
		return
	}
	h.writeEditResult(w, r.PathValue("id"), res)
}

// editOrderHeader applies an order-header edit. Only requested_date is editable today; the body is
// decoded into a pointer so an ABSENT field is distinguishable from an explicit empty string (which
// deliberately CLEARS the date).
func (h *Handler) editOrderHeader(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var body struct {
		RequestedDate *string `json:"requested_date"`
	}
	if err := decodeEditBody(w, r, &body); err != nil {
		h.writeEditErr(w, err)
		return
	}
	if body.RequestedDate == nil {
		h.writeEditErr(w, httpx.BadRequestf("requested_date is the only editable order field"))
		return
	}
	res, err := h.svc.SetRequestedDate(r.Context(), r.PathValue("id"), *body.RequestedDate)
	if err != nil {
		h.writeEditErr(w, err)
		return
	}
	h.writeEditResult(w, r.PathValue("id"), res)
}

func (h *Handler) orderEditHistory(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.OrderEditHistoryFor(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeEditErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// writeEditResult renders a successful edit: the resulting line (absent on a removal), the recomputed
// order money, and the order's status at the moment the write landed. Returning the totals means the
// client re-renders the order's money from the SERVER's arithmetic rather than recomputing it locally
// and risking a divergent figure on screen.
func (h *Handler) writeEditResult(w http.ResponseWriter, orderID string, res EditResult) {
	out := map[string]any{
		"order_id":       orderID,
		"status":         res.Status,
		"subtotal_cents": res.Totals.SubtotalCents,
		"total_cents":    res.Totals.TotalCents,
	}
	if res.Line != nil {
		out["line"] = map[string]any{
			"line_no":          res.Line.LineNo,
			"product_id":       res.Line.ProductID,
			"qty_entered":      res.Line.QtyEntered,
			"qty_base":         res.Line.QtyBase,
			"unit_price_cents": res.Line.UnitPriceCents,
			"line_total_cents": res.Line.LineTotalCents,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// writeEditErr maps the edit-specific errors, then falls back to the shared writeErr (handler.go) so
// the authority and not-found answers are byte-identical to the transition endpoints' for the same
// condition. The edit-specific 409s each carry their OWN message: "blocked" for money, "not editable"
// naming the status, "in use" for a committed line, a caller must be able to tell them apart.
func (h *Handler) writeEditErr(w http.ResponseWriter, err error) {
	var notEditable *NotEditableError
	var priceBlocked *PriceBlockedError
	switch {
	case errors.As(err, &notEditable):
		// The acceptance criterion: a blocked state answers 409 with the status NAMED.
		httpx.Error(w, http.StatusConflict, notEditable.Error())
	case errors.As(err, &priceBlocked):
		httpx.Error(w, http.StatusConflict, priceBlocked.Error())
	case errors.Is(err, ErrEditBlocked):
		httpx.Error(w, http.StatusConflict,
			"this order has a finalized invoice or a committed counter sale, use the POS returns flow to change it")
	case errors.Is(err, ErrEditTaxAssessed):
		httpx.Error(w, http.StatusConflict,
			"this order carries assessed tax, which cannot be re-assessed here, edit refused")
	case errors.Is(err, ErrLineInUse):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrLineNotFound):
		httpx.Error(w, http.StatusNotFound, "order line not found")
	case errors.Is(err, ErrEditUnverifiable):
		// A server wiring fault, deliberately NOT a generic 500, name the unwired seam.
		httpx.Error(w, http.StatusServiceUnavailable,
			"cannot verify the order has no attached invoice or sale (edit guards not wired), edit refused")
	case errors.Is(err, ErrEditNotConfigured):
		httpx.Error(w, http.StatusServiceUnavailable,
			"order editing is not configured on this server (pricing seams not wired), edit refused")
	default:
		h.writeErr(w, err)
	}
}
