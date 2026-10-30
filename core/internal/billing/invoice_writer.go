package billing

// invoice_writer.go, DIB-422: THE ORDER→INVOICE WRITER OUTSIDE THE POS RING.
//
// -- THE GAP THIS CLOSES ------------------------------------------------------------------------
// Until this file there was exactly one production INSERT INTO invoices in the tree, and it lived
// inside the POS sale ring. Everything else that legitimately needed to bill had no path:
//
//   · a DELIVERED non-POS order (quoted, confirmed, picked, delivered on terms) reached FULFILLED
//     and stopped. The only way to invoice it was to ring it through the counter as if the customer
//     were standing there, which produces a pos_sale row describing an event that did not happen.
//   · a 0126 multi-ship group reached COMPLETE and W5-A's InvoiceReadiness published "ready" with
//     WriterBound=false, the decision with no button, named as a PARK in shipgroup.go's header.
//
// -- TWO PRODUCERS, ONE TABLE, ONE SHAPE. THE GUARANTEES, STATED -------------------------------
// The POS ring's INSERT is NOT touched by this wave. That is the whole safety argument: the ring is
// the most exercised money path in the product and re-routing it through a new writer to "share
// code" would put every counter sale at risk to serve a case the counter does not have. Instead the
// two producers are held to the same SHAPE, and here is exactly what that means and how each half
// is held:
//
//   G1 · SAME COLUMNS, SAME MEANINGS. Both writers set invoice_no from '#INV-' || nextval(
//        invoice_no_seq), customer_id, order_id, branch_id, status, due_date, subtotal/tax/total/
//        balance_cents, finalized_at, created_by. Nothing here writes a column the ring leaves NULL
//        with a different meaning. The three columns 0146 adds all DEFAULT to what a ring row means
//        (doc_type='INVOICE', closes_order=TRUE, idempotency_key=NULL), so the ring's statement is
//        still correct without editing it.
//
//   G2 · SAME TAX. Both go through a TaxAssessor seam whose default is flat 13% rounded half-up ONCE
//        at the header, and both split the header tax over the line totals with
//        moneyutil.AllocateLargestRemainder. Not "the same algorithm re-implemented", the SAME
//        function, from the same package, and in the composition root the SAME *tax.Service behind
//        both seams. TestInvoiceWriter_TaxAndGLParityWithPOSRing is the named test that holds this.
//
//   G3 · SAME GL. Neither writer posts a journal. Both emit `ar.invoice.finalized` onto the
//        transactional outbox carrying {invoice_id, customer_id, total_cents, due_date}, and gl's
//        buildInvoiceFinalized reads the invoice's OWN facts through billing.InvoiceForGL to build
//        DR 1100 / CR 4000 / CR 2200. So GL parity is not a thing this file has to achieve, it is a
//        thing it cannot avoid, because the journal is derived from the row, and the rows are the
//        same shape. Same lines in ⇒ byte-identical journal out.
//
//   G4 · SAME AR SUBLEDGER. Both write one customer_transactions row per money event with
//        balance_after as a true running balance and occurred_at = clock_timestamp() (0009's
//        ordering rule, now() is transaction-fixed and two rows in one tx would tie).
//
//   G5 · DIFFERENT WHERE THEY MUST BE, AND ONLY THERE. The ring takes money at the counter, so it
//        writes payments, allocations and pos_sale. This writer takes NO money: an order billed on
//        terms is FINALIZED with balance = total, and settling it is the payments module's job
//        through the paths that already exist. There is no tender concept in this file at all.
//
// -- IDEMPOTENCY IS A DATABASE FACT, NOT A GO CHECK --------------------------------------------
// Every invoice this writer raises carries an idempotency_key, and 0146 puts a partial UNIQUE index
// on it. A double-clicked "invoice this group" therefore races to ONE row in the database rather
// than to a Go-side "have I seen this?" that two processes can both answer no to. The second caller
// gets the FIRST caller's invoice back, not an error, because from the operator's point of view
// the button did what it said.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
)

// Document kinds (0146 invoices.doc_type CHECK set).
const (
	DocInvoice    = "INVOICE"
	DocCreditMemo = "CREDIT_MEMO"
)

// Producer markers (0146 invoices.source_kind CHECK set). Which writer made this row.
const (
	SourcePOS            = "POS"
	SourceOrder          = "ORDER"
	SourceMultiShip      = "MULTISHIP"
	SourceWarrantyCredit = "WARRANTY_CREDIT"
)

// SourceSupplierDeposit marks the deposit document a POS special order ring
// writes (sub item 9.5): an ordinary INVOICE whose posting is DR AR / CR
// Customer Deposits rather than revenue. The value is the pos module's own,
// spelled here rather than imported because a DB value is not a module
// dependency worth buying.
const SourceSupplierDeposit = "SUPPLIER_DEPOSIT"

// EventSourceCounter marks an invoice raised at the counter for an order that has not left the yard
// (QD-5, InvoiceOrderAtAgreed). It rides on the invoice header's metadata and on the
// ar.invoice.finalized payload as "source". AR and the ledger take the event whole and ignore it; the
// order lifecycle consumer reads it so that an invoice for goods still in the yard does not move the
// order or consume its holds. Every other producer sets no source.
const EventSourceCounter = "COUNTER"

// TaxStdON13 is the default reason code, spelled identically to pos.TaxStdON13 and tax.ReasonStdON13.
// Written out rather than imported: a reason code is a wire/DB value, and billing importing pos to
// borrow a string would be a module dependency bought for nothing.
const TaxStdON13 = "STD_ON_HST_13"

