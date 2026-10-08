package order

// shipgroup_repository.go, the pgx half of DIB-78 (shipgroup.go).
//
// ReplaceShipmentPlan is the only method worth reading twice. It rewrites a whole partition inside
// ONE transaction, which is what lets the 0126 constraint trigger, DEFERRABLE INITIALLY DEFERRED -
// see the finished plan rather than each transient half of it. Delete-then-insert (rather than a
// diff) for exactly the reason release-then-reserve is used for holds: a diff has to decide what to
// do about a line that split, merged or vanished, and each of those decisions is somewhere to leave
// a stale row. Delete-then-insert has one possible outcome.
//
// DISPATCHED and COMPLETE groups are NOT deleted. The truck has gone; rewriting what was on it is a
// lie about a delivery that happened. They are preserved and their quantities count towards the
// coverage the new plan must complete, so re-planning after a partial shipment means planning THE
// REMAINDER, which is the only sane thing it could mean.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// compile-time proof the real repository satisfies the shipment store.
var _ shipStore = (*Repository)(nil)

// shipStore is the multi-ship persistence, asserted OFF the injected orderStore (the editStore
// pattern) so every existing service fake keeps compiling.
type shipStore interface {
	ShipmentPlan(ctx context.Context, orderID string) (ShipmentPlan, error)
	ShipmentGroup(ctx context.Context, groupID string) (ShipmentGroup, string, error)
	ReplaceShipmentPlan(ctx context.Context, orderID, actorID string, groups []ShipmentGroupInput) error
	TransitionShipmentGroup(ctx context.Context, groupID, target string, allowedFrom []string, precheck func(ctx context.Context) error) (ShipmentGroup, error)
	SetInvoicePolicy(ctx context.Context, orderID, policy string) error
	OrderHasInvoice(ctx context.Context, orderID string) (bool, error)
}

// ShipmentGroup loads one group joined to its ORDER's branch, with no write. shipment_groups
// carries no branch of its own (0126), the order is where the fence lives. This is the load the
// service gates the caller's reach on BEFORE transitioning (HH-182): the load-first check is the
// shape every other decision surface in this module uses (loadChangeForDecision), and it is what
// keeps a refused cross-branch call from committing the move it just denied.
func (r *Repository) ShipmentGroup(ctx context.Context, groupID string) (ShipmentGroup, string, error) {
	if r.pool == nil {
		return ShipmentGroup{}, "", errNoDB
	}
	var g ShipmentGroup
	var branchID string
	err := r.pool.QueryRow(ctx, `
		SELECT g.id::text, g.order_id::text, g.group_no, g.label, g.status,
		       COALESCE(to_char(g.requested_date,'YYYY-MM-DD'),''),
		       COALESCE(g.delivery_stop_id::text,''),
		       COALESCE(to_char(g.dispatched_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       COALESCE(to_char(g.completed_at  AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       o.branch_id::text
		  FROM shipment_groups g JOIN orders o ON o.id = g.order_id
		 WHERE g.id = $1`, groupID).
		Scan(&g.ID, &g.OrderID, &g.GroupNo, &g.Label, &g.Status, &g.RequestedDate,
			&g.DeliveryStopID, &g.DispatchedAt, &g.CompletedAt, &branchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShipmentGroup{}, "", ErrGroupNotFound
		}
		return ShipmentGroup{}, "", fmt.Errorf("read shipment group: %w", err)
	}
	g.Lines = []ShipmentGroupLine{}
	return g, branchID, nil
}

