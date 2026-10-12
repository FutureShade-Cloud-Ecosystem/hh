package billing

// deposit_release.go, the billing half of the supplier deposit release: the
// seam the order module calls on a delivery (ReleaseDeposits) and the nightly
// sweep that heals a release whose emission was lost (ReleaseDeliveredDeposits).
//
// WHY BILLING. The deposit documents are billing's rows, and the release is
// the event the GL posting consumer owns; reading the order's live documents
// and writing the outbox row both belong here, so the order module reaches
// neither table (decision: reads and writes go through the owning module).
//
// NEVER LOST. The emission is one outbox row per document, each in its own
// short transaction, and a failed document never drops the next
// (releaseEach). A failure is returned to the order module, which logs it; the
// order has by then recorded the delivery on its own row, and the sweep finds
// a delivered order with a live deposit document and no release journal
// (the key deposit.released:<invoice id>) and emits it again. A sweep that
// runs while a row is still pending emits a second row; the ledger's journal
// key keeps the release exactly once, as it does for a redelivered fulfilment.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DepositReleasedTopic is the outbox topic the GL posting consumer owns.
const DepositReleasedTopic = "deposit.released"

// DeliveredOrderReader is the order module's read of its own delivery fact:
// which of these orders had a fulfilment road record the goods leaving. The
// sweep consults it so the release never rests on an order status a
// configurable task rule could reach.
type DeliveredOrderReader interface {
	DeliveredOrderIDs(ctx context.Context, orderIDs []string) ([]string, error)
}

// WithDeliveredOrders injects the delivery fact read the sweep needs. Without
// it the sweep is a skip.
func (w *InvoiceWriter) WithDeliveredOrders(r DeliveredOrderReader) *InvoiceWriter {
	w.delivered = r
	return w
}

// JournalKeyReader is the ledger's keyed existence read: which of these idempotency keys already
// name a journal. Declared here so billing never reads gl_journals itself (the gl service satisfies
// it at the composition root). The release journal's key is derived, deposit.released:<invoice id>,
// so "which deposits have no release?" needs no new column.
type JournalKeyReader interface {
	JournalKeysExist(ctx context.Context, keys []string) ([]string, error)
}

// WithJournalKeyReader injects the ledger read the sweep needs. Without it the sweep is a skip.
func (w *InvoiceWriter) WithJournalKeyReader(r JournalKeyReader) *InvoiceWriter {
	w.journals = r
	return w
}

// DepositSweepResult reports one sweep.
type DepositSweepResult struct {
	// Candidates is how many live deposit documents had no release journal.
	Candidates int
	// Released is how many documents the sweep emitted for.
	Released int
	// Failed is how many emissions refused; they are found again next run.
	Failed int
	// FirstError is the first refusal's message.
	FirstError string
	// Skipped is true when no delivered-order read is wired.
	Skipped bool
}

// ReleaseDeposits emits deposit.released for each of the order's live
// SUPPLIER_DEPOSIT documents, continuing past a failed document.
func (w *InvoiceWriter) ReleaseDeposits(ctx context.Context, orderID string) error {
	if w.pool == nil {
		return errNoDB
	}
	ids, err := depositDocumentIDs(ctx, w.pool, orderID)
	if err != nil {
		return err
	}
	return releaseEach(ctx, ids, func(ctx context.Context, id string) error {
		return w.emitDepositReleased(ctx, id, orderID)
	})
}

// depositQuerier is the read both a pool and a transaction answer.
type depositQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// depositDocumentIDs lists the order's live SUPPLIER_DEPOSIT documents (not DRAFT, not VOID), oldest first.
func depositDocumentIDs(ctx context.Context, q depositQuerier, orderID string) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT id::text FROM invoices
		 WHERE order_id = $1::uuid AND doc_type = 'INVOICE'
		   AND source_kind = 'SUPPLIER_DEPOSIT' AND status NOT IN ('DRAFT','VOID')
		 ORDER BY created_at, id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("billing: deposit documents for %s: %w", orderID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("billing: scan deposit document: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: deposit documents for %s: %w", orderID, err)
	}
	return ids, nil
}

// DepositDocumentIDsTx is the pos return's read of the order's live deposit documents, on the
// return's own transaction (pos.DepositDocumentLister, satisfied at the composition root).
func (w *InvoiceWriter) DepositDocumentIDsTx(ctx context.Context, tx pgx.Tx, orderID string) ([]string, error) {
	if tx == nil { // the pool read, for a preview that holds no transaction
		if w.pool == nil {
			return nil, errNoDB
		}
		return depositDocumentIDs(ctx, w.pool, orderID)
	}
	return depositDocumentIDs(ctx, tx, orderID)
}

