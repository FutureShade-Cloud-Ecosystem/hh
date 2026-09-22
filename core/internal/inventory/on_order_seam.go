package inventory

// on_order_seam.go, DIB-470: where the "already on order" leg of the projected position comes from.
//
// - WHAT WAS WRONG -
//
// The DIB-72 worklist grades `available + in_transit + on_order` against the reorder point, and it
// read on_order out of the 0011 `purchase_orders` / `po_lines` tables. Since migration 0120 those
// rows are RECEIVING DOCUMENTS, purchasing creates one and receives it inside a single transaction
//, so they are never open, and the leg was a CONSTANT ZERO on every environment. A Wave-4 purchase
// order lives in `purchasing_pos`, which this module must not read.
//
// Measured on the live stack before the fix: DBS-TB-BLU60 at Trenton, two ORDERED purchase orders
// totalling 160 PC outstanding, `GET /inventory/shortfall` said `on_order_base: 0` and suggested
// buying 120 PC more, while `GET /purchasing/incoming` listed both lines. The worklist re-ordered
// stock that was already bought, which is the exact mistake replenish.go's own header says the
// projected position exists to prevent.
//
// - THE SEAM -
//
// CONSUMER-DECLARED, like POCreator and ProcurementNudger above it: inventory states the question it
// needs answered, purchasing answers it from ITS OWN tables, and a main.go adapter binds the two.
// Primitive-only types, so neither module imports the other's.
//
// The implementation purchasing binds is the SAME READ the DIB-460 on-order surface is built from -
// same tables, same "still coming" status set, same outstanding expression, shared as SQL fragments
// in purchasing/incoming_repository.go. That is the whole point of routing this through purchasing
// rather than re-deriving it here: the worklist and the incoming panel are now structurally unable
// to disagree about one product, which is what a buyer noticed in the first place.
//
// - WHAT AN UNBOUND SEAM REPORTS, AND WHY IT IS ZERO -
//
// Nothing. The leg is ZERO and the worklist SAYS its on-order figure is unavailable
// (Worklist.OnOrderDegraded → the `on_order_degraded` flag on the payload → one line above the
// table). It does NOT fall back to the old lateral, for three reasons:
//
//  1. The fallback and zero are THE SAME NUMBER. The legacy count has been structurally 0 since
//     0120 on every environment that exists; the only thing the old query added was the appearance
//     of having measured something.
//  2. A number that is always zero and never says so is how this defect survived a wave. A zero the
//     screen admits to is a bug report; a zero dressed as a measurement is a wrong purchase order.
//  3. Under-stating supply is the safe direction for a REPLENISHMENT list, it over-suggests, and a
//     buyer can decline a suggestion. Over-stating it hides a real shortfall until the yard is out.
//     Zero is the conservative answer, and the flag stops it from being a silent one.

import "context"

// OnOrderQty is outstanding purchase-order quantity for one product at one branch, in the product's
// BASE unit, ordered minus received, over every purchase order that is still coming.
//
// Base units, not sell units. The whole worklist is base-unit arithmetic (on hand, reserved, in
// transit, the reorder point, the multiple), and a skid of 60 added to a position counted in pieces
// is a hundredfold error that reads as a plausible number.
type OnOrderQty struct {
	ProductID string
	BranchID  string
	QtyBase   float64
}

// OnOrderReader is the CONSUMER-DECLARED SEAM for the "already on order" leg. Purchasing implements
// it over its own purchase-order tables; a main.go adapter binds it.
//
// branchID "" means EVERY branch, the worklist is cross-branch when no branch is named, and the
// grading is per (product, branch), which is why the answer carries both ids rather than a total.
//
// productIDs nil (or empty) means EVERY product with something outstanding. A caller that wants one
// line, DIB-461's "Order now", names it, so a single-product read stays a single-product read.
//
// FROZEN SIGNATURE, restated verbatim in NOTES-FIX-470.md so the union can wire it without reading
// this file.
type OnOrderReader interface {
	OutstandingOnOrderBase(ctx context.Context, branchID string, productIDs []string) ([]OnOrderQty, error)
}

// WithOnOrderReader injects the purchasing seam.
func (s *Service) WithOnOrderReader(r OnOrderReader) *Service { s.onOrder = r; return s }

// onOrderFor asks the seam for the on-order leg, and reports whether the answer is DEGRADED, i.e.
// whether the zeros it is about to grade with mean "nothing is on order" or "nobody could tell me".
//
// An unbound seam is not an error: the worklist is still the most useful screen in the building
// without it (it still knows on hand, held, and on a truck), and 503-ing a whole worklist because
// one leg is unavailable would take away four facts to protect one. It is degraded, and it says so.
//
// A seam that is bound and FAILS is a different thing and is returned as an error: purchasing
// answered, and what it said was that something is broken. Swallowing that would put a silent zero
// back on the screen, which is the defect this file exists to remove.
func (s *Service) onOrderFor(ctx context.Context, branchID string, productIDs []string) ([]OnOrderQty, bool, error) {
	if s.onOrder == nil {
		return nil, true, nil
	}
	rows, err := s.onOrder.OutstandingOnOrderBase(ctx, branchID, productIDs)
	if err != nil {
		return nil, false, err
	}
	return rows, false, nil
}

// onOrderBaseFor picks ONE (product, branch) total out of a seam answer.
//
// A seam that returns more than one row for the same pair is SUMMED rather than last-one-wins: the
// question is "how much is outstanding", and two rows would be two purchase orders.
func onOrderBaseFor(rows []OnOrderQty, productID, branchID string) float64 {
	var qty float64
	for _, r := range rows {
		if r.ProductID == productID && r.BranchID == branchID {
			qty += r.QtyBase
		}
	}
	return qty
}
