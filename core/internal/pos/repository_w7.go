package pos

// repository_w7.go, the W7 counter-v2 persistence over the 0043 tables (pos_policy / pos_returns /
// pos_parked_tickets / pricing_orders) plus the invoice-credit write, which stays in the M2-debt family
// (repository.go's header note applies: isolated here behind posStore so it can be re-homed later).

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolationErr reports whether err is a Postgres unique_violation (23505), the concurrent-
// duplicate signal on ux_pos_returns_idem (the till/pricing isUniqueViolation pattern).
func isUniqueViolationErr(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// -- POS policy (branch row overrides the org NULL row) ---------------------

func (r *Repository) ReadPosPolicy(ctx context.Context, branchID string) (PosPolicy, error) {
	if r.pool == nil {
		return PosPolicy{}, errNoPool
	}
	// The branch override wins when present; otherwise the org default (branch_id IS NULL, seeded by
	// 0043). A branch with neither row falls back to a missing-default error, 0043 guarantees the seed.
	// balance_visibility is the RETIRED 0181 column (UX-97 item 208): the account position policy
	// decides the surface now, so the read stops selecting it and the column keeps its stored value.
	const q = `
SELECT COALESCE(branch_id::text,''), cod_enabled, restocking_fee_bps, notes,
       line_presentation, quick_chips_enabled, workspace_split_pct, workspace_arrangement,
       lookup_presentation, margin_visibility, quick_chip_set,
       supplier_deposit_cancel
  FROM pos_policy
 WHERE ($1 <> '' AND branch_id::text = $1) OR branch_id IS NULL
 ORDER BY branch_id::text NULLS LAST
 LIMIT 1`
	var p PosPolicy
	var chipSet string // 0170: comma-separated in the column; split for the wire in the service
	err := r.pool.QueryRow(ctx, q, branchID).Scan(&p.BranchID, &p.CODEnabled, &p.RestockingFeeBps, &p.Notes,
		&p.LinePresentation, &p.QuickChipsEnabled, &p.WorkspaceSplitPct, &p.WorkspaceArrangement,
		&p.LookupPresentation, &p.MarginVisibility, &chipSet,
		&p.SupplierDepositCancel)
	p.QuickChipSet = splitChipSet(chipSet)
	if errors.Is(err, pgx.ErrNoRows) {
		return PosPolicy{}, clientErr(http.StatusInternalServerError, "pos: pos_policy default row missing (0043 seed)")
	}
	return p, err
}

func (r *Repository) UpsertPosPolicy(ctx context.Context, p PosPolicy) error {
	if r.pool == nil {
		return errNoPool
	}
	if p.BranchID == "" {
		// The org-wide default row (branch_id IS NULL, UNIQUE does not cover NULLs, so update-in-place).
		// balance_visibility is the retired 0181 column (UX-97 item 208): the write stops touching it,
		// so whatever a dealer's older row stored there stays exactly as it is.
		_, err := r.pool.Exec(ctx,
			`UPDATE pos_policy SET cod_enabled = $1, restocking_fee_bps = $2, notes = $3,
			        line_presentation = $4, quick_chips_enabled = $5,
			        workspace_split_pct = $6, workspace_arrangement = $7,
			        lookup_presentation = $8, margin_visibility = $9, quick_chip_set = $10,
			        supplier_deposit_cancel = $11, updated_at = now()
				  WHERE branch_id IS NULL`,
			p.CODEnabled, p.RestockingFeeBps, p.Notes, p.LinePresentation, p.QuickChipsEnabled,
			p.WorkspaceSplitPct, p.WorkspaceArrangement, p.LookupPresentation,
			p.MarginVisibility, joinChipSet(p.QuickChipSet), p.SupplierDepositCancel)
		return err
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO pos_policy (branch_id, cod_enabled, restocking_fee_bps, notes, line_presentation, quick_chips_enabled,
                        workspace_split_pct, workspace_arrangement, lookup_presentation,
                        margin_visibility, quick_chip_set, supplier_deposit_cancel)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (branch_id) DO UPDATE SET cod_enabled = EXCLUDED.cod_enabled,
  restocking_fee_bps = EXCLUDED.restocking_fee_bps, notes = EXCLUDED.notes,
  line_presentation = EXCLUDED.line_presentation, quick_chips_enabled = EXCLUDED.quick_chips_enabled,
  workspace_split_pct = EXCLUDED.workspace_split_pct, workspace_arrangement = EXCLUDED.workspace_arrangement,
  lookup_presentation = EXCLUDED.lookup_presentation,
  margin_visibility = EXCLUDED.margin_visibility, quick_chip_set = EXCLUDED.quick_chip_set,
  supplier_deposit_cancel = EXCLUDED.supplier_deposit_cancel,
  updated_at = now()`,
		p.BranchID, p.CODEnabled, p.RestockingFeeBps, p.Notes, p.LinePresentation, p.QuickChipsEnabled,
		p.WorkspaceSplitPct, p.WorkspaceArrangement, p.LookupPresentation,
		p.MarginVisibility, joinChipSet(p.QuickChipSet), p.SupplierDepositCancel)
	return err
}

// -- DIB-488 rev 2: pos_user_workspace (0166), one person's workspace ergonomics ---------------

func (r *Repository) ReadUserWorkspace(ctx context.Context, userID string) (userWorkspaceRow, bool, error) {
	if r.pool == nil {
		return userWorkspaceRow{}, false, errNoPool
	}
	const q = `
SELECT user_id::text, split_pct, arrangement, lookup_collapsed, sale_collapsed, lookup_presentation,
       margin_hidden, write_key
  FROM pos_user_workspace
 WHERE user_id::text = $1`
	var row userWorkspaceRow
	err := r.pool.QueryRow(ctx, q, userID).Scan(&row.UserID, &row.SplitPct, &row.Arrangement,
		&row.LookupCollapsed, &row.SaleCollapsed, &row.LookupPresentation, &row.MarginHidden, &row.WriteKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return userWorkspaceRow{}, false, nil // absent is the dealer-defaults answer, not an error
	}
	if err != nil {
		return userWorkspaceRow{}, false, err
	}
	return row, true, nil
}

func (r *Repository) UpsertUserWorkspace(ctx context.Context, row userWorkspaceRow) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO pos_user_workspace (user_id, split_pct, arrangement, lookup_collapsed, sale_collapsed,
                                lookup_presentation, margin_hidden, write_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (user_id) DO UPDATE SET split_pct = EXCLUDED.split_pct,
  arrangement = EXCLUDED.arrangement, lookup_collapsed = EXCLUDED.lookup_collapsed,
  sale_collapsed = EXCLUDED.sale_collapsed, lookup_presentation = EXCLUDED.lookup_presentation,
  margin_hidden = EXCLUDED.margin_hidden,
  write_key = EXCLUDED.write_key, updated_at = now()`,
		row.UserID, row.SplitPct, row.Arrangement, row.LookupCollapsed, row.SaleCollapsed,
		row.LookupPresentation, row.MarginHidden, row.WriteKey)
	return err
}

func (r *Repository) DeleteUserWorkspace(ctx context.Context, userID string) error {
	if r.pool == nil {
		return errNoPool
	}
	// Reset IS a delete (the dashboard ruling); deleting an absent row is already reset, no error.
	_, err := r.pool.Exec(ctx, `DELETE FROM pos_user_workspace WHERE user_id::text = $1`, userID)
	return err
}

// -- returns -----------------------------------------------------------------

// findReturnByKeyQuery is the committed return behind one idempotency key.
const findReturnByKeyQuery = `
SELECT id::text, return_no, order_id::text, invoice_id::text, refund_method,
       subtotal_cents, tax_cents, total_cents, fee_cents, COALESCE(refund_payment_id::text,''), pickup_status, status
  FROM pos_returns WHERE idempotency_key = $1`

// returnOrderContextQuery is the original committed POS sale behind one order id or one typed
// reference. Numbers are stored with a hash (#SO-TRT-21, #INV-17) and the clerk reads them off a
// slip without it, so the reference ($2, already trimmed, upper cased and stripped of a leading hash
// by normalizeSaleRef) matches the order number or the invoice number with or without the hash, and a
// bare run of digits matches the number a branch's order numbers end in (the exact numbers rank
// first, then the newest sale).
const returnOrderContextQuery = `
SELECT o.id::text, o.order_no, o.branch_id::text, o.customer_id::text,
       COALESCE(ps.invoice_id::text,''), COALESCE(i.status,''), ps.tender, COALESCE(pay.gateway_ref,''),
       COALESCE(pay.amount_cents,0)
  FROM pos_sale ps
  JOIN orders o ON o.id = ps.order_id
  LEFT JOIN invoices i ON i.id = ps.invoice_id
  LEFT JOIN payments pay ON pay.id = ps.payment_id
 WHERE ($1 <> '' AND ps.order_id::text = $1)
    OR ($1 = '' AND $2 <> '' AND (
         upper(o.order_no) IN ($2, '#' || $2)
         OR upper(i.invoice_no) IN ($2, '#' || $2)
         OR ($2 ~ '^[0-9]+$' AND o.order_no LIKE '%-' || $2
             AND ($3::text[] IS NULL OR o.branch_id::text = ANY($3::text[])))))
 ORDER BY (upper(o.order_no) IN ($2, '#' || $2) OR upper(i.invoice_no) IN ($2, '#' || $2)) DESC,
          o.created_at DESC
 LIMIT 1`

func (r *Repository) FindReturnByKey(ctx context.Context, key string) (Return, bool, error) {
	if r.pool == nil {
		return Return{}, false, errNoPool
	}
	var ret Return
	err := r.pool.QueryRow(ctx, findReturnByKeyQuery, key).Scan(&ret.ReturnID, &ret.ReturnNo, &ret.OrderID, &ret.InvoiceID,
		&ret.RefundMethod, &ret.SubtotalCents, &ret.TaxCents, &ret.TotalCents, &ret.FeeCents, &ret.RefundPaymentID,
		&ret.PickupStatus, &ret.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Return{}, false, nil
	}
	if err != nil {
		return Return{}, false, err
	}
	return ret, true, nil
}

// FindReturnByKeyTx is the same read on the open Return transaction: the
// in-transaction same-key re-check must never read through the pool while
// the transaction holds a connection, or a pool full of Returns waiting on
// the same sale's advisory lock deadlocks the holder waiting for a second
// connection.
func (r *Repository) FindReturnByKeyTx(ctx context.Context, tx pgx.Tx, key string) (Return, bool, error) {
	var ret Return
	err := tx.QueryRow(ctx, findReturnByKeyQuery, key).Scan(&ret.ReturnID, &ret.ReturnNo, &ret.OrderID, &ret.InvoiceID,
		&ret.RefundMethod, &ret.SubtotalCents, &ret.TaxCents, &ret.TotalCents, &ret.FeeCents, &ret.RefundPaymentID,
		&ret.PickupStatus, &ret.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Return{}, false, nil
	}
	if err != nil {
		return Return{}, false, err
	}
	return ret, true, nil
}

func (r *Repository) ReturnOrderContext(ctx context.Context, orderID, orderNo string) (returnOrderCtx, bool, error) {
	return r.ReturnOrderContextFor(ctx, orderID, orderNo, nil)
}

// ReturnOrderContextFor is the same read for a typed reference scoped to the branches the caller
// reaches: reach nil is a caller who reaches every branch, a non-nil slice (possibly empty) limits the
// bare digits arm to those branch ids. Exact order and invoice numbers stay unscoped, so a full
// number from another branch still meets the branch wall.
func (r *Repository) ReturnOrderContextFor(ctx context.Context, orderID, orderNo string, reach []string) (returnOrderCtx, bool, error) {
	if r.pool == nil {
		return returnOrderCtx{}, false, errNoPool
	}
	// The original ring must be a committed POS sale (a pos_sale facet row exists). gateway_ref rides
	// the ORIGINAL capture's payments row, the CARD refund rail reverses against it.
	var oc returnOrderCtx
	err := r.pool.QueryRow(ctx, returnOrderContextQuery, orderID, orderNo, reach).Scan(&oc.OrderID, &oc.OrderNo, &oc.BranchID,
		&oc.CustomerID, &oc.InvoiceID, &oc.InvoiceStatus, &oc.SaleTender, &oc.GatewayRef, &oc.CapturedCents)
	if errors.Is(err, pgx.ErrNoRows) {
		if orderID == "" {
			return returnOrderCtx{}, false, nil
		}
		return r.convertedOrderContext(ctx, r.pool, orderID)
	}
	if err != nil {
		return returnOrderCtx{}, false, err
	}
	return oc, true, nil
}

// orderQuerier is the one read both the pool and a transaction answer.
type orderQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// convertedOrderContext is the return context of an order a quote became: an order with no pos_sale
// facet (nothing rung at the till). It carries the order's facts only; its invoice comes through the
// billing seam in the service (Converted marks it), and it has no tender and no card capture.
func (r *Repository) convertedOrderContext(ctx context.Context, q orderQuerier, orderID string) (returnOrderCtx, bool, error) {
	oc := returnOrderCtx{Converted: true}
	err := q.QueryRow(ctx, `
SELECT o.id::text, o.order_no, o.branch_id::text, o.customer_id::text
  FROM orders o
 WHERE o.id::text = $1 AND o.quote_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM pos_sale ps WHERE ps.order_id = o.id)`, orderID).Scan(&oc.OrderID, &oc.OrderNo, &oc.BranchID, &oc.CustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return returnOrderCtx{}, false, nil
	}
	if err != nil {
		return returnOrderCtx{}, false, err
	}
	return oc, true, nil
}

// ReturnOrderContextTx is the same read on the open Return transaction, the
// tx twin the in-transaction replay check needs (see FindReturnByKeyTx).
func (r *Repository) ReturnOrderContextTx(ctx context.Context, tx pgx.Tx, orderID, orderNo string) (returnOrderCtx, bool, error) {
	var oc returnOrderCtx
	err := tx.QueryRow(ctx, returnOrderContextQuery, orderID, orderNo, nil).Scan(&oc.OrderID, &oc.OrderNo, &oc.BranchID,
		&oc.CustomerID, &oc.InvoiceID, &oc.InvoiceStatus, &oc.SaleTender, &oc.GatewayRef, &oc.CapturedCents)
	if errors.Is(err, pgx.ErrNoRows) {
		if orderID == "" {
			return returnOrderCtx{}, false, nil
		}
		return r.convertedOrderContext(ctx, tx, orderID)
	}
	if err != nil {
		return returnOrderCtx{}, false, err
	}
	return oc, true, nil
}

// ReadCustomerTx is the customer credit read on the open Return transaction,
// the tx twin the ACCOUNT_CREDIT rail's subledger booking needs (see
// FindReturnByKeyTx): applyAccountCredit runs inside the Return transaction,
// and a customer read through the pool there would have the per-sale lock's
// holder wait for a second connection every other connection is a Return
// waiter that needs the holder's lock, deadlocking a full pool until the
// caller's context ends.
func (r *Repository) ReadCustomerTx(ctx context.Context, tx pgx.Tx, customerID string) (customerRec, error) {
	var c customerRec
	err := tx.QueryRow(ctx, readCustomerQuery, customerID).Scan(
		&c.ID, &c.CustomerType, &c.PaymentTermsDays, &c.CreditLimitCents, &c.OpenBalanceCents, &c.HomeBranchID,
	)
	return c, err
}

func (r *Repository) ListReturnableLines(ctx context.Context, orderID string) ([]ReturnableLineView, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	const q = `
SELECT ol.id::text, COALESCE(ol.product_id::text,''), COALESCE(NULLIF(ol.description, ''), p.name, ''), ol.qty_base::float8,
       ol.unit_price_cents, ol.line_total_cents, ol.tax_cents,
       ` + priorReturnQtyExpr + `::float8
  FROM order_lines ol
  LEFT JOIN products p ON p.id = ol.product_id
 WHERE ol.order_id::text = $1
 ORDER BY ol.line_no`
	rows, err := r.pool.Query(ctx, q, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReturnableLineView{}
	for rows.Next() {
		var v ReturnableLineView
		if err := rows.Scan(&v.OrderLineID, &v.ProductID, &v.Description, &v.QtySold,
			&v.UnitPriceCents, &v.LineTotalCents, &v.TaxCents, &v.QtyReturned); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// The prior-return sums are ONE SQL fact shared by the commit guard (LockReturnableLine, read under
// FOR UPDATE inside the return transaction) and the customer lines read (ListCustomerReturnableOrders,
// the pool read the screen builds its quantity steppers from): the same correlated sums, so the
// screen's qty open and the guard's remaining qty cannot disagree. A line's returned money and tax
// ride the same sums.
const (
	priorReturnQtyExpr   = `COALESCE((SELECT SUM(rl.qty_base) FROM pos_return_lines rl WHERE rl.order_line_id = ol.id), 0)`
	priorReturnMoneyExpr = `COALESCE((SELECT SUM(rl.line_total_cents) FROM pos_return_lines rl WHERE rl.order_line_id = ol.id), 0)`
	priorCreditOnlyExpr  = `COALESCE((SELECT SUM(rl.credit_only_base) FROM pos_return_lines rl WHERE rl.order_line_id = ol.id), 0)`
	priorReturnTaxExpr   = `COALESCE((SELECT SUM(rl.tax_cents) FROM pos_return_lines rl WHERE rl.order_line_id = ol.id), 0)`
)

// leftShelfExpr is the quantity of an order line that has physically left the shelf: the whole line
// on a collect now ring (the ring decrements stock), else what fulfilment has taken out. The
// commit's restock and write off rules read it, so goods that never left the shelf move no stock
// (the D1 defect). The customer lines read's shelf_state speaks the same rule in words.
const leftShelfExpr = `CASE WHEN EXISTS (SELECT 1 FROM pos_sale ps WHERE ps.order_id = ol.order_id
                                              AND ps.status = 'COMMITTED' AND ps.sale_type = 'COLLECT_NOW')
                         THEN ol.qty_base ELSE ol.fulfilled_base END`

// ListCustomerReturnableOrders is the customer lines read (sub item 10.1): every sales order the
// customer holds in HH, the widened Q1 ruling's source. A committed till ring (its newest COMMITTED
// pos_sale facet) and an order a quote became (orders.quote_id, set by the conversion) are orders
// like any other; nothing else is listed (portal and yard originated sales that HH holds as quotes
// or nothing at all are later work, named in the item's report). branchIDs nil reaches every branch;
// a non-nil slice (possibly empty) limits to those branch ids. Orders arrive newest first, bounded
// by limit; each line's open quantity is the guard's own netting (returnableQtyOpen over the shared
// prior-return sums).
func (r *Repository) ListCustomerReturnableOrders(ctx context.Context, customerID string, branchIDs []string, limit int, number string) ([]CustomerOrderView, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	const q = `
WITH cust_orders AS (
  SELECT o.id, o.order_no, o.branch_id::text, o.status,
         COALESCE(NULLIF(o.job_address,''), NULLIF(o.job_reference,''), NULLIF(o.po_job_ref,''), '') AS job_label,
         o.created_at AS created_ts,
         to_char(o.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
         CASE WHEN o.quote_id IS NOT NULL THEN 'QUOTE' ELSE 'TILL' END AS source,
         ps.sale_type, ps.tender, COALESCE(ps.invoice_id::text,'') AS invoice_id,
         (ps.tender = 'CARD' AND COALESCE(pay.gateway_ref,'') <> '') AS card_refundable
    FROM orders o
    LEFT JOIN LATERAL (
      SELECT sale_type, tender, invoice_id, payment_id FROM pos_sale
       WHERE order_id = o.id AND status = 'COMMITTED'
       ORDER BY created_at DESC LIMIT 1) ps ON TRUE
    LEFT JOIN payments pay ON pay.id = ps.payment_id
    LEFT JOIN invoices numinv ON numinv.id = ps.invoice_id
   WHERE o.customer_id = $1::uuid
     AND (ps.sale_type IS NOT NULL OR o.quote_id IS NOT NULL)
     AND ($2::text[] IS NULL OR o.branch_id::text = ANY($2::text[]))
     AND ($4::text = '' OR upper(o.order_no) IN ($4::text, '#' || $4::text) OR upper(numinv.invoice_no) IN ($4::text, '#' || $4::text)
          OR ($4::text ~ '^[0-9]+$' AND o.order_no LIKE '%-' || $4::text))
   ORDER BY o.created_at DESC, o.order_no DESC
   LIMIT $3
)
SELECT co.id::text, co.order_no, co.branch_id, co.status, co.job_label, co.created_at, co.source,
       COALESCE(co.sale_type,''), COALESCE(co.tender,''), COALESCE(co.invoice_id,''), co.card_refundable,
       ol.id::text, COALESCE(ol.product_id::text,''), COALESCE(p.sku::text,''),
       COALESCE(NULLIF(ol.description, ''), p.name, ''), ol.qty_base::float8,
       ol.unit_price_cents, ol.line_total_cents, ol.tax_cents,
       ` + priorReturnQtyExpr + `::float8, ` + priorReturnMoneyExpr + `, ` + priorReturnTaxExpr + `,
       CASE WHEN co.sale_type = 'COLLECT_NOW' THEN 'FULFILLED'
            WHEN ol.fulfilled_base >= ol.qty_base THEN 'FULFILLED'
            WHEN ol.fulfilled_base > 0 THEN 'PARTIALLY_FULFILLED'
            ELSE 'NOT_FULFILLED' END AS shelf_state
  FROM cust_orders co
  JOIN order_lines ol ON ol.order_id = co.id
  LEFT JOIN products p ON p.id = ol.product_id
 ORDER BY co.created_ts DESC, co.order_no DESC, ol.line_no`
	rows, err := r.pool.Query(ctx, q, customerID, branchIDs, limit, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CustomerOrderView{}
	for rows.Next() {
		var o CustomerOrderView
		var l CustomerReturnableLineView
		if err := rows.Scan(&o.OrderID, &o.OrderNo, &o.BranchID, &o.FulfilmentState, &o.JobLabel, &o.CreatedAt, &o.Source,
			&o.SaleType, &o.Tender, &o.InvoiceID, &o.CardRefundable,
			&l.OrderLineID, &l.ProductID, &l.SKU, &l.Description, &l.QtySold,
			&l.UnitPriceCents, &l.LineTotalCents, &l.TaxCents, &l.QtyReturned, &l.MoneyReturnedCents, &l.TaxReturnedCents,
			&l.ShelfState); err != nil {
			return nil, err
		}
		l.QtyOpen = returnableQtyOpen(l.QtySold, l.QtyReturned)
		if n := len(out); n > 0 && out[n-1].OrderID == o.OrderID {
			out[n-1].Lines = append(out[n-1].Lines, l)
			continue
		}
		o.Lines = []CustomerReturnableLineView{l}
		out = append(out, o)
	}
	return out, rows.Err()
}

// returnableLineQuery is the one read of an original order line with its prior-return sums: the
// commit appends FOR UPDATE OF ol (LockReturnableLine), the credit preview reads it as is
// (ReadReturnableLine), so the preview prices off the very same numbers.
const returnableLineQuery = `
SELECT ol.id::text, COALESCE(ol.product_id::text,''), ol.qty_base::float8, ol.unit_price_cents,
       ol.line_total_cents, ol.tax_cents,
       ` + priorReturnQtyExpr + `::float8, ` + priorReturnMoneyExpr + `, ` + priorReturnTaxExpr + `,
       ` + leftShelfExpr + `::float8, ` + priorCreditOnlyExpr + `::float8
  FROM order_lines ol
 WHERE ol.id = $2 AND ol.order_id = $1`

func scanReturnableLine(row pgx.Row) (returnableLine, bool, error) {
	var rl returnableLine
	err := row.Scan(&rl.OrderLineID, &rl.ProductID, &rl.QtySold,
		&rl.UnitPriceCents, &rl.LineTotalCents, &rl.TaxCents, &rl.PriorQty, &rl.PriorMoney, &rl.PriorTax,
		&rl.LeftShelfBase, &rl.PriorCreditOnly)
	if errors.Is(err, pgx.ErrNoRows) {
		return returnableLine{}, false, nil
	}
	if err != nil {
		return returnableLine{}, false, err
	}
	return rl, true, nil
}

func (r *Repository) LockReturnableLine(ctx context.Context, tx pgx.Tx, orderID, orderLineID string) (returnableLine, bool, error) {
	// FOR UPDATE on the original order line serializes concurrent returns of the SAME line, the
	// prior-return sums read after the lock are therefore exact.
	return scanReturnableLine(tx.QueryRow(ctx, returnableLineQuery+`
   FOR UPDATE OF ol`, orderID, orderLineID))
}

// ReadReturnableLine is the credit preview's unlocked read of the same line and sums.
func (r *Repository) ReadReturnableLine(ctx context.Context, orderID, orderLineID string) (returnableLine, bool, error) {
	if r.pool == nil {
		return returnableLine{}, false, errNoPool
	}
	return scanReturnableLine(r.pool.QueryRow(ctx, returnableLineQuery, orderID, orderLineID))
}

// SumCardRefundedTx is what earlier CARD returns of the order have already refunded to the card,
// read on the open return transaction after the per-sale lock (the D5 ceiling's other half);
// exceptReturnID leaves out the return whose header is already written ahead of its rail.
func (r *Repository) SumCardRefundedTx(ctx context.Context, tx pgx.Tx, orderID, exceptReturnID string) (int64, error) {
	var sum int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(total_cents - fee_cents - applied_cents - offset_cents), 0) FROM pos_returns
 WHERE order_id = $1 AND refund_method = 'CARD' AND ($2 = '' OR id::text <> $2)`, orderID, exceptReturnID).Scan(&sum)
	return sum, err
}

// querier is the one read the pool and a transaction both answer.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (r *Repository) qr(tx pgx.Tx) querier {
	if tx != nil {
		return tx
	}
	return r.pool
}

// InvoiceMoney reads an invoice's total and balance, on the transaction when given, else the pool.
func (r *Repository) InvoiceMoney(ctx context.Context, tx pgx.Tx, invoiceID string) (int64, int64, error) {
	if tx == nil && r.pool == nil {
		return 0, 0, errNoPool
	}
	var total, balance int64
	err := r.qr(tx).QueryRow(ctx, `SELECT total_cents, balance_cents FROM invoices WHERE id = $1::uuid`, invoiceID).Scan(&total, &balance)
	return total, balance, err
}

// SumReturnRails is what the order's earlier returns took off its invoice (offsets) and paid out in
// cash or card (payouts), the two figures the ruling's split reads next to the invoice's own money.
func (r *Repository) SumReturnRails(ctx context.Context, tx pgx.Tx, orderID string) (int64, int64, error) {
	if tx == nil && r.pool == nil {
		return 0, 0, errNoPool
	}
	var offsets, payouts int64
	err := r.qr(tx).QueryRow(ctx, `
SELECT COALESCE(SUM(offset_cents), 0),
       COALESCE(SUM(CASE WHEN refund_method IN ('CASH','CARD') THEN total_cents - fee_cents - applied_cents - offset_cents ELSE 0 END), 0)
  FROM pos_returns WHERE order_id = $1::uuid`, orderID).Scan(&offsets, &payouts)
	return offsets, payouts, err
}

// SetReturnOffset records the invoice offset the account rail made for a return.
func (r *Repository) SetReturnOffset(ctx context.Context, tx pgx.Tx, returnID string, offsetCents int64) error {
	_, err := tx.Exec(ctx, `UPDATE pos_returns SET offset_cents = $2 WHERE id = $1::uuid`, returnID, offsetCents)
	return err
}

// LockInvoicesTx takes the named invoices FOR NO KEY UPDATE in the order given (callers sort), the
// way the till's reversal does: a return's credit offset locks an invoice and then bumps the shared
// invoice sequence row, a payment locks its invoices first and bumps the row after, so a return that
// reached the row holding one invoice and then asked for another closed a cycle with a payment on the
// second. Taking every invoice first, sorted, removes the cycle. NO KEY UPDATE, not UPDATE, so a
// return's foreign key share lock on the invoice does not conflict with it.
func (r *Repository) LockInvoicesTx(ctx context.Context, tx pgx.Tx, invoiceIDs []string) error {
	for _, id := range invoiceIDs {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			return err
		}
	}
	return nil
}

// LockPaymentsSequenceTx draws the payments counter through the payments module's own seam, without
// advancing it, before this return's invoice locks: every payments writer takes that counter first,
// and a return that held an owed invoice and then waited on the counter (its refund rail's insert)
// closed a cycle with one (the Opus review's P2-4).
func (r *Repository) LockPaymentsSequenceTx(ctx context.Context, tx pgx.Tx) error {
	return payments.LockPaymentsSequence(ctx, tx)
}

// SumUnbookedCardRefundsTx is the money the processor already refunded for the order inside a
// transaction that then rolled back (pos_card_refund_attempts in UNBOOKED), plus every refund still
// IN_FLIGHT at the processor: while a card refund sits between the two transactions of the HH-690
// flow its amount is already reserved against the order's card capture ceiling, so a concurrent
// different-key refund on the same sale cannot over-refund the card. exceptRefundKey leaves out the
// refund whose own key is being retried (booking it is what the retry does).
func (r *Repository) SumUnbookedCardRefundsTx(ctx context.Context, tx pgx.Tx, orderID, exceptRefundKey string) (int64, error) {
	var sum int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM pos_card_refund_attempts
 WHERE order_id = $1 AND status IN ('IN_FLIGHT', 'UNBOOKED') AND refund_key <> $2`, orderID, exceptRefundKey).Scan(&sum)
	return sum, err
}

// StageCardRefundAttemptTx is short tx 1 of the two-transaction card refund (HH-690): inside the
// staging transaction, under the per-sale lock, it records the attempt as IN_FLIGHT before the
// processor is called outside any DB transaction. An earlier attempt under the SAME refund key that
// the processor already refunded (UNBOOKED with its refund_ref) answers that ref AND the amount the
// processor refunded, but only when the restaged fact is the SAME refund (amount, capture and sale):
// a same-key retry with a changed body must never book a different amount against the earlier
// reversal (HH-690 round 2, P1-1), so any difference refuses 409 in words and leaves the row
// UNBOOKED untouched. An attempt still IN_FLIGHT refuses as a 409 in words; anything else (FAILED,
// a BOOKED row whose return is gone, an UNBOOKED row with no ref) restages the key.
func (r *Repository) StageCardRefundAttemptTx(ctx context.Context, tx pgx.Tx, f cardRefundFact) (string, int64, error) {
	var status, refundRef, gatewayRef, orderID string
	var amount int64
	var callNotSent bool
	err := tx.QueryRow(ctx,
		`SELECT status, refund_ref, amount_cents, gateway_ref, order_id, call_not_sent FROM pos_card_refund_attempts WHERE refund_key = $1 FOR UPDATE`,
		f.RefundKey).Scan(&status, &refundRef, &amount, &gatewayRef, &orderID, &callNotSent)
	if err == nil {
		if status == "UNBOOKED" && refundRef != "" && callNotSent {
			// The ref is a manager's word for a refund whose processor call was never made
			// (PR #501 round 3): it is never booked from here; it goes back to a manager.
			return "", 0, clientErr(http.StatusConflict,
				"pos: the processor ref on this card refund was typed by a manager for a call that was never sent; a manager must check the processor and resolve it again before it can be booked")
		}
		if status == "UNBOOKED" && refundRef != "" {
			if amount != f.AmountCents || gatewayRef != f.GatewayRef || orderID != f.OrderID {
				return "", 0, clientErr(http.StatusConflict,
					"pos: that idempotency key already refunded a different card amount or sale; use a new key or book the original return")
			}
			return refundRef, amount, nil
		}
		if status == "IN_FLIGHT" {
			return "", 0, clientErr(http.StatusConflict,
				"pos: this card refund is already in flight at the processor; wait for it to finish")
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", 0, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO pos_card_refund_attempts (refund_key, order_id, branch_id, customer_id, gateway_ref, refund_ref, amount_cents, status, last_error, attempted_at, updated_at, initiated_by)
VALUES ($1, $2, $3, $4, $5, '', $6, 'IN_FLIGHT', '', now(), now(), $7)
ON CONFLICT (refund_key) DO UPDATE
   SET status = 'IN_FLIGHT', refund_ref = '', amount_cents = EXCLUDED.amount_cents,
       last_error = '', attempted_at = now(), updated_at = now(),
       initiated_by = CASE WHEN pos_card_refund_attempts.status = 'FAILED' OR pos_card_refund_attempts.initiated_by = ''
                           THEN EXCLUDED.initiated_by ELSE pos_card_refund_attempts.initiated_by END,
       processor_at = NULL, call_not_sent = false,
       resolution_log = CASE WHEN pos_card_refund_attempts.resolved_by <> ''
                             THEN pos_card_refund_attempts.resolution_log || jsonb_build_array(jsonb_build_object(
                                  'event', 'RESTAGED', 'at', now(), 'prior_resolved_by', pos_card_refund_attempts.resolved_by))
                             ELSE pos_card_refund_attempts.resolution_log END,
       resolved_by = '', resolved_at = NULL, resolution_note = ''`,
		f.RefundKey, f.OrderID, f.BranchID, f.CustomerID, f.GatewayRef, f.AmountCents, f.InitiatedBy); err != nil {
		return "", 0, err
	}
	return "", 0, nil
}

// HoldCardRefundAttemptInFlight records the processor's unknown-outcome error on an IN_FLIGHT
// attempt while keeping its status IN_FLIGHT, so the amount stays reserved against the capture
// ceiling and a retry refuses rather than calling the processor a second time (HH-690 round 2,
// P1-2: only a decline or a not-dispatched failure is known to have moved no money).
func (r *Repository) HoldCardRefundAttemptInFlight(ctx context.Context, refundKey, reason string) error {
	if r.pool == nil {
		return errNoPool
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE pos_card_refund_attempts SET last_error = $2, processor_at = now(), updated_at = now()
 WHERE refund_key = $1 AND status = 'IN_FLIGHT'`, refundKey, reason)
	if err != nil {
		return err
	}
	return r.flagResolvedByManager(ctx, tag.RowsAffected(), refundKey, "unknown outcome", reason)
}

// StampCardRefundCallStart refreshes attempted_at, immediately before one child's processor call,
// on the child about to be called AND every child still waiting its turn in the same dispatch
// (PR #501 round 2 P2-1, round 3 fix 1): the manager's live request fence measures from this
// stamp, so a waiting child is never older than one call plus the margin. keys[0] is the child
// about to be called. False means that row is no longer IN_FLIGHT (a manager settled it before
// the call began), and the caller must not call the processor.
func (r *Repository) StampCardRefundCallStart(ctx context.Context, keys []string) (bool, error) {
	if r.pool == nil {
		return false, errNoPool
	}
	if len(keys) == 0 {
		return false, nil
	}
	rows, err := r.pool.Query(ctx,
		`UPDATE pos_card_refund_attempts SET attempted_at = now(), updated_at = now()
 WHERE refund_key = ANY($1::text[]) AND status = 'IN_FLIGHT' RETURNING refund_key`, keys)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	started := false
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return false, err
		}
		if k == keys[0] {
			started = true
		}
	}
	return started, rows.Err()
}

// MarkCardRefundCallNotSent writes, on the row itself, that the dispatch found it already settled
// by a manager and so never sent its processor call (PR #501 round 3 fix 2): last_error says so,
// the resolution_log gets a CALL_NOT_SENT event, and call_not_sent makes the same key retry refuse
// to book the manager's typed ref.
func (r *Repository) MarkCardRefundCallNotSent(ctx context.Context, refundKey string) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
UPDATE pos_card_refund_attempts
   SET call_not_sent = true, updated_at = now(),
       last_error = 'the processor call was never sent: a manager settled this attempt before the call began',
       resolution_log = resolution_log || jsonb_build_array(jsonb_build_object('event', 'CALL_NOT_SENT', 'at', now()))
 WHERE refund_key = $1 AND resolved_by <> '' AND status IN ('UNBOOKED', 'FAILED')`, refundKey)
	return err
}

// flagResolvedByManager is the defence in depth of the dispatch-side closes: a write that matched
// no IN_FLIGHT row is silent when nobody resolved the row, but when a manager already did, the
// processor's answer contradicts the manager's word. It logs that loudly for reconciliation and
// returns the typed errCardRefundAlreadyResolved.
func (r *Repository) flagResolvedByManager(ctx context.Context, matched int64, refundKey, answer, reason string) error {
	if matched > 0 {
		return nil
	}
	var status, resolvedBy, refundRef string
	if err := r.pool.QueryRow(ctx,
		`SELECT status, resolved_by, refund_ref FROM pos_card_refund_attempts WHERE refund_key = $1`, refundKey).
		Scan(&status, &resolvedBy, &refundRef); err != nil || resolvedBy == "" {
		return nil
	}
	slog.Default().Error("pos: the processor answered a card refund attempt a manager already resolved; reconcile it against the processor console",
		"refund_key", refundKey, "processor_answer", answer, "reason", reason,
		"status", status, "resolved_by", resolvedBy, "manager_refund_ref", refundRef)
	return errCardRefundAlreadyResolved
}

// RecordCardRefundDispatched persists, on the pool and immediately after the processor answered
// outside any DB transaction, the reversal's own processor ref and the UNBOOKED state: the reference
// is durably recorded before short tx 2 opens, so a crash between the two transactions leaves a row
// reconciliation can act on instead of a refund only the processor knows.
func (r *Repository) RecordCardRefundDispatched(ctx context.Context, f cardRefundFact) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO pos_card_refund_attempts (refund_key, order_id, branch_id, customer_id, gateway_ref, refund_ref, amount_cents, status, last_error, processor_at, updated_at, initiated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'UNBOOKED', '', now(), now(), $8)
ON CONFLICT (refund_key) DO UPDATE
   SET status = 'UNBOOKED', refund_ref = EXCLUDED.refund_ref, amount_cents = EXCLUDED.amount_cents,
       processor_at = now(), updated_at = now(),
       initiated_by = CASE WHEN pos_card_refund_attempts.initiated_by <> ''
                           THEN pos_card_refund_attempts.initiated_by ELSE EXCLUDED.initiated_by END`,
		f.RefundKey, f.OrderID, f.BranchID, f.CustomerID, f.GatewayRef, f.RefundRef, f.AmountCents, f.InitiatedBy)
	return err
}

