package order

// open_orders_seam.go, the numbers-for-orders read (change 5, slice S3): which open sales orders
// wait on a product, as NUMBERS per (product, branch), oldest first.
//
// The inventory shortfall worklist labels every short row with the sales orders it holds up
// ("Short for #SO-TRT-1001"). Those orders are this module's rows: the module header's boundary
// makes order the sole reader of orders + order_lines, so the question crosses a seam and comes
// back as numbers, never as a second module's SQL over the 0008 tables. Same shape as
// purchasing's on_order.go, which exists for the mirror-image reason (its outstanding aggregate
// feeds the same worklist's on-order leg).
//
// - WHAT WAITS -
//
// An order waits on a product while it carries a line the yard still owes: qty_base above what has
// been fulfilled and released, on an order that is not FULFILLED, CLOSED or CANCELLED. This is the
// same open-line predicate the pending-fulfilment surface reads (inventory's DX-01 read), with the
// fulfilment-mode filter left off, deliberately: a PICKUP order's stock is hard-held the moment the
// order is confirmed and a DELIVERY order's is not, and both customers are standing there because
// the shelf is bare. The row is short for both of them.
//
// - NO HTTP ROUTE, DELIBERATELY -
//
// Like purchasing's OutstandingOnOrderBase, this is a Go-level seam read for one in-process
// consumer whose scope the caller has already fenced (the worklist's reach). Publishing it as an
// endpoint would hand any staff caller a cross-customer order summary no route owes them.
//
// - NIL ASKS EVERYTHING, AN EMPTY LIST ASKS NOTHING -
//
// A nil product list is the worklist's cross question: every product with something outstanding.
// An EMPTY list names no product and answers none, on purpose, and this differs from the
// purchasing aggregate's convention beside it: an aggregate mis-scoped to everything reads as a
// too-big number, while a list mis-scoped to everything reads as a customer order list handed to
// whoever asked.

import "context"

// OpenSaleOrder is one open sales order line still waiting on a product at a branch: one order a
// shortfall row can name. Primitive-only, so neither module imports the other's types.
type OpenSaleOrder struct {
	ProductID string
	BranchID  string
	OrderID   string
	OrderNo   string
}

// openOrderStore is the persistence the numbers read needs, reached by type assertion off the
// injected orderStore (the reorderStore pattern), so the existing service fakes keep compiling;
// *Repository satisfies it in prod.
type openOrderStore interface {
	OpenSaleOrdersFor(ctx context.Context, branchID string, productIDs []string) ([]OpenSaleOrder, error)
}

// OpenSaleOrdersFor returns the open sales orders waiting on the named products at the named
// branch, oldest first. branchID "" is every branch the question covers; productIDs nil is every
// product with an open line, an empty non-nil list is no product at all. The caller owns the
// branch fence: there is no caller authority here, the same as OrderBrief and CustomerForOrder,
// because the one consumer is a server-side read whose scope the worklist already decided.
func (s *Service) OpenSaleOrdersFor(ctx context.Context, branchID string, productIDs []string) ([]OpenSaleOrder, error) {
	os, ok := s.store.(openOrderStore)
	if !ok {
		return nil, errNoDB
	}
	return os.OpenSaleOrdersFor(ctx, branchID, productIDs)
}
