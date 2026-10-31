package messagingprovider

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// collections_aging.go, the read-only aging projection that feeds the AR sweep.
//
// -------------------------------------------------------------------------------------------------
// THIS IS A STATED DEBT, NOT A DESIGN.
// The aging of the receivables ledger is internal/billing's fact. This file reads invoices from
// another module's table, which is exactly the coupling the ARAgingSource seam exists to avoid, and
// it does it anyway, because at the time of writing NO module in this program exposes an aging reader
// and inventing one inside billing is outside this bundle's fence.
//
// The containment is deliberate and total:
//   - It is SELECT-only. Nothing in this package writes, updates or locks an invoice.
//   - It is one function behind one interface. When billing grows an aging reader, main.go points the
//     seam at it and this file is deleted; nothing else in the package changes.
//   - It reads no money it does not report and no column it does not need.
//
// It is recorded as a union note in REPORT-W3-D.md rather than left to be discovered.
// -------------------------------------------------------------------------------------------------

// PgARAging is the Postgres aging projection.
type PgARAging struct{ pool *pgxpool.Pool }

// NewPgARAging builds the projection over a pool (nil pool → an empty, harmless source).
func NewPgARAging(pool *pgxpool.Pool) *PgARAging { return &PgARAging{pool: pool} }

var _ ARAgingSource = (*PgARAging)(nil)

// OverdueInvoices returns unpaid invoices at least minDays past due, worst first.
//
// WHAT COUNTS AS OVERDUE, PRECISELY:
//   - status FINALIZED or PARTIALLY_PAID. A DRAFT invoice was never sent, a PAID one is settled, and a
//     VOID one never existed as far as the customer is concerned. Texting about any of the three is a
//     phone call to the office at best.
//   - NET balance > 0 (balance_cents less the open holdback: the holdback is money the customer may hold,
//     never overdue, RT-2). A finalised invoice with a zero balance is paid in substance whatever its
//     status column says, and the balance is the number the customer would argue about.
//   - due_date strictly more than minDays before asOf, where the part of the net a holdback release made
//     payable is due its release day plus the invoice's own terms (see the query).
//
// The ordering is (days past due DESC, balance DESC): the sweep takes the worst first, so a cooldown
// that lets one notice per customer through spends it on the invoice that most needs it.
func (a *PgARAging) OverdueInvoices(ctx context.Context, asOf time.Time, minDays, limit int) ([]AgingInvoice, error) {
	if a.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}
	// The released part of a holdback ages from its release day (RT-3 fix 2), as the aging does: it is due the
	// release house day plus the invoice's own terms, never before the invoice's own due date, and the rest
	// of the net keeps the invoice's due date. A payment comes off the older part first. One row per
	// invoice: the balance of the parts that are at least minDays past due, aged by the oldest of them.
	rows, err := a.pool.Query(ctx, `
		SELECT q.id::text, q.invoice_no::text, q.customer_id::text, SUM(x.bal)::bigint, MIN(x.due)::date,
		       MAX($1::date - x.due)::int AS days_past_due, q.name
		FROM (
		  SELECT i.id, i.invoice_no, i.customer_id, c.name, i.due_date,
		         (i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0))) AS net,
		         invoice_released_cents_as_of(i.id, $4::timestamptz) AS rel_cents,
		         GREATEST(invoice_released_on(i.id, $4::timestamptz) + GREATEST(i.due_date - i.issue_date, 0), i.due_date) AS rel_due
		    FROM invoices i
		    JOIN customers c ON c.id = i.customer_id
		   WHERE i.status IN ('FINALIZED','PARTIALLY_PAID')
		) q
		CROSS JOIN LATERAL (VALUES
		      (q.net - LEAST(q.rel_cents, GREATEST(q.net, 0)), q.due_date),
		      (LEAST(q.rel_cents, GREATEST(q.net, 0)), q.rel_due)) AS x(bal, due)
		WHERE x.bal > 0 AND ($1::date - x.due)::int >= $2
		GROUP BY q.id, q.invoice_no, q.customer_id, q.name
		ORDER BY days_past_due DESC, SUM(x.bal) DESC
		LIMIT $3`, asOf.UTC(), minDays, limit, asOf)
	if err != nil {
		return nil, fmt.Errorf("messagingprovider: aging query: %w", err)
	}
	defer rows.Close()

	var out []AgingInvoice
	for rows.Next() {
		var inv AgingInvoice
		if err := rows.Scan(&inv.InvoiceID, &inv.InvoiceNo, &inv.CustomerID, &inv.BalanceCents,
			&inv.DueDate, &inv.DaysPastDue, &inv.CustomerName); err != nil {
			return nil, fmt.Errorf("messagingprovider: aging scan: %w", err)
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}
