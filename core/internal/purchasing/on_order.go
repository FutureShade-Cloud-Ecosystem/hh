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
// as IncomingForProduct (openPOLineSource / openPOLineIsOpen / openPOLineOutstanding). The status set
// is the one parameter they do not share: the list shows openPOStatuses, this total counts
// onOrderStatuses.
//
// A DRAFT IS NOT ON ORDER. The aggregate counts onOrderStatuses (ORDERED and PARTIAL) and nothing
// else, the principal's ruling (row 1303). An earlier reading counted DRAFT on the argument that the
// buyer who drafted a PO this morning should not be told to order it again this afternoon. A draft
// is not a promise to the vendor: it can sit unsent, be edited, or be abandoned, and while it is
// counted the yard looks covered by an order that may never be placed, so the shortage hides until
// the shelf is bare. The worklist keeps listing the row, and OpenDraftsFor hands the draft's id and
// number alongside it so the screen can say "drafted, not placed" and link the draft. The product
// incoming LIST still shows drafts, as drafts (incoming.go): the same rows, the same outstanding
// expression, a different status set, and the difference is stated, not drifted.
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

// OpenDraft is the newest draft purchase order that has an outstanding line for one product at one
// branch: the draft the worklist row points at.
type OpenDraft struct {
	ProductID string `json:"product_id"`
	BranchID  string `json:"branch_id"`
	POID      string `json:"po_id"`
	PONo      string `json:"po_no"`
}

// onOrderStore is the optional persistence seam for this read, discovered by type assertion off
// *Repository, the shape incomingStore already uses here, and for the same reason: a unit test that
// had to reach a database to prove which STATUSES count would be proving it in the one place the
// decision is hardest to see.
type onOrderStore interface {
	OutstandingOnOrderBase(ctx context.Context, branchID string, productIDs []string) ([]OutstandingOnOrder, error)
	OpenDraftsFor(ctx context.Context, branchID string, productIDs []string) ([]OpenDraft, error)
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

// OpenDraftsFor returns, per (product, branch), the newest DRAFT purchase order with an outstanding
// line for that product: the draft a buyer has started but not placed. It is not supply (see the
// file header), it is the pointer a surface needs to say "drafted, not placed". Same scope rules and
// same absence of caller authority as OutstandingOnOrderBase, for the same reason.
func (s *Service) OpenDraftsFor(ctx context.Context, branchID string, productIDs []string) ([]OpenDraft, error) {
	st, err := s.onOrder()
	if err != nil {
		return nil, err
	}
	return st.OpenDraftsFor(ctx, branchID, productIDs)
}
