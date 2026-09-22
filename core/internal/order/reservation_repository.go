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
		if err := precheck(ctx); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return oldStatus, fmt.Errorf("transition: commit: %w", err)
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
