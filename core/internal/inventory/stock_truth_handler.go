package inventory

// stock_truth_handler.go, the Wave 4 HTTP surface: reservations + allocation policy (DIB-60),
// inter-branch transfers (DIB-71), cross-branch visibility (DIB-73), browse + facets (DIB-61), and
// the replenishment worklist (DIB-72).
//
// Registered by RegisterStockTruthRoutes, which is called from this bundle's main.go wiring block -
// the module.go RegisterRoutes above is left byte-for-byte alone so a merge conflict there is
// impossible.
//
// GUARDS. Reads are RequireStaff (a counter clerk needs to see what Kingston has). Every write that
// MOVES or HOLDS stock is RequireManager: shipping a truck, receiving one, holding stock off the
// floor and rewriting a branch's allocation policy are all decisions with money attached, and the
// existing receiving route already draws the line in exactly that place.

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterStockTruthRoutes registers the Wave 4 inventory routes behind the identity guards.
func (h *Handler) RegisterStockTruthRoutes(r httpx.Registrar, g *guards.Guards) {
	// DIB-73 cross-branch visibility + DIB-61 browse
	r.Handle("GET /api/v1/inventory/branch-stock", g.RequireStaff(h.staff(h.branchStock)))
	r.Handle("GET /api/v1/inventory/browse", g.RequireStaff(h.staff(h.browse)))

	// DIB-60 reservations + policy
	r.Handle("GET /api/v1/inventory/reservations", g.RequireStaff(h.staff(h.listReservations)))
	r.Handle("POST /api/v1/inventory/reservations", g.RequireManager(h.staff(h.createReservation)))
	r.Handle("POST /api/v1/inventory/reservations/release", g.RequireManager(h.staff(h.releaseReservation)))
	r.Handle("POST /api/v1/inventory/reservations/expire", g.RequireManager(h.staff(h.expireReservations)))
	r.Handle("GET /api/v1/inventory/allocation-policy", g.RequireStaff(h.staff(h.getPolicy)))
	r.Handle("PUT /api/v1/inventory/allocation-policy", g.RequireManager(h.staff(h.putPolicy)))

	// DIB-71 transfers
	r.Handle("GET /api/v1/inventory/transfers", g.RequireStaff(h.staff(h.listTransfers)))
	r.Handle("GET /api/v1/inventory/transfers/{id}", g.RequireStaff(h.staff(h.getTransfer)))
	r.Handle("POST /api/v1/inventory/transfers", g.RequireManager(h.staff(h.createTransfer)))
	r.Handle("POST /api/v1/inventory/transfers/{id}/ship", g.RequireManager(h.staff(h.shipTransfer)))
	r.Handle("POST /api/v1/inventory/transfers/{id}/receive", g.RequireManager(h.staff(h.receiveTransfer)))
	r.Handle("POST /api/v1/inventory/transfers/{id}/cancel", g.RequireManager(h.staff(h.cancelTransfer)))

	// DIB-72 replenishment
	r.Handle("GET /api/v1/inventory/shortfall", g.RequireStaff(h.staff(h.shortfall)))
	r.Handle("GET /api/v1/inventory/reorder-points", g.RequireStaff(h.staff(h.listReorderPoints)))
	r.Handle("PUT /api/v1/inventory/reorder-points", g.RequireManager(h.staff(h.putReorderPoint)))
	r.Handle("POST /api/v1/inventory/reorder-points/bootstrap", g.RequireManager(h.staff(h.bootstrapReorderPoints)))
	r.Handle("POST /api/v1/inventory/shortfall/create-po", g.RequireManager(h.staff(h.createPOFromShortfall)))

	// - DIB-460 / DIB-461 · what is coming, and the two ways to ask for more -
	//
	// The GATES are the shipped ones, and each control renders only where its gate would let it act:
	//
	//	incoming-transfers  RequireStaff  , "what's on the truck" is a counter question.
	//	reorder-po          RequireManager, it drafts a purchase order, which is what
	//	                                     shortfall/create-po above is gated at, and for the same
	//	                                     reason: it commits nothing yet, but it is buying.
	//	nudge-procurement   RequireStaff  , saying "we're getting low" commits nothing at all. The
	//	                                     surface still shows it only to the job shapes whose work
	//	                                     includes noticing (see the frontend's reorderControlFor);
	//	                                     a yard picker being ABLE to post one is not a reason to
	//	                                     put a button on their screen.
	r.Handle("GET /api/v1/inventory/incoming-transfers", g.RequireStaff(h.staff(h.incomingTransfers)))
	r.Handle("POST /api/v1/inventory/reorder-po", g.RequireManager(h.staff(h.reorderPO)))
	r.Handle("POST /api/v1/inventory/nudge-procurement", g.RequireStaff(h.staff(h.nudgeProcurement)))
}

