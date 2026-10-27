package inventory

// repository.go, the pgx persistence layer implementing the `store` seam over the 0005 stock ledger + the
// 0037 read models + the 0011 purchasing tables. It reads/writes ONLY inventory-owned + purchasing tables
// (stock_moves / stock_balances / product_branch_costs / purchase_orders / po_lines / po_receipts) and the
// v_stock_on_hand read model; cross-module GL posting goes through the injected GLPoster, never a table.
// A nil pool is tolerated (scaffold/offline), every method returns errNoDB. qty_base > 0 is enforced by the
// 0005 CHECK, so an outbound/negative move is written with abs(qty) and direction encoded in from/to location.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is the shared read/write surface of both *pgxpool.Pool and pgx.Tx, so a method can run either in the
// caller's tx (composed receiving) or standalone on the pool (a read model).
type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// q returns the tx when the caller supplied one, else the pool (nil pool → caller already guarded via errNoDB).
func (r *Repository) q(tx pgx.Tx) querier {
	if tx != nil {
		return tx
	}
	return r.pool
}

// Begin opens a transaction on the pool (the receiving + HTTP paths that own their own tx).
func (r *Repository) Begin(ctx context.Context) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	return r.pool.Begin(ctx)
}

var _ store = (*Repository)(nil)

// nolint:unused // pool nil-guard used by pool-only read methods.
func (r *Repository) pooled() (*pgxpool.Pool, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	return r.pool, nil
}

// - receiving -

func (r *Repository) POHeaderForReceipt(ctx context.Context, tx pgx.Tx, poID string) (poHeader, error) {
	var h poHeader
	err := r.q(tx).QueryRow(ctx, `
		SELECT id::text, po_no::text, branch_id::text, status, currency, fx_rate
		FROM purchase_orders WHERE id = $1 FOR UPDATE`, poID).
		Scan(&h.ID, &h.PONo, &h.BranchID, &h.Status, &h.Currency, &h.FxRate)
	if errors.Is(err, pgx.ErrNoRows) {
		return poHeader{}, errNotFound
	}
	return h, err
}

func (r *Repository) POLinesForReceipt(ctx context.Context, tx pgx.Tx, poID string) ([]poLine, error) {
	rows, err := r.q(tx).Query(ctx, `
		SELECT id::text, line_no, product_id::text, uom_entered::text, qty_base, received_base, unit_cost_cents
		FROM po_lines WHERE po_id = $1 ORDER BY line_no FOR UPDATE`, poID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []poLine
	for rows.Next() {
		var l poLine
		if err := rows.Scan(&l.ID, &l.LineNo, &l.ProductID, &l.UOM, &l.QtyBase, &l.ReceivedBase, &l.UnitCostCents); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) InsertReceipt(ctx context.Context, tx pgx.Tx, poID, receivedBy string) (string, error) {
	var id string
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO po_receipts (po_id, received_by) VALUES ($1, $2) RETURNING id::text`,
		poID, nullStr(receivedBy)).Scan(&id)
	return id, err
}

func (r *Repository) UpdatePOLineReceived(ctx context.Context, tx pgx.Tx, lineID string, received float64) error {
	_, err := r.q(tx).Exec(ctx, `
		UPDATE po_lines SET received_base = $2, updated_at = now() WHERE id = $1`, lineID, received)
	return err
}

func (r *Repository) UpdatePOStatus(ctx context.Context, tx pgx.Tx, poID, status string) error {
	_, err := r.q(tx).Exec(ctx, `
		UPDATE purchase_orders SET status = $2, updated_at = now() WHERE id = $1`, poID, status)
	return err
}

func (r *Repository) EmitEvent(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	b, err := marshalJSON(payload)
	if err != nil {
		return err
	}
	_, err = r.q(tx).Exec(ctx, `INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)`, topic, b)
	return err
}

// - ledger + read-model maintenance -

func (r *Repository) InsertMove(ctx context.Context, tx pgx.Tx, m moveParams) (string, error) {
	// uom_entered falls back to the product base_uom when the caller didn't supply one (a POS SHIP move).
	metaJSON := []byte("{}")
	if len(m.Metadata) > 0 {
		b, err := marshalJSON(m.Metadata)
		if err != nil {
			return "", err
		}
		metaJSON = b
	}
	const sql = `
		INSERT INTO stock_moves
			(product_id, from_location_id, to_location_id, qty_base, qty_entered, uom_entered,
			 move_type, unit_cost_cents, value_cents, source_type, source_ref_id, reason, branch_id, metadata)
		VALUES ($1, $2, $3, $4, $4,
			COALESCE(NULLIF($5,'')::citext, (SELECT base_uom FROM products WHERE id = $1)),
			$6, $7, $8, $9, $10, $11, $12, COALESCE($13::jsonb, '{}'::jsonb))
		RETURNING id::text`
	var id string
	err := r.q(tx).QueryRow(ctx, sql,
		m.ProductID, nullStr(m.FromLocation), nullStr(m.ToLocation), m.QtyBase, m.UOM,
		m.MoveType, m.UnitCostCents, m.ValueCents, nullStr(m.SourceType), nullStr(m.SourceRefID),
		m.Reason, m.BranchID, metaJSON).Scan(&id)
	return id, err
}

func (r *Repository) HasShipMovesForOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	var exists bool
	err := r.q(tx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM stock_moves
			 WHERE move_type = 'SHIP'
			   AND source_ref_id = $1::uuid
		)`, orderID).Scan(&exists)
	return exists, err
}

