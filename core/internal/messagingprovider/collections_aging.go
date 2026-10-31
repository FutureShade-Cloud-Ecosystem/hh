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
//   - due_date strictly more than minDays before asOf.
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
	rows, err := a.pool.Query(ctx, `
		SELECT i.id::text, i.invoice_no::text, i.customer_id::text, (i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0))), i.due_date,
		       ($1::date - i.due_date)::int AS days_past_due, c.name
		FROM invoices i
		JOIN customers c ON c.id = i.customer_id
		WHERE i.status IN ('FINALIZED','PARTIALLY_PAID')
		  AND (i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0))) > 0
		  AND ($1::date - i.due_date)::int >= $2
		ORDER BY days_past_due DESC, (i.balance_cents - LEAST(GREATEST(i.holdback_cents, 0), GREATEST(i.balance_cents, 0))) DESC
		LIMIT $3`, asOf.UTC(), minDays, limit)
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
