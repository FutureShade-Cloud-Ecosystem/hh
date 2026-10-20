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
}

// Journal roles a reversing entry moves. Roles, never account codes, see the header.
const (
	RoleAR      = "ar"
	RoleRevenue = "revenue"
	RoleTax     = "tax"
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
		doc.Reused = true
		return doc, nil
	}

	plan, err := w.planCreditMemo(ctx, in)
	if err != nil {
		return InvoiceDoc{}, err
	}
	if plan.subtotal == 0 && plan.tax == 0 {
		return InvoiceDoc{}, ErrNothingToInvoice
	}

	doc, err := w.insertCreditMemo(ctx, in, plan)
	if err != nil {
		if isUniqueViolation(err) {
			if existing, ok, ferr := w.findByKey(ctx, in.IdempotencyKey); ferr == nil && ok {
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
			IdempotencyKey: creditMemoJournalKey(doc.InvoiceID),
			Memo:           "Credit memo " + doc.InvoiceNo,
			OccurredAt:     w.now(),
			ARCents:        plan.subtotal + plan.tax,
			RevenueCents:   plan.subtotal,
			TaxCents:       plan.tax,
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
}

// planCreditMemo resolves REVERSAL or ACCOMMODATION into one shape.
func (w *InvoiceWriter) planCreditMemo(ctx context.Context, in CreditMemoInput) (creditPlan, error) {
	if in.ReversesInvoiceID == "" {
		return w.planAccommodation(ctx, in)
	}
	return w.planReversal(ctx, in)
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

// planReversal: mirror the original. Tax is COPIED, never re-assessed.
func (w *InvoiceWriter) planReversal(ctx context.Context, in CreditMemoInput) (creditPlan, error) {
	var (
		p                         creditPlan
		origSub, origTax, origTot int64
		docType, sourceKind       string
		customerID, brnch         string
	)
	err := w.pool.QueryRow(ctx, `
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
	if in.CustomerID != "" && in.CustomerID != customerID {
		return creditPlan{}, httpx.BadRequestf("the credit memo's customer must match the invoice it reverses")
	}

	// A PARTIAL credit: the caller named an amount smaller than the original. One summary line, and
	// the tax is PRO-RATED from the original's own tax rather than re-assessed, same reason as above.
	if in.AmountCents > 0 && in.AmountCents < origSub {
		tax := moneyutil.AllocateLargestRemainder(origTax, []int64{in.AmountCents, origSub - in.AmountCents})[0]
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
	if in.AmountCents > origSub {
		return creditPlan{}, httpx.BadRequestf("a credit memo cannot exceed the invoice it reverses")
	}

	// FULL reversal: every line, mirrored.
	rows, err := w.pool.Query(ctx, `
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

// insertCreditMemo writes the header, the negated lines and the AR subledger row in ONE transaction.
func (w *InvoiceWriter) insertCreditMemo(ctx context.Context, in CreditMemoInput, plan creditPlan) (InvoiceDoc, error) {
	total := plan.subtotal + plan.tax
	perLineTax := make([]int64, len(plan.lines))
	if plan.tax != 0 {
		weights := make([]int64, len(plan.lines))
		for i, l := range plan.lines {
			weights[i] = l.LineTotalCents
		}
		perLineTax = moneyutil.AllocateLargestRemainder(plan.tax, weights)
	}

	customerID, branchID := in.CustomerID, in.BranchID
	if customerID == "" || branchID == "" {
		if err := w.pool.QueryRow(ctx,
			`SELECT customer_id::text, branch_id::text FROM invoices WHERE id = $1`,
			in.ReversesInvoiceID).Scan(&customerID, &branchID); err != nil {
			return InvoiceDoc{}, httpx.BadRequestf("a credit memo needs a customer and a branch")
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
		return InvoiceDoc{}, err
	}
	issueDate := zone.Today(w.now)
	dueDate := issueDate

	var doc InvoiceDoc
	err = w.inTx(ctx, func(tx pgx.Tx) error {
		// The parent rows first, in the order internal/payments/LOCK-ORDER.md names: the order row (step 1),
		// then the invoice this memo reverses (step 3), FOR NO KEY UPDATE, and only then the insert, whose
		// invoices counter draw (the sync seq trigger, 0422) comes before its foreign key checks take
		// FOR KEY SHARE on those same rows. A writer holding the order or the invoice FOR UPDATE while it
		// waits for the invoices counter (the order cancel's settlement, a loader, the POS account credit)
		// would otherwise cycle against a memo holding the counter while it waits for the row. NO KEY UPDATE
		// still queues behind such a writer, and a memo changes no key column of either row.
		if in.OrderID != "" {
			if _, err := tx.Exec(ctx, `SELECT 1 FROM orders WHERE id = $1::uuid FOR NO KEY UPDATE`, in.OrderID); err != nil {
				return fmt.Errorf("lock the order for the credit memo: %w", err)
			}
		}
		if in.ReversesInvoiceID != "" {
			if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, in.ReversesInvoiceID); err != nil {
				return fmt.Errorf("lock the invoice the credit memo reverses: %w", err)
			}
		}
		var id, no string
		// Sign flipped HERE, once, for the whole file.
		err := tx.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
                      doc_type, source_kind, idempotency_key, closes_order, reverses_invoice_id)
VALUES ('#CM-' || nextval('invoice_no_seq'), $1, $2, $3, 'FINALIZED', $4, $4,
        $5, $6, $7, $7, now(), 'CREDIT_MEMO', $8, $9, FALSE, $10)
RETURNING id::text, invoice_no`,
			customerID, nullUUID(in.OrderID), branchID, issueDate,
			-plan.subtotal, -plan.tax, -total,
			sourceKind, in.IdempotencyKey, nullUUID(in.ReversesInvoiceID),
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
		return InvoiceDoc{}, err
	}
	return doc, nil
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