// invoiceWriterSeqCTE is the data-modifying CTE the writer's INSERT
// prepends to draw a fresh invoices feed seq atomically in the same
// statement (scope_sequences, the 0213 convention every sync-visible
// write path uses). Gaps from the reuse arm are allowed by the
// convention.
const invoiceWriterSeqCTE = `WITH iseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('invoices')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// clearDepositDocument closes one deposit document on the caller's
// transaction: the balance to zero under the settlement's status, with a
// fresh invoices feed seq drawn in the same statement (the same allocator
// this file's INSERTs draw, the 0213 convention), so the counter and the
// payer's app see the document leave the till. It lives here, beside the
// writer's other seq drawing statements, and is the deposit cancel
// settlement's own close (deposit_cancel.go), never a credit memo.
func (w *InvoiceWriter) clearDepositDocument(ctx context.Context, tx pgx.Tx, invoiceID, status string) error {
	tag, err := tx.Exec(ctx,
		invoiceWriterSeqCTE+`
		UPDATE invoices SET balance_cents = 0, status = $2, updated_at = now(),
		                    seq = (SELECT next_value FROM iseq)
		 WHERE id = $1::uuid`,
		invoiceID, status)
	if err != nil {
		return fmt.Errorf("clear the deposit document: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("clear the deposit document: the document vanished under the settlement's lock")
	}
	return nil
}

// defaultTermsDays is the fallback payment terms when the caller resolves none (a negative terms).
// It matches the customer module's own default; a wrong due date on an invoice is a collections
// problem, so this is deliberately the SHORT end rather than a generous guess. Terms 0 is never
// "none": it is Cash On Demand, the customer's own answer, and stays 0.
const defaultTermsDays = 30

var (
	// ErrNothingToInvoice, the request resolved to no billable lines. A 400, not a silent empty
	// invoice: a zero-line document is not a bill, it is a mistake with a number on it.
	ErrNothingToInvoice = errors.New("billing: nothing to invoice, no billable lines were resolved")
	// ErrOrderSourceUnwired, the order-facts seam is not bound.
	ErrOrderSourceUnwired = errors.New("billing: order invoice source not bound")
)

// -- consumer-declared seams (adapters land in main.go) -----------------------------------------

// TaxAssessor computes the BINDING tax on an invoice subtotal. Declared HERE, in the consuming
// package, with the SAME shape pos declares, so the composition root binds ONE *tax.Service behind
// both and G2 above is structural rather than aspirational.
//
// Exact signature for the union adapter:
//
//	AssessSale(ctx context.Context, in TaxQuoteInput) (TaxQuoteResult, error)
type TaxAssessor interface {
	AssessSale(ctx context.Context, in TaxQuoteInput) (TaxQuoteResult, error)
}

// TaxQuoteInput mirrors pos.TaxQuoteInput field for field.
type TaxQuoteInput struct {
	BranchID      string
	SubtotalCents int64
	ReasonCode    string
}

// TaxQuoteResult mirrors pos.QuoteResult field for field.
type TaxQuoteResult struct {
	TaxCents int64
	RateBps  int
}

// flatHSTRateBps is the binding fallback rate, bit-identical to pos.flatHSTRateBps.
const flatHSTRateBps = 1300

// FlatHSTAssessor is the shipped default: flat 13%, rounded half-up ONCE at the header. It is
// arithmetically identical to pos.FlatHSTAssessor, and that identity is asserted by
// TestInvoiceWriter_TaxAndGLParityWithPOSRing rather than assumed from the comment.
type FlatHSTAssessor struct{}

// AssessSale returns the flat-13% tax and the 1300bps rate.
func (FlatHSTAssessor) AssessSale(_ context.Context, in TaxQuoteInput) (TaxQuoteResult, error) {
	return TaxQuoteResult{
		TaxCents: (in.SubtotalCents*flatHSTRateBps + 5000) / 10000,
		RateBps:  flatHSTRateBps,
	}, nil
}

// OrderInvoiceSource is the ORDER seam: what is billable on this order right now, and the write-back
// that records which document billed it. Declared here so billing never imports internal/order.
//
// Exact signatures for the union adapter:
//
//	InvoiceableOrder(ctx context.Context, orderID string, groupIDs []string) (OrderInvoiceFacts, error)
//	MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error
type OrderInvoiceSource interface {
	InvoiceableOrder(ctx context.Context, orderID string, groupIDs []string) (OrderInvoiceFacts, error)
	MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error
}

// OrderInvoiceFacts is everything the writer needs about an order, resolved by the order module.
//
// The writer asks for FACTS, not for permission. Whether this order MAY be invoiced (the 0126
// readiness ruling, the caller's authority, the policy freeze) is decided entirely in the order
// module before this seam is called, billing's job is to raise a correct document for a decision
// that has already been taken, and splitting the decision across two modules is how two modules
// end up disagreeing about whether something was billed.
type OrderInvoiceFacts struct {
	OrderID    string
	OrderNo    string
	CustomerID string
	BranchID   string
	// TermsDays is the customer's payment terms, resolved by the caller (the credit fact belongs to
	// the customer module; see Service.AvailableCreditCents for the same ownership rule). 0 is the
	// customer's own answer, Cash On Demand; a NEGATIVE value is the caller's "none resolved" and
	// takes the writer's default.
	TermsDays int
	// TaxReasonCode is the order's tax treatment; "" ⇒ TaxStdON13.
	TaxReasonCode string
	// ClosesOrder is FALSE for a 0126 ON_DEMAND_SUBSET progress invoice, see 0146's column comment.
	ClosesOrder bool
	// GroupIDs are the shipment groups this document bills (empty for a whole-order invoice). They
	// are echoed back by MarkGroupsInvoiced and they key the idempotency.
	GroupIDs []string
	Lines    []OrderInvoiceLine
	// FreightCents is the delivery fee the agreed price carries, written as its own line. Zero for
	// every caller but the counter's agreed invoice.
	FreightCents int64
	// AgreedTaxCents, when set, is the tax the customer was shown, billed as stated. Nil for every
	// caller but the counter's agreed invoice.
	AgreedTaxCents *int64
	// RaisedAtCounter marks the counter's agreed invoice: the header and the finalized event carry
	// EventSourceCounter. False for every other caller.
	RaisedAtCounter bool
}

// OrderInvoiceLine is one billable line, in the shape invoice_lines stores. Quantities are DECIMAL
// strings, never floats: a quantity that has been through a float64 is a quantity somebody will
// argue about.
type OrderInvoiceLine struct {
	LineNo         int
	OrderLineID    string
	ProductID      string
	Description    string
	QtyEntered     string
	UOMEntered     string
	QtyBase        string
	UnitPriceCents int64
	PriceUOM       string
	LineTotalCents int64
}

// -- the writer ---------------------------------------------------------------------------------

// InvoiceDoc is what the writer returns: the document, named.
type InvoiceDoc struct {
	InvoiceID     string `json:"invoice_id"`
	InvoiceNo     string `json:"invoice_no"`
	DocType       string `json:"doc_type"`
	CustomerID    string `json:"customer_id"`
	OrderID       string `json:"order_id,omitempty"`
	BranchID      string `json:"branch_id"`
	Status        string `json:"status"`
	DueDate       string `json:"due_date"`
	SubtotalCents int64  `json:"subtotal_cents"`
	TaxCents      int64  `json:"tax_cents"`
	TotalCents    int64  `json:"total_cents"`
	BalanceCents  int64  `json:"balance_cents"`
	ClosesOrder   bool   `json:"closes_order"`
	// AtCounter reports that the document was raised at the counter (EventSourceCounter), read off the
	// header's metadata. Not part of any answer on the wire.
	AtCounter bool `json:"-"`
	// Reused reports that an existing document with this idempotency key was returned rather than a
	// new one raised. The surface says "already invoiced as #INV-1043" instead of pretending.
	Reused bool `json:"reused"`

	// -- credit-memo only (see credit_memo.go) --
	// ReversesInvoiceID is the document this memo reverses, when there is one.
	ReversesInvoiceID string `json:"reverses_invoice_id,omitempty"`
	// GLJournalID is the reversing entry, when the poster seam is bound and accepted it.
	GLJournalID string `json:"gl_journal_id,omitempty"`
	// GLJournalError is set when the memo exists but its journal did not post. Surfaced rather than
	// swallowed: a document with no journal is a real, chaseable state, and hiding it behind a green
	// tick is how a month-end closes with a hole in it.
	GLJournalError string `json:"gl_journal_error,omitempty"`
}

// InvoiceWriter is the second producer. It owns a pool because it writes a transaction that spans
// invoices + invoice_lines + customer_transactions + events_outbox, and those four rows are one
// atomic fact: an invoice that exists without its subledger row is a balance nobody can explain.
type InvoiceWriter struct {
	pool      *pgxpool.Pool
	tax       TaxAssessor
	source    OrderInvoiceSource
	glPoster  CreditMemoPoster
	delivered DeliveredOrderReader // deposit_release.go; nil = the deposit sweep skips
	journals  JournalKeyReader     // deposit_release.go; nil = the deposit sweep skips
	audit     audit.Recorder       // deposit_cancel.go; nil = the settlement audits nothing
	terms     TermsReader          // terms.go; nil = the caller's own resolved terms
	now       func() time.Time
	zone      houseday.Source // the dealer's business day zone; nil = the Ontario dealer's
}

// WithTerms binds the terms seam: the terms a customer's invoice is dated by, read from the
// customer module's master.
//
// Unbound, the writer dates the document by the terms the caller resolved on the facts, exactly
// as it did before the master existed. Bound, the master is the answer: the document carries the
// customer's own terms, and a customer on a discount term is dated by that term's net days and
// carries the term's discount window.
func (w *InvoiceWriter) WithTerms(r TermsReader) *InvoiceWriter {
	w.terms = r
	return w
}

// WithZone binds the dealer's business day zone source: the issue and due days of every document the
// writer raises are filed in the dealer's own calendar. Nil keeps the Ontario dealer's.
func (w *InvoiceWriter) WithZone(src houseday.Source) *InvoiceWriter {
	w.zone = src
	return w
}

// WithAudit binds the recorder the deposit cancel settlement stamps its rows
// with. Passing nil is a no-op so a composition without audit keeps settling.
func (w *InvoiceWriter) WithAudit(r audit.Recorder) *InvoiceWriter {
	if r != nil {
		w.audit = r
	}
	return w
}

// NewInvoiceWriter builds the writer. The tax assessor defaults to FlatHSTAssessor (the W4/W5
// behaviour); the composition root swaps in the real engine, exactly as pos does.
func NewInvoiceWriter(pool *pgxpool.Pool) *InvoiceWriter {
	return &InvoiceWriter{pool: pool, tax: FlatHSTAssessor{}, now: time.Now}
}

// WithTaxAssessor binds the tax engine. Passing nil is a no-op, so a mis-wired composition root
// falls back to flat 13% rather than to a nil dereference on the first invoice.
func (w *InvoiceWriter) WithTaxAssessor(t TaxAssessor) *InvoiceWriter {
	if t != nil {
		w.tax = t
	}
	return w
}

// WithOrderSource binds the order-facts seam.
func (w *InvoiceWriter) WithOrderSource(s OrderInvoiceSource) *InvoiceWriter {
	w.source = s
	return w
}

// WithCreditMemoPoster binds the reversing-entry seam (credit_memo.go). Unbound, a credit memo is
// still raised, the customer's document is the thing they can see, and IssueCreditMemo simply
// returns no GLJournalID. That is the same honest-partial ruling warranty makes about FALLBACK_GL,
// pointed the other way.
func (w *InvoiceWriter) WithCreditMemoPoster(p CreditMemoPoster) *InvoiceWriter {
	w.glPoster = p
	return w
}

// WithClock overrides the clock (tests).
//
// This clock is NOT the clock the deposit cancel reads: that one reads the DATABASE's clock inside
// its own transaction, because the journal it books and the AR rows it writes must be ONE clock and
// the writer's Go clock is a different clock (FW1-01b round 2b, FROM-PRINCIPAL 121). The nil-tolerant
// reader that once sat beside this (nowOrClock) went with that read: it had no other caller, and a
// path that wants nil tolerance again can ask for it.
func (w *InvoiceWriter) WithClock(now func() time.Time) *InvoiceWriter {
	if now != nil {
		w.now = now
	}
	return w
}

func (w *InvoiceWriter) assessor() TaxAssessor {
	if w.tax == nil {
		return FlatHSTAssessor{}
	}
	return w.tax
}

// InvoiceOrderGroups is the MultiShipInvoicer implementation: bill the named shipment groups of an
// order. It is the whole of item 1's second half, order.Service.InvoiceShipments has decided the
// groups may be billed, and this raises the document and writes the link back.
//
// policy is carried through to the idempotency key and to closes_order, because ON_ALL_COMPLETE and
// ON_DEMAND_SUBSET produce genuinely different documents from the same group set: the first ends the
// order's billing, the second does not.
func (w *InvoiceWriter) InvoiceOrderGroups(ctx context.Context, orderID string, groupIDs []string, policy string) (InvoiceDoc, error) {
	if w.source == nil {
		return InvoiceDoc{}, ErrOrderSourceUnwired
	}
	// An order already billed whole at its agreed price BY THE COUNTER (QD-5, raised under the whole
	// order key and marked as the counter's) is not billed again under a shipment plan's group key:
	// the plan's groups are covered by that document, so they are linked to it and nothing new is
	// written. Any other whole order document (the fulfilment path's, the sync door's) may bill only
	// part of the order, so it covers nothing here and the groups are billed as they always were.
	if w.pool != nil {
		if whole, ok, err := w.findByKey(ctx, wholeOrderKey(orderID)); err != nil {
			return InvoiceDoc{}, err
		} else if ok && whole.AtCounter {
			whole.Reused = true
			if len(groupIDs) > 0 {
				if err := w.source.MarkGroupsInvoiced(ctx, groupIDs, whole.InvoiceID, w.now()); err != nil {
					return whole, fmt.Errorf("billing: order already invoiced as %s but the shipment groups could not be marked: %w",
						whole.InvoiceNo, err)
				}
			}
			return whole, nil
		}
	}
	facts, err := w.source.InvoiceableOrder(ctx, orderID, groupIDs)
	if err != nil {
		return InvoiceDoc{}, err
	}
	if len(facts.GroupIDs) == 0 {
		facts.GroupIDs = groupIDs
	}
	doc, err := w.write(ctx, facts, SourceMultiShip, multiShipKey(orderID, facts.GroupIDs), "")
	if err != nil {
		// An overlap refusal first heals the standing document and THEN refuses. The overlap was
		// found against a document whose groups may never have been linked to it: the marking is a
		// separate transaction (see the write-back below), and a fault after its document committed
		// leaves the plan reading those groups as unbilled, so the plan the caller reads again keeps
		// offering one of them and every later request is refused the same way: 400 for good. The
		// marking here is the write-back's own, idempotent (a group already linked is left alone), so
		// the refusal's advice, read the plan again and invoice what is left, names a plan that can
		// actually be followed.
		var overlap *groupOverlapError
		if errors.As(err, &overlap) {
			if standing, ok, ferr := w.findByKey(ctx, overlap.key); ferr != nil {
				return InvoiceDoc{}, ferr
			} else if ok && len(overlap.groups) > 0 {
				if mErr := w.source.MarkGroupsInvoiced(ctx, overlap.groups, standing.InvoiceID, w.now()); mErr != nil {
					return InvoiceDoc{}, fmt.Errorf("billing: invoice %s was found standing but its shipment groups could not be marked: %w",
						standing.InvoiceNo, mErr)
				}
			}
		}
		return InvoiceDoc{}, err
	}
	// The write-back is a SEPARATE transaction, deliberately and with a stated consequence: the group
	// link lives in the order module's table and reaching across a module boundary inside billing's
	// transaction would make billing a writer of order's rows. If the process dies between the two,
	// the invoice exists and the groups are unmarked, and the idempotency key means the retry
	// returns THAT invoice (Reused) and re-runs the marking, converging on the right state. The
	// failure mode is a re-runnable button, not a duplicate document. It is therefore run on the
	// reused path TOO, and the order side is written to be idempotent for exactly this reason.
	if len(facts.GroupIDs) > 0 {
		if err := w.source.MarkGroupsInvoiced(ctx, facts.GroupIDs, doc.InvoiceID, w.now()); err != nil {
			return doc, fmt.Errorf("billing: invoice %s raised but the shipment groups could not be marked: %w",
				doc.InvoiceNo, err)
		}
	}
	return doc, nil
}

// InvoiceOrder bills a whole delivered order, the non-POS, non-multi-ship case (a quoted order
// confirmed, picked, delivered on terms). No groups, closes_order TRUE.
func (w *InvoiceWriter) InvoiceOrder(ctx context.Context, orderID string) (InvoiceDoc, error) {
	return w.InvoiceOrderNamed(ctx, orderID, "")
}

// InvoiceOrderNamed is InvoiceOrder with an optional client-chosen row id,
// the door the invoices sync scope's pushed create takes (HH-3.5): a device
// replaying "bill this order" names the document id it queued offline, and
// the document is raised by THIS writer's own path (facts resolved
// server-side, the writer's own idempotency key), so the desk's
// double-clicked button and the device's replayed outbox converge on ONE
// document: both doors are this one door. docID "" behaves exactly like
// InvoiceOrder (the server mints the id).
//
// The sync legs stamp in the same INSERT (0213's additive columns): the org
// read off the branch's location (the fence every dispatch create uses) and
// the seq from the invoices scope's allocator, so the document is visible
// to a device's pull the moment it lands.
func (w *InvoiceWriter) InvoiceOrderNamed(ctx context.Context, orderID, docID string) (InvoiceDoc, error) {
	if w.source == nil {
		return InvoiceDoc{}, ErrOrderSourceUnwired
	}
	facts, err := w.source.InvoiceableOrder(ctx, orderID, nil)
	if err != nil {
		return InvoiceDoc{}, err
	}
	facts.ClosesOrder = true
	facts.GroupIDs = nil
	return w.write(ctx, facts, SourceOrder, wholeOrderKey(orderID), docID)
}

// wholeOrderKey is the idempotency key of a whole-order invoice: one document per order, whichever
// door raises it (fulfilment, the invoices sync door, or the counter's agreed invoice).
func wholeOrderKey(orderID string) string { return "order-invoice:" + orderID }

// multiShipKey is the retry key for a group invoice: the order plus the EXACT group set, sorted so
// the same set in a different order is the same key. Two different subsets of the same order are two
// different documents and must not collide.
func multiShipKey(orderID string, groupIDs []string) string {
	ids := append([]string(nil), groupIDs...)
	// insertion sort, the set is bounded at 20 by order.maxShipmentGroups
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return "multiship-invoice:" + orderID + ":" + strings.Join(ids, ",")
}

// write is the shared body: totals, tax, and the one transaction. Both entry points land here so
// there is exactly ONE place in this package that inserts an invoice header. docID is the
// client-chosen row id the sync door may name ("" mints one); the INSERT also stamps the two
// 0213 sync legs, the org off the branch's location and the seq from the invoices scope's
// allocator, so every document this writer raises is visible on the invoices feed from the
// moment it lands (the additive columns stay NULL on writers that predate them, ERP-carried
// rows included).
func (w *InvoiceWriter) write(ctx context.Context, f OrderInvoiceFacts, sourceKind, idemKey, docID string) (InvoiceDoc, error) {
	return w.writeCore(ctx, nil, f, sourceKind, idemKey, docID)
}

// writeCore is write on either the writer's own transaction (tx nil) or the caller's (QD-5): with a
// caller's transaction nothing here begins, commits or rolls back, the existing document under the key
// is looked for on that transaction, and the header, the lines, the AR subledger row and the event
// are written on it, so they commit with whatever the caller wrote before and after. A genuine key
// race then surfaces as the database's error (the transaction cannot continue after it), and the
// caller's action fails whole, never half.
func (w *InvoiceWriter) writeCore(ctx context.Context, ctxTx pgx.Tx, f OrderInvoiceFacts, sourceKind, idemKey, docID string) (InvoiceDoc, error) {
	var q keyQuerier
	if ctxTx != nil {
		q = ctxTx
	} else {
		if w.pool == nil {
			return InvoiceDoc{}, errNoDB
		}
		q = w.pool
	}
	if f.CustomerID == "" || f.BranchID == "" {
		return InvoiceDoc{}, httpx.BadRequestf("an invoice needs a customer and a branch")
	}
	if len(f.Lines) == 0 {
		return InvoiceDoc{}, ErrNothingToInvoice
	}

	// An existing document under this key ends the call before any write. This is the fast path for
	// the double click; the UNIQUE index is the slow path for the genuine race.
	if doc, ok, err := findByKeyOn(ctx, q, idemKey); err != nil {
		return InvoiceDoc{}, err
	} else if ok {
		doc.Reused = true
		return doc, nil
	}

	shaped, err := w.shape(ctx, f)
	if err != nil {
		return InvoiceDoc{}, err
	}
	f.Lines = shaped.lines
	subtotal, total := shaped.subtotal, shaped.total
	quote := TaxQuoteResult{TaxCents: shaped.tax}

	// Per-line tax: the largest-remainder split of the HEADER tax over the line totals. Identical
	// call, identical function, identical arguments to the ring's, see G2.
	weights := make([]int64, len(f.Lines))
	for i, l := range f.Lines {
		weights[i] = l.LineTotalCents
	}
	perLineTax := moneyutil.AllocateLargestRemainder(quote.TaxCents, weights)

	// Terms 0 is the CUSTOMER'S OWN answer, Cash On Demand: pay before release, the invoice due on
	// issue with its number while unpaid. Only a negative terms, the caller's "none resolved", falls
	// back to the default; coercing 0 to 30 here is what dated Cash On Demand and walk-in order
	// invoices a month out (AR-09).
	terms := f.TermsDays
	if terms < 0 {
		terms = defaultTermsDays
	}
	// The terms the document is dated by: the customer's own terms from the master when the
	// seam is bound. The master is the answer, so the days the caller resolved cannot date a
	// document by terms the customer is not sold on, and a customer on a discount term carries
	// that term rather than the bare days.
	var docTerms customer.Terms
	if w.terms != nil {
		t, err := w.terms.TermsFor(ctx, f.CustomerID)
		if err != nil {
			return InvoiceDoc{}, err
		}
		docTerms = t
		terms = t.NetDays
	}
	// The document's days are the dealer's, decided here on the writer's clock and bound: between
	// the UTC day change and the house zone's own the database session's CURRENT_DATE is a day the
	// yard has not reached, so an evening invoice would be dated tomorrow and age a day early from
	// the moment it is written. The due date counts the terms from the SAME day the document is
	// issued, so the two dates can never name different "todays".
	zone, err := houseday.ResolveTx(ctx, w.zone, ctxTx)
	if err != nil {
		return InvoiceDoc{}, err
	}
	issueDate := zone.Today(w.now)
	dueDate := zone.AddDays(w.now, terms)
	// The discount runs until the terms' own window is out, counted in house days from the same
	// day the document is issued. A term with no discount carries no window, and the document
	// carries no discount day.
	var discountDate *string
	if docTerms.DiscountDays > 0 {
		d := zone.AddDays(w.now, docTerms.DiscountDays)
		discountDate = &d
	}
	// The terms the document carries: the code it was dated by, so the document names the term
	// it was raised under and not the terms of the day after.
	var termsCode *string
	if docTerms.Code != "" {
		code := docTerms.Code
		termsCode = &code
	}

	var doc InvoiceDoc
	var covered *InvoiceDoc
	insert := func(tx pgx.Tx) error {
		// The order's row lock is this transaction's FIRST statement (QD-5 review round 2): the writers
		// that bill an order (the counter's, the group's, the whole order's) serialise on it, and the
		// existence checks that follow see whatever the one ahead of them committed.
		if f.OrderID != "" {
			if err := lockOrder(ctx, tx, f.OrderID); err != nil {
				return err
			}
			// A group is covered by the counter's whole order document when that document committed
			// while this call waited for the lock: nothing new is written, the groups link to it.
			if sourceKind == SourceMultiShip {
				if whole, ok, err := findByKeyOn(ctx, tx, wholeOrderKey(f.OrderID)); err != nil {
					return err
				} else if ok && whole.AtCounter {
					whole.Reused = true
					covered = &whole
					return nil
				}
				// The group set was read before this lock was held, so it may overlap a group that a
				// call ahead of this one billed while this one waited. Decide against what committed:
				// the same set under the same key is that call's document (the double click), and a set
				// that shares a group with a different document is refused whole, so no group is billed
				// twice and the invoices of the order never total more than the order. The refusal
				// carries the standing document's key and group set (groupOverlapError) so the caller
				// can link that document's groups to it before the refusal is answered.
				if same, ok, err := findByKeyOn(ctx, tx, idemKey); err != nil {
					return err
				} else if ok {
					same.Reused = true
					covered = &same
					return nil
				}
				if no, key, err := overlappingGroupInvoice(ctx, tx, f.OrderID, f.GroupIDs); err != nil {
					return err
				} else if no != "" {
					return &groupOverlapError{
						wrapped: httpx.BadRequestf("a shipment group in this request was invoiced as %s while the request was waiting; read the shipment plan again and invoice what is left", no),
						key:     key,
						groups:  groupsUnderKey(f.OrderID, key),
					}
				}
			}
		}
		var invoiceID, invoiceNo string
		// FINALIZED, not DRAFT. The order was delivered; the document is real the moment it exists,
		// and a DRAFT invoice is a promise to remember to finalize it that nobody keeps.
		err := tx.QueryRow(ctx, invoiceWriterSeqCTE+`
