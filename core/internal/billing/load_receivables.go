package billing

// load_receivables.go, HH-LOAD-1 LD-11 (CP0-6 answer B): the import
// writer for the open receivables door, billing's own write path for
// the load frame. Each open item of the dealer's AR extract lands as an
// imported document: source_kind IMPORT (the 0146 check admits it),
// doc_type INVOICE or CREDIT_MEMO by the carried sign (unapplied cash
// is a negative item and lands as a memo, the sign discipline 0146
// enforces in the schema), the source document number beside HH's own
// minted number (the 0322 column), the issue and due dates as recorded,
// and the open amount in every money column with tax at zero: the
// composition is not carried, the amount is (CP0-7 answer A, no
// re-resolution, no recomputation). One customer_transactions row per
// item (source_type 'invoice', the tag ARBalanceCents counts by and the
// one a non-invoice tag would double), its occurred_at the recorded
// issue date, because the door writes each customer's items oldest
// first and the running balance must read in date order.
//
// THE IMPORT PATH EMITS NO EVENT, AND THAT IS THE WHOLE GL STORY. The
// invoice writer's G3 emits ar.invoice.finalized on the transactional
// outbox, and that event is what drives the GL posting
// (gl/posting.go's consume), the order's INVOICED advance
// (order/consumer.go) and everything downstream. The opening ledger
// comes from the external package's trial balance, not from HH's own
// posting path, so the import emits none: no outbox row, no journal, no
// customer message, no reminder, no credit sweep. The proof is row
// counts beside this writer's statements: it touches invoices and
// customer_transactions and nothing else.
//
// THE WRITER CONTRACT (the LD-1 review's bindings): this writer opens
// no connection of its own and holds no pool; every statement runs on
// the transaction the load frame hands it, and it never commits or
// rolls that transaction back. The frame owns the commit decision: a
// refused item is the caller's savepoint to discard, and the batch's
// all-or-nothing rollback is the frame's. Errors are wrapped so a
// Postgres deadlock stays recognisable in the chain (SQLSTATE 40P01
// reaches the frame's isDeadlock and answers ErrBatchContended at the
// batch boundary).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// The import record's field names, the load door's contract vocabulary.
const (
	loadFieldDocNo   = "source_doc_no"
	loadFieldIssue   = "issue_date"
	loadFieldDue     = "due_date"
	loadFieldAmount  = "open_amount_cents"
	loadFieldJobKey  = "source_job_key"
	loadFieldAddrKey = "source_address_key"
)

// loadDateLayout is the one date shape the import accepts.
const loadDateLayout = "2006-01-02"

// ReceivablesLoadWriter is billing's writer for the open receivables
// door: the load.Writer seam over this module's invoices and AR
// subledger rows. It holds no pool and no clock: the frame's
// transaction is the only database thing it touches, and every date is
// the source's own.
type ReceivablesLoadWriter struct{}

// NewReceivablesLoadWriter builds the writer. There is nothing to bind:
// the collaborators are the frame's (the transaction) and the record's
// (the dates), which is the point: an import has no server-side facts
// to resolve.
func NewReceivablesLoadWriter() *ReceivablesLoadWriter { return &ReceivablesLoadWriter{} }

// importRecord is one open item's carried facts, parsed once.
type importRecord struct {
	docNo      string
	issueDate  time.Time
	dueDate    time.Time
	amount     int64
	jobKey     string
	addressKey string
}

// parseImportRecord reads the carried facts off the load frame's
// decoded record, refusing every shape money cannot land as recorded.
// The door's boundary has already refused these spellings once; the
// writer repeats the rules because the tables it owns deserve their own
// guard, and the refusals name the field, never a value.
func parseImportRecord(in load.Write) (importRecord, *load.Refusal) {
	rec := in.Record
	if rec == nil {
		return importRecord{}, &load.Refusal{Code: load.ReasonRecordShape, Field: "record",
			Detail: "the item carries no record"}
	}
	var out importRecord
	var ok bool
	if out.docNo, ok = rec[loadFieldDocNo].(string); !ok || out.docNo == "" || len(out.docNo) > 128 {
		return importRecord{}, &load.Refusal{Code: load.ReasonRecordShape, Field: loadFieldDocNo,
			Detail: "the source document number is missing or over its bound"}
	}
	for _, f := range []struct {
		field string
		dest  *time.Time
	}{
		{loadFieldIssue, &out.issueDate},
		{loadFieldDue, &out.dueDate},
	} {
		v, isStr := rec[f.field].(string)
		if !isStr {
			return importRecord{}, &load.Refusal{Code: load.ReasonRecordShape, Field: f.field,
				Detail: "the date is missing"}
		}
		tm, err := time.Parse(loadDateLayout, v)
		if err != nil {
			return importRecord{}, &load.Refusal{Code: load.ReasonRecordShape, Field: f.field,
				Detail: "the date is not a strict calendar day"}
		}
		*f.dest = tm
	}
	amount, err := loadMoney(rec[loadFieldAmount])
	if err != nil {
		return importRecord{}, err
	}
	out.amount = amount
	if v, ok := rec[loadFieldJobKey]; ok {
		if s, _ := v.(string); len(s) > 512 {
			return importRecord{}, &load.Refusal{Code: load.ReasonRecordShape, Field: loadFieldJobKey,
				Detail: "the carried job key is over its bound"}
		} else if s != "" {
			out.jobKey = s
		}
	}
	if v, ok := rec[loadFieldAddrKey]; ok {
		if s, _ := v.(string); len(s) > 512 {
			return importRecord{}, &load.Refusal{Code: load.ReasonRecordShape, Field: loadFieldAddrKey,
				Detail: "the carried address key is over its bound"}
		} else if s != "" {
			out.addressKey = s
		}
	}
	return out, nil
}