// reachFromRequest projects the guard-resolved caller onto a branch-scoped read.
//
// It mirrors the composition root's own stockReach (internal/assist/adapters) rather than inventing
// a second rule: GLOBAL reaches everything, a scoped caller reaches exactly its grants, and a scoped
// caller with NO grants reaches NOTHING, the empty slice, never nil. Building it here (rather than
// letting each handler read the context) means there is ONE place a future edit could invert the
// fence, and it is the place with this comment on it.
func reachFromRequest(r *http.Request) BranchReach {
	c, ok := identity.StaffContextFromContext(r.Context())
	if !ok {
		// Unreachable behind the guards, and answered as "nothing" rather than "everything" anyway.
		return BranchReach{BranchIDs: []string{}}
	}
	if c.Scope == identity.ScopeGlobal {
		return BranchReach{Global: true}
	}
	ids := make([]string, 0, len(c.Branches))
	for b, granted := range c.Branches {
		if granted {
			ids = append(ids, b)
		}
	}
	sort.Strings(ids)
	return BranchReach{BranchIDs: ids}
}

// GET /api/v1/inventory/incoming-transfers?product_id=, DIB-460's transfer half.
func (h *Handler) incomingTransfers(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	lines, err := h.svc.InTransitForProduct(r.Context(), reachFromRequest(r), r.URL.Query().Get("product_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// `[]`, never null, "nothing on a truck" is an ordinary answer.
	if lines == nil {
		lines = []InTransitLine{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// POST /api/v1/inventory/reorder-po, DIB-461(a). Drafts a PO for one product through the SAME
// POCreator seam the shortfall worklist uses.
func (h *Handler) reorderPO(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in ReorderRequest
	if !decodeBody(w, r, &in) {
		return
	}
	res, err := h.svc.CreateReorderPO(r.Context(), reachFromRequest(r), in)
	if err != nil {
		// The draft rides along on the 503 an unwired purchasing seam returns, so the surface shows
		// what WOULD have been ordered instead of an empty error toast (the createPOFromShortfall
		// posture, kept identical).
		var ce clientError
		if ok := asClientError(err, &ce); ok && ce.status == http.StatusServiceUnavailable {
			httpx.ErrorWith(w, http.StatusServiceUnavailable, ce.msg, map[string]any{"draft": res.Draft})
			return
		}
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// POST /api/v1/inventory/nudge-procurement, DIB-461(b). One NOTABLE system event, idempotent per
// product/branch/day.
func (h *Handler) nudgeProcurement(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req struct {
		ProductID string `json:"product_id"`
		BranchID  string `json:"branch_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := h.svc.NudgeProcurement(r.Context(), reachFromRequest(r), req.ProductID, req.BranchID)
	if err != nil {
		var ce clientError
		if ok := asClientError(err, &ce); ok && ce.status == http.StatusServiceUnavailable {
			// An unwired messaging seam is a DEPLOYMENT fault, and the body it would have posted is
			// returned so an operator can see exactly what was lost rather than guessing.
			httpx.ErrorWith(w, http.StatusServiceUnavailable, ce.msg, map[string]any{"body": res.Body})
			return
		}
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// decodeBody is the shared bounded JSON decode.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// decodeOptionalBody is decodeBody for a POST whose body is genuinely optional, the ship route, whose
// only field is an eta the caller may have no opinion about.
//
// AN ABSENT BODY IS NOT AN INPUT ERROR. "Ship this transfer, I have nothing to add" is exactly the
// request every caller made before 0158, and answering it with 400 invalid_json would break a shipped
// route to add an optional field. Malformed JSON is still refused, the distinction is between NOTHING
// and NONSENSE, and only the second is a mistake.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func intParam(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// - DIB-73 visibility -

// GET /api/v1/inventory/branch-stock?product_id=&branch_id=, the 4-bucket per-branch view.
// Without branch_id it returns EVERY branch, which is the cross-branch answer the ticket asks for.
func (h *Handler) branchStock(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	productID := r.URL.Query().Get("product_id")
	branchID := r.URL.Query().Get("branch_id")
	if branchID != "" {
		row, err := h.svc.AvailabilityAtBranch(r.Context(), productID, branchID)
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"branches": []BranchStock{row}})
		return
	}
	rows, err := h.svc.AvailabilityAllBranches(r.Context(), productID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"branches": rows})
}

// - DIB-61 browse -

// GET /api/v1/inventory/browse?text=&branch_id=&color_family=a,b&...&in_stock_only=1&limit=&offset=
//
// Multi-valued facets arrive comma-separated. A repeated query parameter would be more RESTful and
// is worse here: the frontend builds these from checkbox sets, and one canonical encoding means the
// URL a user copies out of the address bar reproduces their exact filter.
//
// DIB-452: &offset= pages the read. The response echoes the limit and offset it actually answered
// for alongside the pre-limit total, so a caller can walk the whole match set without assuming its
// request survived clamping. The 200-row cap is per PAGE and has not moved.
func (h *Handler) browse(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	csv := func(name string) []string {
		v := strings.TrimSpace(q.Get(name))
		if v == "" {
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	offset, err := offsetParam(r)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	res, err := h.svc.Browse(r.Context(), BrowseFilter{
		Text:         q.Get("text"),
		BranchID:     q.Get("branch_id"),
		ColorFamily:  csv("color_family"),
		Texture:      csv("texture"),
		ProductSize:  csv("product_size"),
		SoldBy:       csv("sold_by"),
		Manufacturer: csv("manufacturer"),
		Collection:   csv("collection"),
		Color:        csv("color"),
		Finish:       csv("finish"),
		Application:  csv("application"),
		Featured:     q.Get("is_featured") == "1" || q.Get("is_featured") == "true",
		SpecialOrder: q.Get("is_special_order") == "1" || q.Get("is_special_order") == "true",
		InStockOnly:  q.Get("in_stock_only") == "1" || q.Get("in_stock_only") == "true",
		Limit:        intParam(r, "limit", 0),
		Offset:       offset,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// An empty page is `[]`, not null, DIB-452 made the empty page an ordinary thing to ask for
	// (a pager whose last request lands exactly on the end of the list gets one), and "no rows" must
	// not arrive looking like "this field was not populated".
	if res.Rows == nil {
		res.Rows = []BrowseRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// offsetParam reads ?offset= for the browse read, DIB-452.
//
// GRADED rather than silently defaulted, which is a deliberate difference from the limit beside it.
// limit predates this ticket and every existing caller relies on a nonsense value falling back to
// the default; offset is new, so it gets the house rule from the start. And the two failures are not
// alike: a bad limit gives you a differently-sized page of the RIGHT rows, while a bad offset, read
// as 0, gives you page one when you asked for page three, and the caller merges it as if it were
// page three. That is silent data loss dressed as success, and it is exactly the class of thing the
// em dash was hiding before this ticket.
//
// Absent stays absent: no offset means the first page, and empty is never an error.
func offsetParam(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("offset"))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, badReq("offset must be a row number, zero or more")
	}
	return n, nil
}

// - DIB-60 reservations -

// GET /api/v1/inventory/reservations?branch_id=&product_id=&limit=
func (h *Handler) listReservations(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	rows, err := h.svc.ListReservations(r.Context(),
		r.URL.Query().Get("branch_id"), r.URL.Query().Get("product_id"), intParam(r, "limit", 0))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reservations": rows})
}

// POST /api/v1/inventory/reservations, hold stock. The HTTP path opens its own transaction; the
// in-process path (an order confirm) calls ReserveTx with the caller's transaction instead.
func (h *Handler) createReservation(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in ReserveInput
	if !decodeBody(w, r, &in) {
		return
	}
	in.CreatedBy = identity.StaffUserIDFromContext(r.Context())
	tx, err := h.svc.repo.Begin(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	res, err := h.svc.ReserveTx(r.Context(), tx, in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

type releaseReq struct {
	ReservationID string `json:"reservation_id"`
	SourceType    string `json:"source_type"`
	SourceRefID   string `json:"source_ref_id"`
	Status        string `json:"status"`
}

// POST /api/v1/inventory/reservations/release, release by id OR by source document.
func (h *Handler) releaseReservation(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req releaseReq
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Status == "" {
		req.Status = ResReleased
	}
	tx, err := h.svc.repo.Begin(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())

	released := 0
	switch {
	case req.ReservationID != "":
		if err := h.svc.ReleaseTx(r.Context(), tx, req.ReservationID, req.Status); err != nil {
			h.writeErr(w, r, err)
			return
		}
		released = 1
	case req.SourceRefID != "":
		n, err := h.svc.ReleaseBySourceTx(r.Context(), tx, req.SourceType, req.SourceRefID, req.Status)
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		released = n
	default:
		httpx.Error(w, http.StatusBadRequest, "reservation_id or source_ref_id is required")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"released": released, "status": req.Status})
}

// POST /api/v1/inventory/reservations/expire, run the expiry sweep now. Manual today; the same
// service method is the JobFunc the union registers on the jobs runner (see REPORT-W4-B.md).
func (h *Handler) expireReservations(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	n, err := h.svc.ExpireDueReservations(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"expired": n})
}

// GET /api/v1/inventory/allocation-policy?branch_id=
func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	p, err := h.svc.ResolveAllocationPolicy(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// PUT /api/v1/inventory/allocation-policy
func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p AllocationPolicy
	if !decodeBody(w, r, &p) {
		return
	}
	out, err := h.svc.SetAllocationPolicy(r.Context(), p, identity.StaffUserIDFromContext(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// - DIB-71 transfers -

func (h *Handler) listTransfers(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	rows, err := h.svc.ListTransfers(r.Context(),
		r.URL.Query().Get("branch_id"), r.URL.Query().Get("status"), intParam(r, "limit", 0))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"transfers": rows})
}

func (h *Handler) getTransfer(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	t, err := h.svc.GetTransfer(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) createTransfer(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in CreateTransferInput
	if !decodeBody(w, r, &in) {
		return
	}
	in.CreatedBy = identity.StaffUserIDFromContext(r.Context())
	t, err := h.svc.CreateTransfer(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// POST /api/v1/inventory/transfers/{id}/ship, loads the truck, and captures the promised arrival.
//
// THE BODY IS OPTIONAL, IN BOTH SENSES (DIB-472):
//
//	no body / no eta field → no opinion. The branch pair's configured transit time fills the column,
//	                         and an unconfigured pair leaves it NULL. This is byte-for-byte the
//	                         behaviour every caller had before 0158, which is why the shipped
//	                         `body: '{}'` client and a bodyless curl both still work.
//	"eta": "2026-08-20"    → the clerk edited the pre-filled date. Stored as typed (after grading).
//	"eta": ""              → the clerk CLEARED it. Stored as NULL, a promise deliberately withheld,
//	                         which the server must not overwrite with a default.
//
// The pointer is what makes those three distinguishable: a plain string field could not tell "absent"
// from "cleared", and the two mean opposite things here.
func (h *Handler) shipTransfer(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req struct {
		ETA *string `json:"eta"`
	}
	if !decodeOptionalBody(w, r, &req) {
		return
	}
	eta := ShipETA{}
	if req.ETA != nil {
		eta = ShipETA{Supplied: true, Value: *req.ETA}
	}
	t, err := h.svc.ShipTransferWithETA(r.Context(), r.PathValue("id"), identity.StaffUserIDFromContext(r.Context()), eta)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// - DIB-472 · the admin-maintained transit times -

// GET /api/v1/inventory/transit-days, every ordered branch pair, configured or not.
//
// RequireStaff, because this read is what the ship flow pre-fills FROM: a manager loading a truck has
// to be able to see the default they are about to accept, and "how long does the Kingston run take" is
// a counter-level fact, not a secret. The WRITE below is the admin gate.
func (h *Handler) transitDays(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	pairs, err := h.svc.TransitDaysConfig(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// `[]`, never null. A single-branch deployment has no routes, and that is an answer.
	if pairs == nil {
		pairs = []TransitDaysPair{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"pairs": pairs})
}

// PUT /api/v1/inventory/transit-days, store or clear one route's transit time. requireAdmin.
func (h *Handler) putTransitDays(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in SetTransitDaysInput
	if !decodeBody(w, r, &in) {
		return
	}
	in.UpdatedBy = identity.StaffUserIDFromContext(r.Context())
	out, err := h.svc.SetTransitDays(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) receiveTransfer(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	t, err := h.svc.ReceiveTransfer(r.Context(), r.PathValue("id"), identity.StaffUserIDFromContext(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) cancelTransfer(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	t, err := h.svc.CancelTransfer(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// - DIB-72 replenishment -

func (h *Handler) shortfall(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	wl, err := h.svc.ShortfallWorklist(r.Context(), r.URL.Query().Get("branch_id"), intParam(r, "limit", 0))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// on_order_degraded is ALWAYS present rather than omitted when false: a flag that appears only in
	// the broken case is one a client forgets to read. It is false on every wired deployment, so the
	// surface renders nothing for it, see DIB-470.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shortfall":         wl.Rows,
		"on_order_degraded": wl.OnOrderDegraded,
	})
}

func (h *Handler) listReorderPoints(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	rows, err := h.svc.ListReorderPoints(r.Context(), r.URL.Query().Get("branch_id"), intParam(r, "limit", 0))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reorder_points": rows})
}

func (h *Handler) putReorderPoint(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in ReorderPoint
	// A PUT with no is_active in the body means "keep this line managed", defaulting a bool to Go's
	// false here would silently retire a line every time someone edited its quantity.
	in.IsActive = true
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := h.svc.SetReorderPoint(r.Context(), in, identity.StaffUserIDFromContext(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) bootstrapReorderPoints(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req struct {
		BranchID string `json:"branch_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	n, err := h.svc.BootstrapReorderPoints(r.Context(), req.BranchID, identity.StaffUserIDFromContext(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"created": n})
}

func (h *Handler) createPOFromShortfall(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var req struct {
		BranchID   string   `json:"branch_id"`
		ProductIDs []string `json:"product_ids"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	draft, poID, poNo, err := h.svc.CreatePOFromShortfall(r.Context(), req.BranchID, req.ProductIDs)
	if err != nil {
		// The draft rides along even on the 503 the stub returns, so the surface can show the buyer
		// exactly what would have been ordered instead of an empty error toast.
		var ce clientError
		if ok := asClientError(err, &ce); ok && ce.status == http.StatusServiceUnavailable {
			httpx.ErrorWith(w, http.StatusServiceUnavailable, ce.msg, map[string]any{"draft": draft})
			return
		}
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"po_id": poID, "po_no": poNo, "draft": draft})
}
