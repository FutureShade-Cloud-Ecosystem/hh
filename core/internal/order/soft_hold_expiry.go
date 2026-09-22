package order

// soft_hold_expiry.go, DIB-422 item 4: the 'SOFT HOLD EXPIRED' projection.
//
// - THE SILENCE THIS FILLS -
// W5-A gave a quote SOFT holds: accepting a quote's lines raises reservations that keep stock
// notionally spoken for while the customer decides. Those holds carry an expires_at derived from the
// quote's own valid_until, and the EXISTING inventory.ExpireDueReservations sweep times them out -
// which is why W5-A's wiring block records that there was no new job to park.
//
// The sweep works. The problem is that it is SILENT. A hold expires, the stock returns to available,
// and the quote that was relying on it says nothing at all. The rep re-opens a three-week-old quote,
// sees the same prices and the same lines it had on the day it was sent, converts it, and discovers
// at CONFIRM, or worse, at the yard, that the material is gone. Nothing lied to them; nothing told
// them either. The quote's screen is exactly as confident as it was when the hold existed.
//
// This is the read that makes the expiry visible. It writes nothing and decides nothing: the sweep
// remains the only thing that ends a hold, and inventory remains the only owner of availability.
//
// - WHY THIS IS A PROJECTION AND NOT A FLAG ON THE QUOTE -
// The tempting shortcut is a boolean on the quote row, set by the sweep. It is wrong twice over:
// inventory would have to write a quote's column (a module writing a peer's table for a
// presentation fact), and the flag would immediately be able to disagree with the reservations it
// claims to describe, a hold re-raised by a re-priced line would leave a stale "expired" banner
// nobody can clear. Derived at read time from the holds themselves, the answer cannot be stale.
//
// - THE NARROW READ, AND ITS STATED EXCEPTION -
// It SELECTs stock_reservations, which is inventory's table. That is the SAME deliberate exception
// Repository.OrderHolds documents, with the same limits: it writes nothing, takes no lock, computes
// no availability, and every number that means anything, available, in-transit, the four-bucket
// view, is still read through inventory's own surface. If stock_reservations moves, these two
// queries are the single grep.
//
// - WHAT IS PARKED, AND EXACTLY WHERE -
// The QUOTE SURFACE binding. internal/quote and its app surface are outside this wave's fence, so
// this file ships the read and names the wiring rather than reaching into a module it does not own.
// The union binds one adapter over the seam quote already has for this shape:
//
//	quote.Service.WithSoftHoldExpiry(a) where a satisfies
//	    ExpiredSoftHolds(ctx context.Context, quoteID string) ([]order.ExpiredSoftHold, error)
//
// which is exactly *order.Service.ExpiredSoftHolds below, a one-line adapter in main.go, the same
// shape as w6cOrderInvoiceSource. Until then the projection is reachable and tested, and NOTHING
// renders it, which is the honest state rather than a half-built banner.

import (
	"context"
	"fmt"
)

// ResActive and ResExpired complete the status set stock_seam.go started. ResExpired in particular
// was NAMED in that file's comment ("the sweep timed a soft hold out, never written by this module;
// it is the jobs path") but never given a constant, because nothing read it until now.
const (
	ResActive  = "ACTIVE"
	ResExpired = "EXPIRED"
)

