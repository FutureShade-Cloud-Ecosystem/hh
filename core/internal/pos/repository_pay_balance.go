package pos

// repository_pay_balance.go, the pgx side of item 9.4 (Pay balance at the
// counter, pay_balance.go). Reads and writes are the pos M2-debt family's
// own posture: the module already owns the orders, invoices and allocations
// columns it wrote at ring time, and the settle here follows the one money
// lock order (payments/LOCK-ORDER.md), mirrored from the register settle with
// the order row in front.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// settleInvoice is one payment slice applied to one invoice inside the take's
// transaction.
type settleInvoice struct {
	PaymentID, InvoiceID string
	CustomerID, BranchID string
	OrderID              string
	Method               string
	GatewayRef           string
	AmountCents          int64
	AllocatedBy          string
}

// lockPaymentsSequence takes the payments feed counter row WITHOUT advancing
// it, the payments module's own serialising lock (payments/LOCK-ORDER.md,
// lockPaymentsSequenceSQL): every writer that takes both the payments side and
// an invoice takes this row first, so a settle beside this take queues here
// instead of deadlocking on the rows.
const lockPayBalancePaymentsSequence = `INSERT INTO scope_sequences (scope, next_value) VALUES ('payments', 0)
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value`

// owingOrderSelect is the order's own facts plus its whole balance and its
// customer's terms: the read behind the find's rows and the advisory pre-read.
// The balance counts the OPEN invoice balances only (FINALIZED or
// PARTIALLY_PAID), the money this door may take.
const owingOrderSelect = `
	SELECT o.id::text, o.order_no::text, o.customer_id::text, COALESCE(c.name,''), o.branch_id::text,
	       o.fulfillment, c.payment_terms_days, o.total_cents,
	       COALESCE((SELECT SUM(i.balance_cents) FROM invoices i
	                  WHERE i.order_id = o.id AND i.status IN ('FINALIZED','PARTIALLY_PAID')), 0)
	  FROM orders o
	  JOIN customers c ON c.id = o.customer_id`

