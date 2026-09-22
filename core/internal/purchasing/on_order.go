package purchasing

// on_order.go, DIB-470: what is outstanding on purchase orders, as a NUMBER per product per branch.
//
// This is the aggregate behind inventory's OnOrderReader seam. It exists so the DIB-72 replenishment
// worklist can grade a projected position against real purchase orders, which live in this module's
// tables and which inventory must not read.
//
// - IT IS THE SAME READ AS THE ON-ORDER PANEL, AND THAT IS THE POINT -
//
// DIB-470 was filed because two screens disagreed about one product: the product page listed 160 PC
// outstanding on two purchase orders while the replenishment worklist graded that product as if
// nothing were coming. So this does not re-derive "outstanding", it composes the SAME SQL fragments
// as IncomingForProduct (openPOLineSource / openPOLineIsOpen / openPOLineOutstanding), off the same
// openPOStatuses set. Changing what "still coming" means now changes both, or neither.
//
// DRAFT IS INCLUDED, because openPOStatuses includes it, and that is a deliberate re-decision rather
// than an inherited accident. incoming.go argues DRAFT in for the person asking "is anyone dealing
// with this?", a draft PO for 120 is the answer that stops them raising a second one. That argument
// is STRONGER for the worklist, whose entire job is to not suggest ordering something twice: the
// buyer who drafted the PO this morning is the person the worklist would be telling to order it
// again this afternoon. The cost of including it is a suggestion that is skipped while a draft sits
// unsent; the worklist's own surface names the status, and the alternative is the shipped defect.
//
// - THERE IS NO HTTP ROUTE ON THIS, DELIBERATELY -
//
// The public on-order surface is GET /purchasing/incoming, which is branch-fenced per caller
// (BranchReach). This is a Go-level seam read for one in-process consumer, and its scope is exactly
// the scope of the worklist that asks for it. Publishing an unfenced aggregate as an endpoint would
// hand any staff caller a per-branch purchasing summary that the fenced read is careful not to.

import "context"

// OutstandingOnOrder is the total still coming for one product at one branch, in BASE units.
//
// Base units, because the number's only consumer adds it to a stock position that is counted in
// base. A line ordered in skids has already been converted (purchasing_po_lines.qty_ordered_base is
// the base column); summing the entered quantity instead would add 3 to a position of 800 and read
// like a plausible answer.
type OutstandingOnOrder struct {
	ProductID string  `json:"product_id"`
	BranchID  string  `json:"branch_id"`
	QtyBase   float64 `json:"qty_outstanding_base"`
}

// onOrderStore is the optional persistence seam for this read, discovered by type assertion off
// *Repository, the shape incomingStore already uses here, and for the same reason: a unit test that
// had to reach a database to prove which STATUSES count would be proving it in the one place the
// decision is hardest to see.
type onOrderStore interface {
	OutstandingOnOrderBase(ctx context.Context, branchID string, productIDs []string) ([]OutstandingOnOrder, error)
}

func (s *Service) onOrder() (onOrderStore, error) {
	if s.onOrderSeam != nil {
		return s.onOrderSeam, nil
	}
	if s.repo == nil {
		return nil, errNoDB
	}
	return s.repo, nil
}

// OutstandingOnOrderBase returns the outstanding base quantity per (product, branch).
//
// branchID "" = every branch. productIDs nil/empty = every product with something outstanding.
//
// NO CALLER AUTHORITY IS APPLIED, and the signature says so by not taking any: there is no HTTP
// route on this method (see the file header) and its one consumer is a composition-root adapter
// serving a read whose scope is already decided. A BranchReach parameter that the only caller could
// not populate would be a fence in name only, worse than none, because it would look like one.
func (s *Service) OutstandingOnOrderBase(ctx context.Context, branchID string, productIDs []string) ([]OutstandingOnOrder, error) {
	st, err := s.onOrder()
	if err != nil {
		return nil, err
	}
	return st.OutstandingOnOrderBase(ctx, branchID, productIDs)
}
