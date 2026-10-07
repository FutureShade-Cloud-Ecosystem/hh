package tasks

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// yard_handler.go, the Wave 2 yard REST surface, registered SEPARATELY from the 0073 task routes
// (RegisterYardRoutes, called from the composition root) so the shipped surface is untouched and the
// yard surface can be wired, or not, on its own.
//
// Every route is staff-gated exactly like the 0073 routes: the picker/counter identity comes from the
// verified StaffContext the guard stamps, never from the request body. A yard worker cannot report a
// shortage "as" somebody else.

// RegisterYardRoutes wires the pick → stage → shortage surface:
//
//	GET  /api/v1/yard/pick-queue                      the yard board (tasks + tickets + open shortages)
//	GET  /api/v1/yard/pending-tickets                 the raw PickSource feed (not yet queued)
//	GET  /api/v1/yard/picks/{id}                      one pick: task + ticket + full shortage history
//	POST /api/v1/yard/picks/{id}/stage                IN_PROGRESS → STAGED (+ counter EVENT)
//	PUT  /api/v1/yard/picks/{id}/lines                record what was ACTUALLY pulled, per line (0187)
//	PUT  /api/v1/yard/picks/{id}/skids                record or change the order's skid count, with a reason (0370)
//	POST /api/v1/yard/picks/{id}/shortages            report a shortage (+ counter EVENT)
//	POST /api/v1/yard/picks/{id}/shortages/{sid}/resolve   clear one (+ counter EVENT)
func (h *Handler) RegisterYardRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/yard/pick-queue", g.RequireStaff(h.staff(h.pickQueue)))
	r.Handle("GET /api/v1/yard/pending-tickets", g.RequireStaff(h.staff(h.pendingTickets)))
	r.Handle("GET /api/v1/yard/picks/{id}", g.RequireStaff(h.staff(h.pickDetail)))
	r.Handle("POST /api/v1/yard/picks/{id}/stage", g.RequireStaff(h.staff(h.stage)))
	r.Handle("PUT /api/v1/yard/picks/{id}/lines", g.RequireStaff(h.staff(h.recordPick)))
	r.Handle("PUT /api/v1/yard/picks/{id}/skids", g.RequireStaff(h.staff(h.recordSkids)))
	r.Handle("POST /api/v1/yard/picks/{id}/shortages", g.RequireStaff(h.staff(h.reportShortage)))
	r.Handle("POST /api/v1/yard/picks/{id}/shortages/{sid}/resolve", g.RequireStaff(h.staff(h.resolveShortage)))
}

// yardBranchFence applies the program's branch posture (UNION-W3, C's park 2, the
// landedcost/dispatch rule) to the yard ENUMERATION routes: a branch-scoped caller must name a
// branch they can reach (empty branch would enumerate every branch's queue); a GLOBAL caller may
// still list everything. Returns false after writing the refusal.
func (h *Handler) yardBranchFence(w http.ResponseWriter, r *http.Request) bool {
	caller, ok := identity.StaffContextFromContext(r.Context())
	if !ok || caller.UserID == "" {
		httpx.Error(w, http.StatusUnauthorized, "yard: unauthenticated")
		return false
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		if caller.Scope != identity.ScopeGlobal {
			httpx.Error(w, http.StatusForbidden, "yard: a branch-scoped caller must name a reachable branch_id")
			return false
		}
		return true
	}
	if !caller.CanAccessBranch(branchID) {
		httpx.Error(w, http.StatusForbidden, "yard: branch out of scope")
		return false
	}
	return true
}

// taskBranchFence holds a yard WRITE to the branch of the task it names: a branch-scoped caller must
// reach that branch, a GLOBAL caller reaches every one. The recording doors change an order's money
// and tell its rep, so a yard caller at one branch must not be able to reach another branch's order
// by naming its task. An unknown task is the same 404 every other door answers, never a leak. Returns
// false after writing the refusal.
func (h *Handler) taskBranchFence(w http.ResponseWriter, r *http.Request) bool {
	caller, ok := identity.StaffContextFromContext(r.Context())
	if !ok || caller.UserID == "" {
		httpx.Error(w, http.StatusUnauthorized, "yard: unauthenticated")
		return false
	}
	t, err := h.svc.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeYardErr(w, r, err)
		return false
	}
	if t.BranchID != "" && !caller.CanAccessBranch(t.BranchID) {
		httpx.Error(w, http.StatusForbidden, "yard: branch out of scope")
		return false
	}
	return true
}

