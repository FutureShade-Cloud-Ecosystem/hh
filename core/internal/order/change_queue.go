package order

// change_queue.go, the cross order change read (SCOPE:F10 stages 2 and 3).
//
//	GET /api/v1/order-changes?status=PENDING_YARD     active in-branch staff
//
// Yard Pick lists "changes waiting on the yard" (PENDING_YARD) and Manager Home "changes waiting on
// you" (PENDING_FINANCE) from this one read. Every other change route is keyed by an order or a
// change id; this is the only one that spans orders.
//
// - RULES -
// BRANCH FENCE: the caller sees changes of the branches it holds and no other, the same split
// callerBranchScope gives the order reads. A global caller reaches every branch.
//
// MONEY: no money is computed here. delta_cents and finance_threshold_cents are the numbers stored
// when the delta was priced at request time (ruling 2 in change.go), passed through unchanged.
//
// NEXT ACTIONS are the ones GetChange gives this caller, from the one function both read
// (Service.changeActions): sign_apply and finance_approve are dropped on a PENDING_FINANCE row when the
// caller is not a named ORDER_CHANGE_OVER_THRESHOLD approver for the change's branch, so a list never offers
// a button the route would refuse. Every row is still shown; only the actions narrow.

import (
	"context"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
)

// WithRequesterRoles injects the requester role label reader.
func (s *Service) WithRequesterRoles(r RequesterRoleReader) *Service { s.requesterRoles = r; return s }

// ChangeQueueRequest is one page request: an optional status filter and the resume key.
type ChangeQueueRequest struct {
	Status string
	Cursor string // the decoded resume key, "" for the first page
	Limit  int
}

// ChangeQueuePage is the page and the key to resume from, "" when it is the last page.
type ChangeQueuePage struct {
	Items   []ChangeQueueItem
	NextKey string
}

var changeStatuses = map[string]bool{
	ChangeStatusPendingYard: true, ChangeStatusPendingFinance: true, ChangeStatusApproved: true,
	ChangeStatusApplied: true, ChangeStatusRejected: true, ChangeStatusCancelled: true,
}

const changeQueueCursorPrefix = "chq:"
const changeQueueTimeLayout = "2006-01-02T15:04:05.000000Z"

func changeQueueCursorKey(it ChangeQueueItem) string {
	return changeQueueCursorPrefix + it.cursorAt + "|" + it.ID
}

// parseChangeQueueCursor decodes a minted key. ok is false for a malformed one; an empty key is the
// first page.
func parseChangeQueueCursor(key string) (at, id string, has, ok bool) {
	if key == "" {
		return "", "", false, true
	}
	rest, found := strings.CutPrefix(key, changeQueueCursorPrefix)
	if !found {
		return "", "", false, false
	}
	at, id, found = strings.Cut(rest, "|")
	if !found || !looksLikeUUID(id) {
		return "", "", false, false
	}
	if _, err := time.Parse(changeQueueTimeLayout, at); err != nil {
		return "", "", false, false
	}
	return at, id, true, true
}

// ListChangeQueue reads change requests across orders for the caller's branches, oldest first.
func (s *Service) ListChangeQueue(ctx context.Context, in ChangeQueueRequest) (ChangeQueuePage, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ChangeQueuePage{}, err
	}
	if caller.Status != identity.StatusActive {
		return ChangeQueuePage{}, ErrForbidden
	}
	status := strings.ToUpper(strings.TrimSpace(in.Status))
	if status != "" && !changeStatuses[status] {
		return ChangeQueuePage{}, httpx.BadRequestf(
			"unknown status %q (PENDING_YARD|PENDING_FINANCE|APPROVED|APPLIED|REJECTED|CANCELLED)", in.Status)
	}
	afterAt, afterID, hasAfter, ok := parseChangeQueueCursor(in.Cursor)
	if !ok {
		return ChangeQueuePage{}, httpx.BadRequestf("cursor is malformed")
	}
	qs, ok := s.store.(changeQueueStore)
	if !ok {
		return ChangeQueuePage{}, ErrChangeNotConfigured
	}
	limit := in.Limit
	if limit < 1 {
		limit = httpx.DefaultPageLimit
	}
	global, branches := callerBranchScope(caller)
	items, err := qs.ListChangeQueue(ctx, changeQueueQuery{
		Status: status, Global: global, Branches: branches,
		HasAfter: hasAfter, AfterAt: afterAt, AfterID: afterID, FetchRows: limit + 1,
	})
	if err != nil {
		return ChangeQueuePage{}, err
	}
	page := ChangeQueuePage{}
	if len(items) > limit {
		items = items[:limit]
		page.NextKey = changeQueueCursorKey(items[len(items)-1])
	}

	labels := map[string]string{}
	if s.requesterRoles != nil {
		ids := make([]string, 0, len(items))
		for _, it := range items {
			if it.requestedByID != "" {
				ids = append(ids, it.requestedByID)
			}
		}
		if labels, err = s.requesterRoles.RoleLabelsForUsers(ctx, ids); err != nil {
			return ChangeQueuePage{}, err
		}
	}
	approver := map[string]bool{} // branch id -> caller is a named approver there
	for i := range items {
		it := &items[i]
		it.RequestedByRole = labels[it.requestedByID]
		it.NextActions = s.changeActions(ctx, caller, it.Status, it.BranchID, it.requestedByID, approver)
	}
	page.Items = items
	return page, nil
}

func (h *Handler) listChangeQueue(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	p, err := httpx.ParseCursorPage(r)
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	page, err := h.svc.ListChangeQueue(r.Context(), ChangeQueueRequest{
		Status: r.URL.Query().Get("status"), Cursor: p.Cursor, Limit: p.Limit,
	})
	if err != nil {
		h.writeChangeErr(w, err)
		return
	}
	httpx.WriteCursorPage(w, page.Items, page.NextKey, p.Limit)
}
