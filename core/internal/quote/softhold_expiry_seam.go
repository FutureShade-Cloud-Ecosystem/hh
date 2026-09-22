package quote

// softhold_expiry_seam.go, DIB-428, the quote side of W6-C's 'soft hold expired' projection.
//
// - WHAT SHIPPED BEFORE THIS FILE -
// W6-C shipped the READ (order/soft_hold_expiry.go): given a quote id it reports which of that
// quote's SOFT holds have ended and how many still stand, derived at read time from the reservation
// rows themselves. It renders nowhere, by design, internal/quote was outside that wave's fence, so
// the read was shipped and the wiring NAMED rather than reached for. This file is that wiring's
// consumer half, and reserve_handler.go + app/src/quote-hold-expiry.ts are the surface.
//
// - CONSUMER-DECLARED, WITH TYPES THIS MODULE OWNS -
// The house pattern, and the one stock_seam.go states in its own header: "consumer-declared ports
// with types this module owns; nothing here imports inventory". The same holds for order. quote does
// not import order anywhere in the tree today (order imports quote, in a test), and binding the
// surface to order's structs would make a presentation seam the place that reverses that direction.
// So ExpiredHold/SoftHoldStatus below mirror order.ExpiredSoftHold/order.SoftHoldStatus field for
// field, and the main.go adapter maps between them, exactly what w5aQuoteAvailability does for
// inventory.BranchStock.
//
// - THE CONSUMER'S QUESTION IS NOT THE PROJECTION'S QUESTION -
// order's read answers "which of this quote's HOLDS ended". The quote surface has to answer the
// question a rep actually has, which is "which of my LINES are no longer held". Those are different
// questions, and the difference is not academic, it was measured. Adding a SECOND line to a quote
// (dib428_softhold_expiry_integration_test.go, before these rules existed) produced this:
//
//	2 expired / 1 active, where 1 of the two "expired" was a RELEASED row nobody should ever see.
//
// Three facts about how this module writes holds turn a literal read into a banner that cries wolf:
//
//  1. THE SYNC IS TOTAL. Every line write releases ALL of a quote's holds and re-raises them
//     (stock_seam.go: "SYNC (release-all + re-reserve), not incremental"). So an ordinary
//     three-line quote has left RELEASED rows behind before anything has gone wrong at all. Read
//     literally, the banner fires on nearly every multi-line quote in the tree, and a warning that
//     is on every screen is one nobody reads on the screen that matters.
//  2. AN ENDED HOLD CAN BE RE-RAISED. The sweep expires Monday's hold; Tuesday the rep re-holds (or
//     just edits the quantity). A NEW ACTIVE row appears and the EXPIRED row stays forever. The row
//     is not wrong, that hold really did end, it stopped being NEWS the moment the line was held
//     again. This is the "stale expired banner nobody can clear" soft_hold_expiry.go's own header
//     names as the thing a projection exists to avoid.
//  3. A REMOVED LINE RELEASES ITS HOLD. The rep took the line off the quote themselves. Telling
//     them its stock is no longer held is telling them what they just did.
//
// So the consumer filters, and each rule answers exactly one of those. In order:
//
//	DEDUPE       one line is one piece of news, not one per historical churn, keep the most
//	             RECENT ending per (product, branch), which is also the true story of that line.
//	SUPERSEDE    drop an ended hold whose (product, branch) this quote holds again.
//	STILL QUOTED drop an ended hold whose product is no longer a line on this quote.
//
// All three are facts about THIS quote's lines and holds, which order cannot see and quote already
// has (holdStore.QuoteHolds, ACTIVE only, and staffStore.StaffDetail). None of them edits or
// deletes anything: the reservation history is untouched and every rule is re-derived at read time,
// so it cannot go stale. And without SUPERSEDE the re-hold affordance on the banner would be a
// button that visibly does nothing, which is worse than not offering one.
//
// - DEGRADATION -
// An UNBOUND seam reports an empty status with wired=false, the StockSeamWired/AvailabilitySeamWired
// precedent: "we did not ask" and "nothing expired" are different answers and the envelope keeps
// them apart. A reader ERROR, by contrast, is propagated rather than swallowed, this endpoint is
// its own fetch behind its own component, so failing it does not take the quote screen away from the
// rep, and a swallowed error would be indistinguishable from "nothing expired", which is precisely
// the silence DIB-428 exists to end. The COMPONENT stays quiet on that failure; the API does not.