func (h *Handler) pickQueue(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !h.yardBranchFence(w, r) {
		return
	}
	// Cursor pagination (HH-3.2 B5), the same stable-ordering basis the
	// staff task list records (open-first flag, priority rank, created_at,
	// id tie-breaker): the yard board is the same query projection joined
	// to its tickets in memory.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	active, rank, created, id, ok := parseTaskCursor(p.Cursor)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	items, err := h.svc.PickQueue(r.Context(), TaskFilter{
		Status:          r.URL.Query().Get("status"),
		BranchID:        r.URL.Query().Get("branch_id"),
		Limit:           p.Limit + 1,
		AfterCursor:     p.Cursor != "",
		CursorActive:    active,
		CursorPriority:  rank,
		CursorCreatedAt: created,
		CursorID:        id,
	})
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		next = taskCursorKey(items[len(items)-1].Task)
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

func (h *Handler) pendingTickets(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !h.yardBranchFence(w, r) {
		return
	}
	// Cursor pagination (HH-3.2 B5). THE WHOLE WINDOW GOES THROUGH, as it
	// always has: the feed is an in-memory merge of two dispatch reads,
	// and the pin's own recorded rule is that a merge cannot be paged at
	// the database ("the k-th row of the merged feed is not the k-th row
	// of either source"), so the resume token carries the merged feed's
	// position rather than a row key, opaque on the wire like every
	// cursor. Fetch limit+1, drop the extra row, mint next_cursor from the
	// position the last kept row ends at. Recorded as this endpoint's
	// convention difference: the stable-ordering basis is the merge's
	// (scheduled date, source rank) order, which has no unique row key the
	// seam could filter on without crossing module lines.
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	offset, ok := parseFeedPositionCursor(p.Cursor)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	items, _, err := h.svc.PendingTickets(r.Context(), r.URL.Query().Get("branch_id"), p.Limit+1, offset)
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	next := ""
	if len(items) > p.Limit {
		items = items[:p.Limit]
		next = feedPositionCursorKey(offset + p.Limit)
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// feedPositionCursorKey mints the pending-feed resume token: the merged
// feed's position (an offset into a stable merge, the paging unit the
// pin's seam design fixes), opaque under the shared v1 cursor encoding.
func feedPositionCursorKey(offset int) string {
	return "feed:" + strconv.Itoa(offset)
}

// parseFeedPositionCursor decodes a minted feed position; ok is false for
// an empty cursor (first page) or a malformed one.
func parseFeedPositionCursor(key string) (int, bool) {
	if key == "" {
		return 0, true
	}
	if !strings.HasPrefix(key, "feed:") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(key, "feed:"))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func (h *Handler) pickDetail(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	e, err := h.svc.PickDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

type recordPickRequest struct {
	Lines []struct {
		OrderLineNo int    `json:"order_line_no"`
		ProductID   string `json:"product_id"`
		SKU         string `json:"sku"`
		Description string `json:"description"`
		QtyOrdered  string `json:"qty_ordered"`
		QtyPicked   string `json:"qty_picked"`
		UOM         string `json:"uom"`
		ShortReason string `json:"short_reason"`
		ShortNote   string `json:"short_note"`
	} `json:"lines"`
}

// recordPick writes what the yard actually pulled. PUT rather than POST because the whole recorded
// pick is replaced by what is sent: a picker who corrects a number is not appending a second opinion,
// and the schema's UNIQUE (task_id, order_line_no) says the same thing in the database.
func (h *Handler) recordPick(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !h.taskBranchFence(w, r) {
		return
	}
	var req recordPickRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	lines := make([]PickedLineInput, 0, len(req.Lines))
	for _, l := range req.Lines {
		lines = append(lines, PickedLineInput{
			OrderLineNo: l.OrderLineNo, ProductID: l.ProductID, SKU: l.SKU,
			Description: l.Description, QtyOrdered: l.QtyOrdered, QtyPicked: l.QtyPicked, UOM: l.UOM,
			ShortReason: l.ShortReason, ShortNote: l.ShortNote,
		})
	}
	picked, err := h.svc.RecordPick(r.Context(), r.PathValue("id"),
		identity.StaffUserIDFromContext(r.Context()), lines)
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": picked})
}

type recordSkidsRequest struct {
	SkidCount *int   `json:"skid_count"`
	Reason    string `json:"reason"`
	Note      string `json:"note"`
}

type recordSkidsResponse struct {
	SkidCount            int    `json:"skid_count"`
	PreviousSkidCount    *int   `json:"previous_skid_count"`
	DepositCents         int64  `json:"deposit_cents"`
	PreviousDepositCents int64  `json:"previous_deposit_cents"`
	Changed              bool   `json:"changed"`
	OrderNo              string `json:"order_no"`
}

// recordSkids records or changes the order's skid count with a reason. PUT, because the count sent
// replaces the recorded one. The actor is the verified staff caller, never the body.
func (h *Handler) recordSkids(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if !h.taskBranchFence(w, r) {
		return
	}
	var req recordSkidsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SkidCount == nil {
		httpx.Error(w, http.StatusBadRequest, "skid_count is required, zero is an answer")
		return
	}
	res, err := h.svc.RecordSkids(r.Context(), SkidInput{
		TaskID: r.PathValue("id"), ActorID: identity.StaffUserIDFromContext(r.Context()),
		SkidCount: *req.SkidCount, Reason: req.Reason, Note: req.Note,
	})
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, recordSkidsResponse{
		SkidCount: res.CountAfter, PreviousSkidCount: res.CountBefore, DepositCents: res.DepositAfterCents,
		PreviousDepositCents: res.DepositBeforeCents, Changed: res.Changed, OrderNo: res.Order.OrderNo,
	})
}

type stageRequest struct {
	Location string `json:"location"`
	Ref      string `json:"ref"`  // optional; derived when blank
	Note     string `json:"note"` // optional; recorded on the task_events row
}

func (h *Handler) stage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req stageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := h.svc.Stage(r.Context(), StageInput{
		TaskID:   r.PathValue("id"),
		ActorID:  identity.StaffUserIDFromContext(r.Context()),
		Location: req.Location,
		Ref:      req.Ref,
		Note:     req.Note,
	})
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

type shortageRequest struct {
	OrderLineNo *int   `json:"order_line_no"`
	ProductID   string `json:"product_id"`
	SKU         string `json:"sku"`
	Description string `json:"description"`
	QtyShort    string `json:"qty_short"`
	UOM         string `json:"uom"`
	Note        string `json:"note"`
}

func (h *Handler) reportShortage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req shortageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sh, err := h.svc.FlagShortage(r.Context(), ShortageInput{
		TaskID:      r.PathValue("id"),
		OrderLineNo: req.OrderLineNo,
		ProductID:   req.ProductID,
		SKU:         req.SKU,
		Description: req.Description,
		QtyShort:    req.QtyShort,
		UOM:         req.UOM,
		Note:        req.Note,
		ReportedBy:  identity.StaffUserIDFromContext(r.Context()),
	})
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sh)
}

