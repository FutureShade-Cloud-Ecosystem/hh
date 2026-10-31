package billing

// lien_seam.go, SCAFFOLD-owned holdback read seam on *billing.Repository (same-package pool access; no edit
// to billing/service.go). billing is the sole reader of invoices, so the W6 M6 Lien Cockpit resolves the
// retention facts it ages + releases THROUGH here. These satisfy the lien HoldbackFactsReader seam:
//   - InvoicesWithHoldback  (the aging + release worklist)
//   - InvoiceHoldbackFacts  (the release-decision projection)
// FROZEN signatures, the M6 worker consumes these, never edits them.

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/lien"
)

// billedStatuses are the invoice statuses whose retention is REAL money on the Bill 60 clock: an
// invoice that has been issued to the customer and not unwound. Anything outside this set is
// deliberately excluded from the release worklist (DIB-417, escalated as DIB-T2-F1 in Wave 2):
//
//	DRAFT, the retention has not been billed yet. The Bill 60 clock has not started, because the
//	        holdback only becomes legally retained money once the invoice is issued. Putting a DRAFT
//	        on the worklist invites someone to "release" retention that was never withheld, which
//	        creates a payable out of nothing.
//	VOID , the retention was unwound with the invoice. The money is not held any more, so a release
//	        would be a second release of the same dollars.
//
// Both were reaching the worklist because the query filtered on holdback_cents > 0 alone, and both
// are the kind of error nobody spots on a screen: the row looks exactly like a real one, and the only
// tell is a status column the worklist does not show.
var billedStatuses = []string{"FINALIZED", "PARTIALLY_PAID", "PAID"}

// InvoicesWithHoldback returns every BILLED invoice still carrying retention (holdback_cents > 0) for
// a branch ("" = all branches), for the Bill 60 aging + Monday-triage worklist. "Billed" means
// FINALIZED, PARTIALLY_PAID or PAID, see billedStatuses for why DRAFT and VOID retention is not
// releasable money.
//
// A PAID invoice stays ON the worklist deliberately: the customer paying the billed portion does not
// release the retention, and the holdback is precisely the part that outlives payment. Dropping PAID
// here would hide the most common release candidate there is.
func (r *Repository) InvoicesWithHoldback(ctx context.Context, branchID string) ([]lien.HoldbackInvoice, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, invoice_no::text, customer_id::text, COALESCE(job_id::text, ''), branch_id::text, holdback_cents,
		        COALESCE(finalized_at, created_at)
		 FROM invoices
		 WHERE holdback_cents > 0
		   AND status = ANY($2::text[])
		   AND ($1 = '' OR branch_id = $1::uuid)
		 ORDER BY COALESCE(finalized_at, created_at)`, branchID, billedStatuses)
	if err != nil {
		return nil, fmt.Errorf("billing: invoices with holdback: %w", err)
	}
	defer rows.Close()
	var out []lien.HoldbackInvoice
	for rows.Next() {
		var hi lien.HoldbackInvoice
		var finalized time.Time
		if err := rows.Scan(&hi.InvoiceID, &hi.InvoiceNo, &hi.CustomerID, &hi.CustomerJobID, &hi.BranchID, &hi.HoldbackCents, &finalized); err != nil {
			return nil, fmt.Errorf("billing: scan holdback invoice: %w", err)
		}
		hi.FinalizedAt = finalized.Format(time.RFC3339)
		out = append(out, hi)
	}
	return out, rows.Err()
}

// InvoiceHoldbackFacts returns the release-decision projection of one invoice's retention.
func (r *Repository) InvoiceHoldbackFacts(ctx context.Context, invoiceID string) (lien.HoldbackFacts, error) {
	if r.pool == nil {
		return lien.HoldbackFacts{}, errNoDB
	}
	var f lien.HoldbackFacts
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, holdback_cents, branch_id::text, customer_id::text
		 FROM invoices WHERE id = $1`, invoiceID,
	).Scan(&f.InvoiceID, &f.HoldbackCents, &f.BranchID, &f.CustomerID)
	if err != nil {
		return lien.HoldbackFacts{}, fmt.Errorf("billing: invoice holdback facts %q: %w", invoiceID, err)
	}
	return f, nil
}
