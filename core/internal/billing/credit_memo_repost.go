package billing

// credit_memo_repost.go, DIB-429: the nightly re-post of credit-memo journals whose inline post
// failed.
//
// THE HOLE THIS CLOSES. IssueCreditMemo raises the memo and its AR subledger row in ONE transaction,
// then posts the reversing journal AFTER commit. That ordering is deliberate and stays: the
// customer's document must exist even if the chart of accounts is misconfigured, because a missing
// document is visible to the customer and a missing journal is visible to us. When the post refuses,
// the memo carries GLJournalError and the caller can say so, VISIBLE, which W6-C recorded as
// correct, but not SELF-HEALING. Somebody had to notice and re-issue by hand, and the state that
// needed noticing was a field on an HTTP response that nobody kept.
//
// WHAT MAKES THE SWEEP POSSIBLE AT ALL is that the journal's idempotency key is DERIVED, not minted:
//
//	ar.credit_memo.issued:<credit memo id>
//
// So "which memos have no journal?" is a question the database can answer without a new column, a
// new table or a queue, an anti-join against gl_journals on a key that cannot be lost. That is also
// what makes the sweep safe to run twice: a memo whose journal posted is no longer in the result set
// at all, and even if it were, gl.PostExternal is itself idempotent on the same key. The jobs runner
// has no lease and no fencing token (internal/jobs says so in capitals), so a job still running when
// its next window opens is simply STARTED AGAIN, idempotency is not optional here.
//
// WHY NO NEW OUTBOX TOPIC. W6-C's park says it plainly: the dispatcher and the jobs runner have both
// Run() and frozen their handler maps. A sweep needs neither. It is a read, a loop and the SAME
// poster seam the inline path uses, which matters more than the plumbing it avoids, because a
// second way of posting a credit-memo journal would be a second definition of the reversing entry.

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
)

// CreditMemoRepostJobName is the job_schedules.name this sweep registers under.
const CreditMemoRepostJobName = "ar.credit_memo_repost"

// creditMemoRepostLimit is the default batch ceiling. A sweep that tries to post ten thousand
// journals in one tick holds a connection for the whole night and, if it dies halfway, leaves the
// operator unable to say how far it got. A bounded batch converges over nights instead, and the
// count in the run detail is a number somebody can act on.
const creditMemoRepostLimit = 200

// CreditMemoRepostResult reports one sweep.
//
// Reposted and Failed are separate on purpose. A sweep that repaired 4 of 6 is not a failure, the
// four are genuinely fixed, but it is not a success either, and collapsing the two into one number
// is how a chronically broken account code stays invisible behind a green tick for a month.
type CreditMemoRepostResult struct {
	// Scanned is how many memos were found without a journal.
	Scanned int
	// Reposted is how many now have one.
	Reposted int
	// Failed is how many refused again. They stay in the result set and are retried tomorrow.
	Failed int
	// RepostedCents is the AR magnitude repaired, so the run detail states the size of the hole that
	// was in the ledger and not merely the number of documents.
	RepostedCents int64
	// FirstError is the first refusal's message, the diagnosis an operator needs. The rest are
	// counted, not concatenated: fifty copies of "account 4000 not found" is not fifty facts.
	FirstError string
	// Skipped is true when no poster is wired, in which case nothing was attempted.
	Skipped bool
}

// failedCreditMemo is one memo awaiting its journal.
type failedCreditMemo struct {
	id, no, branchID string
	// Magnitudes, POSITIVE. See the query: a credit memo's stored amounts are negative (billing flips
	// the sign once, at the INSERT) and the journal takes positive magnitudes with the ROLES swapped.
	arCents, revenueCents, taxCents int64
	occurredAt                      time.Time
}

