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
	rows, err := w.pool.Query(ctx, `
		SELECT id::text FROM invoices
		 WHERE order_id = $1::uuid AND doc_type = 'INVOICE'
		   AND source_kind = 'SUPPLIER_DEPOSIT' AND status NOT IN ('DRAFT','VOID')
		 ORDER BY created_at, id`, orderID)
	if err != nil {
		return fmt.Errorf("billing: deposit documents for %s: %w", orderID, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("billing: scan deposit document: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("billing: deposit documents for %s: %w", orderID, err)
	}
	return releaseEach(ctx, ids, func(ctx context.Context, id string) error {
		return w.emitDepositReleased(ctx, id, orderID)
	})
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
func (w *InvoiceWriter) ReleaseDeliveredDeposits(ctx context.Context, limit int) (DepositSweepResult, error) {
	if w.pool == nil {
		return DepositSweepResult{}, errNoDB
	}
	if limit <= 0 {
		limit = creditMemoRepostLimit
	}
	if w.delivered == nil {
		return DepositSweepResult{Skipped: true}, nil
	}
	pending, err := w.findDepositsWithoutRelease(ctx)
	if err != nil {
		return DepositSweepResult{}, err
	}
	res := DepositSweepResult{Candidates: len(pending)}
	if len(pending) == 0 {
		return res, nil
	}
	orderIDs := make([]string, 0, len(pending))
	for _, p := range pending {
		orderIDs = append(orderIDs, p.orderID)
	}
	delivered, err := w.delivered.DeliveredOrderIDs(ctx, orderIDs)
	if err != nil {
		return res, fmt.Errorf("billing: delivered orders: %w", err)
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
			break
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
	return res, nil
}