type resolveShortageRequest struct {
	Resolution string `json:"resolution"`
}

func (h *Handler) resolveShortage(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req resolveShortageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sh, err := h.svc.ResolveShortage(r.Context(), ResolveShortageInput{
		TaskID:     r.PathValue("id"),
		ShortageID: r.PathValue("sid"),
		ActorID:    identity.StaffUserIDFromContext(r.Context()),
		Resolution: req.Resolution,
	})
	if err != nil {
		h.writeYardErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sh)
}

// writeYardErr adds the yard-specific errors to the shipped taxonomy: an unwired feed is 503 (the
// server is misconfigured, not the request malformed), an unknown shortage 404, and a double-resolve
// 409, the same shape a duplicate transition already returns.
func (h *Handler) writeYardErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNoPickSource):
		httpx.Error(w, http.StatusServiceUnavailable, "pick source not configured")
	case errors.Is(err, ErrShortageNotFound):
		httpx.Error(w, http.StatusNotFound, "shortage not found")
	case errors.Is(err, ErrYardOrderUnwired):
		httpx.Error(w, http.StatusServiceUnavailable, "yard order changes not configured")
	case errors.Is(err, ErrAboveOrdered):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrOrderRefused):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrShortageResolved):
		httpx.Error(w, http.StatusConflict, "shortage already resolved")
	default:
		h.writeErr(w, r, err)
	}
}
