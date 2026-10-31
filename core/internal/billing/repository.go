package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoDB = errors.New("billing: database not configured")

// invoiceColumns is the canonical invoice header SELECT list (kept in one place so
// List/Get scan identically). order_id is nullable, read as *string.
const invoiceColumns = `id::text, invoice_no::text, customer_id::text, order_id::text,
	status, issue_date, due_date, subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents`

// Repository owns reads of invoices + invoice_lines + customer_transactions (pgx).
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
	// holdback is the retention ledger seam (holdback.go); nil = a write off moves no ledger row.
	holdback Withholder
	// holdOrders and holdCustomers are the pay before release hold's seams to
	// the order and customer modules (pick_hold.go), bound at the composition
	// root.
	holdOrders    PickHoldOrders
	holdCustomers PickHoldCustomers
	// zone is the dealer's business day zone source; nil = the Ontario dealer's. Every date parameter
	// the aging, register and statement reads bind is the dealer's own calendar day.
	zone houseday.Source
}

// WithHoldback binds the holdback seam the write off books its retention moves through (RT-2). Unbound,
// a write off moves no ledger row.
func (r *Repository) WithHoldback(h Withholder) *Repository { r.holdback = h; return r }

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (r *Repository) WithZone(src houseday.Source) *Repository { r.zone = src; return r }

// zoneFor answers the dealer's zone for one read (the Ontario dealer's with no source wired).
func (r *Repository) zoneFor(ctx context.Context) (houseday.Zone, error) {
	return houseday.Resolve(ctx, r.zone)
}

// clockAt is an instant read in the dealer's zone, the date parameter every register, aging and
// statement read binds: houseClock for the dealer the repository serves.
func (r *Repository) clockAt(ctx context.Context, at time.Time) (time.Time, error) {
	z, err := r.zoneFor(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return houseClockIn(z, at), nil
}

// onOrAfterHouseDay says whether the as of calendar day (day) is on or after the current house
// day (today), both houseClock readings in the dealer's zone: an as of on or after today reads the
// books' own balances, only a past date rebuilds them from history.
func onOrAfterHouseDay(day, today time.Time) bool {
	if day.Year() != today.Year() {
		return day.Year() > today.Year()
	}
	return day.YearDay() >= today.YearDay()
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func scanInvoice(row pgx.Row) (Invoice, error) {
	var inv Invoice
	var orderID *string // nullable column
	if err := row.Scan(
		&inv.ID, &inv.InvoiceNo, &inv.CustomerID, &orderID,
		&inv.Status, &inv.IssueDate, &inv.DueDate,
		&inv.SubtotalCents, &inv.TaxCents, &inv.HoldbackCents, &inv.TotalCents, &inv.BalanceCents,
	); err != nil {
		return Invoice{}, err
	}
	if orderID != nil {
		inv.OrderID = *orderID
	}
	return inv, nil
}

// ListInvoices returns a tenant-scoped, optionally status-filtered page of
// invoices. Lines are not joined here. Cursor pagination (HH-3.2 fold):
// stable-ordering basis issue_date DESC, invoice_no ASC-tiebroken (invoice_no
// is unique, so the pair is the list's unique stable key); the caller fetches
// limit+1, drops the extra row and mints next_cursor from the last kept row.
func (r *Repository) ListInvoices(ctx context.Context, customerID string, f Filter) ([]Invoice, error) {
	if r.pool == nil {
		return nil, errNoDB
	}

	where := "customer_id = $1"
	args := []any{customerID}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	var cIssue, cNo any
	if f.AfterCursor {
		cIssue, cNo = f.CursorIssueDate, f.CursorInvoiceNo
	}
	args = append(args, cIssue, cNo, f.Limit)
	q := fmt.Sprintf(
		"SELECT %s FROM invoices WHERE %s AND ($%d::date IS NULL OR issue_date < $%d::date OR (issue_date = $%d::date AND invoice_no > $%d::text)) ORDER BY issue_date DESC, invoice_no LIMIT $%d",
		invoiceColumns, where, len(args)-2, len(args)-2, len(args)-2, len(args)-1, len(args),
	)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list invoices: %w", err)
	}
	defer rows.Close()

	var out []Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan invoice: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetInvoice loads one invoice WITH its lines, scoped to the tenant. A missing or
// cross-tenant id maps pgx.ErrNoRows to ErrNotFound (404, never 403).
func (r *Repository) GetInvoice(ctx context.Context, customerID, invoiceID string) (Invoice, error) {
	if r.pool == nil {
		return Invoice{}, errNoDB
	}

	q := fmt.Sprintf("SELECT %s FROM invoices WHERE customer_id = $1 AND id = $2", invoiceColumns)
	inv, err := scanInvoice(r.pool.QueryRow(ctx, q, customerID, invoiceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invoice{}, ErrNotFound
		}
		return Invoice{}, fmt.Errorf("get invoice %q: %w", invoiceID, err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT line_no, product_id::text, description, qty_entered::text, uom_entered::text,
			unit_price_cents, line_total_cents
		 FROM invoice_lines WHERE invoice_id = $1 ORDER BY line_no`, invoiceID)
	if err != nil {
		return Invoice{}, fmt.Errorf("get invoice lines %q: %w", invoiceID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var ln Line
		var productID *string // nullable column
		if err := rows.Scan(
			&ln.LineNo, &productID, &ln.Description, &ln.QtyEntered, &ln.UOMEntered,
			&ln.UnitPriceCents, &ln.LineTotalCents,
		); err != nil {
			return Invoice{}, fmt.Errorf("scan invoice line: %w", err)
		}
		if productID != nil {
			ln.ProductID = *productID
		}
		inv.Lines = append(inv.Lines, ln)
	}
	if err := rows.Err(); err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// OpenBalanceCents sums open AR for the tenant (FINALIZED + PARTIALLY_PAID balances).
// balance_cents is maintained by the ERP posting path; this read assumes that invariant.
func (r *Repository) OpenBalanceCents(ctx context.Context, customerID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var sum int64
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents),0) FROM invoices
		 WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')`, customerID).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("open balance for %q: %w", customerID, err)
	}
	return sum, nil
}