// FailCardRefundAttempt closes a staged attempt the processor refused (no money moved): IN_FLIGHT to
// FAILED with the refusal in words, so the open-attempts reconciliation does not keep listing it.
func (r *Repository) FailCardRefundAttempt(ctx context.Context, refundKey, reason string) error {
	if r.pool == nil {
		return errNoPool
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE pos_card_refund_attempts SET status = 'FAILED', last_error = $2, processor_at = now(), updated_at = now()
 WHERE refund_key = $1 AND status = 'IN_FLIGHT'`, refundKey, reason)
	if err != nil {
		return err
	}
	return r.flagResolvedByManager(ctx, tag.RowsAffected(), refundKey, "refused", reason)
}

// RecordUnbookedCardRefunds files, on the pool and AFTER the failed transaction ended, every
// processor refund that transaction made and then rolled back (D4). One row per refund key; a retry
// that fails again refreshes the row. A row that already carries the processor's own reversal ref
// keeps that ref and its amount whatever the new fact says (HH-690 round 2, P1-1): the record of
// what the processor actually refunded is never overwritten. A row that is already BOOKED is never
// touched either (UX-102b): a commit that reports an error after it took effect must not turn a
// booked refund back to UNBOOKED, where the cap would count it twice.
func (r *Repository) RecordUnbookedCardRefunds(ctx context.Context, facts []cardRefundFact, reason string) error {
	if r.pool == nil {
		return errNoPool
	}
	const q = `
INSERT INTO pos_card_refund_attempts (refund_key, order_id, branch_id, customer_id, gateway_ref, refund_ref, amount_cents, status, last_error, initiated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'UNBOOKED', $8, $9)
ON CONFLICT (refund_key) DO UPDATE SET status = 'UNBOOKED', last_error = EXCLUDED.last_error,
       refund_ref = CASE WHEN pos_card_refund_attempts.refund_ref <> '' THEN pos_card_refund_attempts.refund_ref ELSE EXCLUDED.refund_ref END,
       amount_cents = CASE WHEN pos_card_refund_attempts.refund_ref <> '' THEN pos_card_refund_attempts.amount_cents ELSE EXCLUDED.amount_cents END,
       initiated_by = CASE WHEN pos_card_refund_attempts.initiated_by <> ''
                           THEN pos_card_refund_attempts.initiated_by ELSE EXCLUDED.initiated_by END,
       updated_at = now()
       WHERE pos_card_refund_attempts.status <> 'BOOKED'`
	for _, f := range facts {
		if _, err := r.pool.Exec(ctx, q, f.RefundKey, f.OrderID, f.BranchID, f.CustomerID, f.GatewayRef, f.RefundRef, f.AmountCents, reason, f.InitiatedBy); err != nil {
			return err
		}
	}
	return nil
}

// MarkCardRefundsBooked closes the records of refunds a committed transaction has now booked: tx 2
// of the HH-690 flow books the return while the attempt stands UNBOOKED (or, on a path that never
// dispatched, IN_FLIGHT), so both open states close to BOOKED with their booking time.
func (r *Repository) MarkCardRefundsBooked(ctx context.Context, refundKeys []string) error {
	if r.pool == nil {
		return errNoPool
	}
	_, err := r.pool.Exec(ctx, `UPDATE pos_card_refund_attempts SET status = 'BOOKED', booked_at = now(), updated_at = now()
 WHERE refund_key = ANY($1::text[]) AND status IN ('IN_FLIGHT', 'UNBOOKED')`, refundKeys)
	return err
}

// MarkCardRefundsBookedTx is MarkCardRefundsBooked on the booking transaction (UX-102): the attempt
// flips BOOKED in the same transaction that writes the pos_returns row, so the card cap reads either
// both or neither. A key with no open attempt row is left alone.
func (r *Repository) MarkCardRefundsBookedTx(ctx context.Context, tx pgx.Tx, refundKeys []string) error {
	_, err := tx.Exec(ctx, `UPDATE pos_card_refund_attempts SET status = 'BOOKED', booked_at = now(), updated_at = now()
 WHERE refund_key = ANY($1::text[]) AND status IN ('IN_FLIGHT', 'UNBOOKED')`, refundKeys)
	return err
}

// -- the manager resolution of open attempts (HH-767) ------------------------

// cardRefundAttemptColumns is the attempt row as the manager's list and
// resolve answer it: the ledger's own facts joined with the order's number
// and the customer's name.
const cardRefundAttemptColumns = `a.id::text, a.refund_key, a.order_id::text, COALESCE(o.order_no, ''),
       a.branch_id::text, COALESCE(a.customer_id::text, ''), COALESCE(c.name, ''),
       a.gateway_ref, a.refund_ref, a.amount_cents, a.status, a.last_error,
       a.initiated_by, a.resolved_by, a.resolved_at, a.resolution_note,
       a.attempted_at, a.processor_at, a.booked_at, a.created_at, a.updated_at, a.call_not_sent`

// scanCardRefundAttempt reads one row of cardRefundAttemptColumns into the
// manager-facing shape (times as RFC3339 strings, the nullable ones nil).
func scanCardRefundAttempt(row pgx.Row) (CardRefundAttempt, error) {
	var a CardRefundAttempt
	var resolvedAt, processorAt, bookedAt *time.Time
	var attemptedAt, createdAt, updatedAt time.Time
	if err := row.Scan(&a.ID, &a.RefundKey, &a.OrderID, &a.OrderNo, &a.BranchID, &a.CustomerID, &a.CustomerName,
		&a.GatewayRef, &a.RefundRef, &a.AmountCents, &a.Status, &a.LastError,
		&a.InitiatedBy, &a.ResolvedBy, &resolvedAt, &a.ResolutionNote,
		&attemptedAt, &processorAt, &bookedAt, &createdAt, &updatedAt, &a.CallNotSent); err != nil {
		return CardRefundAttempt{}, err
	}
	tfmt := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	tptr := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		s := tfmt(*t)
		return &s
	}
	a.AttemptedAt, a.CreatedAt, a.UpdatedAt = tfmt(attemptedAt), tfmt(createdAt), tfmt(updatedAt)
	a.ResolvedAt, a.ProcessorAt, a.BookedAt = tptr(resolvedAt), tptr(processorAt), tptr(bookedAt)
	return a, nil
}

// ListOpenCardRefundAttempts reads the open attempts a manager's branch
// reach covers (HH-767): both open statuses by default or the one named
// status, newest attempted first. A NULL branch slice is the global reach;
// a non-nil one fences the read to the granted branches.
func (r *Repository) ListOpenCardRefundAttempts(ctx context.Context, statusFilter string, branches []string) ([]CardRefundAttempt, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	q := "SELECT " + cardRefundAttemptColumns + ` FROM pos_card_refund_attempts a
 JOIN orders o ON o.id = a.order_id
 LEFT JOIN customers c ON c.id = a.customer_id
 WHERE ($1::text[] IS NULL OR a.branch_id::text = ANY($1::text[])) AND a.status IN ('IN_FLIGHT', 'UNBOOKED')
 ORDER BY a.attempted_at DESC, a.id DESC`
	args := []any{branches}
	if statusFilter != "" {
		q = "SELECT " + cardRefundAttemptColumns + ` FROM pos_card_refund_attempts a
 JOIN orders o ON o.id = a.order_id
 LEFT JOIN customers c ON c.id = a.customer_id
 WHERE ($1::text[] IS NULL OR a.branch_id::text = ANY($1::text[])) AND a.status = $2
 ORDER BY a.attempted_at DESC, a.id DESC`
		args = append(args, statusFilter)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CardRefundAttempt
	for rows.Next() {
		a, err := scanCardRefundAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CardRefundAttemptForUpdateTx is the resolution's locked read (HH-767): the
// attempt row by its id under FOR UPDATE, inside the resolution transaction,
// joined with the order's number and the customer's name.
func (r *Repository) CardRefundAttemptForUpdateTx(ctx context.Context, tx pgx.Tx, attemptID string) (CardRefundAttempt, bool, error) {
	a, err := scanCardRefundAttempt(tx.QueryRow(ctx,
		"SELECT "+cardRefundAttemptColumns+` FROM pos_card_refund_attempts a
 JOIN orders o ON o.id = a.order_id
 LEFT JOIN customers c ON c.id = a.customer_id
 WHERE a.id = $1::uuid
 FOR UPDATE OF a`, attemptID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CardRefundAttempt{}, false, nil
		}
		return CardRefundAttempt{}, false, err
	}
	return a, true, nil
}

// ApplyCardRefundResolutionTx writes the manager's judgment on the locked
// row (HH-767): the status the outcome rules (UNBOOKED for REFUNDED with the
// processor's refund_ref, FAILED for NOT_REFUNDED), who resolved it, when,
// and the note, and appends the judgment to the row's resolution_log. An
// empty refundRef keeps whatever ref the row already holds.
func (r *Repository) ApplyCardRefundResolutionTx(ctx context.Context, tx pgx.Tx, attemptID, status, refundRef, resolvedBy, note string) error {
	tag, err := tx.Exec(ctx, `
UPDATE pos_card_refund_attempts
   SET status = $2, refund_ref = COALESCE(NULLIF($3::text, ''), refund_ref),
       resolved_by = $4, resolved_at = now(), resolution_note = $5,
       processor_at = COALESCE(processor_at, now()), updated_at = now(),
       call_not_sent = (call_not_sent AND $2::text <> 'FAILED'),
       resolution_log = resolution_log || jsonb_build_array(jsonb_build_object(
            'event', CASE WHEN $2::text = 'FAILED' THEN 'NOT_REFUNDED' ELSE 'REFUNDED' END,
            'at', now(), 'by', $4::text, 'refund_ref', COALESCE(NULLIF($3::text, ''), refund_ref), 'note', $5::text))
 WHERE id = $1::uuid AND status IN ('IN_FLIGHT', 'UNBOOKED')`,
		attemptID, status, refundRef, resolvedBy, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return clientErr(http.StatusConflict, "pos: card refund attempt is already resolved")
	}
	return nil
}

// CardRefundRefUsedElsewhereTx answers, inside the settle's own transaction,
// whether the processor's refund ref a manager names is already carried by
// ANOTHER attempt row (one reversal booked against two attempts) or already
// booked as a payment's gateway_ref (the refund landed on the books under
// that ref). Either answer refuses the settle (PR #501 round 1 P1-2); the
// partial unique index on refund_ref backs the attempt side at the table
// itself.
func (r *Repository) CardRefundRefUsedElsewhereTx(ctx context.Context, tx pgx.Tx, refundRef, exceptRefundKey string) (onAttempt, onPayment bool, err error) {
	err = tx.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM pos_card_refund_attempts WHERE refund_ref = $1 AND refund_key <> $2),
       EXISTS(SELECT 1 FROM payments WHERE gateway_ref = $1)`, refundRef, exceptRefundKey).Scan(&onAttempt, &onPayment)
	return onAttempt, onPayment, err
}

