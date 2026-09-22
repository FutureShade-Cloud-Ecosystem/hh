package order

// change_repository.go, the pgx half of SCOPE:F10 (change.go).
//
// The interesting method is ApplyChangeTx. Everything else is a guarded status write in the shape
// this module already uses everywhere: lock the row, re-evaluate the guard INSIDE the lock, write.
//
// - WHY THE LOCKS ARE TAKEN IN THIS ORDER -
// ApplyChangeTx locks the ORDER first, then the CHANGE. Two writers touching the same order, an
// apply and a status transition, or two applies, must acquire in the same order or they deadlock,
// and the order row is the one every other writer in this module already takes first
// (UpdateStatusFromTx, LockOrderTx, lockOrderForEdit). Adding the change lock UNDERNEATH that
// existing lock keeps one global order across the module.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// compile-time proof the real repository satisfies the change-order store.
var _ changeStore = (*Repository)(nil)

// changeApplyHooks are the two closures the service threads INTO the apply transaction: the money
// predicate (run under the order lock, before any line write) and the reservation resync (run after
// the lines and totals, before commit). Both share the mutation's fate.
type changeApplyHooks struct {
	precheck func(ctx context.Context, taxCents int64) error
	resync   func(ctx context.Context, tx pgx.Tx, orderID, branchID string) error
}

// changeStore is the change-order persistence, asserted OFF the injected orderStore (the editStore /
// reservationStore pattern) so every existing service fake keeps compiling.
type changeStore interface {
	InsertChangeOrder(ctx context.Context, co ChangeOrder) (ChangeOrder, error)
	GetChangeOrder(ctx context.Context, id string) (ChangeOrder, error)
	ListChangeOrders(ctx context.Context, orderID string) ([]ChangeOrder, error)
	RecordYardAck(ctx context.Context, id, actor, note, nextStatus string) (ChangeOrder, error)
	RecordFinanceApproval(ctx context.Context, id, actor, note, nextStatus string) (ChangeOrder, error)
	CloseChangeOrder(ctx context.Context, id, target, actor, note string) (ChangeOrder, error)
	ApplyChangeTx(ctx context.Context, id string, hooks changeApplyHooks) (ChangeOrder, error)
}

const changeOrderColumns = `
	co.id::text, co.order_id::text, COALESCE(co.branch_id::text,''), co.change_no, co.status,
	co.reason, co.delta_cents, co.finance_threshold_cents, co.finance_required,
	co.order_status_at_request, co.pick_started_at_request,
	COALESCE(co.requested_by::text,''),
	to_char(co.requested_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	COALESCE(co.yard_acked_by::text,''),
	COALESCE(to_char(co.yard_acked_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
	co.yard_ack_note, COALESCE(co.yard_task_id::text,''),
	COALESCE(co.finance_approved_by::text,''),
	COALESCE(to_char(co.finance_approved_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
	co.finance_note, co.decision_note,
	COALESCE(to_char(co.applied_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'')`

func scanChangeOrder(row pgx.Row) (ChangeOrder, error) {
	var c ChangeOrder
	err := row.Scan(&c.ID, &c.OrderID, &c.BranchID, &c.ChangeNo, &c.Status,
		&c.Reason, &c.DeltaCents, &c.FinanceThresholdCents, &c.FinanceRequired,
		&c.OrderStatusAtRequest, &c.PickStartedAtRequest,
		&c.RequestedBy, &c.RequestedAt,
		&c.YardAckedBy, &c.YardAckedAt, &c.YardAckNote, &c.YardTaskID,
		&c.FinanceApprovedBy, &c.FinanceApprovedAt, &c.FinanceNote, &c.DecisionNote,
		&c.AppliedAt)
	return c, err
}