// RepostFailedCreditMemoJournals finds credit memos with no reversing journal and posts it.
//
// limit <= 0 uses the default batch ceiling.
//
// UNBOUND POSTER IS A SKIP, NOT A FAILURE. A deployment with no CreditMemoPoster wired is one where
// credit memos never post journals at all, the sweep has nothing to repair and no way to repair it.
// Recording that as SUCCEEDED would make job_runs claim the ledger was checked; recording it as an
// error would page somebody about a configuration that is working as configured. It is a SKIP, which
// is the same distinction ar.credit_hold_sweep draws for its flag-off dry run.
func (w *InvoiceWriter) RepostFailedCreditMemoJournals(ctx context.Context, limit int) (CreditMemoRepostResult, error) {
	if w.pool == nil {
		return CreditMemoRepostResult{}, errNoDB
	}
	if limit <= 0 {
		limit = creditMemoRepostLimit
	}
	if w.glPoster == nil {
		return CreditMemoRepostResult{Skipped: true}, nil
	}

	pending, err := w.findCreditMemosWithoutJournals(ctx, limit)
	if err != nil {
		return CreditMemoRepostResult{}, err
	}

	return w.repostAll(ctx, pending), nil
}

// repostAll is the per-memo loop, split from the query so the arithmetic and the failure behaviour
// can be driven by a unit test over a fixed pending set, the SQL half is tested against a real
// database, where it can actually be wrong.
func (w *InvoiceWriter) repostAll(ctx context.Context, pending []failedCreditMemo) CreditMemoRepostResult {
	res := CreditMemoRepostResult{Scanned: len(pending)}
	for _, m := range pending {
		_, perr := w.glPoster.PostCreditMemoJournal(ctx, CreditMemoJournal{
			CreditMemoID: m.id, CreditMemoNo: m.no, BranchID: m.branchID,
			// THE SAME derived key the inline path uses. Not a new one: a second key would let the
			// sweep post a duplicate journal for a memo whose inline post actually succeeded a
			// moment after this query ran.
			IdempotencyKey: creditMemoJournalKey(m.id),
			Memo:           "Credit memo " + m.no + " (re-posted)",
			// The memo's OWN date, not tonight's. A journal dated the night the sweep happened to
			// run would land in the wrong period and re-open a month that was already closed.
			OccurredAt:   m.occurredAt,
			ARCents:      m.arCents,
			RevenueCents: m.revenueCents,
			TaxCents:     m.taxCents,
		})
		if perr != nil {
			res.Failed++
			if res.FirstError == "" {
				res.FirstError = fmt.Sprintf("%s: %v", m.no, perr)
			}
			// Deliberately NOT a return. One memo whose account is misconfigured must not stop the
			// other nineteen from being repaired, and it will be found again tomorrow, because
			// nothing about its state changed.
			continue
		}
		res.Reposted++
		res.RepostedCents += m.arCents
	}
	return res
}

// creditMemoJournalKey is THE key, in one place. IssueCreditMemo and this sweep must derive it
// identically or the anti-join silently finds every memo forever and posts a second journal for each.
func creditMemoJournalKey(creditMemoID string) string {
	return "ar.credit_memo.issued:" + creditMemoID
}

