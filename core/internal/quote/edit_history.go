// edit_history.go, the quote change-history read (DIB-56).
//
// The trail itself is written by the guarded writes (edit.go/edit_repository.go) as one QUOTE_CHANGED
// audit row per column that moved. This is the projection: filter the entity's audit rows down to
// column-level changes, sort them CAUSALLY, and put a human name on the actor.
package quote

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// QuoteFieldChange is one column-level change on a quote, as the history wire format carries it.
type QuoteFieldChange struct {
	Field      string    `json:"field"`                // the quotes/quote_lines column that moved
	LineNo     int       `json:"line_no"`              // the line it moved on; 0 = a quote-header field
	Before     string    `json:"before,omitempty"`     // pre-image ("" for a creation)
	After      string    `json:"after,omitempty"`      // post-image ("" for a removal)
	ActorID    string    `json:"actor_id,omitempty"`   // audit actor_id
	ActorName  string    `json:"actor_name,omitempty"` // users.display_name when the actor is staff
	RequestID  string    `json:"request_id,omitempty"` // groups the rows emitted by ONE edit
	OccurredAt time.Time `json:"occurred_at"`          // audit_log.created_at (display only)

	// seq is audit_log.seq, the CAUSAL sort key. Unexported: it is an internal ordering device, not
	// something a client should key off, so it never crosses the wire.
	seq int64
}

// QuoteEditHistoryOut is one quote's ordered change history (oldest → newest).
type QuoteEditHistoryOut struct {
	QuoteID string             `json:"quote_id"`
	Changes []QuoteFieldChange `json:"changes"`
}

// QuoteEditHistory returns the ordered change history for one quote.
//
// AUTHZ: staff-gating at the route proves the caller is staff but NOT that they may reach THIS quote, so
// the trusted caller is resolved, the quote's owning branch loaded, and the read gated on
// CanAccessBranch. A MISSING quote is ErrNotFound (never a leak of existence through the authz path); an
// existing quote in an unreachable branch is ErrForbidden.
//
// Note this read is deliberately NOT expiry-gated: an expired quote's history is still readable. Expiry
// stops you CHANGING a quote, not from seeing what happened to it, a trail you lose access to when the
// document lapses is not an audit trail.
func (s *StaffService) QuoteEditHistory(ctx context.Context, quoteID string) (QuoteEditHistoryOut, error) {
	if quoteID == "" {
		return QuoteEditHistoryOut{}, httpx.BadRequestf("quote id is required")
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return QuoteEditHistoryOut{}, err
	}
	if caller.Status != identity.StatusActive {
		return QuoteEditHistoryOut{}, ErrForbidden
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return QuoteEditHistoryOut{}, err
	}
	_, branchID, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return QuoteEditHistoryOut{}, err // ErrNotFound for a missing quote, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return QuoteEditHistoryOut{}, ErrForbidden
	}
	out := QuoteEditHistoryOut{QuoteID: quoteID, Changes: []QuoteFieldChange{}}
	if s.trail == nil {
		// Seam unwired, the history is genuinely empty rather than a 501, so the UI renders "no
		// changes" instead of an error (the quote may simply never have been edited).
		return out, nil
	}
	entries, err := s.trail.ReopenTransaction(ctx, "quote", quoteID)
	if err != nil {
		return QuoteEditHistoryOut{}, err
	}
	var actorIDs []string
	for _, e := range entries {
		if e.Action != audit.ActQuoteChanged {
			continue
		}
		field := auditScalar(e.Changes["field"])
		if field == "" {
			// A QUOTE_CHANGED row with no `field` is not a column-level edit and has no before→after to
			// render, skip rather than emit a blank row. (None exist today; this is the same defence the
			// order history needed when pre-DIB-175 rows turned up in its trail.)
			continue
		}
		c := QuoteFieldChange{
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
	// Oldest → newest by the audit SEQUENCE (0078), not the wall-clock timestamp. created_at is the
	// TRANSACTION time, so every row of one edit shares it to the microsecond and two concurrent edits
	// can carry created_at values that invert relative to the order they committed in. seq is assigned at
	// insert time under the quote-row lock that serializes same-quote edits, so it is the true causal
	// order: the rows of one qty edit stay in qty→base→price→total order, and successive edits stay in
	// the order they actually happened. Stable, so a fake trail with seq==0 keeps insertion order.
	sort.SliceStable(out.Changes, func(i, j int) bool {
		return out.Changes[i].seq < out.Changes[j].seq
	})
	if s.actors != nil && len(actorIDs) > 0 {
		if m, nerr := s.actors.ActorNames(ctx, actorIDs); nerr == nil {
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
// round-trip through jsonb, so an integer written as int64 comes back as float64, money must render as
// "1250", never "1250.000000" or "1.25e+03".
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