// releaseEach runs emit for every id, never stopping at a failure, and
// returns the failures joined, each naming its document.
func releaseEach(ctx context.Context, ids []string, emit func(context.Context, string) error) error {
	var errs []error
	for _, id := range ids {
		if err := emit(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("deposit document %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// emitDepositReleased writes one deposit.released outbox row in its own
// transaction.
func (w *InvoiceWriter) emitDepositReleased(ctx context.Context, invoiceID, orderID string) error {
	return w.inTx(ctx, func(tx pgx.Tx) error {
		return emitOutbox(ctx, tx, DepositReleasedTopic, map[string]any{
			"invoice_id": invoiceID, "order_id": orderID,
		})
	})
}

// ReleaseDeliveredDeposits is the sweep: live deposit documents with no
// release journal whose order recorded a delivery get their emission.
// limit <= 0 uses the package's batch ceiling.
//
// The candidates are read a page at a time (depositSweepPageSize), oldest first. Each page's release
// keys go to the ledger (JournalKeyReader) and its orders to the order module, so no read, and no key
// list, grows with the number of deposits waiting on stock; the sweep stops as soon as it has emitted
// the limit.
func (w *InvoiceWriter) ReleaseDeliveredDeposits(ctx context.Context, limit int) (DepositSweepResult, error) {
	if w.pool == nil {
		return DepositSweepResult{}, errNoDB
	}
	if limit <= 0 {
		limit = creditMemoRepostLimit
	}
	if w.delivered == nil || w.journals == nil {
		return DepositSweepResult{Skipped: true}, nil
	}
	var res DepositSweepResult
	var afterAt time.Time
	afterID := "00000000-0000-0000-0000-000000000000"
	for {
		page, err := w.findDepositPage(ctx, afterAt, afterID, depositSweepPageSize)
		if err != nil {
			return res, err
		}
		if len(page) == 0 {
			return res, nil
		}
		last := page[len(page)-1]
		afterAt, afterID = last.createdAt, last.invoiceID

		pending, err := w.withoutRelease(ctx, page)
		if err != nil {
			return res, err
		}
		res.Candidates += len(pending)
		if len(pending) > 0 {
			if err := w.emitDelivered(ctx, pending, limit, &res); err != nil {
				return res, err
			}
		}
		if res.Released+res.Failed >= limit || len(page) < depositSweepPageSize {
			return res, nil
		}
	}
}

// withoutRelease drops the page's documents whose release journal already exists.
func (w *InvoiceWriter) withoutRelease(ctx context.Context, page []depositAwaitingRelease) ([]depositAwaitingRelease, error) {
	keys := make([]string, 0, len(page))
	for _, p := range page {
		keys = append(keys, depositReleaseKey(p.invoiceID))
	}
	have, err := w.journals.JournalKeysExist(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("billing: release journals: %w", err)
	}
	released := make(map[string]bool, len(have))
	for _, k := range have {
		released[k] = true
	}
	var out []depositAwaitingRelease
	for _, p := range page {
		if !released[depositReleaseKey(p.invoiceID)] {
			out = append(out, p)
		}
	}
	return out, nil
}

// emitDelivered emits for the pending documents whose order recorded a delivery, up to the limit.
func (w *InvoiceWriter) emitDelivered(ctx context.Context, pending []depositAwaitingRelease, limit int, res *DepositSweepResult) error {
	orderIDs := make([]string, 0, len(pending))
	for _, p := range pending {
		orderIDs = append(orderIDs, p.orderID)
	}
	delivered, err := w.delivered.DeliveredOrderIDs(ctx, orderIDs)
	if err != nil {
		return fmt.Errorf("billing: delivered orders: %w", err)
	}
	isDelivered := make(map[string]bool, len(delivered))
	for _, id := range delivered {
		isDelivered[id] = true
	}
	for _, p := range pending {
		if !isDelivered[p.orderID] {
			continue
		}
		if res.Released+res.Failed >= limit {
			return nil
		}
		if err := w.emitDepositReleased(ctx, p.invoiceID, p.orderID); err != nil {
			res.Failed++
			if res.FirstError == "" {
				res.FirstError = fmt.Sprintf("%s: %v", p.invoiceID, err)
			}
			continue
		}
		res.Released++
	}
	return nil
}

// depositReleaseKey is the release journal's derived idempotency key, the gl builder's own
// (gl/posting.go): the anti-join the sweep rests on.
func depositReleaseKey(invoiceID string) string { return "deposit.released:" + invoiceID }