INSERT INTO invoices (id, invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
                      discount_date, terms_code,
                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
                      doc_type, source_kind, idempotency_key, closes_order, org_id, seq, metadata)
VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), '#INV-' || nextval('invoice_no_seq'),
        $2, $3, $4, 'FINALIZED', $5, $6, $14, $15,
        $7, $8, $9, $9, now(), 'INVOICE', $10, $11, $12,
        (SELECT org_id FROM locations WHERE id = $4::uuid),
        (SELECT next_value FROM iseq), $13::jsonb)
RETURNING id::text, invoice_no`,
			docID, f.CustomerID, nullUUID(f.OrderID), f.BranchID, issueDate, dueDate,
			subtotal, quote.TaxCents, total, sourceKind, idemKey, f.ClosesOrder, headerMetadata(f),
			discountDate, termsCode,
		).Scan(&invoiceID, &invoiceNo)
		if err != nil {
			return fmt.Errorf("insert invoice: %w", err)
		}

		for i, l := range f.Lines {
			if _, err := tx.Exec(ctx, `
INSERT INTO invoice_lines (invoice_id, line_no, order_line_id, product_id, description, qty_entered,
                           uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8::numeric, $9, $10, $11, $12)`,
				invoiceID, l.LineNo, nullUUID(l.OrderLineID), nullUUID(l.ProductID), l.Description,
				l.QtyEntered, l.UOMEntered, l.QtyBase, l.UnitPriceCents, l.PriceUOM,
				l.LineTotalCents, perLineTax[i]); err != nil {
				return fmt.Errorf("insert invoice line %d: %w", l.LineNo, err)
			}
		}

		// The AR subledger row, G4. balance_after is the customer's running balance, read under this
		// transaction so a concurrent sale cannot interleave a stale one.
		var open int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(balance_cents),0) FROM invoices
			  WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')`,
			f.CustomerID).Scan(&open); err != nil {
			return fmt.Errorf("read open balance: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1, 'INVOICE', $2, $3, 'invoice', $4, $5, $6, clock_timestamp())`,
			f.CustomerID, total, open, invoiceID, f.BranchID,
			invoiceMemo(f, invoiceNo)); err != nil {
			return fmt.Errorf("insert AR subledger row: %w", err)
		}

		// G3: the SAME topic and the SAME payload keys the ring emits. gl derives the journal from the
		// invoice row, so this is the whole of the GL wiring for this producer.
		finalized := map[string]any{
			"invoice_id": invoiceID, "customer_id": f.CustomerID,
			"total_cents": total, "due_date": dueDate,
		}
		if f.RaisedAtCounter {
			finalized["source"] = EventSourceCounter
		}
		if err := emitOutbox(ctx, tx, "ar.invoice.finalized", finalized); err != nil {
			return err
		}

		doc = InvoiceDoc{
			InvoiceID: invoiceID, InvoiceNo: invoiceNo, DocType: DocInvoice,
			CustomerID: f.CustomerID, OrderID: f.OrderID, BranchID: f.BranchID,
			Status: "FINALIZED", DueDate: dueDate,
			SubtotalCents: subtotal, TaxCents: quote.TaxCents, TotalCents: total, BalanceCents: total,
			ClosesOrder: f.ClosesOrder, AtCounter: f.RaisedAtCounter,
		}
		return nil
	}
	if ctxTx != nil {
		if err := insert(ctxTx); err != nil {
			return InvoiceDoc{}, err
		}
		return doc, nil
	}
	err = w.inTx(ctx, insert)
	if err == nil && covered != nil {
		return *covered, nil
	}
	if err != nil {
		// The genuine race: a concurrent caller won the UNIQUE index. Return THEIR document.
		if isUniqueViolation(err) {
			if existing, ok, ferr := w.findByKey(ctx, idemKey); ferr == nil && ok {
				existing.Reused = true
				return existing, nil
			}
		}
		return InvoiceDoc{}, err
	}
	return doc, nil
}

