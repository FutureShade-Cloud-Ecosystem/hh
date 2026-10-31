package billing

// holdback.go, RT-2 (wave 3): an invoice on a job withholds the job's holdback.
//
// THE BASE is the invoice's SUBTOTAL, before tax. The Construction Act holds back a percent of the
// price of the services or materials supplied, and the tax is charged on top of that price, not part
// of it; the province rules RT-1 reads carry a percent of price, and the holdback sync and the lien
// aging already treat holdback_cents as a pre tax figure (lien.holdbackAt). The delivery line is part
// of the price and so of the base. The customer is still billed the tax in full: it falls in the
// net the customer owes now (total less the holdback), never in the holdback.
//
// THE MONEY is integer cents, the percent is basis points, and the half cent goes to the even cent.
//
// THE RULE is read through the lien module's seam (lien.Service.HoldbackRuleForJob) on the writer's
// own transaction (customer.WithCallerTx), so a pool of one never needs a second connection and the
// rule read is the one in force when the document commits.
//
// THE DOORS. Every producer of an INVOICE row goes through InvoiceWriter.writeCore, so each of them
// withholds the same way:
//   - InvoiceOrder and InvoiceOrderNamed (the order's whole invoice, the invoices sync door);
//   - InvoiceOrderGroups (the multi-ship invoice of shipment groups);
//   - InvoiceOrderAtAgreed, AgreedOrderInvoice and their Tx forms (the counter's agreed invoice);
//   - the quote conversion and the pay-balance invoicer, which call those.
// The finance charge run also calls writeCore and is excluded here by source kind: a late fee is
// not the sale on a job and has no job. A supplier deposit document is excluded for the same reason
// the credit memo refuses it (it is a liability, not a sale).

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/lien"
)

// Withholder is the holdback seam: the rule for a job and the ledger's row for a withholding.
// *lien.Service satisfies it. Declared here so the writer names what it needs.
type Withholder interface {
	HasLinkedLienJob(ctx context.Context, customerJobID string) (bool, error)
	HoldbackRuleForJob(ctx context.Context, customerJobID string) (lien.HoldbackRule, error)
	BookRetention(ctx context.Context, b lien.RetentionBooking) (bool, error)
	BookCreditReversal(ctx context.Context, r lien.CreditReversal) (bool, error)
	BookHoldbackMove(ctx context.Context, m lien.HoldbackMove) (bool, error)
	// The release (RT-3): the preservation facts that decide when a job's holdback is due, the invoice's
	// RELEASE row, and the rows a request key already wrote.
	PreservationFor(ctx context.Context, customerJobID string) (lien.Preservation, bool, error)
	BookRelease(ctx context.Context, b lien.ReleaseBooking) (string, error)
	ReleasesForKey(ctx context.Context, key string) ([]lien.ReleasedRow, error)
	HasReleaseOnJob(ctx context.Context, customerJobID string) (bool, error)
}

// WithHoldback binds the holdback seam. Unbound, no invoice withholds (the writer as it was before RT-2).
func (w *InvoiceWriter) WithHoldback(h Withholder) *InvoiceWriter {
	w.holdback = h
	return w
}

// roundHalfEvenBps answers base*bps/10000 in integer cents, the half to the even cent.
func roundHalfEvenBps(base, bps int64) int64 {
	if base <= 0 || bps <= 0 {
		return 0
	}
	q, rem := base*bps/10000, base*bps%10000
	switch twice := rem * 2; {
	case twice > 10000:
		return q + 1
	case twice == 10000 && q%2 == 1:
		return q + 1
	default:
		return q
	}
}

// withheldCents is what the document withholds: nothing without a job, for a document that is not a
// sale, or when the job's rule is zero.
func (w *InvoiceWriter) withheldCents(ctx context.Context, tx pgx.Tx, sourceKind, jobID string, subtotal int64) (int64, error) {
	if w.holdback == nil || jobID == "" || sourceKind == SourceFinanceCharge || sourceKind == SourceSupplierDeposit {
		return 0, nil
	}
	// Only a job a lien job is linked to withholds; any other job never reads the rule, so a province
	// with no rule never refuses an ordinary sale.
	ctx = customer.WithCallerTx(ctx, tx)
	linked, err := w.holdback.HasLinkedLienJob(ctx, jobID)
	if err != nil || !linked {
		return 0, err
	}
	rule, err := w.holdback.HoldbackRuleForJob(ctx, jobID)
	if err != nil {
		return 0, err
	}
	return roundHalfEvenBps(subtotal, int64(rule.HoldbackPctBps)), nil
}