func (r *Repository) OnHandQtyBranch(ctx context.Context, tx pgx.Tx, productID, branchID string) (float64, error) {
	var qty float64
	err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(SUM(on_hand_base), 0) FROM stock_balances
		WHERE product_id = $1 AND branch_id = $2`, productID, branchID).Scan(&qty)
	return qty, err
}

func (r *Repository) UpsertBalanceDelta(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, delta float64) error {
	_, err := r.q(tx).Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (product_id, location_id)
		DO UPDATE SET on_hand_base = stock_balances.on_hand_base + EXCLUDED.on_hand_base, updated_at = now()`,
		productID, locationID, branchID, delta)
	return err
}

// LockBalances takes the branch's existing balance rows of these products FOR UPDATE, sorted by product
// then location, on the caller's transaction. A move that touches several products takes its row locks
// in whatever order its lines name them, so two moves over crossed products can deadlock; a caller that
// knows the whole set takes it here first, in the one order every such caller uses.
func (r *Repository) LockBalances(ctx context.Context, tx pgx.Tx, branchID string, productIDs []string) error {
	if len(productIDs) == 0 {
		return nil
	}
	rows, err := r.q(tx).Query(ctx, `SELECT 1 FROM stock_balances WHERE branch_id = $1::uuid AND product_id = ANY($2::uuid[])
 ORDER BY product_id, location_id FOR UPDATE`, branchID, productIDs)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// LockBranchCost serializes the weighted-average read-modify-write on (product_id, branch_id) so concurrent
// WAC mutators (PO receipt applyReceipt, landed-cost capitalize/restore) cannot lost-update the perpetual
// subledger. It MUST be called inside the caller's tx, at the START of every WAC mutation path, BEFORE the
// OnHandQtyBranch + GetBranchCost reads, the second updater then blocks until the first commits and reads the
// already-committed WAC, so the read-compute-write is serialized and GL 1200 stays tied to the subledger.
//
// A pg_advisory_xact_lock (not SELECT ... FOR UPDATE on product_branch_costs) is used deliberately: the very
// first receipt for a (product, branch) has NO product_branch_costs row yet, so FOR UPDATE would lock nothing
// and leave the classic INSERT phantom gap where two concurrent first-receipts both read "no row" and race the
// upsert. The advisory lock is keyed on the (product_id, branch_id) pair itself, so it serializes even when the
// row does not exist, and it releases automatically at commit/rollback (xact-scoped), no leak on the error path.
// The lock key follows the codebase idiom (hashtextextended with a per-domain seed; 0058 = this migration/domain).
func (r *Repository) LockBranchCost(ctx context.Context, tx pgx.Tx, productID, branchID string) error {
	if tx == nil {
		// No surrounding transaction to scope the lock to: a pg_advisory_xact_lock issued on the pool would
		// acquire and immediately release inside its own implicit autocommit, serializing nothing. Every PRODUCTION
		// WAC-mutation path (receive → applyReceipt, landedcost Apply/Void → Capitalize/RestoreLandedCostTx) always
		// runs inside a real tx and hits the lock below; the nil-tx form is only the standalone seam-test path,
		// where there is by definition no concurrent tx to serialize against, so skipping is correct (not a no-op
		// that hides a bug, there is nothing to serialize when each call autocommits independently).
		return nil
	}
	_, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 58))`, productID+"|"+branchID)
	return err
}

func (r *Repository) GetBranchCost(ctx context.Context, tx pgx.Tx, productID, branchID string) (int64, bool, error) {
	var c int64
	err := r.q(tx).QueryRow(ctx, `
		SELECT avg_unit_cost_cents FROM product_branch_costs
		WHERE product_id = $1 AND branch_id = $2`, productID, branchID).Scan(&c)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return c, true, nil
}

func (r *Repository) UpsertBranchCost(ctx context.Context, tx pgx.Tx, productID, branchID string, avgCents int64) error {
	_, err := r.q(tx).Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (product_id, branch_id)
		DO UPDATE SET avg_unit_cost_cents = EXCLUDED.avg_unit_cost_cents, updated_at = now()`,
		productID, branchID, avgCents)
	return err
}

