package billing

// dataset_read.go, the report writer's sales data set (W3-RW1): the invoice lines, read through this
// module's own documents for the reporting module's curated data sets.
//
// -- WHAT A SALE IS HERE, AND WHAT TIES TO WHAT -------------------------------------------------------
// A line is live when its invoice is neither DRAFT nor VOID: the same live set the AR ledger and the
// GL both agree with the invoice on. Because the writer posts subtotal_cents as the sum of the line
// totals, a dataset read grouped by invoice ties to the invoice headers by construction, and the
// reporting module's named integration test pins exactly that.
//
// -- THE FENCE IS THE CALLER'S OWN REACH --------------------------------------------------------------
// The read answers the stamped caller's reach, branch_reach.go's rule: a GLOBAL caller reads every
// branch, a branch scoped caller reads its granted branches, a scoped caller with no grants reads
// nothing, fail closed. The reach comes from the caller the guard stamped, so a consumer of this read
// cannot forget to pass it. Only this module's own tables are read: the line carries the product's id,
// and a name or a sku is the product module's answer to give, not a join to take.

import (
	"context"
	"errors"
	"fmt"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// ErrDatasetCallerRequired refuses an unstamped context on the dataset invoice lines read (fail closed).
var ErrDatasetCallerRequired = errors.New("billing: dataset read requires a stamped staff caller")

// DatasetSalesLine is one line of one live invoice, projected for the curated data set.
type DatasetSalesLine struct {
	InvoiceID      string
	InvoiceNo      string
	CustomerID     string
	BranchID       string
	ProductID      string
	SourceKind     string
	InvoiceDate    string
	Description    string
	QtyBase        float64
	UnitPriceCents int64
	LineTotalCents int64
	LineTaxCents   int64
}

// DatasetInvoiceLines reads the set over a window of issue dates, inside the stamped caller's reach.
// A zero from or to is refused: an unbounded read of every line ever is not a read this seam serves.
func (r *Repository) DatasetInvoiceLines(ctx context.Context, from, to time.Time) ([]DatasetSalesLine, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	if from.IsZero() || to.IsZero() {
		return nil, fmt.Errorf("billing: the invoice lines read needs a window of issue dates (from and to)")
	}
	c, ok := identity.StaffContextFromContext(ctx)
	if !ok || c.UserID == "" {
		return nil, ErrDatasetCallerRequired
	}
	reach := reachOfCaller(c)
	rows, err := r.pool.Query(ctx, `
		SELECT i.id::text, i.invoice_no, i.customer_id::text, i.branch_id::text,
		       COALESCE(il.product_id::text, ''), i.source_kind,
		       to_char(i.issue_date, 'YYYY-MM-DD'), il.description,
		       il.qty_base::float8, il.unit_price_cents, il.line_total_cents,
		       COALESCE(il.tax_cents, 0)
		  FROM invoices i
		  JOIN invoice_lines il ON il.invoice_id = i.id
		 WHERE i.status NOT IN ('DRAFT', 'VOID')
		   AND i.issue_date >= $1::date
		   AND i.issue_date <= $2::date
		   AND ($3::uuid[] IS NULL OR i.branch_id = ANY($3::uuid[]))
		 ORDER BY i.issue_date, i.invoice_no, il.line_no`,
		from, to, reach.SQLArg())
	if err != nil {
		return nil, fmt.Errorf("billing: dataset invoice lines: %w", err)
	}
	defer rows.Close()
	out := make([]DatasetSalesLine, 0)
	for rows.Next() {
		var l DatasetSalesLine
		if err := rows.Scan(&l.InvoiceID, &l.InvoiceNo, &l.CustomerID, &l.BranchID,
			&l.ProductID, &l.SourceKind, &l.InvoiceDate, &l.Description,
			&l.QtyBase, &l.UnitPriceCents, &l.LineTotalCents, &l.LineTaxCents); err != nil {
			return nil, fmt.Errorf("billing: scan dataset invoice line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
