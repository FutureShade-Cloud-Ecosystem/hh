package billing

// credit_memo.go, DIB-422 item 2: THE CREDIT MEMO, AND THE REVERSING-ENTRY DISCIPLINE.
//
// -- WHAT A CREDIT MEMO IS, AND WHAT IT IS NOT -------------------------------------------------
// A credit memo is a NEGATIVE INVOICE. It is a second document of the opposite sign that lands
// beside the original, and it is emphatically NOT any of the three things people reach for instead:
//
//   · not a DELETED invoice    , the customer has the original in a filing cabinet. Deleting our
//                                 copy does not delete theirs, it only makes the two disagree.
//   · not an EDITED invoice    , "what did this account owe on 2026-06-30" must have ONE answer
//                                 forever. An edit makes that answer a function of when you ask.
//   · not a JOURNAL ENTRY alone, a journal fixes the books and leaves the customer with nothing to
//                                 match against their own ledger. W5-B's FALLBACK_GL is exactly this
//                                 state, and it is deliberately reported as FALLBACK, not POSTED.
//
// 0146 enforces the sign in the SCHEMA (ck_invoices_doc_sign, and the deferred line-sign trigger),
// so the discipline binds the 2am psql session and not merely the callers of this file.
//
// -- THE REVERSING ENTRY: SIDES SWAP, AMOUNTS DO NOT GO NEGATIVE -------------------------------
// This is the single most important fact in this file, and it is a fact about the LEDGER, not about
// taste. gl_journal_lines carries CHECK (debit_cents > 0 ... OR credit_cents > 0): a leg of −$500 is
// not representable, by design, because a ledger where amounts may be negative has two spellings for
// every entry and reconciles to neither.
//
// So a credit memo may NOT re-use `ar.invoice.finalized`. That builder emits
//
//     DR 1100 AR total   CR 4000 revenue subtotal   CR 2200 HST tax
//
// and feeding it a negative invoice would produce three illegal legs and a failed outbox handler -
// a silent one, since the consumer retries. The reversing entry is the SAME THREE ROLES WITH THE
// SIDES EXCHANGED and every amount a positive magnitude:
//
//     DR 4000 revenue subtotal   DR 2200 HST tax   CR 1100 AR total
//
// which is arithmetically identical to negating the original and legal in the schema. That is what
// "reversing-entry discipline" means here, and TestCreditMemo_ReversingEntrySwapsSidesNotSigns is
// the named test that holds it.
//
// -- WHY BILLING NAMES ROLES AND NOT ACCOUNT CODES ---------------------------------------------
// CreditMemoJournal carries ARCents/RevenueCents/TaxCents, WHAT moved, and never the strings
// '1100'/'4000'/'2200'. The chart of accounts belongs to gl and is branch-overridable in
// posting_rules; a hard-coded '4000' in billing is a second, stale copy of a configurable fact. The
// main.go adapter resolves the invoice.finalized roles and swaps the sides, so re-mapping revenue
// for one branch keeps working and this file never learns about it.
//
// -- TWO CALLERS, TWO SHAPES, ONE DOCUMENT -----------------------------------------------------
//   REVERSAL      (ReversesInvoiceID set) mirrors an existing invoice: its lines, negated. The tax is
//                 COPIED, never re-assessed, re-assessing at today's rate would leave a residue in
//                 2200 the day after a rate change, which is precisely the bug a reversing entry
//                 exists to prevent. Amount defaults to the whole original; a partial credit names
//                 its own amount and gets a single summary line.
//   ACCOMMODATION (no original) is warranty's path. AmountCents is the credit's SUBTOTAL and tax IS
//                 assessed, through the SAME TaxAssessor the invoice writer uses. The reasoning: an
//                 accommodation credits value the customer paid HST on, and crediting the value
//                 without the tax leaves them out of pocket for tax on goods they did not get.

import (
	"context"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/taxwords"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
)

// -- consumer-declared seam (adapter lands in main.go) ------------------------------------------

