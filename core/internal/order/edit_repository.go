// edit_repository.go, DIB-175 the guarded order-edit writes (pgx).
//
// Every mutation here follows ONE discipline, and it is the reason this file exists separately from the
// service:
//
//	BEGIN
//	  SELECT status, tax_cents FROM orders WHERE id = $1 FOR UPDATE   ← lock + re-read the latest committed row
//	  status ∈ allowedFrom ?                                          ← the matrix guard, evaluated INSIDE the lock
//	  precheck(tax_cents)                                            ← the money predicate, INSIDE the lock (B2-2)
//	  <mutate the line / the date>
//	  UPDATE orders SET subtotal_cents = Σ line_total_cents, total_cents = Σ + tax_cents
//	  record(tx, before, after, totals)                             ← the audit trail, on THIS tx (B2-1)
//	COMMIT   ← so the mutation, the totals, and the trail are one atomic unit
//
// Why the guard is re-evaluated here and not trusted from the service. The service checks the matrix
// against a status it read moments earlier, unlocked. Between that read and this write a concurrent
// DIB-191 transition can commit, so an edit authorised against CONFIRMED could land on an order that
// is now READY. That is precisely the check-then-write race UpdateStatusFrom closes for status
// transitions (repository.go), and a content edit slipping past the READY wall is the same class of
// defect. Under READ COMMITTED, `SELECT … FOR UPDATE` waits for any in-flight writer to commit and then
// re-reads the latest committed row, so the status this guard sees is the true immediately-prior value.
//
// The precheck + record HOOKS (B2 correction round) fold two things that used to happen OUTSIDE this
// transaction back inside it: the money predicate (was in authorizeEdit, a window a concurrent invoice
// could slip through, B2-2) and the audit write (was after commit + best-effort, so a committed edit
// could carry no trail, B2-1). Both now share the mutation's fate: precheck failure aborts before any
// write; record failure rolls the whole edit back.
//
// Totals are recomputed from the lines themselves (Σ line_total_cents) rather than adjusted by a delta,
// so the order's money is always a function of its current lines and can never drift from them through
// a missed or double-applied increment. tax_cents is READ but never written: M5 owns tax, and a
// money-moving edit is only permitted when tax is zero (the precheck).
package order

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// lockOrderForEdit locks the order row and re-evaluates the status guard inside that lock. It returns
// the order's true current status AND its tax_cents, both read under the lock so the precheck's money
// test (B2-2) sees the same committed snapshot the mutation will. A status outside allowedFrom yields a
// *NotEditableError with the status named but the Field left empty, the calling service method fills
// the field in, since only it knows which command was attempted (nameEditField, edit.go).
func lockOrderForEdit(ctx context.Context, tx pgx.Tx, orderID string, allowedFrom []string) (status string, taxCents int64, err error) {
	err = tx.QueryRow(ctx,
		`SELECT status, tax_cents FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&status, &taxCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrNotFound
		}
		return "", 0, fmt.Errorf("lock order %q for edit: %w", orderID, err)
	}
	for _, s := range allowedFrom {
		if s == status {
			return status, taxCents, nil
		}
	}
	return status, taxCents, &NotEditableError{Status: status}
}

// runPrecheck runs the guarded write's money predicate under the lock (nil hook = no predicate, e.g. a
// date edit).
//
// LOCK CONVENTION, the guarantee and its boundary (B2-2). Running the predicate here, after the
// orders-row FOR UPDATE lock and immediately before the mutation, closes the check→write window against
// every writer that RESPECTS that lock: such a writer cannot commit money onto the order between our
// predicate read and our write, because it is serialized behind the same lock. It does NOT physically
// stop a path that attaches money to an EXISTING order WITHOUT holding the lock, no in-process check
// can. Today no such path exists (the POS ring creates the order and its invoice in one transaction;
// the event-driven status advance takes the same FOR UPDATE on the orders row). This is therefore a
// standing DESIGN CONSTRAINT for future work, AR-finalization in particular: any path that attaches
// money to an already-existing order MUST hold that order's row lock while doing so, or this guarantee
// (and the DIB-191 cancel predicate that shares it) silently weakens.
//
// The precheck joins the guarded write's own transaction (withPrecheckTx), so its invoice read does not
// take a second pool connection while this transaction still holds one.
func runPrecheck(ctx context.Context, tx pgx.Tx, hooks lineEditHooks, taxCents int64) error {
	if hooks.precheck == nil {
		return nil
	}
	return hooks.precheck(withPrecheckTx(ctx, tx), taxCents)
}

// recomputeTotals rewrites the order's money from its CURRENT lines, inside the caller's transaction,
// and returns what it wrote. Σ over zero lines is 0 (COALESCE), so emptying an order zeroes it rather
// than leaving the last computed total standing.
func recomputeTotals(ctx context.Context, tx pgx.Tx, orderID string) (OrderTotals, error) {
	var t OrderTotals
	err := tx.QueryRow(ctx, `
UPDATE orders SET
    subtotal_cents = COALESCE((SELECT SUM(line_total_cents) FROM order_lines WHERE order_id = $1), 0),
    total_cents    = COALESCE((SELECT SUM(line_total_cents) FROM order_lines WHERE order_id = $1), 0)
                     + tax_cents,
    updated_at     = now()
WHERE id = $1
RETURNING subtotal_cents, total_cents`, orderID).Scan(&t.SubtotalCents, &t.TotalCents)
	if err != nil {
		return OrderTotals{}, fmt.Errorf("recompute order %q totals: %w", orderID, err)
	}
	return t, nil
}

// editLineColumns is the canonical snapshot SELECT/RETURNING list for an edited line. Quantities come
// back as text so what we record on the change trail is byte-identical to what the DECIMAL column
// stores (never a float round-trip).
const editLineColumns = `line_no, COALESCE(product_id::text, ''), qty_entered::text, qty_base::text,
	unit_price_cents, line_total_cents`

func scanLineSnapshot(row rowScanner) (LineSnapshot, error) {
	var l LineSnapshot
	err := row.Scan(&l.LineNo, &l.ProductID, &l.QtyEntered, &l.QtyBase, &l.UnitPriceCents, &l.LineTotalCents)
	return l, err
}

// OrderEditHeader reads the three facts every edit guard needs, status, owning branch, assessed tax -
// in one query. A malformed (non-UUID) id short-circuits to ErrNotFound before the query, since
// orders.id is a uuid column and a garbage id would otherwise raise SQLSTATE 22P02 → HTTP 500 (mirrors
// OrderBranch / OrderHeader).
func (r *Repository) OrderEditHeader(ctx context.Context, orderID string) (status, branchID string, taxCents int64, err error) {
	if r.pool == nil {
		return "", "", 0, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", "", 0, ErrNotFound
	}
	err = r.pool.QueryRow(ctx,
		`SELECT status, COALESCE(branch_id::text, ''), tax_cents FROM orders WHERE id = $1`,
		orderID).Scan(&status, &branchID, &taxCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", 0, ErrNotFound
		}
		return "", "", 0, fmt.Errorf("read order edit header %q: %w", orderID, err)
	}
	return status, branchID, taxCents, nil
}

// OrderPricingContext resolves the customer + branch an edited line must be priced for.
func (r *Repository) OrderPricingContext(ctx context.Context, orderID string) (customerID, branchID string, err error) {
	if r.pool == nil {
		return "", "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", "", ErrNotFound
	}
	err = r.pool.QueryRow(ctx,
		`SELECT customer_id::text, COALESCE(branch_id::text, '') FROM orders WHERE id = $1`,
		orderID).Scan(&customerID, &branchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("read order pricing context %q: %w", orderID, err)
	}
	return customerID, branchID, nil
}

// OrderLines reads an order's lines + its money for the staff order read. Quantities come back as text
// so the surface renders the exact DECIMAL the column holds, never a float round-trip.
//
// B2-3: the header money and the lines are read in ONE read-only REPEATABLE READ transaction, so both
// come from a single MVCC snapshot. Two bare pool queries could straddle a concurrent edit's commit and
// return a subtotal that disagrees with Σ(lines) (the panel probe saw exactly that). REPEATABLE READ
// pins the snapshot for the pair; read-only means it never blocks a writer.
func (r *Repository) OrderLines(ctx context.Context, orderID string) ([]OrderViewLine, OrderTotals, int64, error) {
	if r.pool == nil {
		return nil, OrderTotals{}, 0, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return nil, OrderTotals{}, 0, ErrNotFound
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, OrderTotals{}, 0, fmt.Errorf("read order %q: begin: %w", orderID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: rollback is the clean close either way

	var totals OrderTotals
	var taxCents int64
	if err := tx.QueryRow(ctx,
		`SELECT subtotal_cents, tax_cents, total_cents FROM orders WHERE id = $1`,
		orderID).Scan(&totals.SubtotalCents, &taxCents, &totals.TotalCents); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, OrderTotals{}, 0, ErrNotFound
		}
		return nil, OrderTotals{}, 0, fmt.Errorf("read order %q money: %w", orderID, err)
	}
	rows, err := tx.Query(ctx,
		`SELECT line_no, COALESCE(product_id::text, ''), description, qty_entered::text,
		        uom_entered::text, qty_base::text, allocated_base::text, fulfilled_base::text,
		        unit_price_cents, line_total_cents, backorder_entered::text, backorder_base::text
		   FROM order_lines WHERE order_id = $1 ORDER BY line_no`, orderID)
	if err != nil {
		return nil, OrderTotals{}, 0, fmt.Errorf("read order %q lines: %w", orderID, err)
	}
	defer rows.Close()
	out := []OrderViewLine{}
	for rows.Next() {
		var l OrderViewLine
		if err := rows.Scan(&l.LineNo, &l.ProductID, &l.Description, &l.QtyEntered, &l.UOMEntered,
			&l.QtyBase, &l.AllocatedBase, &l.FulfilledBase, &l.UnitPriceCents, &l.LineTotalCents,
			&l.BackorderEntered, &l.BackorderBase); err != nil {
			return nil, OrderTotals{}, 0, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, OrderTotals{}, 0, err
	}
	return out, totals, taxCents, nil
}

// LineForEdit reads one line's current state plus its allocated/fulfilled progress, so the service can
// reprice it and enforce the fulfilment invariants before attempting the write.
func (r *Repository) LineForEdit(ctx context.Context, orderID string, lineNo int) (LineSnapshot, string, float64, float64, error) {
	if r.pool == nil {
		return LineSnapshot{}, "", 0, 0, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return LineSnapshot{}, "", 0, 0, ErrNotFound
	}
	var l LineSnapshot
	var uom string
	var allocated, fulfilled float64
	err := r.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s, uom_entered::text, allocated_base::float8, fulfilled_base::float8
		   FROM order_lines WHERE order_id = $1 AND line_no = $2`, editLineColumns),
		orderID, lineNo).Scan(&l.LineNo, &l.ProductID, &l.QtyEntered, &l.QtyBase,
		&l.UnitPriceCents, &l.LineTotalCents, &uom, &allocated, &fulfilled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LineSnapshot{}, "", 0, 0, ErrLineNotFound
		}
		return LineSnapshot{}, "", 0, 0, fmt.Errorf("read order %q line %d: %w", orderID, lineNo, err)
	}
	return l, uom, allocated, fulfilled, nil
}