// sumCommittedReturnTotalsQuery is the sale's committed return total. Every
// pos_returns row is COMMITTED by the table's own CHECK constraint
// (migration 0043 pins status to the one value 'COMMITTED'), so the sum
// needs no status filter.
const sumCommittedReturnTotalsQuery = `SELECT COALESCE(SUM(total_cents), 0) FROM pos_returns WHERE order_id = $1`

// SumCommittedReturnTotals reads the sale's committed return total before
// any transaction opens: the approval prospect adds it to the requested
// lines' original totals, so a manager credential in the header is still
// consulted when the CUMULATIVE would cross the amount even though the new
// return alone sits under it.
func (r *Repository) SumCommittedReturnTotals(ctx context.Context, orderID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoPool
	}
	var sum int64
	err := r.pool.QueryRow(ctx, sumCommittedReturnTotalsQuery, orderID).Scan(&sum)
	return sum, err
}

// SumCommittedReturnTotalsTx is the same read on the open Return
// transaction, where it feeds the gate: taken after the per-sale advisory
// lock below, the prior total is exact against any concurrent return of the
// same sale.
func (r *Repository) SumCommittedReturnTotalsTx(ctx context.Context, tx pgx.Tx, orderID string) (int64, error) {
	var sum int64
	err := tx.QueryRow(ctx, sumCommittedReturnTotalsQuery, orderID).Scan(&sum)
	return sum, err
}

