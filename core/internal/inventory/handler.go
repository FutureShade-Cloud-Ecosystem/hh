package inventory

// handler.go, the inventory HTTP surface bodies (routes registered in module.go RegisterRoutes). Handlers are
// thin: decode → call the service → map the error taxonomy to HTTP. Money is integer cents on the wire; the
// on-hand read model carries the weighted-average cost so the frontend can render valuation without a second
// round-trip. Reads are RequireStaff; receiving is RequireManager (the frozen guard wiring).

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"strconv"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// writeErr maps the inventory error taxonomy to HTTP (clientError carries its own status; bad-input → 400).
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	var br badRequestError
	var hbr httpx.BadRequestError
	switch {
	case errors.As(err, &ce):
		httpx.Error(w, ce.status, ce.msg)
	case errors.As(err, &br):
		httpx.Error(w, http.StatusBadRequest, br.msg)
	case errors.As(err, &hbr):
		httpx.Error(w, http.StatusBadRequest, hbr.Msg)
	default:
		h.log.Error("inventory: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}

// productIDParam reads ?product_id= for the two stock reads and insists it is a UUID before it can
// reach Postgres.
//
// DIB-443. product_id is a `uuid` column on both sides of these reads, so anything that is not one -
// a SKU pasted out of the catalogue, a UUID copied with a trailing space, arrives at the driver as
// an uncastable literal. That comes back as a bare pgx error, which the taxonomy in writeErr can
// only classify as "internal error": HTTP 500, a line in the error log, and a surface telling the
// user the system broke when the user made a typo. Grading it here turns the whole class into a 400
// that names what the field wants, and, the part that matters downstream, keeps 500 meaning "the
// server actually failed", which is what lets the frontend show a different thing for each.
//
// The value is returned in canonical form (uuid.Parse also accepts the braced and urn: spellings) so
// only one shape ever reaches SQL.
func productIDParam(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("product_id"))
	if raw == "" {
		return "", badReq("product_id is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", badReq("product_id must be a product UUID")
	}
	return id.String(), nil
}

// branchIDParam reads an OPTIONAL ?branch_id= and insists that, when present, it is a UUID before it
// can reach Postgres. Empty is returned as empty, that is the "every branch" case, and an omitted
// optional filter is never an error.
//
// DIB-451. This is productIDParam's rule applied to the other id these reads take, and it is here
// because the pending-fulfilment handler passed the raw query value straight into SQL. branch_id is a
// `uuid` column, so a branch id with one character fat-fingered off the end arrived at the driver as
// an uncastable literal, came back as an untyped pgx error, and writeErr, correctly, given what it
// was handed, called it "internal error" and answered 500. The user made a typo and the system said
// it had broken.
//
// The grade belongs at the door rather than in each handler for the reason DIB-443 named: 500 has to
// keep meaning "the server actually failed", because that is what lets a surface show one thing for
// "fix your input" and another for "this is not your fault". Every handler that takes a branch_id now
// shares this one function, so there is one message and one canonical form (uuid.Parse also accepts
// the braced and urn: spellings) rather than a copy per read that drifts.
func branchIDParam(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("branch_id"))
	if raw == "" {
		return "", nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		// Plain words, in the register the quote builder already uses for the same mistake: name the
		// field, name what it wants, and say where to get one. "invalid UUID format" is the driver's
		// sentence, not a sentence for the person who mistyped.
		return "", badReq("branch_id must be a branch id, copy it from the branch list, not the branch name")
	}
	return id.String(), nil
}

// GET /api/v1/inventory/on-hand?product_id=, the on-hand + weighted-average read model for a product.
//
// A product with no stock_balances rows is a NORMAL answer, not an error: nothing has ever been
// received, counted, or sold against it. It answers 200 with an empty list, never a 404, never a
// 500, and the empty list is written as `[]` rather than JSON null so a client cannot mistake
// "no rows" for "field missing".
func (h *Handler) onHand(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID, err := productIDParam(r)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	rows, err := h.svc.OnHandFor(r.Context(), productID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// X3: the cost leg rides only for a caller the cost rule allows
	// (cost_gate.go); it is byte-absent otherwise.
	if !h.costAllowed(r) {
		blankOnHandCost(rows)
	}
	if rows == nil {
		rows = []OnHand{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"on_hand": rows})
}

// GET /api/v1/inventory/moves?product_id=&branch_id=&limit=, the recent stock-move ledger for a product.
//
// Same contract as onHand: an empty ledger is 200 + [], because "this product has never moved" is an
// answer the caller asked for.
func (h *Handler) moves(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID, err := productIDParam(r)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// branch_id is optional, and casts to uuid in the same query, an unparseable one is the same
	// 500-for-a-typo, so it is graded the same way. Empty stays empty (the "all branches" case).
	// DIB-451 lifted the rule this handler had inlined into branchIDParam, so pending-fulfilment
	// gets the identical grade instead of a second, slightly different copy.
	branchID, err := branchIDParam(r)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := h.svc.db().RecentMoves(r.Context(), productID, branchID, limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// X3: the move ledger prices a transfer's ship move at the branch
	// average, so the unit cost rides only for a caller the cost rule
	// allows (cost_gate.go); it is byte-absent otherwise.
	if !h.costAllowed(r) {
		blankMoveCost(rows)
	}
	if rows == nil {
		rows = []StockMove{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"moves": rows})
}

// GET /api/v1/inventory/pending-fulfilment?branch_id=, the DX-01 deferred-fulfilment gap surface.
//
// DIB-451: branch_id is graded here rather than at the uuid cast, so a mistyped branch is a 400 that
// names the field instead of a 500 that names nothing. Same contract as the two reads above: a branch
// with nothing pending is a 200 and an empty list, the answer to the question, not a failure, and
// the list is written as `[]` rather than JSON null, because "no pending lines" and "this field was
// not populated" are different facts and a client must not have to guess which one it got.
func (h *Handler) pendingFulfilment(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	branchID, err := branchIDParam(r)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	rows, err := h.svc.PendingFulfilments(r.Context(), branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rows == nil {
		rows = []PendingFulfilment{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"pending": rows})
}

// GET /api/v1/inventory/controller-exceptions?branch_id=, lists pre-receipt sales posted at fallback cost
// awaiting true-up at the product's first receipt at the branch (ruling 82.4).
func (h *Handler) controllerExceptions(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	branchID, err := branchIDParam(r)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	rows, err := h.svc.ControllerExceptions(r.Context(), branchID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rows == nil {
		rows = []PreReceiptException{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"exceptions": rows})
}

// POST /api/v1/inventory/receive, receive a PO (all outstanding, or explicit per-line qtys; over-receipt 409).
func (h *Handler) receive(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req ReceiveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	req.ReceivedBy = identity.StaffUserIDFromContext(r.Context())
	res, err := h.svc.receive(r.Context(), req)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