// CreditMemoPoster posts the reversing journal for a credit memo. Declared HERE so billing names the
// movement and gl owns the chart.
//
// Exact signature for the union adapter:
//
//	PostCreditMemoJournal(ctx context.Context, in CreditMemoJournal) (string, error)
//
// The adapter is expected to resolve the `invoice.finalized` posting roles (ar/revenue/tax) for the
// journal's branch and post them with the sides EXCHANGED, see the header. It must be idempotent on
// IdempotencyKey, which gl.PostExternal already is.
type CreditMemoPoster interface {
	PostCreditMemoJournal(ctx context.Context, in CreditMemoJournal) (string, error)
}

// CreditMemoJournal is the reversing entry, stated in ROLES with POSITIVE magnitudes. The adapter
// turns roles into accounts and magnitudes into DR/CR sides; nothing here is negative, because
// nothing in gl_journal_lines may be.
type CreditMemoJournal struct {
	CreditMemoID   string
	CreditMemoNo   string
	BranchID       string
	IdempotencyKey string
	Memo           string
	OccurredAt     time.Time
	// Positive magnitudes. ARCents == RevenueCents + TaxCents, asserted before the call.
	ARCents      int64
	RevenueCents int64
	TaxCents     int64
	// ReversesSourceKind is the source kind of the invoice the memo reverses ("" for a standalone
	// credit). A memo against a FINANCE_CHARGE document debits finance charge income, never sales
	// revenue, because that is the account the charge credited. Every other kind is unchanged.
	ReversesSourceKind string
}

// Journal roles a reversing entry moves. Roles, never account codes, see the header.
const (
	RoleAR      = "ar"
	RoleRevenue = "revenue"
	RoleTax     = "tax"
	// RoleFinanceCharge is the income role a finance charge document credited (posting rule
	// invoice.finalized/finance_charge, account 4220); its reversal debits the same role.
	RoleFinanceCharge = "finance_charge"
)

// SupplierDepositNotCreditable refuses a credit memo against a supplier
// deposit document (sub item 9.5, the delta review's P2-C): that document
// posted DR AR / CR Customer Deposits and booked no revenue and no tax, so
// the memo's ordinary reversing entry would debit revenue that was never
// credited and leave the deposit liability overstated. A deposit is RELEASED
// when its special order is fulfilled (the release item that follows item 9),
// never credited.
type SupplierDepositNotCreditable struct{ InvoiceNo string }

func (e *SupplierDepositNotCreditable) Error() string {
	return "billing: a supplier deposit is released when its special order is fulfilled, not credited"
}

// The refusal's stable code and words at the register's door: the register's
// envelope carries the sentence a person reads and the code a client switches
// on, beside it.
const (
	SupplierDepositNotCreditableCode  = "supplier_deposit_not_creditable"
	SupplierDepositNotCreditableWords = "A supplier deposit is released when its special order is fulfilled, not credited."
)

