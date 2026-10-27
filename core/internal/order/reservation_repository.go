package order

// reservation_repository.go, the pgx half of the reservation lane (reservations.go).
//
// ONE DISCIPLINE, and it is the same one edit_repository.go states for content edits:
//
//	BEGIN
//	  SELECT status, branch_id FROM orders WHERE id = $1 FOR UPDATE   ← lock + latest committed row
//	  status ∈ allowedFrom ?                                          ← the matrix guard, in the lock
//	  precheck()                                                      ← the money predicate, in the lock
//	  UPDATE orders SET status = $2
//	  onTx(tx, oldStatus, branchID)                                   ← the RESERVE / RELEASE, on THIS tx
//	COMMIT   ← so the transition and the stock hold are one atomic unit
//
// This module writes NO stock table. onTx is handed the transaction and calls the injected
// StockReserver, which is inventory's code writing inventory's tables. What this file owns is the
// LOCK and the ORDERING, that the hold is decided after the transition is known to be legal, and
// that both share a commit.
//
// Why branch_id is returned from under the lock rather than read separately: a reserve needs the
// branch, and a second unlocked read is a second snapshot. In practice an order's branch does not
// move, but "in practice it does not change" is exactly the assumption that makes a race take two
// years to find.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// compile-time proof the real repository satisfies the optional transactional lane.
var _ reservationStore = (*Repository)(nil)