// bookWithholding gives the withholding its ledger row on the document's transaction.
func (w *InvoiceWriter) bookWithholding(ctx context.Context, tx pgx.Tx, invoiceID, invoiceNo string, f OrderInvoiceFacts, withheld int64) error {
	if withheld <= 0 || w.holdback == nil {
		return nil
	}
	if _, err := w.holdback.BookRetention(customer.WithCallerTx(ctx, tx), lien.RetentionBooking{
		InvoiceID: invoiceID, InvoiceNo: invoiceNo, CustomerJobID: f.JobID, BranchID: f.BranchID, AmountCents: withheld,
	}); err != nil {
		return fmt.Errorf("book the holdback ledger row: %w", err)
	}
	return nil
}

// reversalHoldback is the holdback part a credit memo takes back from the invoice it reverses, in
// proportion to the subtotal it credits: origHold * credited / origSub, the half to the even cent, and
// never more than the invoice has left to take back (remHold). A memo that credits everything the
// invoice has left takes back everything it has left, so the parts sum to the invoice's holdback to the
// cent however the credits were cut.
func reversalHoldback(origHold, remHold, origSub, remSub, credited int64) int64 {
	if origHold <= 0 || remHold <= 0 || origSub <= 0 || credited <= 0 {
		return 0
	}
	if credited >= remSub {
		return remHold
	}
	num := origHold * credited
	q, rem := num/origSub, num%origSub
	if twice := rem * 2; twice > origSub || (twice == origSub && q%2 == 1) {
		q++
	}
	return min(q, remHold)
}

// ErrNoHoldbackSeam is what a writer or repository with no holdback seam answers when asked to move an
// invoice's holdback. The column and the retention ledger move in one call; with no seam the ledger half
// cannot be written, so the column half is refused too (PR 762 probe D: a bare writer lowered the column
// and booked no ledger row). Refusing at the call, and not requiring the seam at the constructor, keeps
// every writer that never meets a holdback (a quote desk, a deposit door, a test) buildable bare.
var ErrNoHoldbackSeam = errors.New("billing: no holdback seam is bound, so the invoice's holdback column and the retention ledger cannot move together; build the writer with WithHoldback")

// lowerHoldbackAndLedger takes a credit memo's holdback part off the invoice it reverses: the column and
// the retention ledger's negative row, in one call on the memo's own transaction. They never move apart.
// Zero is a no-op; a part above zero with no seam bound is refused (ErrNoHoldbackSeam) before anything
// is touched.
func (w *InvoiceWriter) lowerHoldbackAndLedger(ctx context.Context, tx pgx.Tx, reversesInvoiceID, memoNo, branchID string, part int64) error {
	return lowerHoldbackAndLedger(ctx, tx, w.holdback, reversesInvoiceID, part, func(ctx context.Context) error {
		if _, err := w.holdback.BookCreditReversal(ctx, lien.CreditReversal{
			InvoiceID: reversesInvoiceID, MemoNo: memoNo, BranchID: branchID, AmountCents: part,
		}); err != nil {
			return fmt.Errorf("take the credit memo's holdback off the ledger: %w", err)
		}
		return nil
	})
}

// lowerHoldbackAndLedger is the one call that moves an invoice's holdback down: the column and the
// ledger row `book` writes, on the caller's transaction. A credit memo's reversal row and the RT-3 release
// row both come through here, so neither can lower the column without the ledger, or the ledger without
// the column. `book` runs with the transaction on the context the seam reads.
func lowerHoldbackAndLedger(ctx context.Context, tx pgx.Tx, h Withholder, invoiceID string, part int64, book func(ctx context.Context) error) error {
	if part <= 0 || invoiceID == "" {
		return nil
	}
	if h == nil {
		return ErrNoHoldbackSeam
	}
	if err := lowerInvoiceHoldback(ctx, tx, invoiceID, part); err != nil {
		return err
	}
	return book(customer.WithCallerTx(ctx, tx))
}

// lowerInvoiceHoldback is the column half of lowerHoldbackAndLedger and is called by nothing else. It takes a finalized credit memo's holdback part off the invoice it reverses, so the
// invoice's holdback_cents is always what it still withholds and every reader of it (the payment doors,
// the credit apply, the aging, the lien worklist) is right without knowing about memos. Zero is a no-op.
// The caller holds the invoice's row lock (the plan read it FOR NO KEY UPDATE).
func lowerInvoiceHoldback(ctx context.Context, tx pgx.Tx, invoiceID string, part int64) error {
	if part <= 0 || invoiceID == "" {
		return nil
	}
	// invoices is a trigger table: the trigger draws the change feed's seq, so no explicit draw here.
	tag, err := tx.Exec(ctx, `
UPDATE invoices
   SET holdback_cents = holdback_cents - $2::bigint, updated_at = now()
 WHERE id = $1::uuid AND holdback_cents >= $2::bigint`, invoiceID, part)
	if err != nil {
		return fmt.Errorf("billing: lower the invoice's holdback by the credit's part: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("billing: the invoice %s holds less than the %d cents the credit takes back", invoiceID, part)
	}
	return nil
}