// InsertChangeOrder writes the request + its delta rows in one transaction.
//
// The one-open-request rule is enforced by the 0125 partial unique index, and the 23505 it raises is
// translated to ErrChangeOpenExists here. Checking first in Go would be a check-then-write race with
// a second raise on another connection; letting the index decide makes it impossible.
func (r *Repository) InsertChangeOrder(ctx context.Context, co ChangeOrder) (ChangeOrder, error) {
	if r.pool == nil {
		return ChangeOrder{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ChangeOrder{}, fmt.Errorf("raise change: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO change_orders
			(order_id, branch_id, change_no, status, reason, delta_cents,
			 finance_threshold_cents, finance_required, order_status_at_request,
			 pick_started_at_request, requested_by, yard_task_id)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, '#CO-' || nextval('change_order_no_seq'), $3, $4, $5,
		        $6, $7, $8, $9, NULLIF($10,'')::uuid, NULLIF($11,'')::uuid)
		RETURNING id::text`,
		co.OrderID, co.BranchID, co.Status, co.Reason, co.DeltaCents,
		co.FinanceThresholdCents, co.FinanceRequired, co.OrderStatusAtRequest,
		co.PickStartedAtRequest, co.RequestedBy, co.YardTaskID).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return ChangeOrder{}, ErrChangeOpenExists
		}
		return ChangeOrder{}, fmt.Errorf("raise change: insert: %w", err)
	}
	for _, l := range co.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO change_order_lines
				(change_order_id, seq, op, line_no, product_id, description, qty_entered_new,
				 uom_entered, qty_base_before, qty_base_after, qty_delta_base,
				 unit_price_cents, price_uom, line_delta_cents)
			VALUES ($1::uuid, $2, $3, NULLIF($4,0), NULLIF($5,'')::uuid, $6, NULLIF($7,'')::numeric,
			        NULLIF($8,'')::citext, $9, $10, $11, $12, NULLIF($13,'')::citext, $14)`,
			id, l.Seq, l.Op, l.LineNo, l.ProductID, l.Description, l.QtyEnteredNew,
			l.UOMEntered, l.QtyBaseBefore, l.QtyBaseAfter, l.QtyDeltaBase,
			l.UnitPriceCents, l.PriceUOM, l.LineDeltaCents); err != nil {
			return ChangeOrder{}, fmt.Errorf("raise change: insert line %d: %w", l.Seq, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ChangeOrder{}, fmt.Errorf("raise change: commit: %w", err)
	}
	return r.GetChangeOrder(ctx, id)
}

// GetChangeOrder loads one request with its delta rows.
func (r *Repository) GetChangeOrder(ctx context.Context, id string) (ChangeOrder, error) {
	if r.pool == nil {
		return ChangeOrder{}, errNoDB
	}
	co, err := scanChangeOrder(r.pool.QueryRow(ctx,
		`SELECT `+changeOrderColumns+` FROM change_orders co WHERE co.id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChangeOrder{}, ErrChangeNotFound
		}
		return ChangeOrder{}, fmt.Errorf("get change %q: %w", id, err)
	}
	co.Lines, err = r.changeLines(ctx, id)
	if err != nil {
		return ChangeOrder{}, err
	}
	return co, nil
}

func (r *Repository) changeLines(ctx context.Context, changeID string) ([]ChangeLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT cl.seq, cl.op, COALESCE(cl.line_no,0), COALESCE(cl.product_id::text,''),
		       COALESCE(p.name,''), cl.description,
		       COALESCE(cl.qty_entered_new::text,''), COALESCE(cl.uom_entered::text,''),
		       cl.qty_base_before, cl.qty_base_after, cl.qty_delta_base,
		       cl.unit_price_cents, COALESCE(cl.price_uom::text,''), cl.line_delta_cents
		  FROM change_order_lines cl
		  LEFT JOIN products p ON p.id = cl.product_id
		 WHERE cl.change_order_id = $1
		 ORDER BY cl.seq`, changeID)
	if err != nil {
		return nil, fmt.Errorf("change lines: %w", err)
	}
	defer rows.Close()
	out := []ChangeLine{}
	for rows.Next() {
		var l ChangeLine
		if err := rows.Scan(&l.Seq, &l.Op, &l.LineNo, &l.ProductID, &l.ProductName, &l.Description,
			&l.QtyEnteredNew, &l.UOMEntered, &l.QtyBaseBefore, &l.QtyBaseAfter, &l.QtyDeltaBase,
			&l.UnitPriceCents, &l.PriceUOM, &l.LineDeltaCents); err != nil {
			return nil, fmt.Errorf("scan change line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListChangeOrders returns an order's change history, newest first.
func (r *Repository) ListChangeOrders(ctx context.Context, orderID string) ([]ChangeOrder, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+changeOrderColumns+` FROM change_orders co
		  WHERE co.order_id = $1 ORDER BY co.created_at DESC, co.change_no DESC`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list changes: %w", err)
	}
	ids := []string{}
	out := []ChangeOrder{}
	for rows.Next() {
		co, err := scanChangeOrder(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan change: %w", err)
		}
		out = append(out, co)
		ids = append(ids, co.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		lines, err := r.changeLines(ctx, ids[i])
		if err != nil {
			return nil, err
		}
		out[i].Lines = lines
	}
	return out, nil
}

// RecordYardAck stamps the acknowledgement and advances the ladder, guarded on the CURRENT status
// inside the same statement, so two yard leads pressing ACK at once produce one ACK and one 409.
func (r *Repository) RecordYardAck(ctx context.Context, id, actor, note, nextStatus string) (ChangeOrder, error) {
	return r.guardedChangeUpdate(ctx, id, `
		UPDATE change_orders
		   SET status = $2, yard_acked_by = NULLIF($3,'')::uuid, yard_acked_at = now(),
		       yard_ack_note = $4, updated_at = now()
		 WHERE id = $1 AND status = 'PENDING_YARD'`,
		nextStatus, actor, note)
}

// RecordFinanceApproval stamps the controller's signature, guarded on PENDING_FINANCE.
func (r *Repository) RecordFinanceApproval(ctx context.Context, id, actor, note, nextStatus string) (ChangeOrder, error) {
	return r.guardedChangeUpdate(ctx, id, `
		UPDATE change_orders
		   SET status = $2, finance_approved_by = NULLIF($3,'')::uuid, finance_approved_at = now(),
		       finance_note = $4, updated_at = now()
		 WHERE id = $1 AND status = 'PENDING_FINANCE'`,
		nextStatus, actor, note)
}

// CloseChangeOrder rejects or withdraws a live request.
func (r *Repository) CloseChangeOrder(ctx context.Context, id, target, actor, note string) (ChangeOrder, error) {
	return r.guardedChangeUpdate(ctx, id, `
		UPDATE change_orders
		   SET status = $2, decided_by = NULLIF($3,'')::uuid, decided_at = now(),
		       decision_note = $4, updated_at = now()
		 WHERE id = $1 AND status IN ('DRAFT','PENDING_YARD','PENDING_FINANCE','APPROVED')`,
		target, actor, note)
}

// guardedChangeUpdate runs a status-guarded UPDATE and translates "no row matched" into
// ErrChangeTerminal, the row exists but is no longer in a state this action is legal from, which is
// a 409 and not a 404. The guard lives in the WHERE clause so it is evaluated by the same statement
// that writes, with no window between.
func (r *Repository) guardedChangeUpdate(ctx context.Context, id, sql string, args ...any) (ChangeOrder, error) {
	if r.pool == nil {
		return ChangeOrder{}, errNoDB
	}
	all := append([]any{id}, args...)
	tag, err := r.pool.Exec(ctx, sql, all...)
	if err != nil {
		return ChangeOrder{}, fmt.Errorf("change decision: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Distinguish "gone" from "already moved on": a missing id is 404, a wrong state is 409.
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM change_orders WHERE id=$1)`, id).
			Scan(&exists); err != nil {
			return ChangeOrder{}, err
		}
		if !exists {
			return ChangeOrder{}, ErrChangeNotFound
		}
		return ChangeOrder{}, ErrChangeTerminal
	}
	return r.GetChangeOrder(ctx, id)
}

// ApplyChangeTx is the atomic apply. See change.go's ApplyChange doc for the sequence and the
// header of this file for the lock order.
func (r *Repository) ApplyChangeTx(ctx context.Context, id string, hooks changeApplyHooks) (ChangeOrder, error) {
	if r.pool == nil {
		return ChangeOrder{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ChangeOrder{}, fmt.Errorf("apply change: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The order id is read WITHOUT a lock first, only to know which order row to lock. The change row
	// itself is then locked underneath it and its status re-read, so nothing is decided on this read.
	var orderID string
	if err := tx.QueryRow(ctx, `SELECT order_id::text FROM change_orders WHERE id = $1`, id).
		Scan(&orderID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChangeOrder{}, ErrChangeNotFound
		}
		return ChangeOrder{}, fmt.Errorf("apply change: resolve order: %w", err)
	}

	var orderStatus, branchID string
	var taxCents int64
	if err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text, tax_cents FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&orderStatus, &branchID, &taxCents); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChangeOrder{}, ErrNotFound
		}
		return ChangeOrder{}, fmt.Errorf("apply change: lock order: %w", err)
	}
	// An order that has left the workable range cannot absorb a delta: its lines are the basis of an
	// invoice, a POS sale or nothing at all. Refused here rather than at the request, because the
	// order may legitimately have moved on WHILE the signatures were being collected, which is the
	// direct consequence of ruling 1 (the order stays workable) and has to be handled, not assumed away.
	switch orderStatus {
	case "DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED":
	default:
		return ChangeOrder{}, fmt.Errorf(
			"%w: the order is %s and can no longer absorb a change", ErrChangeTerminal, orderStatus)
	}

	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM change_orders WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
		return ChangeOrder{}, fmt.Errorf("apply change: lock change: %w", err)
	}
	// THE re-entrancy guard: only APPROVED applies, and the flip below happens in this transaction, so
	// a second concurrent apply blocks on this lock and then finds APPLIED.
	if status != ChangeStatusApproved {
		if isTerminalChange(status) {
			return ChangeOrder{}, ErrChangeTerminal
		}
		return ChangeOrder{}, ErrChangeNotApproved
	}

	if hooks.precheck != nil {
		if err := hooks.precheck(ctx, taxCents); err != nil {
			return ChangeOrder{}, err
		}
	}

	lines, err := r.changeLinesTx(ctx, tx, id)
	if err != nil {
		return ChangeOrder{}, err
	}
	for _, l := range lines {
		if err := applyChangeLineTx(ctx, tx, orderID, l); err != nil {
			return ChangeOrder{}, err
		}
	}

	// Totals from the LINES, never a delta increment, the edit_repository.go discipline, for the same
	// reason: the order's money must be a function of its current lines and cannot drift from them.
	if _, err := tx.Exec(ctx, `
		UPDATE orders o
		   SET subtotal_cents = COALESCE(t.sub,0),
		       total_cents    = COALESCE(t.sub,0) + o.tax_cents,
		       updated_at     = now()
		  FROM (SELECT COALESCE(SUM(line_total_cents),0) AS sub FROM order_lines WHERE order_id = $1) t
		 WHERE o.id = $1`, orderID); err != nil {
		return ChangeOrder{}, fmt.Errorf("apply change: recompute totals: %w", err)
	}

	if hooks.resync != nil {
		if err := hooks.resync(ctx, tx, orderID, branchID); err != nil {
			return ChangeOrder{}, fmt.Errorf("apply change: reservation resync: %w", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE change_orders SET status='APPLIED', applied_at=now(), updated_at=now() WHERE id=$1`,
		id); err != nil {
		return ChangeOrder{}, fmt.Errorf("apply change: mark applied: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ChangeOrder{}, fmt.Errorf("apply change: commit: %w", err)
	}
	return r.GetChangeOrder(ctx, id)
}

// applyChangeLineTx writes ONE delta row onto order_lines.
//
// A QTY or REMOVE whose target line has VANISHED since the request fails LOUDLY rather than being
// skipped. Ruling 1 says the order stays workable, so a line genuinely can disappear underneath a
// pending change, and silently dropping that half of a signed delta would apply something nobody
// signed. The whole apply rolls back and a human re-raises against what the order actually is now.
func applyChangeLineTx(ctx context.Context, tx pgx.Tx, orderID string, l ChangeLine) error {
	switch l.Op {
	case ChangeOpAdd:
		var nextNo int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(line_no),0)+1 FROM order_lines WHERE order_id = $1`, orderID).
			Scan(&nextNo); err != nil {
			return fmt.Errorf("apply change: next line_no: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines
				(order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
				 unit_price_cents, price_uom, line_total_cents)
			VALUES ($1::uuid, $2, $3::uuid, $4, $5::numeric, $6::citext, $7,
			        $8, $9::citext, $10)`,
			orderID, nextNo, l.ProductID, l.Description, l.QtyEnteredNew, l.UOMEntered,
			l.QtyBaseAfter, l.UnitPriceCents, l.PriceUOM, l.LineDeltaCents); err != nil {
			return fmt.Errorf("apply change: add line: %w", err)
		}
		return nil

	case ChangeOpQty:
		tag, err := tx.Exec(ctx, `
			UPDATE order_lines
			   SET qty_entered = $3::numeric, qty_base = $4,
			       unit_price_cents = $5, price_uom = COALESCE(NULLIF($6,'')::citext, price_uom),
			       line_total_cents = line_total_cents + $7, updated_at = now()
			 WHERE order_id = $1::uuid AND line_no = $2`,
			orderID, l.LineNo, l.QtyEnteredNew, l.QtyBaseAfter,
			l.UnitPriceCents, l.PriceUOM, l.LineDeltaCents)
		if err != nil {
			return fmt.Errorf("apply change: qty line %d: %w", l.LineNo, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: line %d no longer exists on this order, the change cannot be applied "+
				"as signed; re-raise it against the order as it stands", ErrLineNotFound, l.LineNo)
		}
		return nil

	case ChangeOpRemove:
		tag, err := tx.Exec(ctx,
			`DELETE FROM order_lines WHERE order_id = $1::uuid AND line_no = $2`, orderID, l.LineNo)
		if err != nil {
			return fmt.Errorf("apply change: remove line %d: %w", l.LineNo, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: line %d no longer exists on this order, the change cannot be applied "+
				"as signed; re-raise it against the order as it stands", ErrLineNotFound, l.LineNo)
		}
		return nil
	}
	return fmt.Errorf("apply change: unknown op %q", l.Op)
}

func (r *Repository) changeLinesTx(ctx context.Context, tx pgx.Tx, changeID string) ([]ChangeLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT seq, op, COALESCE(line_no,0), COALESCE(product_id::text,''), description,
		       COALESCE(qty_entered_new::text,''), COALESCE(uom_entered::text,''),
		       qty_base_before, qty_base_after, qty_delta_base,
		       unit_price_cents, COALESCE(price_uom::text,''), line_delta_cents
		  FROM change_order_lines WHERE change_order_id = $1 ORDER BY seq`, changeID)
	if err != nil {
		return nil, fmt.Errorf("apply change: read delta: %w", err)
	}
	defer rows.Close()
	var out []ChangeLine
	for rows.Next() {
		var l ChangeLine
		if err := rows.Scan(&l.Seq, &l.Op, &l.LineNo, &l.ProductID, &l.Description,
			&l.QtyEnteredNew, &l.UOMEntered, &l.QtyBaseBefore, &l.QtyBaseAfter, &l.QtyDeltaBase,
			&l.UnitPriceCents, &l.PriceUOM, &l.LineDeltaCents); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// isUniqueViolation reports a 23505, so a partial unique index can be the enforcement point for a
// business rule without the service racing it with a SELECT first.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

// parseQtyOrZero reads a DECIMAL-as-string quantity, treating an unparseable value as zero. Used only
// where the value came from the database (and is therefore already a valid numeric).
func parseQtyOrZero(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}