// LockSaleForReturn takes the transaction-scoped advisory lock that
// serializes every Return of one sale, released at the transaction's commit
// or rollback (the house pattern, bankrec/repository.go). The line locks
// below serialize only returns of the SAME line: two concurrent returns of
// DIFFERENT lines of one sale would each read the prior return sum before
// the other commits and both slip under the approval amount. Taken at the
// top of the transaction, before any line lock, by every Return including
// the A-06 portal rail, whose committed rows must count in later sums too.
func (r *Repository) LockSaleForReturn(ctx context.Context, tx pgx.Tx, orderID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "pos:return:"+orderID)
	return err
}

func (r *Repository) InsertReturn(ctx context.Context, tx pgx.Tx, ret returnRow) (string, string, error) {
	const q = `
INSERT INTO pos_returns (return_no, order_id, invoice_id, customer_id, branch_id, refund_method,
                         subtotal_cents, tax_cents, total_cents, fee_cents, reason, restock, till_session_id,
                         pickup_required, pickup_site, pickup_requested_date, pickup_status,
                         idempotency_key, created_by, note_id, applied_cents, offset_cents)
VALUES ('#RET-' || nextval('return_no_seq'), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
RETURNING id::text, return_no`
	var id, no string
	err := tx.QueryRow(ctx, q, ret.OrderID, ret.InvoiceID, ret.CustomerID, ret.BranchID, ret.RefundMethod,
		ret.SubtotalCents, ret.TaxCents, ret.TotalCents, ret.FeeCents, ret.Reason, ret.Restock, nullStr(ret.TillSessionID),
		ret.PickupRequired, ret.PickupSite, nullStr(ret.PickupDate), ret.PickupStatus,
		ret.IdempotencyKey, nullStr(ret.CreatedBy), nullStr(ret.NoteID), ret.AppliedCents, ret.OffsetCents).Scan(&id, &no)
	return id, no, err
}