// FindOwingOrders answers the orders that owe money and match the term: an
// order number (the visible #SO form and bare), a customer name, or a phone.
// branchIDs nil reaches every branch (a global caller); otherwise the order's
// branch must be one of them, which is the whole fence: another branch's
// order is simply not in the answer.
func (r *Repository) FindOwingOrders(ctx context.Context, term string, branchIDs []string, limit int) ([]OwingOrder, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	if limit <= 0 {
		limit = 20
	}
	like := "%" + term + "%"
	rows, err := r.pool.Query(ctx, `
		SELECT o.id::text, o.order_no::text, o.customer_id::text, COALESCE(c.name,''),
		       o.fulfillment, o.total_cents, c.payment_terms_days,
		       COALESCE((SELECT SUM(i.balance_cents) FROM invoices i
		                  WHERE i.order_id = o.id AND i.doc_type = 'INVOICE'
		                    AND i.status IN ('FINALIZED','PARTIALLY_PAID')), 0) AS owed,
		       COALESCE((SELECT SUM(i.total_cents - i.balance_cents) FROM invoices i
		                  WHERE i.order_id = o.id AND i.doc_type = 'INVOICE' AND i.status NOT IN ('DRAFT','VOID')), 0) AS paid,
		       COALESCE(NULLIF(o.job_reference,''), NULLIF(o.po_job_ref,''), '') AS job
		  FROM orders o
		  JOIN customers c ON c.id = o.customer_id
		 WHERE (o.total_cents - COALESCE((SELECT SUM(i.total_cents) FROM invoices i
		                  WHERE i.order_id = o.id AND i.doc_type = 'INVOICE' AND i.status NOT IN ('DRAFT','VOID')), 0)
		        + COALESCE((SELECT SUM(i.balance_cents) FROM invoices i
		                  WHERE i.order_id = o.id AND i.doc_type = 'INVOICE'
		                    AND i.status IN ('FINALIZED','PARTIALLY_PAID')), 0)) > 0
		   AND (o.order_no ILIKE $1 OR c.name ILIKE $1
		        OR EXISTS (SELECT 1 FROM customer_contacts cc
		                    WHERE cc.customer_id = o.customer_id AND cc.is_active AND cc.phone LIKE $1))
		   AND ($2::uuid[] IS NULL OR o.branch_id = ANY($2::uuid[]))
		 ORDER BY o.created_at DESC
		 LIMIT $3`, like, branchIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("pos: find owing orders: %w", err)
	}
	defer rows.Close()
	var out []OwingOrder
	ids := make([]string, 0, limit)
	for rows.Next() {
		var o OwingOrder
		var termsDays int
		if err := rows.Scan(&o.OrderID, &o.OrderNo, &o.CustomerID, &o.CustomerName, &o.Fulfilment,
			&o.OrderTotalCents, &termsDays, &o.StillOwedCents, &o.PaidCents, &o.Job); err != nil {
			return nil, fmt.Errorf("pos: scan owing order: %w", err)
		}
		o.HeldFromPicking = termsDays <= 0 && o.StillOwedCents > 0
		out = append(out, o)
		ids = append(ids, o.OrderID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	// The money trail, one query for the page: each payment applied to the
	// order's invoices, with its method and amount. Collected per order id and
	// attached AFTER the rows loop, because appending to `out` above can
	// reallocate it and a pointer taken mid-loop would point at the old array.
	trail := map[string][]OwingPayment{}
	trows, err := r.pool.Query(ctx, `
		SELECT i.order_id::text, p.id::text, p.method, p.amount_cents
		  FROM invoices i
		  JOIN payment_allocations a ON a.invoice_id = i.id
		  JOIN payments p ON p.id = a.payment_id AND p.status = 'RECEIVED'
		 WHERE i.order_id = ANY($1::uuid[])
		 ORDER BY p.received_at, p.id`, ids)
	if err != nil {
		return nil, fmt.Errorf("pos: owing orders' payments: %w", err)
	}
	defer trows.Close()
	for trows.Next() {
		var orderID string
		var pay OwingPayment
		if err := trows.Scan(&orderID, &pay.PaymentID, &pay.Method, &pay.AmountCents); err != nil {
			return nil, fmt.Errorf("pos: scan owing payment: %w", err)
		}
		trail[orderID] = append(trail[orderID], pay)
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Payments = trail[out[i].OrderID]
		if out[i].Payments == nil {
			out[i].Payments = []OwingPayment{}
		}
	}
	return out, nil
}

// ReadOwingOrder is the advisory pool read of one order: its facts and its
// whole balance. found false is the door's 404 (a missing order and an order
// that owes nothing are different answers, the caller refuses the latter in
// words).
func (r *Repository) ReadOwingOrder(ctx context.Context, orderID string) (payBalanceOrderFacts, bool, error) {
	if r.pool == nil {
		return payBalanceOrderFacts{}, false, errNoPool
	}
	var f payBalanceOrderFacts
	var owed int64
	err := r.pool.QueryRow(ctx, owingOrderSelect+` WHERE o.id = $1::uuid`, orderID).
		Scan(&f.OrderID, &f.OrderNo, &f.CustomerID, &f.CustomerName, &f.BranchID, &f.Fulfilment,
			&f.TermsDays, &f.TotalCents, &owed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return payBalanceOrderFacts{}, false, nil
		}
		return payBalanceOrderFacts{}, false, fmt.Errorf("pos: read owing order: %w", err)
	}
	if owed > 0 {
		f.Invoices = []payBalanceInvoice{{InvoiceID: "", InvoiceNo: "", BalanceCents: owed}}
	}
	return f, true, nil
}

// FindPayBalancePaymentByKey answers the payments row a take's key already
// wrote, with the order its allocation paid (the replay answer's facts).
func (r *Repository) FindPayBalancePaymentByKey(ctx context.Context, key string) (payBalanceReplayRow, bool, error) {
	if r.pool == nil {
		return payBalanceReplayRow{}, false, errNoPool
	}
	var row payBalanceReplayRow
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT i.order_id::text FROM payment_allocations a
		                  JOIN invoices i ON i.id = a.invoice_id
		                 WHERE a.payment_id = p.id LIMIT 1), ''),
		       p.id::text, p.method, p.amount_cents
		  FROM payments p WHERE p.idempotency_key = $1`, key).
		Scan(&row.OrderID, &row.PaymentID, &row.Method, &row.AmountCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return payBalanceReplayRow{}, false, nil
		}
		return payBalanceReplayRow{}, false, fmt.Errorf("pos: pay balance payment by key: %w", err)
	}
	return row, true, nil
}

// LockPayBalanceOrderTx is the one-order writer's locked read, in the money
// lock order: the order row, the payments sequence row without advancing it,
// then the order's open invoice rows FOR NO KEY UPDATE ascending.
func (r *Repository) LockPayBalanceOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (payBalanceOrderFacts, error) {
	var f payBalanceOrderFacts
	if err := tx.QueryRow(ctx,
		`SELECT id::text, order_no::text, customer_id::text, branch_id::text, fulfillment, total_cents
		   FROM orders WHERE id = $1::uuid FOR NO KEY UPDATE`, orderID).
		Scan(&f.OrderID, &f.OrderNo, &f.CustomerID, &f.BranchID, &f.Fulfilment, &f.TotalCents); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return payBalanceOrderFacts{}, ErrPayBalanceOrderNotFound
		}
		return payBalanceOrderFacts{}, fmt.Errorf("pos: lock pay balance order: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT name, payment_terms_days FROM customers WHERE id = $1::uuid`, f.CustomerID).
		Scan(&f.CustomerName, &f.TermsDays); err != nil {
		return payBalanceOrderFacts{}, fmt.Errorf("pos: pay balance customer: %w", err)
	}
	if _, err := tx.Exec(ctx, lockPayBalancePaymentsSequence); err != nil {
		return payBalanceOrderFacts{}, fmt.Errorf("pos: lock payments sequence: %w", err)
	}
	irows, err := tx.Query(ctx, `
		SELECT id::text, invoice_no::text, balance_cents
		  FROM invoices
		 WHERE order_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents > 0
		 ORDER BY id ASC
		 FOR NO KEY UPDATE`, orderID)
	if err != nil {
		return payBalanceOrderFacts{}, fmt.Errorf("pos: lock pay balance invoices: %w", err)
	}
	defer irows.Close()
	for irows.Next() {
		var inv payBalanceInvoice
		if err := irows.Scan(&inv.InvoiceID, &inv.InvoiceNo, &inv.BalanceCents); err != nil {
			return payBalanceOrderFacts{}, fmt.Errorf("pos: scan pay balance invoice: %w", err)
		}
		f.Invoices = append(f.Invoices, inv)
	}
	return f, irows.Err()
}

