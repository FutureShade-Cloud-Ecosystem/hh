package inventory

// incoming.go, DIB-460, inventory's half: what is on a TRUCK BETWEEN OUR OWN YARDS for one product.
//
// The four-bucket read already reports in_transit as a NUMBER (DIB-73). This read is the same units
// broken into the documents behind them, because "15 PC in transit" does not answer the question the
// ticket is about, "15 PC from Kingston, shipped Monday" does.
//
// - THIS FILE STILL DOES NOT INVENT A DATE. NOW IT CAN READ ONE -
//
// As DIB-460 shipped, stock_transfers (migration 0116) carried created_at, shipped_at and received_at
// and no promised arrival, so a transfer line reported SHIPPED_AT, a fact, and the surface said
// "shipped Aug 10" beside a purchase-order line saying "expected Aug 19". That asymmetry was the
// complaint DIB-472 was filed about (NOTES-FIX-INV460.md, PARK P2).
//
// Ruling D-083 added the column (migration 0158) and a CAPTURE POINT for it: the ship flow pre-fills
// ship date + the branch pair's admin-maintained transit days, editable before confirm. So the eta on
// a line here is a STORED PROMISE somebody looked at, not a derivation.
//
// THE RULE THIS FILE HELD IS UNCHANGED, AND STILL LOAD-BEARING: nothing here computes a date. A
// transfer whose route nobody has configured, or whose clerk cleared the field, reports eta '' and the
// surface says "shipped Aug 10" exactly as it did before. Deriving an ETA at READ time from shipped_at
// plus a guessed transit time would still put a date on the screen that nothing in the database
// supports, and a person plans around a date.
//
// - WHY THIS IS NOT ON THE branch-stock PAYLOAD -
//
// AvailabilityAtBranch is called per quote LINE (the W5 at-quote read). Hanging another lateral off
// its query would put this ticket's cost on the quote path, which is the hottest caller of that read
// and does not want the documents. A separate read is asked for only by the surface that shows them.

import (
	"context"
	"strings"
)

// InTransitLine is one open leg of an inter-branch transfer for one product: units that have left
// the source yard and have not arrived at the destination.
//
// The quantity is the DERIVED in-transit figure (shipped − received), the same arithmetic
// TransferLine.InTransitBase does, so a partly-unloaded truck reports what is still on it.
type InTransitLine struct {
	TransferID   string `json:"transfer_id"`
	TransferNo   string `json:"transfer_no"`
	FromBranchID string `json:"from_branch_id"`
	FromCode     string `json:"from_branch_code"`
	FromName     string `json:"from_branch_name"`
	ToBranchID   string `json:"to_branch_id"`
	ToCode       string `json:"to_branch_code"`
	// QtyBase is what is still on the truck: qty_shipped_base − qty_received_base.
	QtyBase float64 `json:"qty_base"`
	UOM     string  `json:"uom"`
	// ShippedAt is YYYY-MM-DD, and it is the FALLBACK sentence's fact, it stays on every line whether
	// or not a promise was made, because "when did it leave" is knowable when "when does it land" is not.
	ShippedAt string `json:"shipped_at"`
	// ETA is the promised arrival DAY (YYYY-MM-DD), '' when the transfer carries no promise, DIB-472,
	// ruling D-083, stored by the ship flow (transfer_eta.go) into the 0158 column.
	//
	// This is the field DIB-460's header said it would not invent, and the distinction still holds: it
	// is not DERIVED here, it is READ. Nothing in this file computes a date, and an eta-less transfer
	// reports '' rather than a guess, so the field is never a phantom, and the surface's fallback
	// ("shipped Aug 12") stays the honest answer for every route nobody has configured.
	ETA string `json:"eta"`
}

// inTransitStore is the optional persistence seam for this read, discovered by type assertion off
// the base store, the stockLookupStore shape, and for the same reason: `store` is implemented by
// fakes in several files here, and widening a seam that others implement is a breaking change
// dressed up as an addition.
type inTransitStore interface {
	InTransitLinesForProduct(ctx context.Context, productID string, reach BranchReach) ([]InTransitLine, error)
}

func (s *Service) inTransitStore() (inTransitStore, error) {
	ts, ok := s.db().(inTransitStore)
	if !ok {
		return nil, ErrStockLookupNotConfigured
	}
	return ts, nil
}

// InTransitForProduct returns the open transfer legs bringing this product INTO the branches the
// caller can reach, oldest shipment first (the one that should already have landed reads first).
//
// INBOUND ONLY, matching the bucket it details. Stock shipped OUT of a branch has already left that
// branch's on-hand; listing it again as "in transit" invites someone to subtract it twice, the
// asymmetry visibility.go spells out, kept here so the two reads cannot disagree.
func (s *Service) InTransitForProduct(ctx context.Context, reach BranchReach, productID string) ([]InTransitLine, error) {
	if strings.TrimSpace(productID) == "" {
		return nil, badReq("inventory: product_id is required")
	}
	ts, err := s.inTransitStore()
	if err != nil {
		return nil, err
	}
	// A caller with no grants is answered without a round trip; the SQL's empty-array predicate
	// would return the same nothing, so this is the optimisation and the SQL is the fence.
	if reach.reachesNothing() {
		return nil, nil
	}
	return ts.InTransitLinesForProduct(ctx, productID, reach)
}