// findReturnNoteByKeyQuery is the committed note behind one idempotency key, its children read
// beside it (each child keeping its own return number and its own facts).
const findReturnNoteByKeyQuery = `
SELECT n.id::text, n.note_no, n.customer_id::text, n.branch_id::text, n.refund_method,
       n.subtotal_cents, n.tax_cents, n.total_cents
  FROM return_notes n WHERE n.idempotency_key = $1`

const findReturnNoteChildrenQuery = `
SELECT r.id::text, r.return_no, r.order_id::text, r.invoice_id::text, r.refund_method,
       r.subtotal_cents, r.tax_cents, r.total_cents, r.fee_cents,
       COALESCE(r.refund_payment_id::text,''), r.pickup_status, r.status, r.applied_cents
  FROM pos_returns r WHERE r.note_id = $1 ORDER BY r.return_no`

func scanReturnNote(row pgx.Row) (ReturnNote, bool, error) {
	var n ReturnNote
	err := row.Scan(&n.NoteID, &n.NoteNo, &n.CustomerID, &n.BranchID, &n.RefundMethod,
		&n.SubtotalCents, &n.TaxCents, &n.TotalCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReturnNote{}, false, nil
	}
	if err != nil {
		return ReturnNote{}, false, err
	}
	return n, true, nil
}