// ShipmentPlan reads an order's whole multi-ship view: groups, their lines decorated with the order
// line facts, the per-line coverage arithmetic, and the money share of each group.
func (r *Repository) ShipmentPlan(ctx context.Context, orderID string) (ShipmentPlan, error) {
	if r.pool == nil {
		return ShipmentPlan{}, errNoDB
	}
	if !looksLikeUUID(orderID) {
		return ShipmentPlan{}, ErrNotFound
	}
	var plan ShipmentPlan
	plan.OrderID = orderID
	if err := r.pool.QueryRow(ctx,
		`SELECT order_no::text, multiship_invoice_policy FROM orders WHERE id = $1`, orderID).
		Scan(&plan.OrderNo, &plan.InvoicePolicy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShipmentPlan{}, ErrNotFound
		}
		return ShipmentPlan{}, fmt.Errorf("shipment plan: header: %w", err)
	}
	frozen, err := r.OrderHasInvoice(ctx, orderID)
	if err != nil {
		return ShipmentPlan{}, err
	}
	plan.PolicyFrozen = frozen

	rows, err := r.pool.Query(ctx, `
		SELECT g.id::text, g.group_no, g.label, g.status,
		       COALESCE(to_char(g.requested_date,'YYYY-MM-DD'),''),
		       COALESCE(g.delivery_stop_id::text,''),
		       COALESCE(to_char(g.dispatched_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       COALESCE(to_char(g.completed_at  AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       COALESCE(g.invoice_id::text,''), COALESCE(i.invoice_no::text,'')
		  FROM shipment_groups g
		  LEFT JOIN invoices i ON i.id = g.invoice_id
		 WHERE g.order_id = $1 ORDER BY g.group_no`, orderID)
	if err != nil {
		return ShipmentPlan{}, fmt.Errorf("shipment plan: groups: %w", err)
	}
	groups := []ShipmentGroup{}
	for rows.Next() {
		var g ShipmentGroup
		g.OrderID = orderID
		if err := rows.Scan(&g.ID, &g.GroupNo, &g.Label, &g.Status, &g.RequestedDate,
			&g.DeliveryStopID, &g.DispatchedAt, &g.CompletedAt, &g.InvoiceID, &g.InvoiceNo); err != nil {
			rows.Close()
			return ShipmentPlan{}, fmt.Errorf("scan shipment group: %w", err)
		}
		// Invoiced is derived from the LINK, not from the document's existence: 0147's FK is ON DELETE
		// SET NULL, so a purged invoice correctly makes the group billable again rather than leaving
		// it stranded pointing at nothing.
		g.Invoiced = g.InvoiceID != ""
		groups = append(groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ShipmentPlan{}, err
	}

	// One read for every group line on the order, joined to its order line. Read in one query and
	// bucketed in Go rather than per group: N groups meant N round trips, and a plan read on a busy
	// dispatch board is not the place to grow one.
	lineRows, err := r.pool.Query(ctx, `
		SELECT sgl.shipment_group_id::text, ol.id::text, ol.line_no,
		       COALESCE(ol.product_id::text,''), ol.description,
		       sgl.qty_base, ol.qty_base, ol.fulfilled_base,
		       ol.unit_price_cents, ol.line_total_cents
		  FROM shipment_group_lines sgl
		  JOIN shipment_groups g ON g.id = sgl.shipment_group_id
		  JOIN order_lines ol    ON ol.id = sgl.order_line_id
		 WHERE g.order_id = $1
		 ORDER BY ol.line_no, g.group_no`, orderID)
	if err != nil {
		return ShipmentPlan{}, fmt.Errorf("shipment plan: group lines: %w", err)
	}
	type gl struct {
		groupID string
		line    ShipmentGroupLine
		total   int64
	}
	var all []gl
	for lineRows.Next() {
		var e gl
		if err := lineRows.Scan(&e.groupID, &e.line.OrderLineID, &e.line.LineNo,
			&e.line.ProductID, &e.line.Description, &e.line.QtyBase, &e.line.OrderQtyBase,
			&e.line.FulfilledBase, &e.line.UnitPriceCents, &e.total); err != nil {
			lineRows.Close()
			return ShipmentPlan{}, fmt.Errorf("scan shipment group line: %w", err)
		}
		all = append(all, e)
	}
	lineRows.Close()
	if err := lineRows.Err(); err != nil {
		return ShipmentPlan{}, err
	}

	// Pro-rate each ORDER LINE's money across the groups that carry it, so the shares sum exactly.
	byLine := map[string][]int{}
	for i, e := range all {
		byLine[e.line.OrderLineID] = append(byLine[e.line.OrderLineID], i)
	}
	for _, idxs := range byLine {
		qtys := make([]float64, len(idxs))
		for i, ix := range idxs {
			qtys[i] = all[ix].line.QtyBase
		}
		shares := prorateShares(all[idxs[0]].total, qtys)
		for i, ix := range idxs {
			all[ix].line.ShareCents = shares[i]
		}
	}
	byGroup := map[string][]ShipmentGroupLine{}
	for _, e := range all {
		byGroup[e.groupID] = append(byGroup[e.groupID], e.line)
	}
	for i := range groups {
		groups[i].Lines = byGroup[groups[i].ID]
		if groups[i].Lines == nil {
			groups[i].Lines = []ShipmentGroupLine{}
		}
		var sub int64
		for _, l := range groups[i].Lines {
			sub += l.ShareCents
		}
		groups[i].SubtotalCents = sub
	}
	plan.Groups = groups

	// Coverage: the same arithmetic the 0126 trigger enforces, computed for the surface so a
	// half-built plan can be SHOWN rather than only refused at commit. Cancelled groups excluded,
	// exactly as the trigger excludes them.
	covRows, err := r.pool.Query(ctx, `
		SELECT ol.line_no, ol.qty_base, COALESCE(SUM(sgl.qty_base),0)
		  FROM order_lines ol
		  LEFT JOIN shipment_group_lines sgl ON sgl.order_line_id = ol.id
		  LEFT JOIN shipment_groups g ON g.id = sgl.shipment_group_id AND g.status <> 'CANCELLED'
		 WHERE ol.order_id = $1 AND (g.id IS NOT NULL OR sgl.id IS NULL)
		 GROUP BY ol.id, ol.line_no, ol.qty_base
		 ORDER BY ol.line_no`, orderID)
	if err != nil {
		return ShipmentPlan{}, fmt.Errorf("shipment plan: coverage: %w", err)
	}
	defer covRows.Close()
	plan.Coverage = []ShipmentCoverage{}
	for covRows.Next() {
		var c ShipmentCoverage
		if err := covRows.Scan(&c.LineNo, &c.OrderQtyBase, &c.PlannedBase); err != nil {
			return ShipmentPlan{}, fmt.Errorf("scan coverage: %w", err)
		}
		c.Covered = qtyEqual(c.PlannedBase, c.OrderQtyBase)
		plan.Coverage = append(plan.Coverage, c)
	}
	return plan, covRows.Err()
}

