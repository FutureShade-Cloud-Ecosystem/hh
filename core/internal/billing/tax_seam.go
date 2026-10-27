package billing

// tax_seam.go, the collected side of the tax module's HST reconcile (FW1-11). billing owns the
// invoices table; tax reads the period's document tax through this seam and never the table.

import (
	"context"
	"fmt"
)

// DocumentSalesHSTCents sums the tax on the period's sales documents: the invoices' tax_cents with the
// credit memos' subtracted (IssueCreditMemo stores a credit memo's tax_cents negative, so summing
// tax_cents as stored subtracts the memo's tax once), on the documents' own issue dates (the dealer's
// calendar, not the server's reading of an instant). Only live documents count: FINALIZED,
// PARTIALLY_PAID and PAID; a VOID credit memo gives nothing back. Documents carrying the opening
// balance marker (metadata.opening_balance, set by the seed and by wave 2's load doors on exactly
// the open documents that arrived without a journal of their own) are NOT this period's business:
// they sit outside salesCents and come back named (count and HST) so the report can list them. The
// marker is explicit on the document, never inferred from a missing journal. month 0 is the whole
// year; branchID "" is consolidated.
func (r *Repository) DocumentSalesHSTCents(ctx context.Context, year, month int, branchID string) (salesCents int64, openingCount int, openingCents int64, err error) {
	if r.pool == nil {
		return 0, 0, 0, errNoDB
	}
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents) FILTER (WHERE NOT opening), 0),
		       COUNT(*) FILTER (WHERE opening)::int,
		       COALESCE(SUM(tax_cents) FILTER (WHERE opening), 0)
		FROM (
			SELECT tax_cents, metadata @> '{"opening_balance": true}'::jsonb AS opening
			FROM invoices
			WHERE status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
			  AND EXTRACT(YEAR FROM issue_date) = $1
			  AND ($2 = 0 OR EXTRACT(MONTH FROM issue_date) = $2)
			  AND ($3 = '' OR branch_id = $3::uuid)
		) d`, year, month, branchID).Scan(&salesCents, &openingCount, &openingCents)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("billing: document sales hst: %w", err)
	}
	return salesCents, openingCount, openingCents, nil
}
