package billing

// deposit_cancel.go, the settlement half of a cancelled special order's
// supplier deposit (decision log row 1193): what the order module's manual
// cancel does with the order's live deposit documents.
//
// THE TWO DISPOSITIONS, AND WHAT EACH OWES:
//
//   CREDIT_BACK (the dealer default): the deposit is credited back to the
//   customer's account. The document's open balance clears and the document
//     leaves the live set (VOID: it is no longer money attached to anything,
//     exactly what every live-deposit read already means by the word), and
//     whatever the customer had PAID of it lands on the account as a credit,
//     one ADJUSTMENT row off this settlement. An unpaid deposit needs no
//     account row: the credit exactly clears the open document, so the
//     subledger and the ledger both read zero with no second row to carry it.
//
//   KEPT (a manager's override, or the dealer's own default): the dealer
//     keeps the deposit. The document stands exactly as it is (open and owed
//     when unpaid, PAID when paid) and nothing lands on the account: the
//     liability's move into other income is the BOOKING's business (the GL
//     pair this file emits, posted by the gl consumer), never the
//     subledger's.
//
// WHY NO CREDIT MEMO. The deposit document never routes through the credit
// memo path (SupplierDepositNotCreditable stands): a memo would reverse
// revenue that was never booked. This settlement is the deposit's own exit.
//
// NEVER ROUTED THROUGH THE RETURNS FLOW. The settlement rides the cancel's
// own transaction when the caller hands one over (the order cancel runs it
// inside the order-row lock, the same transaction the status flip commits
// in): a cancel whose settlement fails never flips the order, so the money
// can never be orphaned on a CANCELLED order the way audit H2 found it. A
// caller with no transaction (nil tx) gets one transaction for the whole
// order, all documents or none.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// The disposition vocabulary, the frozen words the pos_policy column's CHECK
// and the GL builder share (migration 0375). Stated here so the settlement
// owns its own refusal rather than trusting the caller's spelling.
const (
	DepositCancelCreditBack = "CREDIT_BACK"
	DepositCancelKept       = "KEPT"
)

// DepositCancelledTopic is the outbox topic a cancelled deposit document
// emits once per settlement; the GL posting consumer owns it (deposit_cancel
// in gl). Its journal key is deposit.cancelled:<invoice id>, the release's
// own deposit.released:<invoice id> pattern, so a replayed event posts
// nothing.
const DepositCancelledTopic = "deposit.cancelled"

// ActDepositCancelled is the audit action of one settled deposit document:
// which disposition, who cancelled, and the reason they gave. Declared here,
// beside the settlement that writes the rows.
const ActDepositCancelled = audit.ActDepositCancelled

// CancelOrderDeposits settles the order's live SUPPLIER_DEPOSIT documents for
// a cancellation under the named disposition, and answers how many documents
// it settled. A tx of nil runs the whole settlement in its own transaction;
// the order cancel passes its own so the settlement commits with the status
// flip. Documents that are no longer live (a second cancel, a raced void)
// settle nothing, so the settlement is idempotent at the document level
// before the ledger's journal key is ever asked.
func (w *InvoiceWriter) CancelOrderDeposits(ctx context.Context, tx pgx.Tx, orderID, disposition, reason string) (int, error) {
	switch disposition {
	case DepositCancelCreditBack, DepositCancelKept:
	case "":
		return 0, fmt.Errorf("billing: a deposit cancel needs a disposition")
	default:
		return 0, fmt.Errorf("billing: unknown deposit cancel disposition %q", disposition)
	}
	if w.pool == nil {
		return 0, errNoDB
	}
	settled := 0
	settle := func(ctx context.Context, q pgx.Tx) error {
		ids, err := depositDocumentIDs(ctx, q, orderID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := w.settleDepositDocument(ctx, q, orderID, id, disposition, reason); err != nil {
				return fmt.Errorf("deposit document %s: %w", id, err)
			}
			settled++
		}
		return nil
	}
	if tx != nil {
		return settled, settle(ctx, tx)
	}
	if err := w.inTx(ctx, func(q pgx.Tx) error { return settle(ctx, q) }); err != nil {
		return 0, err
	}
	return settled, nil
}

// depositCancelDoc is one live deposit document's money facts.
type depositCancelDoc struct {
	id       string
	no       string
	customer string
	branch   string
	total    int64
	balance  int64
}