// qtyEqual compares DECIMAL(19,4) quantities that have round-tripped through float64. The tolerance
// is half of the smallest representable step (0.0001), so two values that are equal in the database
// compare equal here and two that differ by one tick do not.
func qtyEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.00005
}

// ReplaceShipmentPlan rewrites the order's live plan in one transaction. See the file header for why
// it replaces rather than appends, and why dispatched/complete groups survive.
func (r *Repository) ReplaceShipmentPlan(ctx context.Context, orderID, actorID string, groups []ShipmentGroupInput) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("plan shipment: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize against a concurrent plan or a change-order apply on the same order, the module's
	// one lock order, order row first.
	if err := tx.QueryRow(ctx, `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(new(int)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("plan shipment: lock order: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM shipment_groups
		 WHERE order_id = $1 AND status IN ('PLANNED','PICKING','READY','CANCELLED')`, orderID); err != nil {
		return fmt.Errorf("plan shipment: clear plan: %w", err)
	}

	var nextNo int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(group_no),0)+1 FROM shipment_groups WHERE order_id = $1`, orderID).
		Scan(&nextNo); err != nil {
		return fmt.Errorf("plan shipment: next group_no: %w", err)
	}

	for i, g := range groups {
		var groupID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO shipment_groups (order_id, group_no, label, requested_date, created_by)
			VALUES ($1::uuid, $2, $3, NULLIF($4,'')::date, NULLIF($5,'')::uuid)
			RETURNING id::text`,
			orderID, nextNo+i, g.Label, g.RequestedDate, actorID).Scan(&groupID); err != nil {
			return fmt.Errorf("plan shipment: insert group %d: %w", i+1, err)
		}
		for _, l := range g.Lines {
			tag, err := tx.Exec(ctx, `
				INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
				SELECT $1::uuid, ol.id, $3
				  FROM order_lines ol WHERE ol.order_id = $2::uuid AND ol.line_no = $4`,
				groupID, orderID, l.QtyBase, l.LineNo)
			if err != nil {
				return fmt.Errorf("plan shipment: group %d line %d: %w", i+1, l.LineNo, err)
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("%w: line %d is not on this order", ErrLineNotFound, l.LineNo)
			}
		}
	}

	// SET CONSTRAINTS is not needed, the trigger is INITIALLY DEFERRED, so this COMMIT is where it
	// fires with the finished plan. A partition error surfaces as the trigger's own message, which
	// names the line and both quantities.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %s", ErrShipPlanIncomplete, err.Error())
	}
	return nil
}

