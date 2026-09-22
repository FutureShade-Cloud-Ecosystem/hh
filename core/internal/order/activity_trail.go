// activity_trail.go, DIB-494. One order's WHOLE recorded trail, as one caller-scoped read.
// (Union train 10: this lane shipped the file as activity.go; DIB-492/493 landed its own
// activity.go, the staff panel read + human notes, in the same train, so this one was renamed.
// Content is the lane's, unchanged. The notes-interleave follow-up remains P1 in NOTES-FIX-494.md.)
//
// The order already answers "how did its status move" (history.go, DIB-159) and "who edited which
// field" (edit_history.go, DIB-175), two staff surfaces over the same append-only audit spine.
// This read is their UNION, shaped for a consumer that wants the one question a person actually
// asks ("what happened on this order?") answered in a single pass: every ORDER_STATUS_CHANGED row
// and every column-level ORDER_CHANGED row, merged into one causally ordered timeline.
//
// IT SHOWS NOTHING THE TWO EXISTING SURFACES DO NOT. The row filters are copied from OrderHistory
// and OrderEditHistoryFor verbatim, status rows, and field rows with a `field` key, so a consumer
// of this read sees exactly the union of the History and Edits panels, never a wider projection of
// audit_log. A pre-DIB-175 ORDER_CHANGED row with no `field` is skipped here for the same reason
// edit_history.go skips it: it has no before→after to render.
//
// THE AUTHORITY IS SummaryByNumber's, LITERALLY. This read resolves the reference THROUGH
// SummaryByNumber, the same branch-scoped ResolveOrderID, the same post-load reaches() check, the
// same collapse of "no such order" and "not your branch" into found=false, so it can never surface
// a trail whose order the summary read would refuse. One fence, used twice, rather than a second
// fence that can drift.
package order

import (
	"context"
	"sort"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// Activity event kinds. Two, matching the two trails this read merges.
const (
	ActivityStatus = "STATUS" // an ORDER_STATUS_CHANGED row: from→to, who, when, why
	ActivityEdit   = "EDIT"   // a column-level ORDER_CHANGED row: field, before→after, who, when
)

// OrderActivityEvent is one recorded change on an order, a status transition or a field edit -
// with attribution. The zero values of the half that does not apply are simply absent on the wire
// of whoever consumes this (a STATUS event has no Field; an EDIT event has no ToStatus).
type OrderActivityEvent struct {
	Kind       string    `json:"kind"`                  // ActivityStatus | ActivityEdit
	FromStatus string    `json:"from_status,omitempty"` // "" at the first CONFIRMED (creation)
	ToStatus   string    `json:"to_status,omitempty"`
	Reason     string    `json:"reason,omitempty"` // optional short reason recorded on a transition
	Field      string    `json:"field,omitempty"`  // the orders/order_lines column that moved
	LineNo     int       `json:"line_no,omitempty"`
	Before     string    `json:"before,omitempty"`
	After      string    `json:"after,omitempty"`
	ActorID    string    `json:"actor_id,omitempty"`
	ActorName  string    `json:"actor_name,omitempty"` // users.display_name; "" for a portal/system actor
	OccurredAt time.Time `json:"occurred_at"`
	seq        int64     // audit_log.seq, the CAUSAL sort key (B2-1); unexported, as in edit_history.go
}

// ActivityByNumber resolves a visible order number (or id) within the caller's reach and returns
// its headline summary plus the merged change trail, oldest → newest.
//
// found=false is an ANSWER, the same three-way collapse SummaryByNumber makes (no such number, a
// number in another branch, no branch grants at all), and it is the summary read that makes it, so
// the two cannot disagree. An unwired audit seam returns an EMPTY trail rather than an error: the
// order is real, it simply has nothing recorded, which is an ordinary state (history.go says the
// same thing about its own nil-seam path).
func (s *Service) ActivityByNumber(ctx context.Context, scope ReadScope, ref string) (Summary, []OrderActivityEvent, bool, error) {
	sum, found, err := s.SummaryByNumber(ctx, scope, ref)
	if err != nil || !found {
		return Summary{}, nil, false, err
	}
	events := []OrderActivityEvent{}
	if s.auditTrail == nil {
		return sum, events, true, nil
	}
	entries, err := s.auditTrail.ReopenTransaction(ctx, "order", sum.OrderID)
	if err != nil {
		return Summary{}, nil, false, err
	}
	var actorIDs []string
	for _, e := range entries {
		ev, ok := activityEventFrom(e)
		if !ok {
			continue
		}
		events = append(events, ev)
		if ev.ActorID != "" {
			actorIDs = append(actorIDs, ev.ActorID)
		}
	}
	// Oldest → newest by the audit SEQUENCE (0078), exactly as edit_history.go orders its rows and
	// for the same reason: created_at is transaction time, shared by every row of one edit and able
	// to invert across concurrent edits, while seq is assigned under the order-row lock. Stable so a
	// fake trail with seq==0 keeps insertion order.
	sort.SliceStable(events, func(i, j int) bool { return events[i].seq < events[j].seq })
	if s.names != nil && len(actorIDs) > 0 {
		if m, nerr := s.names.ActorNames(ctx, actorIDs); nerr == nil {
			for i := range events {
				if n := m[events[i].ActorID]; n != "" {
					events[i].ActorName = n
				}
			}
		}
		// Name resolution failing degrades to actor ids, it never fails the whole read.
	}
	return sum, events, true, nil
}

// activityEventFrom shapes one audit row into an activity event, or reports that the row is not
// part of the trail this read shows. The two accept-rules are OrderHistory's and
// OrderEditHistoryFor's, copied rather than loosened:
//
//	ORDER_STATUS_CHANGED       → always a STATUS event
//	ORDER_CHANGED with `field` → an EDIT event; without `field` (pre-DIB-175 rows) → skipped
//	anything else              → skipped (the coarse HTTP rows are the admin viewer's, not this one's)
func activityEventFrom(e audit.Entry) (OrderActivityEvent, bool) {
	switch e.Action {
	case audit.ActOrderStatusChanged:
		ev := OrderActivityEvent{
			Kind:       ActivityStatus,
			FromStatus: auditStr(e.Changes["old_status"]),
			ToStatus:   auditStr(e.Changes["new_status"]),
			Reason:     auditStr(e.Changes["reason"]),
			ActorID:    e.ActorID,
			seq:        e.Seq,
		}
		if at, perr := time.Parse(time.RFC3339Nano, e.CreatedAt); perr == nil {
			ev.OccurredAt = at
		}
		return ev, true
	case audit.ActOrderChanged:
		field := auditStr(e.Changes["field"])
		if field == "" {
			return OrderActivityEvent{}, false
		}
		ev := OrderActivityEvent{
			Kind:    ActivityEdit,
			Field:   field,
			LineNo:  auditInt(e.Changes["line_no"]),
			Before:  auditScalar(e.Changes["before"]),
			After:   auditScalar(e.Changes["after"]),
			ActorID: e.ActorID,
			seq:     e.Seq,
		}
		if at, perr := time.Parse(time.RFC3339Nano, e.CreatedAt); perr == nil {
			ev.OccurredAt = at
		}
		return ev, true
	default:
		return OrderActivityEvent{}, false
	}
}