// headerMetadata is the invoice header's metadata: empty for every producer but the counter's agreed
// invoice, which says so (the same marker the finalized event carries).
func headerMetadata(f OrderInvoiceFacts) string {
	if f.RaisedAtCounter {
		return `{"source":"` + EventSourceCounter + `"}`
	}
	return `{}`
}

// shapedDocument is the document's money as billing shapes it: the lines (with the delivery fee as
// its own line when the facts carry one), the subtotal over them, the tax and the total.
type shapedDocument struct {
	lines                []OrderInvoiceLine
	subtotal, tax, total int64
}

// shape is the ONE place the document's totals are decided, so the counter's preview and the write
// can never disagree. For every caller but the counter's agreed invoice the facts carry no freight
// and no agreed tax, and the answer is exactly the lines' sum with the assessed tax.
func (w *InvoiceWriter) shape(ctx context.Context, f OrderInvoiceFacts) (shapedDocument, error) {
	lines := append([]OrderInvoiceLine(nil), f.Lines...)
	if f.FreightCents > 0 {
		lines = append(lines, OrderInvoiceLine{
			LineNo: len(lines) + 1, Description: "Delivery", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: f.FreightCents, PriceUOM: "EA", LineTotalCents: f.FreightCents,
		})
	}
	var subtotal int64
	for _, l := range lines {
		subtotal += l.LineTotalCents
	}
	if subtotal < 0 {
		// A negative bill is a credit memo, and a credit memo has its own writer with its own
		// document type. Refusing here keeps 0146's sign discipline meaningful rather than letting a
		// caller mint a memo-shaped row that says INVOICE on it.
		return shapedDocument{}, httpx.BadRequestf("an invoice subtotal cannot be negative, use IssueCreditMemo")
	}
	var tax int64
	if f.AgreedTaxCents != nil {
		// The tax the customer was shown, billed as stated: assessing it again could move the total
		// the quote agreed.
		if *f.AgreedTaxCents < 0 {
			return shapedDocument{}, httpx.BadRequestf("an agreed tax cannot be negative")
		}
		tax = *f.AgreedTaxCents
	} else {
		reason := f.TaxReasonCode
		if reason == "" {
			reason = TaxStdON13
		}
		q, err := w.assessor().AssessSale(ctx, TaxQuoteInput{
			BranchID: f.BranchID, SubtotalCents: subtotal, ReasonCode: reason,
		})
		if err != nil {
			return shapedDocument{}, err
		}
		tax = q.TaxCents
	}
	return shapedDocument{lines: lines, subtotal: subtotal, tax: tax, total: subtotal + tax}, nil
}