// UpdateStatusFromTx is UpdateStatusFromChecked plus an in-transaction hook. Outcomes match
// UpdateStatusFrom exactly, id absent → ErrNotFound; current == newStatus → ErrNoTransition;
// current ∉ allowedFrom → ErrIllegalTransition, so routing a transition through here can never
// change whether it is legal, only what else commits with it.
//
// The hook runs AFTER the UPDATE. That order matters for the reserve case: inventory's overdraft
// guard and its qty_reserved projection both read the order's own transaction, and a reserve that
// ran before the status write would be holding stock for an order that might still fail its guard.
func (r *Repository) UpdateStatusFromTx(ctx context.Context, orderID, newStatus string, allowedFrom []string,
	precheck func(context.Context) error,
	onTx func(ctx context.Context, tx pgx.Tx, oldStatus, branchID string) error) (oldStatus string, err error) {

	if r.pool == nil {
		return "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("transition: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	oldStatus, err = r.UpdateStatusFromTxOn(ctx, tx, orderID, newStatus, allowedFrom, precheck, onTx)
	if err != nil {
		return oldStatus, err
	}
	if err := tx.Commit(ctx); err != nil {
		return oldStatus, fmt.Errorf("transition: commit: %w", err)
	}
	return oldStatus, nil
}

// UpdateStatusFromTxOn is UpdateStatusFromTx on the CALLER's transaction: the same lock, the same
// ladder, the same in-transaction hook, and NO commit of its own (round 2, S3's P2).
//
// It exists for the one caller that must not have its own transaction: the pickup's confirm runs
// the order's fulfilment inside the confirm's own transaction, so the collection, the order's
// status, its holds and its stock relief commit or roll back together. A delivered order can then
// never be left with no stock relief and no COGS, which is what a fulfilment AFTER the commit (and
// a swallowed error) could do.
//
// The hook's error rolls the STATUS back, exactly as it does in the committing variant: the caller
// decides what the failure means (it rolls its own transaction back, here).
func (r *Repository) UpdateStatusFromTxOn(ctx context.Context, tx pgx.Tx, orderID, newStatus string, allowedFrom []string,
	precheck func(context.Context) error,
	onTx func(ctx context.Context, tx pgx.Tx, oldStatus, branchID string) error) (oldStatus string, err error) {

	if r.pool == nil {
		return "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", ErrNotFound
	}
	var branchID string
	if err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&oldStatus, &branchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("transition: lock order %q: %w", orderID, err)
	}
	if oldStatus == newStatus {
		return oldStatus, ErrNoTransition
	}
	if !containsStatus(allowedFrom, oldStatus) {
		return oldStatus, ErrIllegalTransition
	}
	if precheck != nil {
		if err := precheck(withPrecheckTx(ctx, tx)); err != nil {
			return oldStatus, err
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = $2, updated_at = now() WHERE id = $1`, orderID, newStatus); err != nil {
		return oldStatus, fmt.Errorf("transition: update order %q: %w", orderID, err)
	}
	if onTx != nil {
		if err := onTx(ctx, tx, oldStatus, branchID); err != nil {
			return oldStatus, err // rolls the STATUS back too, that is the point of this method
		}
	}
	return oldStatus, nil
}

// LockOrderTx runs fn under the order row's write lock without touching status. The two callers are
// the operator resync and the change-order apply: both rewrite this order's holds, and both must be
// serialized against a concurrent transition that would do the same.
func (r *Repository) LockOrderTx(ctx context.Context, orderID string,
	fn func(ctx context.Context, tx pgx.Tx, status, branchID string) error) error {

	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(orderID) {
		return ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lock order: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, branchID string
	if err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&status, &branchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock order %q: %w", orderID, err)
	}
	if fn != nil {
		if err := fn(ctx, tx, status, branchID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("lock order: commit: %w", err)
	}
	return nil
}

// LockOrderOnTx is LockOrderTx on the CALLER's transaction, with no commit of its own (round 2,
// S3's P2). The pickup's confirm runs the order's relief inside its own transaction, and a relief
// that takes its own transaction there would be a second fate for the same collection.
func (r *Repository) LockOrderOnTx(ctx context.Context, tx pgx.Tx, orderID string,
	fn func(ctx context.Context, tx pgx.Tx, status, branchID string) error) error {

	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(orderID) {
		return ErrNotFound
	}
	var status, branchID string
	if err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&status, &branchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock order %q: %w", orderID, err)
	}
	if fn != nil {
		if err := fn(ctx, tx, status, branchID); err != nil {
			return err
		}
	}
	return nil
}

// ReservableLinesTx reads the (product, qty_base) projection on the caller's transaction.
//
// Lines with a NULL product_id are returned with an empty ProductID rather than filtered out in SQL,
// so the "which lines are skipped and why" decision lives in ONE place (reserveOrderLinesTx) and is
// unit-testable without a database.
func (r *Repository) ReservableLinesTx(ctx context.Context, tx pgx.Tx, orderID string) ([]reservableLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT line_no, COALESCE(product_id::text, ''), qty_base
		  FROM order_lines
		 WHERE order_id = $1
		 ORDER BY line_no`, orderID)
	if err != nil {
		return nil, fmt.Errorf("reservable lines: %w", err)
	}
	defer rows.Close()
	var out []reservableLine
	for rows.Next() {
		var ln reservableLine
		if err := rows.Scan(&ln.LineNo, &ln.ProductID, &ln.QtyBase); err != nil {
			return nil, fmt.Errorf("scan reservable line: %w", err)
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}

// ReadGroupClaimTx reads a SHIP GROUP's own claim: each of the group's lines
// with the group's share of the quantity, capped at the line's remaining
// unfulfilled base (round 2, S3's P1). No write: the advance follows the real
// stock move (the P3, kept).
func (r *Repository) ReadGroupClaimTx(ctx context.Context, tx pgx.Tx, orderID, groupID string) ([]string, []string, []float64, error) {
	rows, err := tx.Query(ctx, `
		SELECT ol.id::text, COALESCE(ol.product_id::text, ''),
		       LEAST(sgl.qty_base, GREATEST(ol.qty_base - ol.fulfilled_base - ol.released_base, 0))::float8
		  FROM shipment_group_lines sgl
		  JOIN order_lines ol ON ol.id = sgl.order_line_id
		 WHERE sgl.shipment_group_id = $2::uuid AND ol.order_id = $1::uuid
		 ORDER BY ol.line_no
		   FOR UPDATE OF ol`, orderID, groupID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read group claim: %w", err)
	}
	defer rows.Close()
	var lineIDs, productIDs []string
	var qtys []float64
	for rows.Next() {
		var lineID, productID string
		var qty float64
		if err := rows.Scan(&lineID, &productID, &qty); err != nil {
			return nil, nil, nil, fmt.Errorf("scan group claim: %w", err)
		}
		if productID == "" || qty <= 0.00005 {
			continue
		}
		lineIDs = append(lineIDs, lineID)
		productIDs = append(productIDs, productID)
		qtys = append(qtys, qty)
	}
	return lineIDs, productIDs, qtys, rows.Err()
}

// AdvanceGroupClaimTx advances fulfilled_base by the group's claimed share,
// behind the group's own stock move.
func (r *Repository) AdvanceGroupClaimTx(ctx context.Context, tx pgx.Tx, lineIDs []string, qtys []float64) error {
	for i, lineID := range lineIDs {
		if _, err := tx.Exec(ctx, `
			UPDATE order_lines
			   SET fulfilled_base = fulfilled_base + $2,
			       updated_at = now()
			 WHERE id = $1`, lineID, qtys[i]); err != nil {
			return fmt.Errorf("advance group claim: %w", err)
		}
	}
	return nil
}

// AdvanceFulfilledByProductTx advances the order's lines' fulfilled_base by the quantities the
// order's own event carried, matched by product (round 2: the POS ring's base advance, given to
// the ORDER MODULE'S OWN CONSUMER so that no other module writes order_lines).
//
// WHY THE CONSUMER OWNS IT. The ring's COLLECT_NOW sale moves the stock without the order module's
// help, so the base the order's own claim would have advanced never advanced: a ring sale's order
// sat FULFILLED with its lines still reading unfulfilled. The event is the ring's own fact ("this
// much of this product left for this order"), the order module owns order_lines, and a census
// adjustment that let the ring write another module's table is not the answer.
//
// THE ROOM IS THE LINE'S OWN: qty_base - fulfilled_base - released_base. A line that was released
// (cancelled) has no room left, and the CHECK (fulfilled_base + released_base <= qty_base) holds
// the line honest.
//
// THE ORDER OF THE LINES IS THE ORDER'S OWN, and it is what makes the advance idempotent: the
// quantity goes to the first line carrying the product that has room, then the next. A redelivered
// event finds no room and advances nothing.
func (r *Repository) AdvanceFulfilledByProductTx(ctx context.Context, tx pgx.Tx, orderID string, qtys map[string]float64) (int, error) {
	if len(qtys) == 0 {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, COALESCE(product_id::text, ''),
		       (qty_base - fulfilled_base - released_base)::float8
		  FROM order_lines
		 WHERE order_id = $1 AND product_id IS NOT NULL
		 ORDER BY line_no
		   FOR UPDATE`, orderID)
	if err != nil {
		return 0, fmt.Errorf("advance the fulfilled base: %w", err)
	}
	type lineRoom struct {
		lineID    string
		productID string
		room      float64
	}
	var lines []lineRoom
	for rows.Next() {
		var l lineRoom
		if err := rows.Scan(&l.lineID, &l.productID, &l.room); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan the line's room: %w", err)
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	pending := make(map[string]float64, len(qtys))
	for productID, qty := range qtys {
		pending[productID] = qty
	}
	advanced := 0
	for _, l := range lines {
		want := pending[l.productID]
		if want <= 0 || l.room <= 0 {
			continue
		}
		take := l.room
		if want < take {
			take = want
		}
		if _, err := tx.Exec(ctx, `
			UPDATE order_lines
			   SET fulfilled_base = fulfilled_base + $2,
			       updated_at = now()
			 WHERE id = $1`, l.lineID, take); err != nil {
			return advanced, fmt.Errorf("advance the line's fulfilled base: %w", err)
		}
		pending[l.productID] = want - take
		advanced++
	}
	return advanced, nil
}

// OrderFulfilmentDebtTx answers whether the order still owes stock to a customer, and how much is
// still open (round 2, S3's P1: the INVOICED holds rule's own question).
//
// DEFERRED means the order's fulfilment mode puts the stock move in the yard's hands, not the
// counter's: a DELIVERY or WILL_CALL order. Those are the two modes inventory's pending-fulfilment
// read lists as awaiting a stock move. A PICKUP order is not deferred: the customer collects at the
// counter, so its goods left when the sale did.
//
// OPEN is the sum of what the order's stocked lines still owe (qty_base - fulfilled_base -
// released_base). A line with no product is a service line and owes no stock.
//
// Both halves are read ON THE CALLER'S TRANSACTION, so the answer belongs to the transaction that
// asked and not to the pool's view of the world.
func (r *Repository) OrderFulfilmentDebtTx(ctx context.Context, tx pgx.Tx, orderID string) (bool, float64, error) {
	var fulfillment string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(fulfillment, '') FROM orders WHERE id = $1::uuid`, orderID).Scan(&fulfillment); err != nil {
		return false, 0, fmt.Errorf("read order fulfilment: %w", err)
	}
	var open float64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(qty_base - fulfilled_base - released_base), 0)::float8
		  FROM order_lines
		 WHERE order_id = $1::uuid AND product_id IS NOT NULL`, orderID).Scan(&open); err != nil {
		return false, 0, fmt.Errorf("read order open quantity: %w", err)
	}
	deferred := fulfillment == "DELIVERY" || fulfillment == "WILL_CALL"
	return deferred, open, nil
}

// ClaimUnfulfilledLinesTx locks the order's lines, advances fulfilled_base by
// each line's remaining unfulfilled quantity (qty_base - fulfilled_base - released_base),
// and returns the stocked (product_id, qty_base) pairs for this order.
func (r *Repository) ClaimUnfulfilledLinesTx(ctx context.Context, tx pgx.Tx, orderID string) ([]string, []float64, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, COALESCE(product_id::text, ''),
		       (qty_base - fulfilled_base - released_base)::float8,
		       (qty_base - released_base)::float8
		  FROM order_lines
		 WHERE order_id = $1
		 ORDER BY line_no
		   FOR UPDATE`, orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("claim unfulfilled lines: %w", err)
	}
	type lineClaim struct {
		lineID    string
		productID string
		rem       float64
		totalOpen float64
	}
	var claims []lineClaim
	for rows.Next() {
		var c lineClaim
		if err := rows.Scan(&c.lineID, &c.productID, &c.rem, &c.totalOpen); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("scan unfulfilled line: %w", err)
		}
		claims = append(claims, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var productIDs []string
	var qtys []float64
	for _, c := range claims {
		if c.rem > 0.00005 {
			if _, err := tx.Exec(ctx, `
				UPDATE order_lines
				   SET fulfilled_base = fulfilled_base + $2,
				       updated_at = now()
				 WHERE id = $1`, c.lineID, c.rem); err != nil {
				return nil, nil, fmt.Errorf("update line fulfilled_base: %w", err)
			}
			if c.productID != "" {
				productIDs = append(productIDs, c.productID)
				qtys = append(qtys, c.rem)
			}
		} else if c.productID != "" && c.totalOpen > 0.00005 {
			productIDs = append(productIDs, c.productID)
			qtys = append(qtys, c.totalOpen)
		}
	}
	return productIDs, qtys, nil
}

// OrderHolds reads this order's ACTIVE stock reservations for the surface.
//
// It SELECTs stock_reservations, which is inventory's table, and that is a deliberate, narrow
// exception to the zero-trust reading rule with a stated reason: this is a READ of rows THIS MODULE
// WROTE, keyed by (source_type='order', source_ref_id=this order). It writes nothing, holds no lock
// and computes no availability, every number that means anything (available, in-transit, the
// four-bucket view) is still read through inventory's own surface. Routing it through a seam would
// mean either inventory growing an order-shaped query or this panel making N round trips; both are
// worse than one honest projection over rows we own. If stock_reservations ever moves, this query
// is the single grep.
func (r *Repository) OrderHolds(ctx context.Context, orderID string) ([]OrderHold, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `
		SELECT sr.id::text, sr.product_id::text, COALESCE(p.name,''), COALESCE(p.sku::text,''),
		       sr.branch_id::text, sr.hold_mode, sr.qty_base, sr.status,
		       to_char(sr.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		  FROM stock_reservations sr
		  LEFT JOIN products p ON p.id = sr.product_id
		 WHERE sr.source_type = 'order' AND sr.source_ref_id = $1 AND sr.status = 'ACTIVE'
		 ORDER BY sr.created_at, sr.id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("order holds: %w", err)
	}
	defer rows.Close()
	out := []OrderHold{}
	for rows.Next() {
		var h OrderHold
		if err := rows.Scan(&h.ReservationID, &h.ProductID, &h.ProductName, &h.SKU,
			&h.BranchID, &h.HoldMode, &h.QtyBase, &h.Status, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order hold: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
