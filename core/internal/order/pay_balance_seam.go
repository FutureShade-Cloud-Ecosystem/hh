package order

// pay_balance_seam.go, the order reads the POS Pay balance door (item 9.4)
// composes with the money modules' reads: the orders table is this module's,
// so the door asks here for the order's own facts and never reads the table
// itself. Every method takes the caller's transaction (the door opens a read
// only one for its advisory reads), so the locked take and the pool reads
// share one spelling and one snapshot.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PayBalanceOrder is one order's own facts as the Pay balance door reads
// them: its header, never its money (billing answers that) and never its
// customer (the customer module answers that).
type PayBalanceOrder struct {
	OrderID    string
	OrderNo    string
	CustomerID string
	BranchID   string
	Fulfilment string
	Status     string
	Job        string
	TotalCents int64
	// PastPicking is true when the order has already reached or passed the
	// picking rung (the module's own ladder), the fact the find's hold words
	// turn on (item 9 Opus P3-4).
	PastPicking bool
}

const payBalanceOrderColumns = `o.id::text, o.order_no::text, o.customer_id::text, o.branch_id::text,
	o.fulfillment, o.status, COALESCE(NULLIF(o.job_reference,''), NULLIF(o.po_job_ref,''), ''), o.total_cents`

func scanPayBalanceOrder(row pgx.Row) (PayBalanceOrder, error) {
	var o PayBalanceOrder
	err := row.Scan(&o.OrderID, &o.OrderNo, &o.CustomerID, &o.BranchID, &o.Fulfilment, &o.Status, &o.Job, &o.TotalCents)
	o.PastPicking = pastPicking(o.Status)
	return o, err
}

// PayBalanceInvoiced is what the money module knows of one order, handed in so
// the order module can pick the orders that owe at the source: the open
// invoice balance and what the live invoices bill.
type PayBalanceInvoiced struct {
	OrderID       string
	OpenCents     int64
	InvoicedCents int64
}

// PayBalanceOrderCandidates lists the live (not cancelled) orders a Pay
// balance find may offer, newest first: those whose order number matches the
// LIKE pattern, or whose customer is one of customerIDs AND that can owe, which
// is to say an order with an open invoice balance, or one whose total is more
// than its invoices bill (an order billed in full and paid is not a candidate,
// so a customer's many paid orders never crowd the owing one out of a page).
// invoiced is billing's answer for the customers' invoiced orders; an order
// absent from it is uninvoiced. Fenced to branchIDs (nil reaches every
// branch). The door settles the exact figure from the money modules and pages
// through with offset until it has enough.
func (r *Repository) PayBalanceOrderCandidates(ctx context.Context, tx pgx.Tx, like string, customerIDs, branchIDs []string, invoiced []PayBalanceInvoiced, limit, offset int) ([]PayBalanceOrder, error) {
	ids := make([]string, 0, len(invoiced))
	billed := make([]int64, 0, len(invoiced))
	open := make([]bool, 0, len(invoiced))
	for _, f := range invoiced {
		ids = append(ids, f.OrderID)
		billed = append(billed, f.InvoicedCents)
		open = append(open, f.OpenCents > 0)
	}
	rows, err := tx.Query(ctx, `
		WITH inv AS (SELECT * FROM unnest($6::uuid[], $7::bigint[], $8::bool[]) AS t(order_id, billed_cents, has_open))
		SELECT `+payBalanceOrderColumns+`
		  FROM orders o
		  LEFT JOIN inv ON inv.order_id = o.id
		 WHERE o.status <> 'CANCELLED'
		   AND (o.order_no ILIKE $1
		        OR (o.customer_id = ANY($2::uuid[])
		            AND (COALESCE(inv.has_open, false) OR o.total_cents > COALESCE(inv.billed_cents, 0))))
		   AND ($3::uuid[] IS NULL OR o.branch_id = ANY($3::uuid[]))
		 ORDER BY o.created_at DESC, o.id
		 LIMIT $4 OFFSET $5`, like, customerIDs, branchIDs, limit, offset, ids, billed, open)
	if err != nil {
		return nil, fmt.Errorf("order: pay balance candidates: %w", err)
	}
	defer rows.Close()
	var out []PayBalanceOrder
	for rows.Next() {
		o, err := scanPayBalanceOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("order: scan pay balance candidate: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// PayBalanceOrderFacts reads one order's facts on the caller's transaction;
// found is false for a missing order. A cancelled order is answered as it is
// (the door refuses it in words).
func (r *Repository) PayBalanceOrderFacts(ctx context.Context, tx pgx.Tx, orderID string) (PayBalanceOrder, bool, error) {
	o, err := scanPayBalanceOrder(tx.QueryRow(ctx,
		`SELECT `+payBalanceOrderColumns+` FROM orders o WHERE o.id = $1::uuid`, orderID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PayBalanceOrder{}, false, nil
		}
		return PayBalanceOrder{}, false, fmt.Errorf("order: pay balance facts: %w", err)
	}
	return o, true, nil
}

// LockPayBalanceOrderTx is the first step of the money lock order
// (payments/LOCK-ORDER.md): the order row FOR NO KEY UPDATE, answered with
// its facts. found is false for a missing order.
func (r *Repository) LockPayBalanceOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (PayBalanceOrder, bool, error) {
	o, err := scanPayBalanceOrder(tx.QueryRow(ctx,
		`SELECT `+payBalanceOrderColumns+` FROM orders o WHERE o.id = $1::uuid FOR NO KEY UPDATE OF o`, orderID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PayBalanceOrder{}, false, nil
		}
		return PayBalanceOrder{}, false, fmt.Errorf("order: lock pay balance order: %w", err)
	}
	return o, true, nil
}

// PickHoldOrderTx is the order half of the pay before release hold (billing
// declares the seam it reads this through): the customer and the total of the
// order, on the caller's transaction. found is false for a missing order.
func (r *Repository) PickHoldOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (customerID string, totalCents int64, found bool, err error) {
	err = tx.QueryRow(ctx,
		`SELECT customer_id::text, total_cents FROM orders WHERE id = $1::uuid`, orderID).
		Scan(&customerID, &totalCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("order: pick hold order: %w", err)
	}
	return customerID, totalCents, true, nil
}