// settleDepositDocument settles ONE live deposit document under the
// disposition. It re-reads the document inside the caller's transaction so a
// document that stopped being live under the cancel's lock settles nothing.
func (w *InvoiceWriter) settleDepositDocument(ctx context.Context, tx pgx.Tx, orderID, invoiceID, disposition, reason string) error {
	var d depositCancelDoc
	err := tx.QueryRow(ctx, `
		SELECT id::text, invoice_no::text, customer_id::text, branch_id::text, total_cents, balance_cents
		  FROM invoices
		 WHERE id = $1::uuid AND doc_type = 'INVOICE' AND source_kind = 'SUPPLIER_DEPOSIT'
		   AND status NOT IN ('DRAFT','VOID')`,
		invoiceID).Scan(&d.id, &d.no, &d.customer, &d.branch, &d.total, &d.balance)
	if err == pgx.ErrNoRows {
		return nil // no longer live, a raced void or a second cancel: nothing to settle
	}
	if err != nil {
		return fmt.Errorf("read the deposit document: %w", err)
	}

	if disposition == DepositCancelCreditBack {
		// The open balance clears and the document leaves the live set. A paid
		// document (balance 0) keeps its PAID shape: it was paid, voiding it
		// would erase a real payment's document.
		if d.balance > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE invoices SET balance_cents = 0, status = 'VOID', updated_at = now() WHERE id = $1::uuid`,
				d.id); err != nil {
				return fmt.Errorf("clear the deposit document: %w", err)
			}
		}
		// The paid part (total less what is still owed) becomes the account's
		// credit. An unpaid deposit paid nothing, so no row: the cleared
		// document is itself the credit (the subledger and the ledger both
		// read zero). source_type 'deposit_cancel' keeps the row out of the
		// credit memo document family and inside the AR tie-out's second arm.
		if paid := d.total - d.balance; paid > 0 {
			var open int64
			if err := tx.QueryRow(ctx,
				`SELECT COALESCE(SUM(balance_cents),0) FROM invoices
				  WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
				d.customer).Scan(&open); err != nil {
				return fmt.Errorf("read open balance: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO customer_transactions
					(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
				VALUES ($1::uuid, 'ADJUSTMENT', $2, $3, 'deposit_cancel', $4::uuid, $5::uuid, $6)`,
				d.customer, -paid, open-paid, d.id, d.branch,
				"Supplier deposit "+d.no+" credited back on the order's cancel"); err != nil {
				return fmt.Errorf("write the account credit: %w", err)
			}
		}
	}
	// KEPT settles neither the document nor the subledger: the liability's
	// move into other income is the booking's, emitted beside this settlement
	// and posted by the gl consumer.

	// sub item 3's booking: the GL event, one per settled document, on the
	// same transaction, so the journal can never lag the settlement it
	// belongs to.
	if err := emitOutbox(ctx, tx, DepositCancelledTopic, map[string]any{
		"invoice_id": d.id, "order_id": orderID, "disposition": disposition,
	}); err != nil {
		return err
	}

	// The audited record of the settlement: who cancelled, which disposition,
	// the reason. Best-effort beside the money, exactly like every other
	// Recorder call on this writer's module.
	w.recordDepositCancel(ctx, tx, d, disposition, reason)
	return nil
}

// recordDepositCancel writes the settlement's audit row on the caller's
// transaction when the recorder is transactional (the real *audit.Service
// is), so the who/what/why commits or rolls back with the money it names.
func (w *InvoiceWriter) recordDepositCancel(ctx context.Context, tx pgx.Tx, d depositCancelDoc, disposition, reason string) {
	if w.audit == nil {
		return
	}
	row := audit.SensitiveAccess{
		Action:     ActDepositCancelled,
		EntityType: "invoice",
		EntityID:   d.id,
		BranchID:   d.branch,
		Reason:     reason,
		Extra: map[string]any{
			"disposition": disposition,
			"invoice_no":  d.no,
			"amount_cents": d.total,
		},
	}
	if rx, ok := w.audit.(interface {
		RecordTx(context.Context, audit.Execer, audit.SensitiveAccess) error
	}); ok && tx != nil {
		_ = rx.RecordTx(ctx, tx, row)
		return
	}
	_ = w.audit.Record(ctx, row)
}