// FindReturnNoteByKey answers the committed note behind an idempotency key, children included: the
// note replay's pool read.
func (r *Repository) FindReturnNoteByKey(ctx context.Context, key string) (ReturnNote, bool, error) {
	if r.pool == nil {
		return ReturnNote{}, false, errNoPool
	}
	n, found, err := scanReturnNote(r.pool.QueryRow(ctx, findReturnNoteByKeyQuery, key))
	if err != nil || !found {
		return ReturnNote{}, found, err
	}
	if err := r.readNoteChildren(ctx, r.pool.Query, &n); err != nil {
		return ReturnNote{}, false, err
	}
	return n, true, nil
}

// FindReturnNoteByKeyTx is the same read on the open ReturnAcross transaction, the FindReturnByKeyTx
// discipline: the in-transaction same-key re-check never asks the pool for a second connection while
// the transaction holds one.
func (r *Repository) FindReturnNoteByKeyTx(ctx context.Context, tx pgx.Tx, key string) (ReturnNote, bool, error) {
	n, found, err := scanReturnNote(tx.QueryRow(ctx, findReturnNoteByKeyQuery, key))
	if err != nil || !found {
		return ReturnNote{}, found, err
	}
	if err := r.readNoteChildren(ctx, tx.Query, &n); err != nil {
		return ReturnNote{}, false, err
	}
	return n, true, nil
}