// UpdateLineGuarded applies a repriced quantity to one line and recomputes the order totals, all inside
// one transaction under the order row lock, with the status guard, the money precheck, and the audit
// record all evaluated INSIDE that transaction (see the file header).
func (r *Repository) UpdateLineGuarded(ctx context.Context, w lineEditWrite, hooks lineEditHooks) (before, after LineSnapshot, totals OrderTotals, status string, err error) {
	if r.pool == nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, "", errNoDB
	}
	if !looksLikeUUID(w.OrderID) {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, "", fmt.Errorf("edit line: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var taxCents int64
	status, taxCents, err = lockOrderForEdit(ctx, tx, w.OrderID, w.AllowedFrom)
	if err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, err
	}
	if err = runPrecheck(ctx, tx, hooks, taxCents); err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, err
	}
	// The pre-image, read under the same lock so it is the true value the edit replaced.
	before, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM order_lines WHERE order_id = $1 AND line_no = $2 FOR UPDATE`, editLineColumns),
		w.OrderID, w.LineNo))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, ErrLineNotFound
		}
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("edit line: pre-image: %w", err)
	}
	// fulfilled_base <= qty_base is a 0008 CHECK; re-assert it here under the lock so a concurrent
	// fulfilment cannot land between the service's check and this write and turn a valid edit into an
	// opaque constraint violation.
	after, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(
		`UPDATE order_lines
		    SET qty_entered = $3::decimal, qty_base = $4::decimal,
		        unit_price_cents = $5, price_uom = $6, line_total_cents = $7, updated_at = now()
		  WHERE order_id = $1 AND line_no = $2 AND fulfilled_base <= $4::decimal
		  RETURNING %s`, editLineColumns),
		w.OrderID, w.LineNo, w.QtyEntered, w.QtyBase, w.UnitPriceCents, w.PriceUOM, w.LineTotalCents))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, ErrLineInUse
		}
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("edit line: update: %w", err)
	}
	if err = clearedByEdit(ctx, tx, hooks, w.OrderID, w.LineNo); err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, err
	}
	totals, err = recomputeTotals(ctx, tx, w.OrderID)
	if err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, err
	}
	if err = runLineRecord(ctx, hooks, tx, before, after, totals); err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LineSnapshot{}, LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("edit line: commit: %w", err)
	}
	return before, after, totals, status, nil
}

// InsertLineGuarded appends a priced line at the next free line_no and recomputes the order totals, in
// one transaction under the order row lock. line_no is derived inside that lock, so two concurrent adds
// cannot collide on the (order_id, line_no) unique index.
func (r *Repository) InsertLineGuarded(ctx context.Context, w lineEditWrite, hooks lineEditHooks) (after LineSnapshot, totals OrderTotals, status string, err error) {
	if r.pool == nil {
		return LineSnapshot{}, OrderTotals{}, "", errNoDB
	}
	if !looksLikeUUID(w.OrderID) {
		return LineSnapshot{}, OrderTotals{}, "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, "", fmt.Errorf("add line: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var taxCents int64
	status, taxCents, err = lockOrderForEdit(ctx, tx, w.OrderID, w.AllowedFrom)
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	if err = runPrecheck(ctx, tx, hooks, taxCents); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	after, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(
		`INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered,
		                          uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		 SELECT $1,
		        COALESCE((SELECT MAX(line_no) FROM order_lines WHERE order_id = $1), 0) + 1,
		        $2::uuid, $3, $4::decimal, $5, $6::decimal, $7, $8, $9
		 RETURNING %s`, editLineColumns),
		w.OrderID, w.ProductID, w.Description, w.QtyEntered, w.UOMEntered, w.QtyBase,
		w.UnitPriceCents, w.PriceUOM, w.LineTotalCents))
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("add line: insert: %w", err)
	}
	if err = rebaseShortfallBasisTx(ctx, tx, w.OrderID); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	totals, err = recomputeTotals(ctx, tx, w.OrderID)
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	// An add has no pre-image; the record hook receives a zero `before`.
	if err = runLineRecord(ctx, hooks, tx, LineSnapshot{}, after, totals); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("add line: commit: %w", err)
	}
	return after, totals, status, nil
}

// DeleteLineGuarded removes a line and recomputes the order totals, in one transaction under the order
// row lock.
//
// The DELETE is conditional on allocated_base = 0 AND fulfilled_base = 0, re-asserting under the lock
// what the service already checked: a concurrent allocation must not be able to land between the two
// and leave us deleting a line the yard has committed against. The pre-image is RETURNED from the
// delete itself, it is the only surviving record of what was removed (C1), since the row is gone; it
// is handed to the record hook so the trail names it before the row vanishes.
func (r *Repository) DeleteLineGuarded(ctx context.Context, orderID string, lineNo int, allowedFrom []string, hooks lineEditHooks) (before LineSnapshot, totals OrderTotals, status string, err error) {
	if r.pool == nil {
		return LineSnapshot{}, OrderTotals{}, "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return LineSnapshot{}, OrderTotals{}, "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, "", fmt.Errorf("remove line: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var taxCents int64
	status, taxCents, err = lockOrderForEdit(ctx, tx, orderID, allowedFrom)
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	if err = runPrecheck(ctx, tx, hooks, taxCents); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	// Distinguish "no such line" (404) from "line is in use" (409): the conditional delete returns no
	// rows for BOTH, so probe existence first, under the same lock.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT true FROM order_lines WHERE order_id = $1 AND line_no = $2 FOR UPDATE`,
		orderID, lineNo).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LineSnapshot{}, OrderTotals{}, status, ErrLineNotFound
		}
		return LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("remove line: probe: %w", err)
	}
	short, err := readShortTx(ctx, tx, orderID, lineNo, "0.0000")
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	before, err = scanLineSnapshot(tx.QueryRow(ctx, fmt.Sprintf(
		`DELETE FROM order_lines
		  WHERE order_id = $1 AND line_no = $2 AND allocated_base = 0 AND fulfilled_base = 0
		  RETURNING %s`, editLineColumns), orderID, lineNo))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LineSnapshot{}, OrderTotals{}, status, ErrLineInUse
		}
		return LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("remove line: delete: %w", err)
	}
	if short != nil && hooks.cleared != nil {
		if err = hooks.cleared(ctx, tx, *short); err != nil {
			return LineSnapshot{}, OrderTotals{}, status, err
		}
	}
	if err = rebaseShortfallBasisTx(ctx, tx, orderID); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	totals, err = recomputeTotals(ctx, tx, orderID)
	if err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	// A remove has no post-image; the record hook receives a zero `after`.
	if err = runLineRecord(ctx, hooks, tx, before, LineSnapshot{}, totals); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LineSnapshot{}, OrderTotals{}, status, fmt.Errorf("remove line: commit: %w", err)
	}
	return before, totals, status, nil
}

