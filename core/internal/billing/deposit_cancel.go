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
//     one ADJUSTMENT row off this settlement. The closed part lands on the
//     ledger as its own row (see THE ACCOUNT ROWS below), so the running
//     balance and the statement follow the clearing.
//
//   KEPT (a manager's override, or the dealer's own default; row 1228,
//   Colton's A: keep only what was paid): the dealer keeps what the customer
//   PAID of the deposit, and nothing more is owed. The unpaid rest is closed
//   by the same clearing the credit back uses (the balance goes to zero in
//   this transaction, with the one deposit.cancelled:<invoice id> journal key
//   booking it): a part paid document reads PAID, an entirely unpaid one VOID,
//   and a fully paid one stands PAID. The closed part's ledger row lands as
//   for the credit back; no credit row does (the paid part is the dealer's,
//   not the account's). The booking splits
//   the one entry: DR 2300 for the whole deposit, CR other income for the paid
//   part, CR the receivable for the closed rest.
//
// WHY NO CREDIT MEMO. The deposit document never routes through the credit
// memo path (SupplierDepositNotCreditable stands): a memo would reverse
// revenue that was never booked. This settlement is the deposit's own exit.
//
// THE ACCOUNT ROWS. The running balance every credit gate reads is the LEDGER's
// latest balance_after (the POS sale gate, the quote conversion gate, the
// account picker), and the statement is the ledger's rows summed, so a balance
// that clears only on the document leaves both reading money that is not owed.
// The settlement therefore writes a row for the part it closes, negative,
// tagged source_type 'invoice': that tag is LOAD BEARING exactly as the credit
// memo's insert explains (credit_memo.go), because the cleared document has
// already left the open invoice arm of the AR tie out and of v_ar_aging, and a
// second count of the same close would break the 1100 tie out by the closed
// amount. The paid part's credit back keeps its own row (source_type
// 'deposit_cancel', inside the tie out's second arm, the credit the customer
// is owed).
//
// ONE CLOCK, AND NO TIE. The closed row stamps the SETTLEMENT'S OWN INSTANT: the
// clock_timestamp() this settlement reads inside the cancel's own transaction,
// the same instant the GL event carries as occurred_at (FW1-01b round 2b,
// FROM-PRINCIPAL 121). The journal the ledger posts for the cancel and the
// settlement's AR row are then ONE clock by construction, so the ledger's date
// and the customer's statement are the same date and can never land in
// different months. The credit back row stamps ITS OWN clock_timestamp(), a
// moment LATER, so the two rows never tie: the 0009 ordering rule, now() is
// transaction fixed and two rows written with it would tie, and the gate that
// reads the customer's balance (ORDER BY occurred_at DESC LIMIT 1, no
// tiebreaker) would then pick arbitrarily and could read the closed row's
// balance and miss the credit back.
//
// ONE READ PER DOCUMENT, NOT ONE PER CANCEL. The same gate reads the LATEST
// row, so the rows must land in write order. One instant for the whole cancel
// would give every document's closed row the same stamp, and a credit back row
// written before a later document's closed row would then sort ABOVE it, so
// the gate could read a balance that is not the final one.
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
	"sort"
	"time"

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
		// Every document's row is taken BEFORE the first balance move draws
		// the invoices counter (the one money lock order): the clearing below
		// stamps the feed seq, so a walk that locked its second document only
		// after the first's counter draw would hold the counter while waiting
		// on a row another invoice writer already held. Ascending id, the one
		// order the apply's multi invoice walk takes. The WALK is the cancel's
		// own: the shared reader hands the documents back oldest first (the
		// order the POS return's account rail offsets them in), so the cancel
		// sorts the ids itself and walks the sorted list, keeping the apply's
		// lock order without bending the reader to it.
		sorted := append([]string(nil), ids...)
		sort.Strings(sorted)
		for _, id := range sorted {
			if err := lockDepositDocumentRow(ctx, q, id); err != nil {
				return err
			}
		}
		for _, id := range sorted {
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
//
// The re-read takes the row FOR NO KEY UPDATE, the lock the payments path
// takes on the invoices it allocates against (lockAndTotal). A payment racing
// the cancel either commits first, and the settlement then reads its balance
// and credits the paid part, or waits behind the settlement and is refused on
// the voided document. Without the lock the overwrite below swallowed a
// payment that committed between the read and the write. The lock order is
// unchanged: the caller's order row first, then this invoice row.
//
// The settlement's OWN INSTANT is read HERE, from the DATABASE's clock, inside
// the cancel's transaction (FW1-01b round 2b, FROM-PRINCIPAL 121): the writer's
// own Go clock is a DIFFERENT clock, and a cancel that straddles a month
// boundary would put the ledger's journal in one month and the AR rows in
// another, so the statement and the books would disagree about the same act.
// Reading the database's clock here makes the journal's date and the
// settlement's AR row one clock BY CONSTRUCTION.
func (w *InvoiceWriter) settleDepositDocument(ctx context.Context, tx pgx.Tx, orderID, invoiceID, disposition, reason string) error {
	var d depositCancelDoc
	err := tx.QueryRow(ctx, `
		SELECT id::text, invoice_no::text, customer_id::text, branch_id::text, total_cents, balance_cents
		  FROM invoices
		 WHERE id = $1::uuid AND doc_type = 'INVOICE' AND source_kind = 'SUPPLIER_DEPOSIT'
		   AND status NOT IN ('DRAFT','VOID')
		   FOR NO KEY UPDATE`,
		invoiceID).Scan(&d.id, &d.no, &d.customer, &d.branch, &d.total, &d.balance)
	if err == pgx.ErrNoRows {
		return nil // no longer live, a raced void or a second cancel: nothing to settle
	}
	if err != nil {
		return fmt.Errorf("read the deposit document: %w", err)
	}

	paid, closed := d.total-d.balance, d.balance

	// The cancel's own instant, from the database's clock, in the cancel's own transaction.
	var settleAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&settleAt); err != nil {
		return fmt.Errorf("read the settlement's own instant: %w", err)
	}

	// The open balance clears for both dispositions (credit back: the credit
	// exactly clears the open document; kept: the unpaid rest is closed so
	// nothing more is owed on the cancelled order). This is the settlement's
	// own close, never a credit memo (SupplierDepositNotCreditable stands) and
	// never a hand edit outside this transaction. The status follows what is
	// left of the document: an unpaid one leaves the live set (VOID, exactly
	// what every live-deposit read already means by the word); a part paid
	// one under KEPT reads PAID, the paid part being all there is; a paid
	// document (balance 0) is untouched and keeps its PAID shape.
	if d.balance > 0 {
		status := "VOID"
		if disposition == DepositCancelKept && paid > 0 {
			status = "PAID"
		}
		// The clearing draws the invoices feed seq beside the balance move
		// (clearDepositDocument, the writer's own statement for it, the 0213
		// convention): the invoices sync scope reads i.seq, so a deposit
		// leaving the till that no pull can see is a feed that lies by
		// omission, exactly the settle's own words.
		if err := w.clearDepositDocument(ctx, tx, d.id, status); err != nil {
			return err
		}
	}

	// The account rows (see THE ACCOUNT ROWS in the file header), both chained
	// on the account's AR read AFTER the clearing update, the figure the till's
	// NSF reversal anchors on: open invoice balances plus the standing
	// non invoice movements. The closed row lands first so the credit back,
	// when there is one, is the later row the gates read.
	writeClosed, writeCredit := closed > 0, disposition == DepositCancelCreditBack && paid > 0
	var arAfter int64
	if writeClosed || writeCredit {
		if err := tx.QueryRow(ctx, `
			SELECT
			  (SELECT COALESCE(SUM(balance_cents - LEAST(GREATEST(holdback_cents, 0), GREATEST(balance_cents, 0))), 0) FROM invoices
			    WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents <> 0)
			+ (SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
			    WHERE customer_id = $1::uuid AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND source_type <> 'invoice')`,
			d.customer).Scan(&arAfter); err != nil {
			return fmt.Errorf("read the account's AR after the clearing: %w", err)
		}
	}
	if writeClosed {
		// The closed row stamps the settlement's OWN instant, the one the GL event carries:
		// the journal's date and this row are the same instant, read from the same clock.
		if _, err := tx.Exec(ctx, `
			INSERT INTO customer_transactions
				(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
			VALUES ($1::uuid, 'ADJUSTMENT', $2, $3, 'invoice', $4::uuid, $5::uuid, $6, $7)`,
			d.customer, -closed, arAfter, d.id, d.branch,
			"Supplier deposit "+d.no+" closed on the order's cancel", settleAt); err != nil {
			return fmt.Errorf("write the closed part's ledger row: %w", err)
		}
	}
	if writeCredit {
		// The paid part (total less what was still owed) becomes the account's
		// credit. source_type 'deposit_cancel' keeps the row out of the credit
		// memo document family and inside the AR tie-out's second arm.
		//
		// This row stamps ITS OWN clock_timestamp(), a moment AFTER the closed row's
		// instant: the two must never tie, because the gate that reads the customer's
		// balance takes the LATEST row with no tiebreaker, and a tie would let it read
		// the closed row's balance and miss the credit back this row carries.
		if _, err := tx.Exec(ctx, `
			INSERT INTO customer_transactions
				(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
			VALUES ($1::uuid, 'ADJUSTMENT', $2, $3, 'deposit_cancel', $4::uuid, $5::uuid, $6, clock_timestamp())`,
			d.customer, -paid, arAfter-paid, d.id, d.branch,
			"Supplier deposit "+d.no+" credited back on the order's cancel"); err != nil {
			return fmt.Errorf("write the account credit: %w", err)
		}
	}
	// KEPT writes no credit row: the paid part's move into other income and
	// the closed rest's clearing are the booking's, emitted beside this
	// settlement and posted by the gl consumer.

	// sub item 3's booking: the GL event, one per settled document, on the
	// same transaction, so the journal can never lag the settlement it
	// belongs to. The event carries the settlement's OWN instant (occurred_at),
	// read from the database's clock inside this transaction, so the ledger dates the
	// cancel by the cancel and never by the deposit's finalization, which is an
	// earlier document (FW1-01b round 2, S3's P1), and the journal's date and the
	// settlement's AR row are one clock by construction (FW1-01b round 2b,
	// FROM-PRINCIPAL 121).
	if err := emitOutbox(ctx, tx, DepositCancelledTopic, map[string]any{
		"invoice_id": d.id, "order_id": orderID, "disposition": disposition,
		"paid_cents": paid, "closed_cents": closed, "occurred_at": settleAt,
	}); err != nil {
		return err
	}

	// The audited record of the settlement: who cancelled, which disposition,
	// the reason. Best-effort beside the money, exactly like every other
	// Recorder call on this writer's module.
	w.recordDepositCancel(ctx, tx, d, disposition, reason, paid, closed)
	return nil
}