// SettleInvoiceTx applies one payment slice to one invoice: the allocation
// row, the balance move with its invoices feed stamp and status flip, and the
// subledger PAYMENT row with the running account balance under the locks. The
// shape mirrors the payments module's applyAllocations (one spelling of
// applying money to an invoice; a second would be how the ledger acquires two
// answers to one question).
func (r *Repository) SettleInvoiceTx(ctx context.Context, tx pgx.Tx, s settleInvoice) error {
	var by any
	if s.AllocatedBy != "" {
		by = s.AllocatedBy
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, allocated_by)
		 VALUES ($1::uuid, $2::uuid, $3, $4)`,
		s.PaymentID, s.InvoiceID, s.AmountCents, by); err != nil {
		return fmt.Errorf("pos: pay balance allocation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		WITH iseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('invoices')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		UPDATE invoices
		   SET balance_cents = balance_cents - $3,
		       status = CASE WHEN balance_cents - $3 <= 0 THEN 'PAID' ELSE 'PARTIALLY_PAID' END,
		       updated_at = now(),
		       seq = (SELECT next_value FROM iseq)
		 WHERE id = $2::uuid AND customer_id = $1::uuid`,
		s.CustomerID, s.InvoiceID, s.AmountCents); err != nil {
		return fmt.Errorf("pos: pay balance invoice move: %w", err)
	}
	// The running account balance AFTER this slice: the balance move above
	// already ran, so the open balances' sum is the after figure.
	var runningAR int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		  WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		s.CustomerID).Scan(&runningAR); err != nil {
		return fmt.Errorf("pos: pay balance AR read: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id)
		 VALUES ($1::uuid, 'PAYMENT', $2, $3, 'payment', $4::uuid, $5::uuid)`,
		s.CustomerID, -s.AmountCents, runningAR, s.PaymentID, s.BranchID); err != nil {
		return fmt.Errorf("pos: pay balance subledger row: %w", err)
	}
	return nil
}

// OrderPaymentsTrailTx reads the money trail on the caller's transaction.
func (r *Repository) OrderPaymentsTrailTx(ctx context.Context, tx pgx.Tx, orderID string) ([]OwingPayment, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.id::text, p.method, p.amount_cents
		  FROM invoices i
		  JOIN payment_allocations a ON a.invoice_id = i.id
		  JOIN payments p ON p.id = a.payment_id AND p.status = 'RECEIVED'
		 WHERE i.order_id = $1::uuid
		 ORDER BY p.received_at, p.id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("pos: order payments trail: %w", err)
	}
	defer rows.Close()
	return scanOwingPayments(rows)
}

// OrderPaymentsTrail is the pool twin.
func (r *Repository) OrderPaymentsTrail(ctx context.Context, orderID string) ([]OwingPayment, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id::text, p.method, p.amount_cents
		  FROM invoices i
		  JOIN payment_allocations a ON a.invoice_id = i.id
		  JOIN payments p ON p.id = a.payment_id AND p.status = 'RECEIVED'
		 WHERE i.order_id = $1::uuid
		 ORDER BY p.received_at, p.id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("pos: order payments trail: %w", err)
	}
	defer rows.Close()
	return scanOwingPayments(rows)
}

func scanOwingPayments(rows pgx.Rows) ([]OwingPayment, error) {
	var out []OwingPayment
	for rows.Next() {
		var p OwingPayment
		if err := rows.Scan(&p.PaymentID, &p.Method, &p.AmountCents); err != nil {
			return nil, fmt.Errorf("pos: scan owing payment: %w", err)
		}
		out = append(out, p)
	}
	if out == nil {
		out = []OwingPayment{}
	}
	return out, rows.Err()
}