// runLineRecord invokes the audit record hook on the tx (B2-1). A nil hook means no transactional
// recorder is wired (a fake that lacks RecordTx); the edit then commits with no trail, mirroring the
// pre-B2 best-effort posture for that degenerate case. In production the hook is always present.
func runLineRecord(ctx context.Context, hooks lineEditHooks, tx pgx.Tx, before, after LineSnapshot, totals OrderTotals) error {
	if hooks.record == nil {
		return nil
	}
	return hooks.record(ctx, tx, before, after, totals)
}

// UpdateRequestedDateGuarded moves orders.requested_date under the same row lock + status guard, and
// records the change on the same tx (B2-1). An empty newDate clears the column (it is nullable); a
// cleared or absent date reads back as "". No money precheck, a date is not money.
func (r *Repository) UpdateRequestedDateGuarded(ctx context.Context, orderID, newDate string, allowedFrom []string, hooks dateEditHooks) (before, after, status string, err error) {
	if r.pool == nil {
		return "", "", "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", "", "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("edit requested date: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, _, err = lockOrderForEdit(ctx, tx, orderID, allowedFrom)
	if err != nil {
		return "", "", status, err
	}
	// The pre-image, read explicitly under the lock. (Reading it from a subquery inside the UPDATE's
	// RETURNING would depend on statement-snapshot semantics to see the old value, correct today, but
	// far too subtle to rest an audit trail on.) A NULL date reads back as "".
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(to_char(requested_date, 'YYYY-MM-DD'), '') FROM orders WHERE id = $1`,
		orderID).Scan(&before); err != nil {
		return "", "", status, fmt.Errorf("edit requested date: pre-image: %w", err)
	}
	// NULLIF($2,'') → NULL, so an empty newDate clears the column in the same statement shape.
	if err := tx.QueryRow(ctx, `
UPDATE orders SET requested_date = NULLIF($2, '')::date, updated_at = now()
 WHERE id = $1
 RETURNING COALESCE(to_char(requested_date, 'YYYY-MM-DD'), '')`,
		orderID, newDate).Scan(&after); err != nil {
		return "", "", status, fmt.Errorf("edit requested date: update: %w", err)
	}
	if hooks.record != nil {
		if err := hooks.record(ctx, tx, before, after); err != nil {
			return "", "", status, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", status, fmt.Errorf("edit requested date: commit: %w", err)
	}
	return before, after, status, nil
}

// UpdateStatusFromChecked is the DIB-191 CANCEL path's guarded write (B2-2): a status transition whose
// money predicate runs INSIDE the order row lock, so no writer can attach money in a check→write gap.
//
// It exists because the plain UpdateStatusFrom is a single locked-CTE statement with no seam for a
// Go-side predicate mid-way. Only the cancel action needs the predicate, so only the cancel action
// routes here; picking/ready/confirm and every event advance keep the single-statement UpdateStatusFrom
// (AdvanceFromEvent is deliberately untouched, minimal blast radius).
//
// Outcomes match UpdateStatusFrom exactly: id absent → ErrNotFound; current == newStatus →
// ErrNoTransition; current ∉ allowedFrom → ErrIllegalTransition. The precheck runs after the legality
// is confirmed (an illegal cancel is refused without consulting money state) and before the write; its
// error propagates and rolls the transition back.
func (r *Repository) UpdateStatusFromChecked(ctx context.Context, orderID, newStatus string, allowedFrom []string, precheck func(ctx context.Context) error) (oldStatus string, err error) {
	if r.pool == nil {
		return "", errNoDB
	}
	if !looksLikeUUID(orderID) {
		return "", ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("cancel: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx,
		`SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&oldStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("cancel: lock order %q: %w", orderID, err)
	}
	if oldStatus == newStatus {
		return oldStatus, ErrNoTransition // already there, a benign no-op (matches UpdateStatusFrom)
	}
	if !containsStatus(allowedFrom, oldStatus) {
		return oldStatus, ErrIllegalTransition // not a legal source for this move (forward-only)
	}
	if precheck != nil {
		if err := precheck(withPrecheckTx(ctx, tx)); err != nil {
			return oldStatus, err // e.g. ErrCancelBlocked / ErrCancelUnverifiable, refuses under the lock
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = $2, updated_at = now() WHERE id = $1`, orderID, newStatus); err != nil {
		return oldStatus, fmt.Errorf("cancel: update order %q: %w", orderID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return oldStatus, fmt.Errorf("cancel: commit: %w", err)
	}
	return oldStatus, nil
}

// containsStatus reports whether status is in the allowed-from set.
func containsStatus(allowedFrom []string, status string) bool {
	for _, s := range allowedFrom {
		if s == status {
			return true
		}
	}
	return false
}

// clearedByEdit runs after an edit set a line's quantity: a short line's backorder is cleared (the edit's
// quantity is the new order quantity), the hook records it, and the header's ordered basis is rebased.
func clearedByEdit(ctx context.Context, tx pgx.Tx, hooks lineEditHooks, orderID string, lineNo int) error {
	c, err := clearShortfallTx(ctx, tx, orderID, lineNo)
	if err != nil {
		return err
	}
	if c != nil && hooks.cleared != nil {
		if err := hooks.cleared(ctx, tx, *c); err != nil {
			return err
		}
	}
	return rebaseShortfallBasisTx(ctx, tx, orderID)
}