// JournalLeg is one leg of the reversing entry, addressed by ROLE. Exactly one of DebitCents /
// CreditCents is non-zero, and both are POSITIVE magnitudes.
type JournalLeg struct {
	Role        string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// ReversingLegs is THE SIDE SWAP, and it lives here rather than in the composition root on purpose.
//
// It used to live in main.go's adapter. A mutation test found that out: flipping AR from credited to
// debited there, the exact negate-instead-of-swap bug this whole design exists to prevent, was a
// SURVIVOR, because nothing in any module can reach a composition-root adapter to test it. Arithmetic
// that no test can see is arithmetic with no owner. So the rule the file header states is now
// enforced by a function billing owns and tests, and main.go's adapter is reduced to what an adapter
// should be: role → account code, and nothing else.
//
//	an invoice   DR ar total   CR revenue subtotal   CR tax
//	its reversal DR revenue subtotal   DR tax   CR ar total
//
// Swapping rather than negating is forced by gl_journal_lines' CHECK (debit_cents > 0 OR
// credit_cents > 0): a leg of −$500 is not representable, deliberately, because a ledger that admits
// negative amounts has two spellings for every entry and reconciles to neither.
//
// A zero-tax memo emits NO tax leg at all rather than a zero one, since a zero leg would violate that
// same CHECK.
func (j CreditMemoJournal) ReversingLegs() []JournalLeg { return j.ReversingLegsFor(taxwords.HST) }

// ReversingLegsFor is ReversingLegs with the tax leg's memo naming the tax by the dealer's word
// ("HST reversed", "Sales tax reversed"). The amounts and roles do not depend on the word.
func (j CreditMemoJournal) ReversingLegsFor(taxWord string) []JournalLeg {
	legs := []JournalLeg{{Role: RoleRevenue, DebitCents: j.RevenueCents, Memo: "revenue reversed"}}
	if j.ReversesSourceKind == SourceFinanceCharge {
		legs = []JournalLeg{{Role: RoleFinanceCharge, DebitCents: j.RevenueCents, Memo: "finance charge reversed"}}
	}
	if j.TaxCents != 0 {
		legs = append(legs, JournalLeg{Role: RoleTax, DebitCents: j.TaxCents, Memo: taxWord + " reversed"})
	}
	legs = append(legs, JournalLeg{Role: RoleAR, CreditCents: j.ARCents, Memo: "AR credited"})
	return legs
}

// -- input --------------------------------------------------------------------------------------

// CreditMemoInput is a request to credit a customer.
type CreditMemoInput struct {
	CustomerID string
	BranchID   string
	OrderID    string
	// JobID is the job the credit is opened on (JA-2), "" when the credit belongs to the account
	// as a whole. Fenced by the route before the memo is raised; carried onto the document.
	JobID string
	// ReversesInvoiceID selects REVERSAL mode: mirror this document, negated. Empty ⇒ ACCOMMODATION.
	ReversesInvoiceID string
	// AmountCents is a POSITIVE magnitude, the sign is this file's business, never the caller's, so
	// that no caller has to hold billing's convention in its head (see warranty.CustomerCreditRequest).
	// In REVERSAL mode, 0 means "the whole original".
	AmountCents int64
	// TaxReasonCode applies in ACCOMMODATION mode only; "" ⇒ TaxStdON13.
	TaxReasonCode string
	Memo          string
	// IdempotencyKey is mandatory. A credit memo is money leaving the company; a retry that mints a
	// second one is a real loss, not an untidy table.
	IdempotencyKey string
	SourceKind     string // SourceWarrantyCredit etc.; "" ⇒ SourceOrder
	TermsDays      int
}

// IssueCreditMemo raises a credit memo and posts its reversing entry.
//
// The document and its AR subledger row are ONE transaction. The journal is posted AFTER commit and
// its failure is non-fatal-but-reported, matching warranty's own settlement ruling: the customer's
// document must exist even if the chart of accounts is misconfigured, because a missing document is
// visible to the customer and a missing journal is visible to us. GLJournalID comes back empty in
// that case and the caller can say so.
func (w *InvoiceWriter) IssueCreditMemo(ctx context.Context, in CreditMemoInput) (InvoiceDoc, error) {
	if w.pool == nil {
		return InvoiceDoc{}, errNoDB
	}
	if in.IdempotencyKey == "" {
		return InvoiceDoc{}, httpx.BadRequestf("a credit memo needs an idempotency key")
	}
	if in.AmountCents < 0 {
		return InvoiceDoc{}, httpx.BadRequestf("a credit memo amount is a positive magnitude; the sign is billing's")
	}

	// The double-click fast path. A reused memo is returned, never re-posted.
	if doc, ok, err := w.findByKey(ctx, in.IdempotencyKey); err != nil {
		return InvoiceDoc{}, err
	} else if ok {
		// The fast path answers with the memo the key named only when it is the same credit; a racing loser whose
		// pre check ran before the winner committed lands here, and must get the same 409 as the fallback below.
		same, cerr := w.sameCreditBody(ctx, doc, in)
		if cerr != nil {
			return InvoiceDoc{}, cerr
		}
		if !same {
			return InvoiceDoc{}, &creditMemoKeyConflict{msg: "this idempotency key already named a different credit; a key belongs to the credit that first used it"}
		}
		doc.Reused = true
		return doc, nil
	}

	// The plan read here is a fast refusal and, for an accommodation, the plan itself. A REVERSAL is planned AGAIN
	// inside insertCreditMemo's transaction, under the lock on the reversed invoice, because what the invoice has
	// left to credit can only be read there: two reversals of one invoice must not both pass the cap (SF-2e).
	plan, err := w.planCreditMemo(ctx, w.pool, in)
	if err != nil {
		return InvoiceDoc{}, err
	}
	if plan.subtotal == 0 && plan.tax == 0 {
		return InvoiceDoc{}, ErrNothingToInvoice
	}

	doc, plan, err := w.insertCreditMemo(ctx, in, plan)
	if err != nil {
		if isUniqueViolation(err) {
			if existing, ok, ferr := w.findByKey(ctx, in.IdempotencyKey); ferr == nil && ok {
				// The racing loser is answered with the winner's memo only when it is the same credit
				// (customer, amount, job), as the non racing reuse checks; a different body is a conflict.
				same, cerr := w.sameCreditBody(ctx, existing, in)
				if cerr != nil {
					return InvoiceDoc{}, cerr
				}
				if !same {
					return InvoiceDoc{}, &creditMemoKeyConflict{msg: "this idempotency key already named a different credit; a key belongs to the credit that first used it"}
				}
				existing.Reused = true
				return existing, nil
			}
		}
		return InvoiceDoc{}, err
	}

	// The reversing entry. Magnitudes, positive, roles named.
	if w.glPoster != nil {
		jid, perr := w.glPoster.PostCreditMemoJournal(ctx, CreditMemoJournal{
			CreditMemoID: doc.InvoiceID, CreditMemoNo: doc.InvoiceNo, BranchID: in.BranchID,
			IdempotencyKey:     creditMemoJournalKey(doc.InvoiceID),
			Memo:               "Credit memo " + doc.InvoiceNo,
			OccurredAt:         w.now(),
			ARCents:            plan.subtotal + plan.tax,
			RevenueCents:       plan.subtotal,
			TaxCents:           plan.tax,
			ReversesSourceKind: plan.origSourceKind,
		})
		if perr != nil {
			// Reported, not fatal, see the doc comment. The memo exists; the journal is chaseable by
			// its idempotency key, which is derived from the memo id and therefore never lost.
			doc.GLJournalError = perr.Error()
		} else {
			doc.GLJournalID = jid
		}
	}
	return doc, nil
}

// creditPlan is the resolved money of a memo, in POSITIVE magnitudes. Negation happens once, at the
// INSERT, so there is exactly one place in this package where a sign is flipped.
type creditPlan struct {
	subtotal int64
	tax      int64
	lines    []OrderInvoiceLine // positive magnitudes; negated at insert
	origNo   string
	// origSourceKind is the reversed invoice's source kind, carried to the memo's journal.
	origSourceKind string
	// THE ROOM: what the invoice has left to credit, before tax and with it (SF-2e). A credit
	// against an invoice is capped at the room, so the room is the figure a person needs to see
	// before typing an amount; the quote door answers it, and the desk shows it. Zero for a
	// standalone credit, which has no invoice to have a room.
	roomSubtotal int64
	roomTax      int64
}

// planCreditMemo resolves REVERSAL or ACCOMMODATION into one shape.
func (w *InvoiceWriter) planCreditMemo(ctx context.Context, q planQuerier, in CreditMemoInput) (creditPlan, error) {
	if in.ReversesInvoiceID == "" {
		return w.planAccommodation(ctx, in)
	}
	return w.planReversal(ctx, q, in)
}

// planQuerier is the read side of a pool or a transaction: planReversal runs on the pool as a fast refusal and again
// on the memo's transaction, where the remainder is read under the lock.
type planQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// planAccommodation: the amount is a subtotal, tax is assessed through the shared seam.
func (w *InvoiceWriter) planAccommodation(ctx context.Context, in CreditMemoInput) (creditPlan, error) {
	if in.CustomerID == "" || in.BranchID == "" {
		return creditPlan{}, httpx.BadRequestf("a credit memo needs a customer and a branch")
	}
	if in.AmountCents == 0 {
		return creditPlan{}, ErrNothingToInvoice
	}
	reason := in.TaxReasonCode
	if reason == "" {
		reason = TaxStdON13
	}
	quote, err := w.assessor().AssessSale(ctx, TaxQuoteInput{
		BranchID: in.BranchID, SubtotalCents: in.AmountCents, ReasonCode: reason,
	})
	if err != nil {
		return creditPlan{}, err
	}
	desc := in.Memo
	if desc == "" {
		desc = "Credit"
	}
	return creditPlan{
		subtotal: in.AmountCents,
		tax:      quote.TaxCents,
		lines: []OrderInvoiceLine{{
			LineNo: 1, Description: desc,
			QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: in.AmountCents, PriceUOM: "EA", LineTotalCents: in.AmountCents,
		}},
	}, nil
}

// planReversal: mirror what the original has LEFT. Tax is COPIED from the original, never re-assessed.
//
// The cap (SF-2e): the remainder is the original's subtotal less the subtotal of every credit memo already linked to
// it (reverses_invoice_id), and its tax less theirs. A whole reversal credits the remainder; a partial one is capped
// at it; an invoice credited in full has nothing left. Run inside the memo's transaction, under the reversed invoice's
// FOR NO KEY UPDATE lock, the sum cannot change between this read and the insert.
func (w *InvoiceWriter) planReversal(ctx context.Context, q planQuerier, in CreditMemoInput) (creditPlan, error) {
	var (
		p                         creditPlan
		origSub, origTax, origTot int64
		docType, sourceKind       string
		customerID, brnch         string
	)
	err := q.QueryRow(ctx, `
SELECT invoice_no::text, doc_type, source_kind, customer_id::text, branch_id::text,
       subtotal_cents, tax_cents, total_cents
  FROM invoices WHERE id = $1`, in.ReversesInvoiceID).
		Scan(&p.origNo, &docType, &sourceKind, &customerID, &brnch, &origSub, &origTax, &origTot)
	if err != nil {
		if err == pgx.ErrNoRows {
			return creditPlan{}, httpx.BadRequestf("no such invoice to reverse")
		}
		return creditPlan{}, fmt.Errorf("billing: read invoice to reverse: %w", err)
	}
	if docType != DocInvoice {
		// Reversing a reversal is how a ledger becomes a hall of mirrors. If the credit was wrong,
		// the answer is an invoice, which is a document with its own name and its own number.
		return creditPlan{}, httpx.BadRequestf("a credit memo cannot reverse another credit memo")
	}
	if sourceKind == SourceSupplierDeposit {
		// The deposit is a liability, not revenue (P2-C above): a memo against it
		// would reverse revenue that was never booked. The fulfilment path
		// releases it, whole or not at all.
		return creditPlan{}, &SupplierDepositNotCreditable{InvoiceNo: p.origNo}
	}
	p.origSourceKind = sourceKind
	if in.CustomerID != "" && in.CustomerID != customerID {
		return creditPlan{}, httpx.BadRequestf("the credit memo's customer must match the invoice it reverses")
	}

	// What earlier memos already took. Memos hold negative figures; the sums are read positive.
	var prevSub, prevTax int64
	if err := q.QueryRow(ctx, `
SELECT COALESCE(-SUM(subtotal_cents), 0), COALESCE(-SUM(tax_cents), 0)
  FROM invoices WHERE reverses_invoice_id = $1::uuid AND doc_type = 'CREDIT_MEMO' AND status <> 'VOID'`,
		in.ReversesInvoiceID).Scan(&prevSub, &prevTax); err != nil {
		return creditPlan{}, fmt.Errorf("billing: read what is already credited: %w", err)
	}
	remSub, remTax := origSub-prevSub, origTax-prevTax
	if remSub <= 0 {
		return creditPlan{}, httpx.BadRequestf("invoice %s has nothing left to credit: it has already been credited in full", p.origNo)
	}
	// THE ROOM, held for whoever asks what is left: the cap the amount is held to, before tax and
	// with it. A paid invoice still has a room: what has been paid is not what has been credited.
	p.roomSubtotal, p.roomTax = remSub, remTax
	if in.AmountCents > remSub {
		return creditPlan{}, httpx.BadRequestf("a credit memo cannot be more than invoice %s has left to credit: %s before tax (%s with tax) is left",
			p.origNo, money(remSub), money(remSub+remTax))
	}

	// A PARTIAL credit: the caller named an amount smaller than what is left. One summary line, and the tax is
	// PRO-RATED from the tax that is left (the original's own tax for the first memo) rather than re-assessed, same
	// reason as above. A credit of exactly what is left finishes the invoice's tax to the cent.
	if in.AmountCents > 0 && in.AmountCents < remSub {
		tax := moneyutil.AllocateLargestRemainder(remTax, []int64{in.AmountCents, remSub - in.AmountCents})[0]
		desc := in.Memo
		if desc == "" {
			desc = "Partial credit against " + p.origNo
		}
		p.subtotal, p.tax = in.AmountCents, tax
		p.lines = []OrderInvoiceLine{{
			LineNo: 1, Description: desc, QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: in.AmountCents, PriceUOM: "EA", LineTotalCents: in.AmountCents,
		}}
		return p, nil
	}
	if prevSub > 0 {
		// A whole reversal (or a credit of exactly what is left) after earlier memos: the lines are partly credited
		// already, so what is left is one summary line, with the tax that is left.
		desc := in.Memo
		if desc == "" {
			desc = "Credit against " + p.origNo + ", what was left"
		}
		p.subtotal, p.tax = remSub, remTax
		p.lines = []OrderInvoiceLine{{
			LineNo: 1, Description: desc, QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: remSub, PriceUOM: "EA", LineTotalCents: remSub,
		}}
		return p, nil
	}

	// FULL reversal: every line, mirrored.
	rows, err := q.Query(ctx, `
SELECT line_no, COALESCE(order_line_id::text,''), COALESCE(product_id::text,''), description,
       qty_entered::text, uom_entered, qty_base::text, unit_price_cents, price_uom, line_total_cents
  FROM invoice_lines WHERE invoice_id = $1 ORDER BY line_no`, in.ReversesInvoiceID)
	if err != nil {
		return creditPlan{}, fmt.Errorf("billing: read lines to reverse: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l OrderInvoiceLine
		if err := rows.Scan(&l.LineNo, &l.OrderLineID, &l.ProductID, &l.Description, &l.QtyEntered,
			&l.UOMEntered, &l.QtyBase, &l.UnitPriceCents, &l.PriceUOM, &l.LineTotalCents); err != nil {
			return creditPlan{}, fmt.Errorf("billing: scan line to reverse: %w", err)
		}
		l.Description = "Credit: " + l.Description
		p.lines = append(p.lines, l)
	}
	if err := rows.Err(); err != nil {
		return creditPlan{}, fmt.Errorf("billing: read lines to reverse: %w", err)
	}
	if len(p.lines) == 0 {
		// A header with no lines still gets a faithful mirror, one summary line for the whole.
		p.lines = []OrderInvoiceLine{{
			LineNo: 1, Description: "Credit against " + p.origNo,
			QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: origSub, PriceUOM: "EA", LineTotalCents: origSub,
		}}
	}
	p.subtotal, p.tax = origSub, origTax
	return p, nil
}

// lockForCreditPlan takes the writer's locks on the caller's transaction, in the order
// internal/payments/LOCK-ORDER.md names: the order row first when the memo carries one, then the
// invoice the memo reverses, FOR NO KEY UPDATE. Every credit plan reads the invoice's remainder under
// these locks, so two credits of one invoice cannot both pass the cap (SF-2e), and a parked credit's
// room is held from the park to the approval. The posted writer and the park share it.
func lockForCreditPlan(ctx context.Context, tx pgx.Tx, in CreditMemoInput) error {
	if in.OrderID != "" {
		// The one lock every writer that bills an order takes first (lockOrder, invoice_writer.go).
		if err := lockOrder(ctx, tx, in.OrderID); err != nil {
			return err
		}
	}
	if in.ReversesInvoiceID != "" {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, in.ReversesInvoiceID); err != nil {
			return fmt.Errorf("lock the invoice the credit memo reverses: %w", err)
		}
	}
	return nil
}

// insertCreditMemo writes the header, the negated lines and the AR subledger row in ONE transaction.
func (w *InvoiceWriter) insertCreditMemo(ctx context.Context, in CreditMemoInput, plan creditPlan) (InvoiceDoc, creditPlan, error) {
	customerID, branchID := in.CustomerID, in.BranchID
	if customerID == "" || branchID == "" {
		if err := w.pool.QueryRow(ctx,
			`SELECT customer_id::text, branch_id::text FROM invoices WHERE id = $1`,
			in.ReversesInvoiceID).Scan(&customerID, &branchID); err != nil {
			return InvoiceDoc{}, plan, httpx.BadRequestf("a credit memo needs a customer and a branch")
		}
	}
	sourceKind := in.SourceKind
	if sourceKind == "" {
		sourceKind = SourceOrder
	}
	// A memo is due the day it is issued: there is nothing to wait for, the money is already the
	// customer's. Dating it 30 days out would park a credit in the "current" aging bucket and make
	// the account look worse than it is for a month. The day it is issued is the dealer's, decided
	// here on the writer's clock and bound: between the UTC day change and the house zone's own the
	// database session's CURRENT_DATE is a day the yard has not reached, so an evening memo would
	// otherwise be issued tomorrow and due tomorrow, one day late for its whole life.
	zone, err := houseday.Resolve(ctx, w.zone)
	if err != nil {
		return InvoiceDoc{}, plan, err
	}
	issueDate := zone.Today(w.now)
	dueDate := issueDate

	var doc InvoiceDoc
	var total int64
	var perLineTax []int64
	err = w.inTx(ctx, func(tx pgx.Tx) error {
		// The parent rows first, in the order internal/payments/LOCK-ORDER.md names: the order row (step 1),
		// then the invoice this memo reverses (step 3, FOR NO KEY UPDATE), and only then the insert, whose
		// invoices counter draw (the sync seq trigger, 0422) comes before its foreign key checks take
		// FOR KEY SHARE on those same rows. A writer holding the order or the invoice FOR UPDATE while it
		// waits for the invoices counter (the order cancel's settlement, a loader, the POS account credit)
		// would otherwise cycle against a memo holding the counter while it waits for the row. The memo now
		// queues behind such a writer instead; it changes no key column of either row.
		if err := lockForCreditPlan(ctx, tx, in); err != nil {
			return err
		}
		// The cap (SF-2e): a REVERSAL is planned here, under the lock just taken, so the remainder it reads (the
		// invoice less every memo already linked to it) cannot change before this memo's row is inserted. A second
		// reversal of the same invoice queues on the lock above and then sees this memo in its sum.
		if in.ReversesInvoiceID != "" {
			locked, perr := w.planReversal(ctx, tx, in)
			if perr != nil {
				return perr
			}
			if locked.subtotal == 0 && locked.tax == 0 {
				return ErrNothingToInvoice
			}
			plan = locked
		}
		total = plan.subtotal + plan.tax
		perLineTax = make([]int64, len(plan.lines))
		if plan.tax != 0 {
			weights := make([]int64, len(plan.lines))
			for i, l := range plan.lines {
				weights[i] = l.LineTotalCents
			}
			perLineTax = moneyutil.AllocateLargestRemainder(plan.tax, weights)
		}
		orgID, oerr := branchOrgID(ctx, tx, branchID)
		if oerr != nil {
			return oerr
		}
		var id, no string
		// The memo's org is its branch's (SF-2p), the org the staff feed fences on (`i.org_id = $1`; the sync
		// handler filters on the caller's org, which is the branch location's), so Counter Till serves it. The seq
		// is drawn by 0422's trg_sync_seq_draw on this INSERT; no explicit draw is added here.
		// Sign flipped HERE, once, for the whole file.
		err := tx.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, order_id, job_id, branch_id, status, issue_date, due_date,
                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
                      doc_type, source_kind, idempotency_key, closes_order, reverses_invoice_id, org_id)
VALUES ('#CM-' || nextval('invoice_no_seq'), $1, $2, $12, $3, 'FINALIZED', $4, $4,
        $5, $6, $7, $7, now(), 'CREDIT_MEMO', $8, $9, FALSE, $10,
        $11)
RETURNING id::text, invoice_no`,
			customerID, nullUUID(in.OrderID), branchID, issueDate,
			-plan.subtotal, -plan.tax, -total,
			sourceKind, in.IdempotencyKey, nullUUID(in.ReversesInvoiceID), orgID, nullUUID(in.JobID),
		).Scan(&id, &no)
		if err != nil {
			return fmt.Errorf("insert credit memo: %w", err)
		}

		for i, l := range plan.lines {
			if _, err := tx.Exec(ctx, `
INSERT INTO invoice_lines (invoice_id, line_no, order_line_id, product_id, description, qty_entered,
                           uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8::numeric, $9, $10, $11, $12)`,
				id, i+1, nullUUID(l.OrderLineID), nullUUID(l.ProductID), l.Description,
				l.QtyEntered, l.UOMEntered, l.QtyBase,
				// The unit PRICE stays positive on every document (0146): a price is a price, and the
				// direction of the money is carried by the line total.
				l.UnitPriceCents, l.PriceUOM, -l.LineTotalCents, -perLineTax[i]); err != nil {
				return fmt.Errorf("insert credit memo line %d: %w", i+1, err)
			}
		}

		var open int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(balance_cents),0) FROM invoices
			  WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')`,
			customerID).Scan(&open); err != nil {
			return fmt.Errorf("read open balance: %w", err)
		}
		// source_type='invoice' is LOAD-BEARING, not cosmetic. Repository.ARBalanceCents sums open
		// invoice balances PLUS non-invoice AR transactions; a CREDIT_MEMO row tagged anything else
		// would be counted twice, once as this document's negative balance and once as a standalone
		// adjustment, and the 1100 tie-out would break by exactly the memo.
		if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1, 'CREDIT_MEMO', $2, $3, 'invoice', $4, $5, $6, clock_timestamp())`,
			customerID, -total, open, id, branchID, creditMemoMemo(no, plan.origNo, in.Memo)); err != nil {
			return fmt.Errorf("insert AR subledger row: %w", err)
		}

		doc = InvoiceDoc{
			InvoiceID: id, InvoiceNo: no, DocType: DocCreditMemo,
			CustomerID: customerID, OrderID: in.OrderID, BranchID: branchID,
			Status: "FINALIZED", DueDate: dueDate,
			SubtotalCents: -plan.subtotal, TaxCents: -plan.tax,
			TotalCents: -total, BalanceCents: -total,
			ReversesInvoiceID: in.ReversesInvoiceID, ClosesOrder: false,
		}
		return nil
	})
	if err != nil {
		return InvoiceDoc{}, plan, err
	}
	return doc, plan, nil
}

// creditMemoMemo names the document on the subledger row.
func creditMemoMemo(no, origNo, reason string) string {
	s := no
	if origNo != "" {
		s += ", reverses " + origNo
	}
	if reason != "" {
		s += " (" + reason + ")"
	}
	return s
}

// sameCreditBody reports whether the memo a key already named is the credit this request asks for: the
// same customer, the same job and, when the request names an amount, the same amount. The job is read
// back from the memo's own row because the key lookup does not carry it.
func (w *InvoiceWriter) sameCreditBody(ctx context.Context, existing InvoiceDoc, in CreditMemoInput) (bool, error) {
	var jobID string
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(job_id::text,'') FROM invoices WHERE id = $1::uuid`, existing.InvoiceID).Scan(&jobID); err != nil {
		return false, fmt.Errorf("billing: read the job of the credit memo this key named: %w", err)
	}
	if existing.CustomerID != in.CustomerID || jobID != in.JobID {
		return false, nil
	}
	if in.AmountCents != 0 && -existing.SubtotalCents != in.AmountCents {
		return false, nil
	}
	return true, nil
}
