package inventory

// short_order_seam.go, where the shortfall row learns WHICH sales orders it holds up (change 5,
// slice S3). The row is short because somebody sold the stock; until now the worklist said by how
// much and never for whom, so the buyer drafting the PO could not see the order they were about to
// delay, and the board's "Short for SO-n" tag had nothing to draw on.
//
// CONSUMER-DECLARED, like OnOrderReader and DraftPOReader beside it: inventory states the question,
// the order module (the sole reader of the 0008 orders tables) answers it from its own tables, and
// a main.go adapter binds the two. Primitive-only types, so neither module imports the other's.
// The owning module's side of this seam is order/open_orders_seam.go.
//
// - WHAT THE ROW CARRIES -
//
// At most three numbers, OLDEST FIRST, with the count of the rest beside them: a worklist row is a
// line in a buyer's scan, not an order list, and three names with a "two more" is the most a row
// can carry without becoming the screen the orders app already is. The row keeps the seam's order,
// so the named three are the three orders the shortfall delays longest.
//
// The fields are absent, never empty: no seam bound, or nothing waiting, and neither key rides.
// An empty list reads as a measurement ("nobody waits") and only a bound seam measures; the
// numbers label the row, they do not grade it, so an unbound seam costs the row nothing.

import "context"

// shortOrderNosCap is how many order numbers one row names; the rest are counted, not listed.
const shortOrderNosCap = 3

// ShortOrder is one open sales order waiting on a product at a branch, as the order module
// answers it: ids and the visible number, nothing else.
type ShortOrder struct {
	ProductID string
	BranchID  string
	OrderID   string
	OrderNo   string
}

// ShortOrderReader is the CONSUMER-DECLARED SEAM for the "who is waiting" leg. The order module
// implements it over its own tables; a composition-root adapter binds it.
//
// branchID "" means EVERY branch, the worklist is cross-branch when no branch is named.
// productIDs names the rows' products, so a single-product read stays a single-product read.
// The reply is ordered oldest first; the row keeps that order.
type ShortOrderReader interface {
	OpenOrdersFor(ctx context.Context, branchID string, productIDs []string) ([]ShortOrder, error)
}

// WithShortOrderReader injects the order module's seam.
func (s *Service) WithShortOrderReader(r ShortOrderReader) *Service { s.shortOrders = r; return s }

// shortOrderWindow picks ONE row's answer out of the seam's ordered reply: the oldest
// shortOrderNosCap numbers for the row's (product, branch), and how many more wait behind them.
// The seam's reply may cover the whole worklist and branches outside the caller's reach; both are
// dropped here, the row never carries another branch's order, whatever the seam answered.
func shortOrderWindow(orders []ShortOrder, productID, branchID string) ([]string, int) {
	nos := []string{}
	more := 0
	for _, o := range orders {
		if o.ProductID != productID || o.BranchID != branchID {
			continue
		}
		if len(nos) < shortOrderNosCap {
			nos = append(nos, o.OrderNo)
		} else {
			more++
		}
	}
	if len(nos) == 0 {
		return nil, 0
	}
	return nos, more
}

// attachShortOrders stamps each row with the sales orders waiting on its product at its branch.
// An unbound seam leaves the rows as they are, the draft pointer's posture: the worklist grades
// and ranks exactly as it did, the numbers only name the shortfall. A bound seam that FAILS is an
// error, not a silent gap: a row with no numbers reads "no order waits", and the buyer would draft
// the PO believing nobody is standing there.
func (s *Service) attachShortOrders(ctx context.Context, reach BranchReach, branchID string, rows []ShortfallRow) error {
	if s.shortOrders == nil || len(rows) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ProductID)
	}
	orders, err := s.shortOrders.OpenOrdersFor(ctx, branchID, ids)
	if err != nil {
		return err
	}
	// The window read carries the caller's reach too: an order at a branch outside it is never
	// matched, whatever the seam answered for an all-branches question.
	allowed := make([]ShortOrder, 0, len(orders))
	for _, o := range orders {
		if reach.covers(o.BranchID) {
			allowed = append(allowed, o)
		}
	}
	for i := range rows {
		rows[i].ShortOrderNos, rows[i].ShortOrderMore = shortOrderWindow(allowed, rows[i].ProductID, rows[i].BranchID)
	}
	return nil
}