// readNoteChildren loads the note's pos_returns children onto the note.
func (r *Repository) readNoteChildren(ctx context.Context, query func(context.Context, string, ...any) (pgx.Rows, error), n *ReturnNote) error {
	rows, err := query(ctx, findReturnNoteChildrenQuery, n.NoteID)
	if err != nil {
		return err
	}
	defer rows.Close()
	n.Returns = []Return{}
	for rows.Next() {
		var ret Return
		if err := rows.Scan(&ret.ReturnID, &ret.ReturnNo, &ret.OrderID, &ret.InvoiceID, &ret.RefundMethod,
			&ret.SubtotalCents, &ret.TaxCents, &ret.TotalCents, &ret.FeeCents, &ret.RefundPaymentID,
			&ret.PickupStatus, &ret.Status, &ret.AppliedCents); err != nil {
			return err
		}
		n.Returns = append(n.Returns, ret)
	}
	return rows.Err()
}

// InsertReturnNote writes the note header inside the ReturnAcross transaction, before its children:
// every child carries the note's id. The note number is its own sequence ('#NOTE-n').
func (r *Repository) InsertReturnNote(ctx context.Context, tx pgx.Tx, n returnNoteRow) (string, string, error) {
	const q = `
INSERT INTO return_notes (note_no, customer_id, branch_id, refund_method, subtotal_cents, tax_cents,
                          total_cents, reason, idempotency_key, created_by)
VALUES ('#NOTE-' || nextval('return_note_no_seq'), $1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id::text, note_no`
	var id, no string
	err := tx.QueryRow(ctx, q, n.CustomerID, n.BranchID, n.RefundMethod, n.SubtotalCents, n.TaxCents,
		n.TotalCents, n.Reason, n.IdempotencyKey, nullStr(n.CreatedBy)).Scan(&id, &no)
	return id, no, err
}

func (r *Repository) InsertReturnLines(ctx context.Context, tx pgx.Tx, returnID string, lines []returnLineRow) error {
	for _, l := range lines {
		const q = `
INSERT INTO pos_return_lines (return_id, order_line_id, product_id, qty_base, unit_price_cents,
                              line_total_cents, tax_cents, reason_code, disposition, restocked_base, written_off_base, credit_only_base)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
		if _, err := tx.Exec(ctx, q, returnID, l.OrderLineID, l.ProductID, l.QtyBase, l.UnitPriceCents,
			l.LineTotalCents, l.TaxCents, l.ReasonCode, l.Disposition, l.RestockedBase, l.WrittenOffBase, l.CreditOnlyBase); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) SetReturnRefundPayment(ctx context.Context, tx pgx.Tx, returnID, paymentID string) error {
	_, err := tx.Exec(ctx, `UPDATE pos_returns SET refund_payment_id = $2 WHERE id = $1`, returnID, paymentID)
	return err
}

// ApplyCreditToInvoice reduces the invoice balance by up to amountCents (floor 0), flipping status →
// PAID only when the balance is fully offset. It returns the cents actually applied (0 when the
// invoice carries no balance). The invoice row is locked by the UPDATE itself, no separate SELECT.
// The move stamps the invoices feed seq beside it (the payments settle's own posture): a credit
// landing on the payer's document is the change the payer's device must see.
func (r *Repository) ApplyCreditToInvoice(ctx context.Context, tx pgx.Tx, invoiceID string, amountCents int64) (int64, error) {
	// RETURNING sees the NEW row, so the applied amount derives from the OLD balance via the CTE.
	const q = `
WITH old AS (SELECT id, balance_cents FROM invoices WHERE id::text = $1 FOR UPDATE),
iseq AS (INSERT INTO scope_sequences (scope) VALUES ('invoices')
         ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
         RETURNING next_value)
UPDATE invoices i
   SET balance_cents = old.balance_cents - LEAST(old.balance_cents, $2),
       status = CASE WHEN old.balance_cents <= $2 THEN 'PAID' ELSE i.status END,
       updated_at = now(),
       seq = (SELECT next_value FROM iseq)
  FROM old
 WHERE i.id = old.id
RETURNING LEAST(old.balance_cents, $2)`
	var applied int64
	err := tx.QueryRow(ctx, q, invoiceID, amountCents).Scan(&applied)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return applied, err
}

func (r *Repository) ListReturnPickups(ctx context.Context, branchID string) ([]ReturnPickup, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	const q = `
SELECT id::text, return_no, order_id::text, customer_id::text, branch_id::text, pickup_site,
       COALESCE(pickup_requested_date::text,''), pickup_status, total_cents, created_at::text
  FROM pos_returns
 WHERE branch_id::text = $1 AND pickup_status IN ('REQUESTED','SCHEDULED')
 ORDER BY created_at`
	rows, err := r.pool.Query(ctx, q, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReturnPickup{}
	for rows.Next() {
		var p ReturnPickup
		if err := rows.Scan(&p.ReturnID, &p.ReturnNo, &p.OrderID, &p.CustomerID, &p.BranchID, &p.Site,
			&p.RequestedDate, &p.PickupStatus, &p.TotalCents, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) GetReturnPickup(ctx context.Context, returnID string) (ReturnPickup, bool, error) {
	if r.pool == nil {
		return ReturnPickup{}, false, errNoPool
	}
	const q = `
SELECT id::text, return_no, order_id::text, customer_id::text, branch_id::text, pickup_site,
       COALESCE(pickup_requested_date::text,''), pickup_status, total_cents, created_at::text
  FROM pos_returns WHERE id::text = $1`
	var p ReturnPickup
	err := r.pool.QueryRow(ctx, q, returnID).Scan(&p.ReturnID, &p.ReturnNo, &p.OrderID, &p.CustomerID,
		&p.BranchID, &p.Site, &p.RequestedDate, &p.PickupStatus, &p.TotalCents, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReturnPickup{}, false, nil
	}
	if err != nil {
		return ReturnPickup{}, false, err
	}
	return p, true, nil
}

// SetPickupStatus walks the pickup forward with an optimistic status guard, a concurrent transition
// loses with a 409, so the queue only ever moves REQUESTED → SCHEDULED → DONE once.
func (r *Repository) SetPickupStatus(ctx context.Context, returnID, from, to string) error {
	if r.pool == nil {
		return errNoPool
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE pos_returns SET pickup_status = $3 WHERE id::text = $1 AND pickup_status = $2`,
		returnID, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return clientErr(http.StatusConflict, "pos: pickup already moved past %s", from)
	}
	return nil
}

// -- parked tickets ----------------------------------------------------------

func (r *Repository) InsertParkedTicket(ctx context.Context, t ParkedTicket, actor string) (ParkedTicket, error) {
	if r.pool == nil {
		return ParkedTicket{}, errNoPool
	}
	linesJSON, err := json.Marshal(t.Lines)
	if err != nil {
		return ParkedTicket{}, err
	}
	const q = `
INSERT INTO pos_parked_tickets (ticket_no, branch_id, customer_id, sale_type, notes, lines, status, parked_by)
VALUES ('#PARK-' || nextval('parked_ticket_no_seq'), $1, $2, $3, $4, $5, 'PARKED', $6)
RETURNING id::text, ticket_no, created_at::text`
	err = r.pool.QueryRow(ctx, q, t.BranchID, nullStr(t.CustomerID), t.SaleType, t.Notes, linesJSON,
		nullStr(actor)).Scan(&t.ID, &t.TicketNo, &t.CreatedAt)
	if err != nil {
		return ParkedTicket{}, err
	}
	t.Status = "PARKED"
	return t, nil
}