// loadMoney is the money guard: whole cents, exact, never a float. The
// wire's literal (json.Number) with no fraction and no exponent, or an
// integer an in-process caller handed over; a float64 always refuses,
// whatever it holds (AGENTS.md).
func loadMoney(v any) (int64, *load.Refusal) {
	refuse := func() (int64, *load.Refusal) {
		return 0, &load.Refusal{Code: load.ReasonRecordShape, Field: loadFieldAmount,
			Detail: "the open amount is not exact whole cents: money is integer cents, never a float"}
	}
	var cents int64
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return refuse()
		}
		cents = i
	case int:
		cents = int64(n)
	case int64:
		cents = n
	default:
		return refuse()
	}
	if cents == 0 {
		return 0, &load.Refusal{Code: load.ReasonRecordShape, Field: loadFieldAmount,
			Detail: "the open amount is zero: an open item of nothing is not an open item"}
	}
	return cents, nil
}

// refHHID reads one resolved reference's HH row id, refusing when the
// kind is absent or arrived without its resolved row.
func refHHID(refs []load.RefMatch, kind string) (string, *load.Refusal) {
	for _, r := range refs {
		if r.Kind == kind {
			if r.HHID == "" {
				return "", &load.Refusal{Code: load.ReasonRecordShape, Field: "requires",
					Detail: fmt.Sprintf("the %s reference arrived without its resolved row", kind)}
			}
			return r.HHID, nil
		}
	}
	return "", &load.Refusal{Code: load.ReasonRecordShape, Field: "requires",
		Detail: fmt.Sprintf("the item must name one %s by legacy key", kind)}
}

