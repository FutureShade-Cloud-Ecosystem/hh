package pos

// sale_supplier_deposit.go, sub item 9.5 (the row 1047 deposits work): the
// supplier's demanded deposit on a special order, for a credit customer.
//
// THE RULING (decision log row 1047): special orders need no deposit on a pay
// later account UNLESS the supplier demands one. When the supplier demands
// one, the rep marks it on the sale and types the amount the supplier asked
// for; the amount varies by supplier and order, and there is no dealer
// percentage setting.
//
// THE BOOKING, for a credit customer: the deposit is BILLED TO THE ACCOUNT at
// once, its own FINALIZED invoice for the deposit amount (due from the terms,
// its GL posting DR 1100 / CR 2300 Customer Deposits through the ordinary
// ar.invoice.finalized path, its own subledger INVOICE row), never taken at
// the counter; the sale's own invoice carries THE REST (the total less the
// deposit) exactly as an on-account sale's invoice books today, so the
// account's two invoices together bill the sale's whole total once and the
// Pay balance take settles exactly what the hold reads. The 2300 liability's
// release to revenue belongs to the fulfilment path that applies the deposit
// when the special order lands, and is out of this item's scope.
//
// For a Cash On Demand or walk in customer NOTHING changes: their deposit is
// already line one at the counter.
//
// THE MARKER. No path marks a sale line as a special order today (the ring's
// LineInput carries no special order flag and nothing writes
// order_lines.is_special_order), so the deposit's marker sits on the SALE
// itself, the amount the rep typed: the shape guards below are the whole
// rule. The report says so plainly.
//
// The refusals are the problem body style the ring's other money refusals
// use: a stable word, integer cents fields, a message in dollars.

import (
	"context"
	"fmt"
)

// SourceSupplierDeposit marks the deposit document (invoices.source_kind).
const SourceSupplierDeposit = "SUPPLIER_DEPOSIT"

// SupplierDepositWords is what the Summary and the receipt print for the
// billed deposit, server owned.
const SupplierDepositWords = "Supplier deposit, billed to the account"

// ErrSupplierDepositNotCredit refuses a supplier deposit on a shape that is
// not a credit account's on-account sale: the deposit is billed to the
// account, so an account with no terms or no line, a pay before release sale,
// a counter deposit or a split cannot carry it.
type ErrSupplierDepositNotCredit struct {
	TermsDays        int
	CreditLimitCents int64
}

func (e ErrSupplierDepositNotCredit) Error() string {
	return fmt.Sprintf(
		"pos: a supplier deposit is billed to a credit account's own sale (terms %d days, line %d cents)",
		e.TermsDays, e.CreditLimitCents)
}

// supplierDepositNotCreditMessage is the refusal's sentence, dollars only.
func supplierDepositNotCreditMessage(e ErrSupplierDepositNotCredit) string {
	return "A supplier deposit is billed to a credit account, on account. This sale is not that shape: ring it as the account fits, or take the deposit at the counter."
}

// ErrSupplierDepositOverPretax refuses a supplier deposit larger than the
// sale's pre-tax amount: the rest's invoice would carry a negative subtotal,
// and a supplier's deposit never exceeds the goods it secures.
type ErrSupplierDepositOverPretax struct {
	DepositCents  int64
	SubtotalCents int64
}

func (e ErrSupplierDepositOverPretax) Error() string {
	return fmt.Sprintf(
		"pos: the supplier deposit %d exceeds the sale's pre-tax %d cents", e.DepositCents, e.SubtotalCents)
}

// supplierDepositOverPretaxMessage is the refusal's sentence in dollars.
func supplierDepositOverPretaxMessage(e ErrSupplierDepositOverPretax) string {
	return fmt.Sprintf("The supplier deposit of %s is more than the sale's pre-tax %s.",
		splitMismatchDollars(e.DepositCents), splitMismatchDollars(e.SubtotalCents))
}

// supplierDepositAllowedOn derives whether the SALE's shape may carry a
// supplier deposit: a credit account's plain on-account ring. Pure, so the
// guard and the preview's answer cannot disagree.
func supplierDepositAllowedOn(tender string, isSplit bool, depositCents int64, termsDays int, creditLimitCents int64) bool {
	return tender == TenderAccount && !isSplit && depositCents == 0 &&
		termsDays > 0 && creditLimitCents > 0
}

// supplierDepositDocumentLine is the deposit document's own line: the deposit
// amount, positive, billed once. It borrows the first goods line's units
// rather than naming one itself: the uoms table is the catalogue's own
// vocabulary and a synthetic line inventing a unit would be a second source of
// truth for it.
func supplierDepositDocumentLine(lines []orderLineRow, cents int64) orderLineRow {
	l := orderLineRow{
		LineNo:         1,
		Description:    "Supplier deposit on a special order",
		QtyEntered:     1,
		QtyBase:        1,
		UnitPriceCents: cents,
		LineTotalCents: cents,
	}
	if len(lines) > 0 {
		l.UOMEntered, l.PriceUOM = lines[0].UOMEntered, lines[0].PriceUOM
	}
	return l
}

// supplierDepositPointerLine is the line the REST's invoice carries beside the
// unchanged goods lines: zero cents, naming the deposit document. The schema
// forbids a negative line on an INVOICE (0021 and 0146's trigger, the sign
// discipline), so the deposit is NOT struck off a goods line and the rest's
// lines are the goods at their real prices; the pointer line says, on the
// document, where the deposit lives, and the header's subtotal and total carry
// the rest. The gap between the goods lines' sum and the header's subtotal is
// the deposit, named on the document by this line. The words say so (item 9
// Opus P3-5): the amount, the document it lives on, and that it is not in this
// invoice's total, so every register detail, copy or listing that shows the
// lines reads the zero row as the explanation of the gap, not an arithmetic
// error. No line report counts it: it has no product and no money.
func supplierDepositPointerLine(lines []orderLineRow, depositInvoiceNo string, depositCents int64) orderLineRow {
	l := orderLineRow{
		LineNo: len(lines) + 1,
		Description: "Supplier deposit of " + splitMismatchDollars(depositCents) + ", billed on its own invoice " +
			depositInvoiceNo + ", is not part of this invoice's total",
		QtyEntered: 1,
		QtyBase:    1,
	}
	if len(lines) > 0 {
		l.UOMEntered, l.PriceUOM = lines[0].UOMEntered, lines[0].PriceUOM
	}
	return l
}

// SupplierDepositOption is the preview's answer for the supplier deposit:
// whether the counter may mark one, and the words its line prints.
type SupplierDepositOption struct {
	Allowed bool   `json:"allowed"`
	Words   string `json:"words,omitempty"`
}

// previewSupplierDeposit answers the allowance for the preview: the same
// credit shape the ring's guard holds (an account that books on account, with
// no hold; a held credit account is cash or card only, the hold rule's own
// posture, and its deposit is taken at the counter like any pay before release
// account's).
func (s *Service) previewSupplierDeposit(ctx context.Context, cust customerRec) *SupplierDepositOption {
	if !supplierDepositAllowedOn(TenderAccount, false, 0, cust.PaymentTermsDays, cust.CreditLimitCents) {
		return &SupplierDepositOption{Allowed: false}
	}
	held := true
	if s.holds != nil {
		if ok, err := s.holds.HasActiveHold(ctx, cust.ID); err == nil {
			held = ok
		}
		// An unreadable hold keeps held=true, the preview's fail closed posture.
	}
	if held {
		return &SupplierDepositOption{Allowed: false}
	}
	return &SupplierDepositOption{Allowed: true, Words: SupplierDepositWords}
}