// TransitionShipmentGroup moves one group, guarded on its current status inside the statement. The
// service has ALREADY fenced the caller's branch reach on the ShipmentGroup load (HH-182); the
// status predicate stays in the UPDATE as the backstop against a concurrent move between that load
// and this write, which is the one window a service-side ladder check can never close.
//
// A non-nil precheck (the pay before release hold, item 9 F review N4) runs INSIDE the write's own
// transaction, behind the legality check and under the order's row lock, the order ladder's own
// shape (UpdateStatusFromChecked): a refusal rolls the move back. Every money route that locks the
// order row (the Pay balance take does) is then ordered against the move; a reversal that does not
// take the order row (an NSF) narrows the window to the statement gap and does not close it.
func (r *Repository) TransitionShipmentGroup(ctx context.Context, groupID, target string, allowedFrom []string, precheck func(ctx context.Context) error) (ShipmentGroup, error) {
	if r.pool == nil {
		return ShipmentGroup{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ShipmentGroup{}, fmt.Errorf("transition shipment group: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if precheck != nil {
		// The order row first, as every other guarded status write takes it.
		var locked bool
		if err := tx.QueryRow(ctx, `
			SELECT true FROM orders o
			 WHERE o.id = (SELECT order_id FROM shipment_groups WHERE id = $1) FOR UPDATE`, groupID).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ShipmentGroup{}, ErrGroupNotFound
			}
			return ShipmentGroup{}, fmt.Errorf("transition shipment group: lock order: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE shipment_groups
		   SET status = $2,
		       dispatched_at = CASE WHEN $2 = 'DISPATCHED' THEN now() ELSE dispatched_at END,
		       completed_at  = CASE WHEN $2 = 'COMPLETE'   THEN now() ELSE completed_at  END,
		       updated_at = now()
		 WHERE id = $1 AND status = ANY($3)`, groupID, target, allowedFrom)
	if err != nil {
		return ShipmentGroup{}, fmt.Errorf("transition shipment group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shipment_groups WHERE id=$1)`, groupID).
			Scan(&exists); err != nil {
			return ShipmentGroup{}, err
		}
		if !exists {
			return ShipmentGroup{}, ErrGroupNotFound
		}
		return ShipmentGroup{}, ErrGroupIllegalTransition
	}
	if precheck != nil {
		if err := precheck(withPrecheckTx(ctx, tx)); err != nil {
			return ShipmentGroup{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ShipmentGroup{}, fmt.Errorf("transition shipment group: commit: %w", err)
	}
	g, _, err := r.ShipmentGroup(ctx, groupID)
	if err != nil {
		return ShipmentGroup{}, err
	}
	return g, nil
}

// SetInvoicePolicy writes the per-order invoicing ruling.
func (r *Repository) SetInvoicePolicy(ctx context.Context, orderID, policy string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders SET multiship_invoice_policy = $2, updated_at = now() WHERE id = $1`,
		orderID, policy)
	if err != nil {
		return fmt.Errorf("set invoice policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// OrderHasInvoice reports whether any invoice exists against the order. It is the freeze test for
// the invoicing policy: once money has been billed, the ruling that produced it is history.
func (r *Repository) OrderHasInvoice(ctx context.Context, orderID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var found bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM invoices WHERE order_id = $1)`, orderID).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("order has invoice: %w", err)
	}
	return found, nil
}