import "context"

// ResExpired completes this module's reservation vocabulary (stock_seam.go holds RELEASED/CONSUMED,
// the two THIS module writes). EXPIRED is written by inventory's sweep and never by quote, it is
// added here, on the read side, for the same reason order added it: something finally reads it.
const ResExpired = "EXPIRED"

// ExpiredHold is one line's worth of "the stock you were holding is no longer held". Mirrors
// order.ExpiredSoftHold; the main.go adapter maps it.
//
// It carries the PRODUCT and the QUANTITY, not just a count, because "12 EA of Bergerac Pavers is no
// longer held" is a sentence a rep can act on and "some of your holds expired" is a scavenger hunt.
type ExpiredHold struct {
	ReservationID string  `json:"reservation_id"`
	ProductID     string  `json:"product_id"`
	ProductName   string  `json:"product_name"`
	SKU           string  `json:"sku"`
	BranchID      string  `json:"branch_id"`
	QtyBase       float64 `json:"qty_base"`
	// Status is EXPIRED (the sweep timed it out) or RELEASED (something gave it back early). Both
	// mean "not held any more"; the distinction is kept because a yard manager does ask which.
	// CONSUMED never appears, that is the quote converting and the goods shipping, and order's read
	// is the thing that keeps it out (its own integration test is built around that case).
	Status string `json:"status"`
	// ExpiresAt is when the hold was due to lapse; EndedAt is when it actually did.
	ExpiresAt string `json:"expires_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
}

// SoftHoldStatus is the whole answer for one quote, in the shape the surface renders directly.
// Mirrors order.SoftHoldStatus.
type SoftHoldStatus struct {
	QuoteID string `json:"quote_id"`
	// ActiveCount is holds still standing, so the banner can say "2 of 5 lines are no longer held"
	// rather than implying the whole quote has evaporated.
	ActiveCount int `json:"active_count"`
	// Expired is every ended hold this surface still considers news (see the supersession rule).
	Expired []ExpiredHold `json:"expired"`
	// AnyExpired is the ONE boolean the banner branches on. Derived, never stored, and derived HERE
	// from the filtered list, so a superseded hold cannot switch the banner on.
	AnyExpired bool `json:"any_expired"`
}

// SoftHoldExpiryReader is the consumer-declared port. *order.Service satisfies this shape through a
// one-line-ish main.go adapter over *order.Service.ExpiredSoftHolds, see NOTES-FIX-428.md PARK 1
// for the exact binding, which is the union's to apply.
type SoftHoldExpiryReader interface {
	ExpiredSoftHolds(ctx context.Context, quoteID string) (SoftHoldStatus, error)
}

// WithSoftHoldExpiry injects the projection port.
func (s *StaffService) WithSoftHoldExpiry(r SoftHoldExpiryReader) *StaffService {
	s.holdExpiry = r
	return s
}

// SoftHoldExpirySeamWired reports the binding to the surface, so an unwired deploy can tell "we did
// not ask" from "nothing expired" instead of rendering a confident silence.
func (s *StaffService) SoftHoldExpirySeamWired() bool { return s.holdExpiry != nil }

// SoftHoldExpiry answers "which of this quote's lines are no longer held", branch-scoped to the
// caller the same way every other read on this service is.
//
// The bool is seam_wired. An unbound seam returns an empty status and NO error: the quote screen
// must still render, and a decoration that cannot be computed is not a fault of the quote.
func (s *StaffService) SoftHoldExpiry(ctx context.Context, quoteIDOrNo string) (SoftHoldStatus, bool, error) {
	_, quoteID, err := s.resolveQuoteForCaller(ctx, quoteIDOrNo)
	if err != nil {
		return SoftHoldStatus{}, false, err
	}
	if s.holdExpiry == nil {
		return SoftHoldStatus{QuoteID: quoteID, Expired: []ExpiredHold{}}, false, nil
	}
	st, err := s.holdExpiry.ExpiredSoftHolds(ctx, quoteID)
	if err != nil {
		return SoftHoldStatus{}, true, err
	}
	st.QuoteID = quoteID

	// The two reads the consumer rules are decided against. Each degrades independently: a store
	// that cannot answer simply leaves its rule unapplied, which is the projection's own answer -
	// a defensible fallback, not a wrong one.
	var held []QuoteHold
	if hs, ok := s.store.(holdStore); ok {
		if rows, err := hs.QuoteHolds(ctx, quoteID); err == nil {
			held = rows
		}
	}
	// nil (not empty) means "the lines are unknown", which switches the STILL-QUOTED rule off rather
	// than silencing every line, an empty map would read as "this quote has no lines" and hide a
	// real expiry.
	var quoted map[string]bool
	if view, err := s.store.StaffDetail(ctx, quoteID); err == nil {
		quoted = make(map[string]bool, len(view.Lines))
		for _, ln := range view.Lines {
			if ln.ProductID != "" {
				quoted[ln.ProductID] = true
			}
		}
	}

	st.Expired = expiredHoldNews(st.Expired, held, quoted)
	// AnyExpired is derived HERE, from what the surface will actually render. Copying the
	// projection's own boolean would light a banner whose list is empty.
	st.AnyExpired = len(st.Expired) > 0
	return st, true, nil
}

// expiredHoldNews applies the three consumer rules, dedupe, supersede, still-quoted, to the
// projection's ended holds and returns what is worth telling a rep. See this file's header for why
// each exists; each one was a false banner before it did.
//
// A PURE function, so the rule that decides whether a banner appears at all is testable without a
// database, the gradeAvailability precedent in reserve.go.
//
// Keyed on (product, branch) rather than reservation id throughout, on purpose: the hold sync is
// total (release-all, re-reserve), so a re-held line NEVER carries its old reservation id. Identity
// by row would mean these rules never fire, which is the same as not having written them.
//
// quotedProducts == nil means the quote's lines could not be read; the still-quoted rule is skipped
// rather than applied to an empty set.
func expiredHoldNews(ended []ExpiredHold, held []QuoteHold, quotedProducts map[string]bool) []ExpiredHold {
	out := make([]ExpiredHold, 0, len(ended))
	if len(ended) == 0 {
		return out
	}
	stillHeld := make(map[string]bool, len(held))
	for _, h := range held {
		if h.ProductID != "" {
			stillHeld[holdKey(h.ProductID, h.BranchID)] = true
		}
	}
	// DEDUPE, by the most RECENT ending. EndedAt is order's `to_char(... 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	//, fixed-width UTC, so string order IS time order. Compared explicitly rather than trusting the
	// projection's ORDER BY, because "newest first" is a detail of a module this one does not own.
	newest := make(map[string]int, len(ended))
	for i, e := range ended {
		k := holdKey(e.ProductID, e.BranchID)
		if j, seen := newest[k]; !seen || ended[i].EndedAt > ended[j].EndedAt {
			newest[k] = i
		}
	}
	for i, e := range ended {
		k := holdKey(e.ProductID, e.BranchID)
		if newest[k] != i {
			continue // an earlier ending of the same line, churn, not news
		}
		if stillHeld[k] {
			continue // SUPERSEDE: the line is held again
		}
		if quotedProducts != nil && !quotedProducts[e.ProductID] {
			continue // STILL QUOTED: the rep took this line off the quote themselves
		}
		out = append(out, e)
	}
	return out
}

func holdKey(productID, branchID string) string { return productID + "@" + branchID }