// Apply inserts or updates exactly one imported document and its AR
// subledger row inside the frame's transaction, and returns the
// document's HH id for the registry. The create arm runs when RowID is
// empty, the update arm names the row otherwise.
func (ReceivablesLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	rec, refused := parseImportRecord(in)
	if refused != nil {
		return "", refused
	}
	customerID, refused := refHHID(in.Refs, load.RefKindCustomer)
	if refused != nil {
		return "", refused
	}
	branchID, refused := refHHID(in.Refs, load.RefKindBranch)
	if refused != nil {
		return "", refused
	}
	// The branch belongs to the loading org or the batch refuses: the
	// invoice's org comes from the machine principal, and a branch of
	// another org under it would be a row no scoped read of this org
	// can explain.
	var branchOrg string
	err := tx.QueryRow(ctx, `SELECT org_id::text FROM locations WHERE id = $1::uuid`, branchID).Scan(&branchOrg)
	if err != nil {
		return "", &load.Refusal{Code: load.ReasonMissingReference, Field: load.RefKindBranch,
			Detail: "the named branch resolves to no location row"}
	}
	if branchOrg != in.OrgID {
		return "", &load.Refusal{Code: load.ReasonMissingReference, Field: load.RefKindBranch,
			Detail: "the named branch belongs to another org"}
	}

	// No postdated item: the balance readers of the till, the quote desk
	// and the customer module take the ledger row with the latest date,
	// so a row dated after the load day would sit above every HH sale
	// until its day came and understate the balance. The load day is the
	// day of this transaction, UTC, read in SQL because the writer holds
	// no clock.
	var loadDay time.Time
	if err := tx.QueryRow(ctx, `SELECT (transaction_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&loadDay); err != nil {
		return "", fmt.Errorf("billing: read the load day: %w", err)
	}
	if rec.issueDate.After(loadDay) {
		return "", &load.Refusal{Code: load.ReasonRecordShape, Field: loadFieldIssue,
			Detail: "the issue date is after the load day: an open receivable carries no postdated item"}
	}
	docType := DocInvoice
	txnType := "INVOICE"
	if rec.amount < 0 {
		docType = DocCreditMemo
		txnType = "CREDIT_MEMO"
	}
	metadata := map[string]any{}
	if rec.jobKey != "" {
		metadata["load_source_job_key"] = rec.jobKey
	}
	if rec.addressKey != "" {
		metadata["load_source_address_key"] = rec.addressKey
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("billing: import metadata encode: %w", err)
	}
	// The retry key, the same shape the module's other writers stamp:
	// the load frame's derived key, the name-based uuid over the same
	// four parts the registry names. NULL when the caller named none;
	// the registry name remains the replay authority either way.
	idemKey := any(nil)
	if in.DerivedKey != "" {
		idemKey = "load-receivable:" + in.DerivedKey
	}

	if in.RowID == "" {
		return createImportedDocument(ctx, tx, in, rec, customerID, branchID, docType, txnType, idemKey, metaJSON)
	}
	return updateImportedDocument(ctx, tx, in, rec, customerID, branchID, docType, txnType, idemKey, metaJSON)
}

// createImportedDocument inserts the document and its subledger row,
// one statement pair on the caller's transaction. The seq draw rides
// the INSERT the same CTE way every sync-visible write of this module
// draws it, so the document is visible on the invoices feed from the
// moment it lands.
func createImportedDocument(ctx context.Context, tx pgx.Tx, in load.Write, rec importRecord,
	customerID, branchID, docType, txnType string, idemKey any, metaJSON []byte) (string, error) {

	var docID, docNo string
	err := tx.QueryRow(ctx, invoiceWriterSeqCTE+`
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
                      doc_type, source_kind, idempotency_key, closes_order, source_doc_no,
                      metadata, org_id, seq)
VALUES ($1 || nextval('invoice_no_seq'), $2, NULL, $3, 'FINALIZED', $4, $5,
        $6, 0, $6, $6, $4::date::timestamptz,
        $7, 'IMPORT', $8, FALSE, $9,
        $10::jsonb, $11, (SELECT next_value FROM iseq))
RETURNING id::text, invoice_no::text`,
		invoicePrefix(docType), customerID, branchID, formatDate(rec.issueDate), formatDate(rec.dueDate),
		rec.amount, docType, idemKey, rec.docNo, string(metaJSON), in.OrgID,
	).Scan(&docID, &docNo)
	if err != nil {
		return "", fmt.Errorf("billing: insert imported document: %w", err)
	}
	balance, err := customerOpenBalance(ctx, tx, in.OrgID, customerID)
	if err != nil {
		return "", err
	}
	if err := insertLedgerRow(ctx, tx, customerID, txnType, rec.amount, balance, docID, branchID,
		importMemo(docNo, rec.docNo), rec.issueDate.UTC()); err != nil {
		return "", err
	}
	return docID, nil
}

// updateImportedDocument moves the named document and its ledger row in
// place: a changed open amount (the source restated the item between
// extracts) updates the money columns, the dates, the source number and
// the carried keys, draws one new feed sequence, and the ledger row
// follows with the amount and the customer's current open balance. A
// ledger row that has gone missing is inserted again rather than
// left absent: one ledger row per open item is the invariant the
// tie-out reads.
func updateImportedDocument(ctx context.Context, tx pgx.Tx, in load.Write, rec importRecord,
	customerID, branchID, docType, txnType string, idemKey any, metaJSON []byte) (string, error) {

	// A source item that changed sign is a different document: the
	// document's number prefix and type were minted for the sign it
	// arrived with, so the update refuses instead of re-typing it.
	var currentTotal, currentBalance int64
	var currentStatus, currentCustomer string
	if err := tx.QueryRow(ctx, `SELECT total_cents, balance_cents, status, customer_id::text FROM invoices
		WHERE id = $1::uuid AND org_id = $2::uuid FOR UPDATE`,
		in.RowID, in.OrgID).Scan(&currentTotal, &currentBalance, &currentStatus, &currentCustomer); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", &load.Refusal{Code: load.ReasonUpdateTargetMissing, Field: "legacy_id",
				Detail: "the name's document is gone; the update refuses rather than creating a second row"}
		}
		return "", fmt.Errorf("billing: read imported document: %w", err)
	}
	if currentStatus != "FINALIZED" || currentBalance != currentTotal || currentCustomer != customerID {
		return "", workedInHH()
	}
	if (currentTotal < 0) != (rec.amount < 0) {
		return "", &load.Refusal{Code: load.ReasonSignChanged, Field: loadFieldAmount,
			Detail: "the restated open amount changed sign: a source item that changed sign is a different document"}
	}
	tag, err := tx.Exec(ctx, invoiceWriterSeqCTE+`
UPDATE invoices SET
    issue_date = $3, due_date = $4,
    subtotal_cents = $5, tax_cents = 0, total_cents = $5, balance_cents = $5,
    doc_type = $6, idempotency_key = $7, source_doc_no = $8,
    metadata = $9::jsonb, updated_at = now(),
    seq = (SELECT next_value FROM iseq)
WHERE id = $1::uuid AND org_id = $2::uuid AND customer_id = $10::uuid
  AND status = 'FINALIZED' AND balance_cents = total_cents`,
		in.RowID, in.OrgID, formatDate(rec.issueDate), formatDate(rec.dueDate),
		rec.amount, docType, idemKey, rec.docNo, string(metaJSON), customerID)
	if err != nil {
		return "", fmt.Errorf("billing: update imported document: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", workedInHH()
	}
	balance, err := customerOpenBalance(ctx, tx, in.OrgID, customerID)
	if err != nil {
		return "", err
	}
	var docNo string
	if err := tx.QueryRow(ctx, `SELECT invoice_no::text FROM invoices WHERE id = $1::uuid AND org_id = $2::uuid`, in.RowID, in.OrgID).Scan(&docNo); err != nil {
		return "", fmt.Errorf("billing: read imported document number: %w", err)
	}
	day := rec.issueDate.UTC()
	var ledgerID string
	var oldAmount int64
	var oldCreated, oldAt time.Time
	err = tx.QueryRow(ctx, `
SELECT id::text, amount_cents, created_at, occurred_at FROM customer_transactions
 WHERE source_type = 'invoice' AND source_ref_id = $1::uuid FOR UPDATE`, in.RowID).Scan(&ledgerID, &oldAmount, &oldCreated, &oldAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := insertLedgerRow(ctx, tx, customerID, txnType, rec.amount, balance, in.RowID, branchID,
			importMemo(docNo, rec.docNo), day); err != nil {
			return "", err
		}
		return in.RowID, nil
	}
	if err != nil {
		return "", fmt.Errorf("billing: read imported ledger row: %w", err)
	}
	// The row keeps its place when its issue day did not move; a move to
	// another day takes the next free position on that day.
	newAt := oldAt
	oy, om, od := oldAt.UTC().Date()
	ny, nm, nd := day.Date()
	if oy != ny || om != nm || od != nd {
		newAt, err = ledgerPosition(ctx, tx, customerID, day, ledgerID)
		if err != nil {
			return "", err
		}
	}
	// The running balances after the moved row follow the move: every
	// other row of the customer that sits after the row's old position
	// gives back the old amount, every one that sits after its new
	// position takes the new amount, so a restated item in the middle of
	// the ledger leaves every later running balance true. Position is
	// the ledger's own order: occurred_at, then created_at, then id.
	if _, err := tx.Exec(ctx, `
UPDATE customer_transactions SET balance_after = balance_after
    + CASE WHEN (occurred_at, created_at, id) > ($2::timestamptz, $4::timestamptz, $1::uuid) THEN -$5::bigint ELSE 0 END
    + CASE WHEN (occurred_at, created_at, id) > ($3::timestamptz, $4::timestamptz, $1::uuid) THEN $6::bigint ELSE 0 END
 WHERE customer_id = $7::uuid AND id <> $1::uuid`,
		ledgerID, oldAt, newAt, oldCreated, oldAmount, rec.amount, customerID); err != nil {
		return "", fmt.Errorf("billing: move later running balances: %w", err)
	}
	// The moved row's own running balance is the customer's open total
	// less everything that now sits after it.
	if _, err := tx.Exec(ctx, `
UPDATE customer_transactions SET txn_type = $2, amount_cents = $3, occurred_at = $4, memo = $5,
    balance_after = $6::bigint - COALESCE((SELECT SUM(o.amount_cents) FROM customer_transactions o
        WHERE o.customer_id = $7::uuid AND o.id <> $1::uuid
          AND (o.occurred_at, o.created_at, o.id) > ($4::timestamptz, $8::timestamptz, $1::uuid)), 0)
 WHERE id = $1::uuid`,
		ledgerID, txnType, rec.amount, newAt, importMemo(docNo, rec.docNo), balance, customerID, oldCreated); err != nil {
		return "", fmt.Errorf("billing: update imported ledger row: %w", err)
	}
	return in.RowID, nil
}

// ledgerPosition is the occurred_at a receivables ledger row takes for
// its issue day: the day itself (midnight UTC) when the customer has no
// row on that day yet, otherwise one microsecond after the customer's
// latest row on that day. The recorded day is kept, same day rows get
// distinct values in write order, and a reader that takes the row with
// the latest occurred_at and no tie break (the customer, till and quote
// desk balance reads all do) lands on the last row written, whose
// running balance is the open total. self is the row being moved, kept
// out of the reading, or empty for a new row.
func ledgerPosition(ctx context.Context, tx pgx.Tx, customerID string, day time.Time, self string) (time.Time, error) {
	var at time.Time
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(occurred_at) + interval '1 microsecond', $2::timestamptz)
  FROM customer_transactions
 WHERE customer_id = $1::uuid AND occurred_at >= $2::timestamptz
   AND occurred_at < $2::timestamptz + interval '1 day'
   AND ($3 = '' OR id <> $3::uuid)`, customerID, day, self).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("billing: ledger position: %w", err)
	}
	return at, nil
}