// AgreedOrderSource is the optional order seam the counter's agreed invoice reads: every line at
// its agreed total with the delivery fee and the agreed tax stated. A source that does not answer
// it cannot raise the counter's invoice.
type AgreedOrderSource interface {
	AgreedInvoiceableOrder(ctx context.Context, orderID string) (OrderInvoiceFacts, error)
}

// AgreedOrderSourceTx is the same answer read on the caller's transaction (QD-5), for an order written
// in that transaction and not yet visible to any other connection.
type AgreedOrderSourceTx interface {
	AgreedInvoiceableOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (OrderInvoiceFacts, error)
}

func (w *InvoiceWriter) agreedSource() (AgreedOrderSource, error) {
	if w.source == nil {
		return nil, ErrOrderSourceUnwired
	}
	src, ok := w.source.(AgreedOrderSource)
	if !ok {
		return nil, ErrOrderSourceUnwired
	}
	return src, nil
}

// agreedFacts is the order's agreed facts, read through the pool (ctxTx nil) or the caller's
// transaction.
func (w *InvoiceWriter) agreedFacts(ctx context.Context, ctxTx pgx.Tx, orderID string) (OrderInvoiceFacts, error) {
	if ctxTx == nil {
		src, err := w.agreedSource()
		if err != nil {
			return OrderInvoiceFacts{}, err
		}
		return src.AgreedInvoiceableOrder(ctx, orderID)
	}
	if w.source == nil {
		return OrderInvoiceFacts{}, ErrOrderSourceUnwired
	}
	src, ok := w.source.(AgreedOrderSourceTx)
	if !ok {
		return OrderInvoiceFacts{}, ErrOrderSourceUnwired
	}
	return src.AgreedInvoiceableOrderTx(ctx, ctxTx, orderID)
}

