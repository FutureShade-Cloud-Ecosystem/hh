package billing

// tax_seam.go, the collected side of the tax module's HST reconcile (FW1-11). billing owns the
// invoices table; tax reads the period's document tax through this seam and never the table.

import (
	"context"
	"fmt"
)

// DocumentSalesHSTCents sums the tax on the period's sales documents: the invoices' tax_cents with the
// credit memos' subtracted (a return or a finance credit gives its tax back), on the documents' own
// issue dates (the dealer's calendar, not the server's reading of an instant). Only live documents
// count: FINALIZED, PARTIALLY_PAID and PAID; a VOID credit memo gives nothing back. month 0 is the
// whole year; branchID "" is consolidated.
func (r *Repository) DocumentSalesHSTCents(ctx context.Context, year, month int, branchID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var total int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN doc_type = 'CREDIT_MEMO' THEN -tax_cents ELSE tax_cents END), 0)
		FROM invoices
		WHERE status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
		  AND EXTRACT(YEAR FROM issue_date) = $1
		  AND ($2 = 0 OR EXTRACT(MONTH FROM issue_date) = $2)
		  AND ($3 = '' OR branch_id = $3::uuid)`, year, month, branchID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("billing: document sales hst: %w", err)
	}
	return total, nil
}