// insertLedgerRow lands one receivables ledger row in its date position
// for the customer: created_at is the statement clock, so same-day rows
// keep the order the door wrote them in (a transaction's now() would tie
// them all), every row of the customer already dated after the new one
// takes the new amount into its running balance, and the new row's own
// running balance is the customer's open total less everything that sits
// after it. A batch that carries older items than an earlier batch
// therefore leaves every running balance true in date order, which is
// what keeps aging reading right across batches.
func insertLedgerRow(ctx context.Context, tx pgx.Tx, customerID, txnType string, amount, open int64,
	docID, branchID, memo string, day time.Time) error {

	at, err := ledgerPosition(ctx, tx, customerID, day, "")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at, created_at)
VALUES ($1::uuid, $2, $3::bigint,
        $4::bigint - COALESCE((SELECT SUM(amount_cents) FROM customer_transactions
                                WHERE customer_id = $1::uuid AND occurred_at > $8::timestamptz), 0),
        'invoice', $5::uuid, $6::uuid, $7, $8::timestamptz, clock_timestamp())`,
		customerID, txnType, amount, open, docID, branchID, memo, at); err != nil {
		return fmt.Errorf("billing: insert imported ledger row: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE customer_transactions SET balance_after = balance_after + $3::bigint
 WHERE customer_id = $1::uuid AND occurred_at > $2::timestamptz`,
		customerID, at, amount); err != nil {
		return fmt.Errorf("billing: move later running balances: %w", err)
	}
	return nil
}