// OrderAlreadyInvoicedError is the counter's refusal to raise an invoice for an order that already has
// one raised another way (by shipment group, say): billing the whole order again would bill the lines
// the earlier document already carries.
type OrderAlreadyInvoicedError struct {
	InvoiceNo string
}

func (e *OrderAlreadyInvoicedError) Error() string {
	return "this order is already invoiced as " + e.InvoiceNo + " by another route, so the counter cannot bill it again; " +
		"hand the customer to Payables and receivables, or to the General Manager or Financial controller, " +
		"who record the payment against that invoice"
}

// lockOrder takes the order's row lock for the rest of the transaction: the one lock every writer that
// bills an order takes first, so a group invoice and a counter invoice of one order queue behind each
// other. Order edits, reservations and shipment groups already take the same lock first. An order that
// does not exist locks nothing and is left to the facts read to refuse.
func lockOrder(ctx context.Context, q keyQuerier, orderID string) error {
	var id string
	err := q.QueryRow(ctx, `SELECT id::text FROM orders WHERE id = $1::uuid FOR UPDATE`, orderID).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("billing: lock the order: %w", err)
	}
	return nil
}

// groupOverlapError is the overlap refusal carrying what its recovery needs: the standing document's
// idempotency key and the group set that document bills. The refusal itself is unchanged (it wraps
// the same 400 with the same words); the caller links the standing document's groups to it before
// the refusal is answered, see InvoiceOrderGroups.
type groupOverlapError struct {
	wrapped error
	key     string
	groups  []string
}