func (r *Repository) ListParkedTickets(ctx context.Context, branchID string) ([]ParkedTicket, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	const q = `
SELECT id::text, ticket_no, branch_id::text, COALESCE(customer_id::text,''), sale_type, notes, lines, status, created_at::text
  FROM pos_parked_tickets
 WHERE branch_id::text = $1 AND status = 'PARKED'
 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ParkedTicket{}
	for rows.Next() {
		t, err := scanParkedTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanParkedTicket(row pgx.Row) (ParkedTicket, error) {
	var t ParkedTicket
	var linesJSON []byte
	if err := row.Scan(&t.ID, &t.TicketNo, &t.BranchID, &t.CustomerID, &t.SaleType, &t.Notes, &linesJSON,
		&t.Status, &t.CreatedAt); err != nil {
		return ParkedTicket{}, err
	}
	if err := json.Unmarshal(linesJSON, &t.Lines); err != nil {
		return ParkedTicket{}, err
	}
	return t, nil
}

// GetParkedTicket reads one ticket whatever its status, the parked fence's pre-read: the service
// holds the branch wall before any write, so a foreign branch's ticket is refused on its branch
// and not on its status.
func (r *Repository) GetParkedTicket(ctx context.Context, id string) (ParkedTicket, bool, error) {
	if r.pool == nil {
		return ParkedTicket{}, false, errNoPool
	}
	const q = `
SELECT id::text, ticket_no, branch_id::text, COALESCE(customer_id::text,''), sale_type, notes, lines, status, created_at::text
  FROM pos_parked_tickets
 WHERE id::text = $1`
	t, err := scanParkedTicket(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ParkedTicket{}, false, nil
	}
	if err != nil {
		return ParkedTicket{}, false, err
	}
	return t, true, nil
}

func (r *Repository) MarkParkedResumed(ctx context.Context, id, actor string) (ParkedTicket, error) {
	if r.pool == nil {
		return ParkedTicket{}, errNoPool
	}
	const q = `
UPDATE pos_parked_tickets
   SET status = 'RESUMED', resumed_by = $2, resumed_at = now(), updated_at = now()
 WHERE id::text = $1 AND status = 'PARKED'
RETURNING id::text, ticket_no, branch_id::text, COALESCE(customer_id::text,''), sale_type, notes, lines, status, created_at::text`
	t, err := scanParkedTicket(r.pool.QueryRow(ctx, q, id, nullStr(actor)))
	if errors.Is(err, pgx.ErrNoRows) {
		return ParkedTicket{}, r.parkedConflict(ctx, id, "resume")
	}
	return t, err
}

func (r *Repository) MarkParkedDiscarded(ctx context.Context, id string) error {
	if r.pool == nil {
		return errNoPool
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE pos_parked_tickets SET status = 'DISCARDED', updated_at = now() WHERE id::text = $1 AND status = 'PARKED'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return r.parkedConflict(ctx, id, "discard")
	}
	return nil
}

// parkedConflict distinguishes a missing ticket (404) from a non-PARKED one (409).
func (r *Repository) parkedConflict(ctx context.Context, id, verb string) error {
	var status string
	err := r.pool.QueryRow(ctx, `SELECT status FROM pos_parked_tickets WHERE id::text = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return errParkedNotFound
	}
	if err != nil {
		return err
	}
	return clientErr(http.StatusConflict, "pos: cannot %s a %s ticket", verb, status)
}

// -- pricing orders ----------------------------------------------------------

func (r *Repository) InsertPricingOrder(ctx context.Context, po PricingOrder, actor string) (PricingOrder, error) {
	if r.pool == nil {
		return PricingOrder{}, errNoPool
	}
	err := r.RunInTx(ctx, func(tx pgx.Tx) error {
		const q = `
INSERT INTO pricing_orders (doc_no, branch_id, customer_id, customer_name, notes, valid_until,
                            subtotal_cents, tax_cents, total_cents, created_by)
VALUES ('#PRICE-' || nextval('pricing_order_no_seq'), $1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id::text, doc_no, created_at::text`
		if err := tx.QueryRow(ctx, q, po.BranchID, nullStr(po.CustomerID), po.CustomerName, po.Notes,
			nullStr(po.ValidUntil), po.SubtotalCents, po.TaxCents, po.TotalCents,
			nullStr(actor)).Scan(&po.ID, &po.DocNo, &po.CreatedAt); err != nil {
			return err
		}
		for _, l := range po.Lines {
			const ql = `
INSERT INTO pricing_order_lines (pricing_order_id, line_no, product_id, sku, name, qty_entered,
                                 uom_entered, unit_price_cents, list_price_cents, line_total_cents)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
			if _, err := tx.Exec(ctx, ql, po.ID, l.LineNo, l.ProductID, l.SKU, l.Name, l.QtyEntered,
				l.UOMEntered, l.UnitPriceCents, l.ListPriceCents, l.LineTotalCents); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return PricingOrder{}, err
	}
	return po, nil
}

func (r *Repository) GetPricingOrder(ctx context.Context, id string) (PricingOrder, bool, error) {
	if r.pool == nil {
		return PricingOrder{}, false, errNoPool
	}
	const q = `
SELECT id::text, doc_no, branch_id::text, COALESCE(customer_id::text,''), customer_name, notes,
       COALESCE(valid_until::text,''), subtotal_cents, tax_cents, total_cents, created_at::text
  FROM pricing_orders WHERE id::text = $1`
	var po PricingOrder
	err := r.pool.QueryRow(ctx, q, id).Scan(&po.ID, &po.DocNo, &po.BranchID, &po.CustomerID, &po.CustomerName,
		&po.Notes, &po.ValidUntil, &po.SubtotalCents, &po.TaxCents, &po.TotalCents, &po.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PricingOrder{}, false, nil
	}
	if err != nil {
		return PricingOrder{}, false, err
	}
	rows, err := r.pool.Query(ctx, `
SELECT line_no, product_id::text, sku, name, qty_entered::float8, uom_entered, unit_price_cents,
       list_price_cents, line_total_cents
  FROM pricing_order_lines WHERE pricing_order_id = $1 ORDER BY line_no`, po.ID)
	if err != nil {
		return PricingOrder{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var l PricingOrderLine
		if err := rows.Scan(&l.LineNo, &l.ProductID, &l.SKU, &l.Name, &l.QtyEntered, &l.UOMEntered,
			&l.UnitPriceCents, &l.ListPriceCents, &l.LineTotalCents); err != nil {
			return PricingOrder{}, false, err
		}
		po.Lines = append(po.Lines, l)
	}
	return po, true, rows.Err()
}

func (r *Repository) ListPricingOrders(ctx context.Context, branchID string, limit int) ([]PricingOrder, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
SELECT id::text, doc_no, branch_id::text, COALESCE(customer_id::text,''), customer_name, notes,
       COALESCE(valid_until::text,''), subtotal_cents, tax_cents, total_cents, created_at::text
  FROM pricing_orders
 WHERE branch_id::text = $1
 ORDER BY created_at DESC
 LIMIT $2`
	rows, err := r.pool.Query(ctx, q, branchID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PricingOrder{}
	for rows.Next() {
		var po PricingOrder
		if err := rows.Scan(&po.ID, &po.DocNo, &po.BranchID, &po.CustomerID, &po.CustomerName, &po.Notes,
			&po.ValidUntil, &po.SubtotalCents, &po.TaxCents, &po.TotalCents, &po.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, po)
	}
	return out, rows.Err()
}

// ReadProductSnapshot reads the printable SKU + name for a pricing-doc line (read-only sibling read,
// M2-debt family).
func (r *Repository) ReadProductSnapshot(ctx context.Context, productID string) (string, string, error) {
	if r.pool == nil {
		return "", "", errNoPool
	}
	var sku, name string
	err := r.pool.QueryRow(ctx, `SELECT sku::text, name FROM products WHERE id::text = $1`, productID).Scan(&sku, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", clientErr(http.StatusUnprocessableEntity, "pos: unknown product %s", productID)
	}
	return sku, name, err
}

// InsertSwap writes the swap's link row inside the swap's transaction, after both documents exist.
func (r *Repository) InsertSwap(ctx context.Context, tx pgx.Tx, w swapRow) (string, error) {
	const q = `
INSERT INTO pos_swaps (idempotency_key, note_id, order_id, customer_id, branch_id, credit_cents, applied_cents,
                       new_total_cents, leftover_cents, shortfall_cents, leftover_method, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id::text`
	var id string
	err := tx.QueryRow(ctx, q, w.IdempotencyKey, w.NoteID, w.OrderID, w.CustomerID, w.BranchID, w.CreditCents,
		w.AppliedCents, w.NewTotalCents, w.LeftoverCents, w.ShortfallCents, w.LeftoverMethod, nullStr(w.CreatedBy)).Scan(&id)
	return id, err
}

// FindSwapByKey is the swap replay's pool read.
func (r *Repository) FindSwapByKey(ctx context.Context, key string) (swapRow, bool, error) {
	if r.pool == nil {
		return swapRow{}, false, errNoPool
	}
	var w swapRow
	err := r.pool.QueryRow(ctx, `
SELECT id::text, idempotency_key, note_id::text, order_id::text, customer_id::text, branch_id::text, credit_cents,
       applied_cents, new_total_cents, leftover_cents, shortfall_cents, leftover_method
  FROM pos_swaps WHERE idempotency_key = $1`, key).Scan(&w.ID, &w.IdempotencyKey, &w.NoteID, &w.OrderID, &w.CustomerID,
		&w.BranchID, &w.CreditCents, &w.AppliedCents, &w.NewTotalCents, &w.LeftoverCents, &w.ShortfallCents, &w.LeftoverMethod)
	if errors.Is(err, pgx.ErrNoRows) {
		return swapRow{}, false, nil
	}
	if err != nil {
		return swapRow{}, false, err
	}
	return w, true, nil
}