// findCreditMemosWithoutJournals is the anti-join.
//
// Amounts come back as POSITIVE magnitudes (`-subtotal_cents`), because a memo is stored with the
// sign already flipped and CreditMemoJournal is specced in magnitudes with the roles swapped, a
// negative leg is not representable in gl_journal_lines, deliberately, and passing one through would
// be caught only at the CHECK constraint.
//
// OccurredAt is the memo's own finalized/created date so a re-post lands in the memo's period.
//
// Ordered OLDEST FIRST: the hole that has been in the ledger longest is the one to close first, and a
// stable order means a memo that keeps failing does not starve the ones behind it out of the batch.
func (w *InvoiceWriter) findCreditMemosWithoutJournals(ctx context.Context, limit int) ([]failedCreditMemo, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT i.id::text, i.invoice_no::text, i.branch_id::text,
		       -i.total_cents, -i.subtotal_cents, -i.tax_cents,
		       COALESCE(i.finalized_at, i.created_at)
		  FROM invoices i
		 WHERE i.doc_type = 'CREDIT_MEMO'
		   AND i.source_kind <> 'IMPORT'
		   AND NOT EXISTS (
		         SELECT 1 FROM gl_journals j
		          WHERE j.idempotency_key = 'ar.credit_memo.issued:' || i.id::text)
		 ORDER BY COALESCE(i.finalized_at, i.created_at), i.id
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("billing: find credit memos without journals: %w", err)
	}
	defer rows.Close()

	var out []failedCreditMemo
	for rows.Next() {
		var m failedCreditMemo
		if err := rows.Scan(&m.id, &m.no, &m.branchID, &m.arCents, &m.revenueCents, &m.taxCents, &m.occurredAt); err != nil {
			return nil, fmt.Errorf("billing: scan credit memo without journal: %w", err)
		}
		// A memo with no money in it has no journal to post, there are no legs, and gl would refuse
		// an empty entry. Skipping it here keeps it out of the Failed count, where it would look like
		// a chart-of-accounts problem forever.
		if m.arCents == 0 {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// depositAwaitingRelease is one live deposit document with no release journal.
type depositAwaitingRelease struct{ invoiceID, orderID string }

// findDepositsWithoutRelease is the deposit sweep's anti-join, the same shape as the credit memo's
// above: the release journal's key is DERIVED (deposit.released:<invoice id>, the gl builder's key),
// so "which deposits have no release?" needs no new column. Live SUPPLIER_DEPOSIT documents bound to
// an order, oldest first. Whether the order's goods actually left is not asked here, the order
// module answers that (DeliveredOrderReader), never a status read.
func (w *InvoiceWriter) findDepositsWithoutRelease(ctx context.Context) ([]depositAwaitingRelease, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT i.id::text, i.order_id::text
		  FROM invoices i
		 WHERE i.doc_type = 'INVOICE' AND i.source_kind = 'SUPPLIER_DEPOSIT'
		   AND i.status NOT IN ('DRAFT','VOID') AND i.order_id IS NOT NULL
		   AND NOT EXISTS (
		         SELECT 1 FROM gl_journals j
		          WHERE j.idempotency_key = 'deposit.released:' || i.id::text)
		 ORDER BY i.created_at, i.id`)
	if err != nil {
		return nil, fmt.Errorf("billing: find deposits without a release: %w", err)
	}
	defer rows.Close()
	var out []depositAwaitingRelease
	for rows.Next() {
		var d depositAwaitingRelease
		if err := rows.Scan(&d.invoiceID, &d.orderID); err != nil {
			return nil, fmt.Errorf("billing: scan deposit without a release: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CreditMemoRepostJob builds the nightly JobFunc.
//
// Exported as a constructor rather than wired here, matching CreditHoldSweepJob: composition stays in
// the composition root.
//
// The run is recorded SKIPPED when no poster is wired (see RepostFailedCreditMemoJournals) and when
// there was simply nothing to repair. The second one is the important distinction for an operator:
// SUCCEEDED on this job means journals were WRITTEN, so a run history of SUCCEEDED rows is a history
// of nights the ledger had a hole in it, and a wall of SKIPPED is the healthy state.
func CreditMemoRepostJob(w *InvoiceWriter, limit int) jobs.JobFunc {
	return func(ctx context.Context) (jobs.Result, error) {
		res, err := w.RepostFailedCreditMemoJournals(ctx, limit)
		if err != nil {
			return jobs.Result{}, err
		}
		out := renderRepostJobResult(res)
		// The same nightly reconciliation also heals a supplier deposit release whose emission was
		// lost (deposit_release.go); its counts ride the run detail beside the credit memo's.
		dep, derr := w.ReleaseDeliveredDeposits(ctx, limit)
		if derr != nil {
			return out, derr
		}
		out.RowsRead += int64(dep.Candidates)
		out.RowsWritten += int64(dep.Released)
		out.Skipped = out.Skipped && dep.Released == 0
		out.Detail["deposit_candidates"] = dep.Candidates
		out.Detail["deposit_released"] = dep.Released
		out.Detail["deposit_failed"] = dep.Failed
		if dep.FirstError != "" {
			out.Detail["deposit_first_error"] = dep.FirstError
		}
		return out, nil
	}
}

// renderRepostJobResult is what an operator reads in job_runs, split out so the SKIPPED rule and the
// run detail are assertable without a scheduler.
func renderRepostJobResult(res CreditMemoRepostResult) jobs.Result {
	detail := map[string]any{
		"scanned":        res.Scanned,
		"reposted":       res.Reposted,
		"failed":         res.Failed,
		"reposted_cents": res.RepostedCents,
		"no_poster":      res.Skipped,
	}
	if res.FirstError != "" {
		detail["first_error"] = res.FirstError
	}
	return jobs.Result{
		RowsRead:    int64(res.Scanned),
		RowsWritten: int64(res.Reposted),
		Skipped:     res.Skipped || res.Scanned == 0,
		Detail:      detail,
	}
}