func (r *Repository) FallbackCost(ctx context.Context, tx pgx.Tx, productID, branchID string) (int64, string, bool, error) {
	// 1. LAST_KNOWN_COST: latest non-provisional stock move cost, or other-branch recorded cost, or latest PO line cost.
	var lastMoveCost *int64
	err := r.q(tx).QueryRow(ctx, `
		SELECT unit_cost_cents
		  FROM stock_moves
		 WHERE product_id = $1
		   AND unit_cost_cents > 0
		   AND COALESCE(metadata->>'provisional_cost', 'false') <> 'true'
		 ORDER BY occurred_at DESC, id DESC
		 LIMIT 1`, productID).Scan(&lastMoveCost)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, err
	}
	if lastMoveCost != nil && *lastMoveCost > 0 {
		return *lastMoveCost, "LAST_KNOWN_COST", true, nil
	}

	var otherBranchAvg *int64
	err = r.q(tx).QueryRow(ctx, `
		SELECT ROUND(AVG(avg_unit_cost_cents))::bigint
		  FROM product_branch_costs
		 WHERE product_id = $1
		   AND ($2 = '' OR branch_id <> $2::uuid)
		   AND avg_unit_cost_cents > 0`, productID, branchID).Scan(&otherBranchAvg)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, err
	}
	if otherBranchAvg != nil && *otherBranchAvg > 0 {
		return *otherBranchAvg, "LAST_KNOWN_COST", true, nil
	}

	// 2. CATALOGUE_COST: product metadata cost.
	var catCost int64
	err = r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(
			(metadata->>'catalogue_cost_cents')::bigint,
			(metadata->>'cost_cents')::bigint,
			(metadata->>'unit_cost_cents')::bigint,
			0
		)
		  FROM products
		 WHERE id = $1`, productID).Scan(&catCost)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, err
	}
	if catCost > 0 {
		return catCost, "CATALOGUE_COST", true, nil
	}

	// 3. Fallback to latest PO line cost when no catalogue cost is present.
	var poLineCost *int64
	err = r.q(tx).QueryRow(ctx, `
		SELECT unit_cost_cents
		  FROM po_lines
		 WHERE product_id = $1
		   AND unit_cost_cents > 0
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`, productID).Scan(&poLineCost)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, err
	}
	if poLineCost != nil && *poLineCost > 0 {
		return *poLineCost, "LAST_KNOWN_COST", true, nil
	}

	return 0, "", false, nil
}

func (r *Repository) UntruedProvisionalShipMoves(ctx context.Context, tx pgx.Tx, productID, branchID string) ([]PreReceiptException, error) {
	return r.queryPreReceiptExceptions(ctx, tx, branchID, productID)
}

func (r *Repository) PreReceiptExceptions(ctx context.Context, branchID string) ([]PreReceiptException, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	return r.queryPreReceiptExceptions(ctx, nil, branchID, "")
}

// PreReceiptExceptionsInOrg lists untrued provisional pre-receipt sales held to the caller's org
// first and then to the caller's branch reach (round 3, FROM-PRINCIPAL 184). A GLOBAL caller
// reaches every branch of its own org, never another org's; a scoped caller reaches only the
// branches of its own org in reach.
func (r *Repository) PreReceiptExceptionsInOrg(ctx context.Context, orgID, branchID string, reach BranchReach) ([]PreReceiptException, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if reach.reachesNothing() {
		return []PreReceiptException{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT sm.id::text, COALESCE(sm.source_ref_id::text, ''), sm.product_id::text, sm.branch_id::text,
		       sm.qty_base::float8, COALESCE(sm.unit_cost_cents, 0), COALESCE(sm.value_cents, 0),
		       COALESCE(sm.metadata->>'fallback_source', 'LAST_KNOWN_COST'),
		       to_char(sm.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		  FROM stock_moves sm
		  JOIN locations l ON l.id = sm.branch_id AND l.loc_type = 'BRANCH'
		 WHERE sm.move_type = 'SHIP'
		   AND COALESCE(sm.metadata->>'provisional_cost', 'false') = 'true'
		   AND l.org_id = $1::uuid
		   AND ($2 = '' OR sm.branch_id = $2::uuid)
		   AND ($3::uuid[] IS NULL OR sm.branch_id = ANY($3::uuid[]))
		   AND NOT EXISTS (
		       SELECT 1 FROM stock_moves rec
		        WHERE rec.product_id = sm.product_id
		          AND rec.branch_id = sm.branch_id
		          AND rec.move_type = 'RECEIPT'
		          AND rec.metadata->'trued_up_move_ids' ? sm.id::text
		   )
		 ORDER BY sm.occurred_at ASC, sm.id ASC`, orgID, branchID, reach.branchFilter())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PreReceiptException
	for rows.Next() {
		var ex PreReceiptException
		if err := rows.Scan(
			&ex.MoveID, &ex.OrderID, &ex.ProductID, &ex.BranchID,
			&ex.QtyBase, &ex.ProvisionalUnitCostCents, &ex.ProvisionalValueCents,
			&ex.FallbackSource, &ex.OccurredAt,
		); err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

func (r *Repository) queryPreReceiptExceptions(ctx context.Context, tx pgx.Tx, branchID, productID string) ([]PreReceiptException, error) {
	rows, err := r.q(tx).Query(ctx, `
		SELECT sm.id::text, COALESCE(sm.source_ref_id::text, ''), sm.product_id::text, sm.branch_id::text,
		       sm.qty_base::float8, COALESCE(sm.unit_cost_cents, 0), COALESCE(sm.value_cents, 0),
		       COALESCE(sm.metadata->>'fallback_source', 'LAST_KNOWN_COST'),
		       to_char(sm.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		  FROM stock_moves sm
		 WHERE sm.move_type = 'SHIP'
		   AND COALESCE(sm.metadata->>'provisional_cost', 'false') = 'true'
		   AND ($1 = '' OR sm.branch_id = $1::uuid)
		   AND ($2 = '' OR sm.product_id = $2::uuid)
		   AND NOT EXISTS (
		       SELECT 1 FROM stock_moves rec
		        WHERE rec.product_id = sm.product_id
		          AND rec.branch_id = sm.branch_id
		          AND rec.move_type = 'RECEIPT'
		          AND rec.metadata->'trued_up_move_ids' ? sm.id::text
		   )
		 ORDER BY sm.occurred_at ASC, sm.id ASC`, branchID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PreReceiptException
	for rows.Next() {
		var ex PreReceiptException
		if err := rows.Scan(
			&ex.MoveID, &ex.OrderID, &ex.ProductID, &ex.BranchID,
			&ex.QtyBase, &ex.ProvisionalUnitCostCents, &ex.ProvisionalValueCents,
			&ex.FallbackSource, &ex.OccurredAt,
		); err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

// - read models (pool) -

// ProductCostCents is the product-level weighted-average cost (branch-agnostic, the order.fulfilled COGS
// basis carries no branch). It weights each branch's average by that branch's positive on-hand; when no branch
// carries positive on-hand (everything sold through) it falls back to the plain average of the recorded branch
// costs so a COGS basis still exists. found=false only when the product has NO cost row at all → COGS dormant.
func (r *Repository) ProductCostCents(ctx context.Context, productID string) (int64, bool, error) {
	if r.pool == nil {
		return 0, false, errNoDB
	}
	var weighted *int64
	err := r.pool.QueryRow(ctx, `
		SELECT ROUND(SUM(pbc.avg_unit_cost_cents * b.qty) / NULLIF(SUM(b.qty), 0))::bigint
		FROM product_branch_costs pbc
		JOIN (SELECT branch_id, SUM(on_hand_base) AS qty FROM stock_balances
		      WHERE product_id = $1 GROUP BY branch_id) b USING (branch_id)
		WHERE pbc.product_id = $1 AND b.qty > 0`, productID).Scan(&weighted)
	if err != nil {
		return 0, false, err
	}
	if weighted != nil {
		return *weighted, true, nil
	}
	// Fallback: plain average of recorded branch costs (all on-hand ≤ 0, e.g. sold-through / negative stock).
	var avg *int64
	err = r.pool.QueryRow(ctx, `
		SELECT ROUND(AVG(avg_unit_cost_cents))::bigint FROM product_branch_costs
		WHERE product_id = $1 AND avg_unit_cost_cents > 0`, productID).Scan(&avg)
	if err != nil {
		return 0, false, err
	}
	if avg == nil {
		return 0, false, nil
	}
	return *avg, true, nil
}

func (r *Repository) BranchCostCents(ctx context.Context, productID, branchID string) (int64, bool, error) {
	if r.pool == nil {
		return 0, false, errNoDB
	}
	return r.GetBranchCost(ctx, nil, productID, branchID)
}

func (r *Repository) OnHandFor(ctx context.Context, productID string) ([]OnHand, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT v.product_id::text, v.location_id::text, v.branch_id::text,
		       v.on_hand_base, v.allocated_base, v.available_base,
		       COALESCE(pbc.avg_unit_cost_cents, 0)
		FROM v_stock_on_hand v
		LEFT JOIN product_branch_costs pbc
		       ON pbc.product_id = v.product_id AND pbc.branch_id = v.branch_id
		WHERE v.product_id = $1
		ORDER BY v.branch_id`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OnHand
	for rows.Next() {
		var o OnHand
		if err := rows.Scan(&o.ProductID, &o.LocationID, &o.BranchID, &o.OnHandBase,
			&o.AllocatedBase, &o.AvailableBase, &o.AvgCostCents); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// InStockProductIDs returns the distinct product ids that currently have positive on-hand in
// the stock ledger, optionally scoped to a branch. It backs the M4 catalogue in_stock_only
// filter (product.StockReader seam, A-4): the product module reads in-stock through this
// inventory read rather than SELECTing stock_balances itself (zero-trust boundary, the
// inventory module is the sole owner of stock_balances).
func (r *Repository) InStockProductIDs(ctx context.Context, branchID string) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// DIB-60: "in stock" on the catalogue filter means AVAILABLE, not on-hand. A product whose entire
	// on-hand is hard-held for a confirmed order is not something a browsing customer can buy, and
	// listing it under an in-stock-only filter is the oversell in its most public form. The subtraction
	// is inline rather than via AvailableBase because it happens in SQL, the Go-side definition and
	// this predicate are asserted to agree by TestIntegration_InStockProductIDs_ExcludesHardReserved.
	q := `SELECT DISTINCT product_id::text FROM stock_balances WHERE on_hand_base - qty_reserved > 0`
	args := []any{}
	if branchID != "" {
		q += ` AND branch_id = $1`
		args = append(args, branchID)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Repository) RecentMoves(ctx context.Context, productID, branchID string, limit int) ([]StockMove, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, product_id::text, branch_id::text, qty_base, uom_entered::text, move_type,
		       COALESCE(source_type,''), COALESCE(source_ref_id::text,''),
		       COALESCE(unit_cost_cents,0),
		       CASE WHEN to_location_id IS NOT NULL AND from_location_id IS NULL THEN 'IN' ELSE 'OUT' END,
		       to_char(occurred_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM stock_moves
		WHERE product_id = $1 AND ($2 = '' OR branch_id = $2::uuid)
		ORDER BY occurred_at DESC LIMIT $3`, productID, branchID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StockMove
	for rows.Next() {
		var m StockMove
		if err := rows.Scan(&m.ID, &m.ProductID, &m.BranchID, &m.QtyBase, &m.UOM, &m.MoveType,
			&m.SourceType, &m.SourceRefID, &m.UnitCostCents, &m.Direction, &m.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PendingFulfilments quantifies the DX-01 gap: order lines whose fulfilment mode defers the stock move
// (DELIVERY / WILL_CALL, i.e. DELIVERED / COLLECT_LATER) and that are not yet fully fulfilled, no SHIP move
// has been booked for them. This is the surface that makes the PARTIAL closure measurable.
func (r *Repository) PendingFulfilments(ctx context.Context, branchID string) ([]PendingFulfilment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT o.id::text, ol.product_id::text, (ol.qty_base - ol.fulfilled_base - ol.released_base) AS pending,
		       CASE o.fulfillment WHEN 'DELIVERY' THEN 'DELIVERED' WHEN 'WILL_CALL' THEN 'COLLECT_LATER'
		            ELSE o.fulfillment END
		FROM orders o
		JOIN order_lines ol ON ol.order_id = o.id
		WHERE o.fulfillment IN ('DELIVERY','WILL_CALL')
		  AND o.status NOT IN ('FULFILLED','CLOSED','CANCELLED')
		  AND ol.product_id IS NOT NULL
		  AND ol.qty_base > ol.fulfilled_base + ol.released_base
		  AND ($1 = '' OR o.branch_id = $1::uuid)
		ORDER BY o.created_at`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingFulfilment
	for rows.Next() {
		var p PendingFulfilment
		if err := rows.Scan(&p.OrderID, &p.ProductID, &p.QtyBase, &p.SaleType); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SnapshotOnHand is the count-sheet freeze basis: on-hand + weighted-avg for every product at a branch.
// A caller holding a transaction passes it so the read rides that connection; nil reads through the pool.
func (r *Repository) SnapshotOnHand(ctx context.Context, tx pgx.Tx, branchID string) ([]CountSnapshot, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.q(tx).Query(ctx, `
		SELECT sb.product_id::text, sb.location_id::text, sb.on_hand_base,
		       COALESCE(pbc.avg_unit_cost_cents, 0)
		FROM stock_balances sb
		LEFT JOIN product_branch_costs pbc
		       ON pbc.product_id = sb.product_id AND pbc.branch_id = sb.branch_id
		WHERE sb.branch_id = $1
		ORDER BY sb.product_id`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CountSnapshot
	for rows.Next() {
		var s CountSnapshot
		if err := rows.Scan(&s.ProductID, &s.LocationID, &s.OnHandBase, &s.AvgCostCents); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ReceivedLinesForPO returns the received lines of a PO for landed-cost allocation (extended CAD value =
// received_base × unit_cost_cents; special-buy flagged off metadata). weight = received_base × per-unit kg.
func (r *Repository) ReceivedLinesForPO(ctx context.Context, poID string) ([]ReceivedLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT pl.id::text, pl.product_id::text, pl.received_base,
		       ROUND(pl.received_base * pl.unit_cost_cents)::bigint AS value_cents,
		       COALESCE(pl.received_base * p.weight_per_unit_kg, 0) AS weight_kg,
		       COALESCE((p.metadata->>'special_buy')::boolean, false) AS is_special_buy
		FROM po_lines pl
		JOIN products p ON p.id = pl.product_id
		WHERE pl.po_id = $1 AND pl.received_base > 0
		ORDER BY pl.line_no`, poID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReceivedLine
	for rows.Next() {
		var l ReceivedLine
		if err := rows.Scan(&l.POLineID, &l.ProductID, &l.QtyBase, &l.ValueCents, &l.WeightKg, &l.IsSpecialBuy); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// OnHandByBranch is the pgx read behind Service.OnHandByBranch (DIB-404): one query over the whole
// requested id set, grouped by product + branch.
//
// Rows summing to zero are DROPPED. A stock_balances row can exist at 0 after a product is drained,
// and reporting that branch as "0" is different from reporting it as absent only if the catalogue
// treats them differently, it does not, and carrying the zeros makes every fully-drained product
// render "TRT 0 · KGN 0" instead of "-". Negative sums are kept: a negative balance is a real
// reconciliation problem someone should see, not noise to hide.
func (r *Repository) OnHandByBranch(ctx context.Context, productIDs []string) (map[string]map[string]float64, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(productIDs) == 0 {
		return map[string]map[string]float64{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT sb.product_id::text, sb.branch_id::text, SUM(sb.on_hand_base)::float8
		  FROM stock_balances sb
		 WHERE sb.product_id = ANY($1::uuid[])
		 GROUP BY sb.product_id, sb.branch_id`, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]map[string]float64, len(productIDs))
	for rows.Next() {
		var productID, branchID string
		var qty float64
		if err := rows.Scan(&productID, &branchID, &qty); err != nil {
			return nil, err
		}
		if qty == 0 {
			continue
		}
		if out[productID] == nil {
			out[productID] = map[string]float64{}
		}
		out[productID][branchID] = qty
	}
	return out, rows.Err()
}