// lockDepositDocumentRow takes one live deposit document's row FOR NO KEY
// UPDATE, the lock the payments path takes on the invoices it allocates
// against, ahead of the settlement's own re-read (which takes the same lock
// again on a row this transaction already holds, a no-op). Called over the
// order's documents in ascending id order before any balance move, so the
// counter a clearing draws is never held while another of the order's rows is
// still unlocked.
func lockDepositDocumentRow(ctx context.Context, q pgx.Tx, invoiceID string) error {
	var id string
	err := q.QueryRow(ctx, `
		SELECT id::text FROM invoices
		 WHERE id = $1::uuid AND doc_type = 'INVOICE' AND source_kind = 'SUPPLIER_DEPOSIT'
		   AND status NOT IN ('DRAFT','VOID')
		   FOR NO KEY UPDATE`, invoiceID).Scan(&id)
	if err == pgx.ErrNoRows {
		return nil // no longer live, a raced void or a second cancel
	}
	if err != nil {
		return fmt.Errorf("lock the deposit document: %w", err)
	}
	return nil
}

// recordDepositCancel writes the settlement's audit row on the caller's
// transaction when the recorder is transactional (the real *audit.Service
// is), so the who/what/why commits or rolls back with the money it names.
func (w *InvoiceWriter) recordDepositCancel(ctx context.Context, tx pgx.Tx, d depositCancelDoc, disposition, reason string, paid, closed int64) {
	if w.audit == nil {
		return
	}
	// What the customer paid is kept by the dealer under KEPT and credited to
	// the account under CREDIT_BACK; the closed amount is the unpaid rest the
	// settlement cleared. Both ride the audited row beside the reason.
	paidKey := "paid_kept_cents"
	if disposition == DepositCancelCreditBack {
		paidKey = "paid_credited_cents"
	}
	row := audit.SensitiveAccess{
		Action:     ActDepositCancelled,
		EntityType: "invoice",
		EntityID:   d.id,
		BranchID:   d.branch,
		Reason:     reason,
		Extra: map[string]any{
			"disposition":  disposition,
			"invoice_no":   d.no,
			"amount_cents": d.total,
			paidKey:        paid,
			"closed_cents": closed,
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