// workedInHH is the refusal for a loaded document HH has worked since:
// it is no longer FINALIZED, its balance moved, or it belongs to another
// customer. The target is not missing, it moved on, so FB Move drops the
// item from its extract; the reload never overwrites it.
func workedInHH() *load.Refusal {
	return &load.Refusal{Code: load.ReasonTargetWorkedInHH, Field: "legacy_id",
		Detail: "the name's document has been worked in HH since it loaded (no longer FINALIZED, its balance moved, or it belongs to another customer); the reload refuses rather than overwrite it, so drop the item from the extract"}
}

// customerOpenBalance reads the customer's open balance the way every
// writer of this module does: the sum over FINALIZED and PARTIALLY_PAID
// documents under this transaction, so the row just written or moved is
// counted and a concurrent sale cannot interleave a stale figure.
func customerOpenBalance(ctx context.Context, tx pgx.Tx, orgID, customerID string) (int64, error) {
	var open int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents),0) FROM invoices
		  WHERE customer_id = $1::uuid AND org_id = $2::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		customerID, orgID).Scan(&open); err != nil {
		return 0, fmt.Errorf("billing: read open balance: %w", err)
	}
	return open, nil
}

// invoicePrefix is HH's minted number prefix for the document type: an
// invoice and a memo number from the one sequence, the shapes the
// module's other writers mint.
func invoicePrefix(docType string) string {
	if docType == DocCreditMemo {
		return "#CM-"
	}
	return "#INV-"
}

// importMemo names both numbers on the ledger row: HH's minted one and
// the source's own, so an AR clerk reading the ledger sees the carried
// document without opening it.
func importMemo(docNo, sourceDocNo string) string {
	if docNo == "" {
		return "imported " + sourceDocNo
	}
	return docNo + ", imported " + sourceDocNo
}

// formatDate renders a parsed record date back onto the wire shape.
func formatDate(t time.Time) string { return t.Format(loadDateLayout) }
