// edit_history.go, DIB-175 the READ side of the staff order-edit trail: "who changed what on this
// order, from what to what, and when".
//
//	GET /api/v1/orders/{id}/edits → the ordered change history for one order (staff-gated)
//
// Like the DIB-159 status timeline it sits beside (history.go), this reconstructs the history from the
// append-only audit trail rather than a dedicated table, the same "audit-log IS the history store"
// pattern, so DIB-175 needs no migration. The two reads are deliberately separate surfaces over the
// same spine: ORDER_STATUS_CHANGED rows are the status timeline, ORDER_CHANGED rows are this one.
//
// Rows carry the audit request id, which the write path gets for free (audit.Service stamps it from
// the request context). That is what lets a client group the four rows a single qty edit emits -
// qty_entered, qty_base, unit_price_cents, line_total_cents, back into one operator action, with no
// correlation column and no schema change.
package order

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// OrderFieldChange is one column-level edit on an order, with attribution.
type OrderFieldChange struct {
	Field      string    `json:"field"`                // the order/order_lines column that moved
	LineNo     int       `json:"line_no"`              // the line it moved on; 0 = an order-header field
	Before     string    `json:"before,omitempty"`     // pre-image ("" for a creation)
	After      string    `json:"after,omitempty"`      // post-image ("" for a removal)
	ActorID    string    `json:"actor_id,omitempty"`   // audit actor_id
	ActorName  string    `json:"actor_name,omitempty"` // users.display_name when the actor is staff
	RequestID  string    `json:"request_id,omitempty"` // groups the rows emitted by ONE edit
	OccurredAt time.Time `json:"occurred_at"`          // audit_log.created_at (display only)
	seq        int64     // audit_log.seq, the CAUSAL sort key (B2-1); unexported, not on the wire
}

// OrderEditHistory is one order's ordered change history (oldest → newest).
type OrderEditHistory struct {
	OrderID string             `json:"order_id"`
	Changes []OrderFieldChange `json:"changes"`
}

// OrderEditHistoryFor returns the ordered change history for one order.
//
// AUTHZ is identical to the status timeline (history.go): staff-gating at the route proves the caller
// is staff but NOT that they may reach THIS order, so the trusted caller is resolved, the order's
// owning branch loaded, and the read gated on CanAccessBranch. A MISSING order is ErrNotFound (never a
// leak of existence through the authz path); an existing order in an unreachable branch is ErrForbidden.
func (s *Service) OrderEditHistoryFor(ctx context.Context, orderID string) (OrderEditHistory, error) {
	if orderID == "" {
		return OrderEditHistory{}, httpx.BadRequestf("order id is required")
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return OrderEditHistory{}, err
	}
	// DIB-379/F3: the visible order_no is an accepted alternate key here too, resolved once so the
	// branch check and the audit-trail read key off the same id (mirrors OrderHistory). B2-4: scoped to
	// the caller's branches so a cross-branch #SO answers 404.
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, err = s.store.ResolveOrderID(ctx, orderID, rGlobal, rBranches)
	if err != nil {
		return OrderEditHistory{}, err
	}
	branchID, err := s.store.OrderBranch(ctx, orderID)
	if err != nil {
		return OrderEditHistory{}, err // ErrNotFound for a missing order, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return OrderEditHistory{}, ErrForbidden
	}
	out := OrderEditHistory{OrderID: orderID, Changes: []OrderFieldChange{}}
	if s.auditTrail == nil {
		// Seam unwired, the history is genuinely empty rather than a 501, so the UI renders "no
		// changes" instead of an error (the order may simply never have been edited).
		return out, nil
	}
	entries, err := s.auditTrail.ReopenTransaction(ctx, "order", orderID)
	if err != nil {
		return OrderEditHistory{}, err
	}
	var actorIDs []string
	for _, e := range entries {
		if e.Action != audit.ActOrderChanged {
			continue
		}
		field := auditStr(e.Changes["field"])
		if field == "" {
			// A pre-DIB-175 ORDER_CHANGED row (the portal delivery-instructions and reorder-creation
			// records, which carry no `field`). They are real order changes but not column-level edits,
			// so they have no before→after to render, skip rather than emit a blank row.
			continue
		}
		c := OrderFieldChange{
			Field:     field,
			LineNo:    auditInt(e.Changes["line_no"]),
			Before:    auditScalar(e.Changes["before"]),
			After:     auditScalar(e.Changes["after"]),
			ActorID:   e.ActorID,
			RequestID: e.RequestID,
			seq:       e.Seq,
		}
		if at, perr := time.Parse(time.RFC3339Nano, e.CreatedAt); perr == nil {
			c.OccurredAt = at
		}
		out.Changes = append(out.Changes, c)
		if e.ActorID != "" {
			actorIDs = append(actorIDs, e.ActorID)
		}
	}
	// Oldest → newest by the audit SEQUENCE (0078), not the wall-clock timestamp (B2-1). created_at is
	// the transaction time, so every row of one edit shares it and two concurrent edits can carry
	// created_at values that invert relative to the order they committed in. seq is assigned at insert
	// time under the order-row lock that serializes same-order edits, so it is the true causal order:
	// the rows of one qty edit stay in qty→base→price→total order, and successive edits stay in the
	// order they actually happened. Stable so a fake trail with seq==0 keeps insertion order.
	sort.SliceStable(out.Changes, func(i, j int) bool {
		return out.Changes[i].seq < out.Changes[j].seq
	})
	if s.names != nil && len(actorIDs) > 0 {
		if m, nerr := s.names.ActorNames(ctx, actorIDs); nerr == nil {
			for i := range out.Changes {
				if n := m[out.Changes[i].ActorID]; n != "" {
					out.Changes[i].ActorName = n
				}
			}
		}
		// Name resolution failing degrades to actor ids, it never fails the whole read.
	}
	return out, nil
}

// auditScalar renders an audit changes value as the string the history wire format carries. Values
// round-trip through jsonb, so an integer written as int64 comes back as float64, money must render
// as "1250", never "1250.000000" or "1.25e+03".
func auditScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

// auditInt reads an integer out of an audit changes blob (jsonb numbers arrive as float64).
func auditInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	default:
		return 0
	}
}