func (e *groupOverlapError) Error() string { return e.wrapped.Error() }
func (e *groupOverlapError) Unwrap() error { return e.wrapped }

// groupsUnderKey reads the group set out of a group invoice's idempotency key (multiShipKey's
// encoding: the order id, then the sorted group ids joined by commas). An empty answer means no
// group is named, and the caller marks nothing.
func groupsUnderKey(orderID, key string) []string {
	if key == "" {
		return nil
	}
	prefix := "multiship-invoice:" + orderID + ":"
	var out []string
	for _, g := range strings.Split(strings.TrimPrefix(key, prefix), ",") {
		if g != "" {
			out = append(out, g)
		}
	}
	return out
}

// overlappingGroupInvoice names a live group invoice of the order that already bills any of the given
// shipment groups ("" when none does), with its idempotency key beside the name. The set a group
// invoice bills is carried in that key (multiShipKey), which is how a later request finds the groups
// an earlier one took without reading the order module's tables. Run on the order's locked
// transaction, it sees every document committed ahead.
func overlappingGroupInvoice(ctx context.Context, tx pgx.Tx, orderID string, groupIDs []string) (no, key string, err error) {
	prefix := "multiship-invoice:" + orderID + ":"
	rows, err := tx.Query(ctx, `
SELECT invoice_no::text, idempotency_key FROM invoices
 WHERE order_id = $1::uuid AND doc_type = 'INVOICE' AND status <> 'VOID'
   AND idempotency_key LIKE $2
 ORDER BY created_at`, orderID, prefix+"%")
	if err != nil {
		return "", "", fmt.Errorf("billing: look up the order's group invoices: %w", err)
	}
	defer rows.Close()
	want := make(map[string]bool, len(groupIDs))
	for _, g := range groupIDs {
		want[g] = true
	}
	for rows.Next() {
		if err := rows.Scan(&no, &key); err != nil {
			return "", "", fmt.Errorf("billing: read the order's group invoices: %w", err)
		}
		for _, g := range strings.Split(strings.TrimPrefix(key, prefix), ",") {
			if want[g] {
				return no, key, nil
			}
		}
	}
	return "", "", rows.Err()
}

// otherLiveInvoice names an invoice of the order that is not the counter's whole order document: any
// live INVOICE of the order under any other key (a shipment group's, a fulfilment document raised before
// the counter's key existed). "" when there is none.
func otherLiveInvoice(ctx context.Context, q keyQuerier, orderID string) (string, error) {
	var no string
	err := q.QueryRow(ctx, `
SELECT invoice_no::text FROM invoices
 WHERE order_id = $1::uuid AND doc_type = 'INVOICE' AND status <> 'VOID'
   AND idempotency_key IS DISTINCT FROM $2
 ORDER BY created_at LIMIT 1`, orderID, wholeOrderKey(orderID)).Scan(&no)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("billing: look up the order's other invoices: %w", err)
	}
	return no, nil
}

// AgreedOrderInvoice answers, read only, what the order's invoice at its agreed totals is: the
// document itself when one exists under the whole order key (exists true), else the document that
// would be raised (exists false, no id, balance equal to the total).
func (w *InvoiceWriter) AgreedOrderInvoice(ctx context.Context, orderID string) (InvoiceDoc, bool, error) {
	if w.pool == nil {
		return InvoiceDoc{}, false, errNoDB
	}
	return w.agreedOrderInvoice(ctx, nil, w.pool, orderID)
}

// AgreedOrderInvoiceTx is AgreedOrderInvoice read on the caller's transaction (QD-5).
func (w *InvoiceWriter) AgreedOrderInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (InvoiceDoc, bool, error) {
	return w.agreedOrderInvoice(ctx, tx, tx, orderID)
}

func (w *InvoiceWriter) agreedOrderInvoice(ctx context.Context, ctxTx pgx.Tx, q keyQuerier, orderID string) (InvoiceDoc, bool, error) {
	if ctxTx != nil {
		if err := lockOrder(ctx, ctxTx, orderID); err != nil {
			return InvoiceDoc{}, false, err
		}
	}
	if doc, ok, err := findByKeyOn(ctx, q, wholeOrderKey(orderID)); err != nil {
		return InvoiceDoc{}, false, err
	} else if ok {
		return doc, true, nil
	}
	if no, err := otherLiveInvoice(ctx, q, orderID); err != nil {
		return InvoiceDoc{}, false, err
	} else if no != "" {
		return InvoiceDoc{}, false, &OrderAlreadyInvoicedError{InvoiceNo: no}
	}
	facts, err := w.agreedFacts(ctx, ctxTx, orderID)
	if err != nil {
		return InvoiceDoc{}, false, err
	}
	if len(facts.Lines) == 0 {
		return InvoiceDoc{}, false, ErrNothingToInvoice
	}
	shaped, err := w.shape(ctx, facts)
	if err != nil {
		return InvoiceDoc{}, false, err
	}
	return InvoiceDoc{
		CustomerID: facts.CustomerID, OrderID: orderID, BranchID: facts.BranchID, DocType: DocInvoice,
		SubtotalCents: shaped.subtotal, TaxCents: shaped.tax, TotalCents: shaped.total,
		BalanceCents: shaped.total, ClosesOrder: true,
	}, false, nil
}