// ExpiredSoftHold is one line's worth of "the stock you were holding is no longer held".
//
// It deliberately carries the PRODUCT and the QUANTITY rather than just a count. "Some of your holds
// expired" sends a rep to compare two screens; "12 EA of Bergerac Pavers is no longer held" is a
// sentence they can act on, and acting on it is the entire point of surfacing this at all.
type ExpiredSoftHold struct {
	ReservationID string  `json:"reservation_id"`
	ProductID     string  `json:"product_id"`
	ProductName   string  `json:"product_name"`
	SKU           string  `json:"sku"`
	BranchID      string  `json:"branch_id"`
	QtyBase       float64 `json:"qty_base"`
	// Status is EXPIRED (the sweep timed it out) or RELEASED (something gave it back early). Both
	// mean "not held any more" and both are shown, because a rep does not care which mechanism let
	// the stock go, but the distinction is kept because "why did 40 units come back on Tuesday" is a
	// question a yard manager genuinely asks (the stock_seam.go ruling, applied to the read side).
	Status string `json:"status"`
	// ExpiresAt is when the hold was due to lapse; EndedAt is when it actually did.
	ExpiresAt string `json:"expires_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
}

// SoftHoldStatus is the whole answer for one quote, in the shape a surface renders directly.
type SoftHoldStatus struct {
	QuoteID string `json:"quote_id"`
	// ActiveCount is holds still standing. Reported alongside the expired ones so a banner can say
	// "3 of 5 lines are still held" rather than implying the whole quote has evaporated.
	ActiveCount int `json:"active_count"`
	// Expired is every hold that has lapsed, newest first.
	Expired []ExpiredSoftHold `json:"expired"`
	// AnyExpired is the one boolean a surface branches on. Derived, never stored.
	AnyExpired bool `json:"any_expired"`
}

// softHoldStore is the optional store capability this read needs, the shipStore precedent, so a
// store that cannot answer simply reports "nothing expired" instead of erroring a quote screen.
type softHoldStore interface {
	ExpiredSoftHolds(ctx context.Context, quoteID string) (SoftHoldStatus, error)
}

// ExpiredSoftHolds reports which of a quote's soft holds have lapsed.
//
// An unwired or incapable store returns an EMPTY status and no error, deliberately. This is a
// decoration on a quote screen; failing the whole read because a projection is unavailable would
// take a rep's quote away from them to tell them something about stock. Silence degrades to the
// pre-W6 behaviour, which is exactly what it was.
func (s *Service) ExpiredSoftHolds(ctx context.Context, quoteID string) (SoftHoldStatus, error) {
	ss, ok := s.store.(softHoldStore)
	if !ok {
		return SoftHoldStatus{QuoteID: quoteID}, nil
	}
	return ss.ExpiredSoftHolds(ctx, quoteID)
}

// ExpiredSoftHolds is the projection. See this file's header for why it reads inventory's table.
func (r *Repository) ExpiredSoftHolds(ctx context.Context, quoteID string) (SoftHoldStatus, error) {
	out := SoftHoldStatus{QuoteID: quoteID, Expired: []ExpiredSoftHold{}}
	if r.pool == nil {
		return out, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return out, ErrNotFound
	}

	// ONE query for both halves. Two would be two round trips and, worse, two moments in time, a
	// hold could expire between them and be counted in neither, which is the one arithmetic a banner
	// must not get wrong.
	rows, err := r.pool.Query(ctx, `
		SELECT sr.id::text, sr.product_id::text, COALESCE(p.name,''), COALESCE(p.sku::text,''),
		       sr.branch_id::text, sr.qty_base, sr.status,
		       COALESCE(to_char(sr.expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       COALESCE(to_char(sr.closed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),'')
		  FROM stock_reservations sr
		  LEFT JOIN products p ON p.id = sr.product_id
		 WHERE sr.source_type = $1 AND sr.source_ref_id = $2
		   AND sr.hold_mode = 'SOFT'
		 ORDER BY sr.closed_at DESC NULLS LAST, sr.id`, ResSourceQuote, quoteID)
	if err != nil {
		return out, fmt.Errorf("expired soft holds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h ExpiredSoftHold
		if err := rows.Scan(&h.ReservationID, &h.ProductID, &h.ProductName, &h.SKU,
			&h.BranchID, &h.QtyBase, &h.Status, &h.ExpiresAt, &h.EndedAt); err != nil {
			return out, fmt.Errorf("scan soft hold: %w", err)
		}
		switch h.Status {
		case ResActive:
			out.ActiveCount++
		case ResExpired, ResReleased:
			// EndedAt is closed_at, and the table's own CHECK ties it to the status
			// ((status = 'ACTIVE') = (closed_at IS NULL)), so a non-ACTIVE row always carries one.
			out.Expired = append(out.Expired, h)
		default:
			// CONSUMED: the stock left on the resulting order. That is a hold ending in SUCCESS and it
			// must never appear in an "expired" banner, the quote converted and the goods shipped,
			// which is the outcome everybody wanted.
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("expired soft holds: %w", err)
	}
	out.AnyExpired = len(out.Expired) > 0
	return out, nil
}