// InvoiceOrderAtAgreed raises the order's invoice at its agreed totals, the counter's invoice for an
// order paid before it leaves the yard. It is raised under the WHOLE order key, the same key the
// fulfilment path uses, so billing the order again when it leaves returns this document and bills
// nothing twice; a document already under the key is returned (Reused) before the order is asked
// anything. Raising it again is the same call.
func (w *InvoiceWriter) InvoiceOrderAtAgreed(ctx context.Context, orderID string) (InvoiceDoc, error) {
	if w.pool == nil {
		return InvoiceDoc{}, errNoDB
	}
	// On a transaction of its own, so the order's lock and the checks that follow it are one unit with
	// the write.
	var doc InvoiceDoc
	err := w.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		doc, err = w.invoiceOrderAtAgreed(ctx, tx, tx, orderID)
		return err
	})
	return doc, err
}

// InvoiceOrderAtAgreedTx is InvoiceOrderAtAgreed on the caller's transaction (QD-5): the existing
// document is looked for, the order's facts are read, and the invoice, its lines, its AR subledger row
// and its event are written, all on that transaction, which this method neither commits nor rolls back.
func (w *InvoiceWriter) InvoiceOrderAtAgreedTx(ctx context.Context, tx pgx.Tx, orderID string) (InvoiceDoc, error) {
	return w.invoiceOrderAtAgreed(ctx, tx, tx, orderID)
}

func (w *InvoiceWriter) invoiceOrderAtAgreed(ctx context.Context, ctxTx pgx.Tx, q keyQuerier, orderID string) (InvoiceDoc, error) {
	if ctxTx != nil {
		if err := lockOrder(ctx, ctxTx, orderID); err != nil {
			return InvoiceDoc{}, err
		}
	}
	if doc, ok, err := findByKeyOn(ctx, q, wholeOrderKey(orderID)); err != nil {
		return InvoiceDoc{}, err
	} else if ok {
		doc.Reused = true
		return doc, nil
	}
	if no, err := otherLiveInvoice(ctx, q, orderID); err != nil {
		return InvoiceDoc{}, err
	} else if no != "" {
		return InvoiceDoc{}, &OrderAlreadyInvoicedError{InvoiceNo: no}
	}
	facts, err := w.agreedFacts(ctx, ctxTx, orderID)
	if err != nil {
		return InvoiceDoc{}, err
	}
	facts.ClosesOrder = true
	facts.GroupIDs = nil
	facts.RaisedAtCounter = true
	return w.writeCore(ctx, ctxTx, facts, SourceOrder, wholeOrderKey(orderID), "")
}

// invoiceMemo names the document on the subledger row so an AR clerk reading the ledger sees WHY the
// balance moved without opening the invoice.
func invoiceMemo(f OrderInvoiceFacts, invoiceNo string) string {
	if f.OrderNo != "" {
		if len(f.GroupIDs) > 0 && !f.ClosesOrder {
			return fmt.Sprintf("%s, progress invoice for order %s (%d shipment group(s))", invoiceNo, f.OrderNo, len(f.GroupIDs))
		}
		return fmt.Sprintf("%s, order %s", invoiceNo, f.OrderNo)
	}
	return invoiceNo
}

// keyQuerier is what an invoice is looked up through: the pool, or a caller's transaction (QD-5).
type keyQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// findByKey resolves an existing document by its idempotency key.
func (w *InvoiceWriter) findByKey(ctx context.Context, key string) (InvoiceDoc, bool, error) {
	return findByKeyOn(ctx, w.pool, key)
}

func findByKeyOn(ctx context.Context, q keyQuerier, key string) (InvoiceDoc, bool, error) {
	if key == "" {
		return InvoiceDoc{}, false, nil
	}
	var d InvoiceDoc
	var orderID *string
	var due time.Time
	err := q.QueryRow(ctx, `
SELECT id::text, invoice_no::text, doc_type, customer_id::text, order_id::text, branch_id::text,
       status, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, closes_order,
       COALESCE(metadata->>'source', '') = '`+EventSourceCounter+`'
  FROM invoices WHERE idempotency_key = $1`, key).
		Scan(&d.InvoiceID, &d.InvoiceNo, &d.DocType, &d.CustomerID, &orderID, &d.BranchID,
			&d.Status, &due, &d.SubtotalCents, &d.TaxCents, &d.TotalCents, &d.BalanceCents, &d.ClosesOrder,
			&d.AtCounter)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InvoiceDoc{}, false, nil
		}
		return InvoiceDoc{}, false, fmt.Errorf("billing: look up invoice by key: %w", err)
	}
	if orderID != nil {
		d.OrderID = *orderID
	}
	d.DueDate = due.Format("2006-01-02")
	return d, true, nil
}

// -- small shared helpers (this file + credit_memo.go) ------------------------------------------

// inTx runs fn in a transaction, rolling back on any error.
// branchOrgID reads the org a branch's location carries (nil when the location has none yet: a box that has not been
// provisioned). It sits in this file, beside the invoice writer's own org stamp, because the locations anchor read is
// allowlisted here (tablegate); the credit memo takes its org through it.
func branchOrgID(ctx context.Context, tx pgx.Tx, branchID string) (any, error) {
	var org *string
	if err := tx.QueryRow(ctx, `SELECT org_id::text FROM locations WHERE id = $1::uuid`, branchID).Scan(&org); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("billing: read the branch's org: %w", err)
	}
	if org == nil {
		return nil, nil
	}
	return *org, nil
}

func (w *InvoiceWriter) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("billing: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("billing: commit: %w", err)
	}
	return nil
}

// emitOutbox writes one transactional-outbox row. The payload carries no PII beyond ids, matching
// the ring's discipline.
func emitOutbox(ctx context.Context, tx pgx.Tx, topic string, payload map[string]any) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)`, topic, payload); err != nil {
		return fmt.Errorf("emit %s: %w", topic, err)
	}
	return nil
}

// nullUUID maps "" to a NULL parameter (pgx will not coerce "" into a uuid).
func nullUUID(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// isUniqueViolation reports a Postgres 23505 anywhere in the error chain.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}
