package purchasing

// lifecycle.go, the PO transition matrix, as a pure function over facts.
//
// TWO KINDS OF MOVE, AND WHY THEY ARE SEPARATED.
//
//   OPERATOR-DRIVEN  DRAFT→ORDERED, →CANCELLED, →CLOSED. A person decides these, so they arrive as an
//                    explicit request and are checked here.
//   RECEIPT-DRIVEN   →PARTIAL, →RECEIVED. Nobody decides these; they are a COUNT of what landed.
//                    StatusAfterReceipt computes them and no endpoint can request one.
//
// Collapsing the two would let a clerk mark a PO RECEIVED without receiving anything, which is precisely the
// control the three-way match depends on, since the match measures billed against RECEIVED.

import "fmt"

// Transition is a requested operator-driven lifecycle move plus the facts that make it legal or not.
type Transition struct {
	From string
	To   string
	// AnyReceived: at least one unit has landed against this PO.
	AnyReceived bool
	// HasLines: the PO has at least one line.
	HasLines bool
	// AllReceived: every line is fully received (used only to reject a premature CLOSED-vs-RECEIVED mix-up).
	AllReceived bool
}

// operatorMoves is the matrix of moves a PERSON may request. Everything absent is refused.
//
// The rulings encoded here, each of which a mutation test pins:
//
//	DRAFT→ORDERED       placing the order. Requires at least one line: an empty PO sent to a vendor is a
//	                    document that promises nothing and can never be matched against.
//	DRAFT→CANCELLED     abandoning a draft. Always legal, nothing was promised.
//	ORDERED→CANCELLED   legal ONLY while nothing has been received. Once goods are on the yard the PO is the
//	                    only record of what we owe for them; cancelling it would strand the GRNI accrual with
//	                    no document behind it. Short-shipped orders are CLOSED instead.
//	ORDERED→CLOSED      the vendor is never going to ship the rest (or we changed our mind after a partial).
//	PARTIAL→CLOSED      the same, after some of it landed. This is the honest end state for a short shipment.
//	RECEIVED→CLOSED     filing a fully-received PO away.
//
// NOT in the matrix, deliberately:
//
//	DRAFT→RECEIVED / DRAFT→PARTIAL     goods cannot arrive against an order that was never placed.
//	ORDERED→PARTIAL / ORDERED→RECEIVED receipt-driven; see StatusAfterReceipt.
//	CLOSED→*, CANCELLED→*              both terminal. Reopening a closed PO would let a bill be matched
//	                                   against quantities that were already reconciled and filed.
//	*→DRAFT                            a placed PO never becomes editable again.
var operatorMoves = map[string]map[string]bool{
	StatusDraft:     {StatusOrdered: true, StatusCancelled: true},
	StatusOrdered:   {StatusCancelled: true, StatusClosed: true},
	StatusPartial:   {StatusClosed: true},
	StatusReceived:  {StatusClosed: true},
	StatusClosed:    {},
	StatusCancelled: {},
}

// KnownStatus reports whether s is one of the six lifecycle states.
func KnownStatus(s string) bool {
	switch s {
	case StatusDraft, StatusOrdered, StatusPartial, StatusReceived, StatusClosed, StatusCancelled:
		return true
	}
	return false
}

// CheckTransition returns nil when the operator-driven move is legal, else a 409 naming exactly why. The
// message is written for the person who clicked the button, not for a log grepper.
func CheckTransition(t Transition) error {
	if !KnownStatus(t.From) {
		return conflict("purchasing: %q is not a purchase-order status", t.From)
	}
	if !KnownStatus(t.To) {
		return badReq("purchasing: %q is not a purchase-order status", t.To)
	}
	if t.From == t.To {
		return conflict("purchasing: purchase order is already %s", t.From)
	}
	// The receipt-driven states are never requestable. Saying so explicitly beats "not allowed", because the
	// caller's next question is always "then how do I get there".
	if t.To == StatusPartial || t.To == StatusReceived {
		return conflict("purchasing: %s is reached by receiving goods, not by a status change", t.To)
	}
	if !operatorMoves[t.From][t.To] {
		return conflict("purchasing: cannot move a %s purchase order to %s", t.From, t.To)
	}
	if t.To == StatusOrdered && !t.HasLines {
		return conflict("purchasing: cannot order an empty purchase order, add at least one line")
	}
	if t.To == StatusCancelled && t.AnyReceived {
		return conflict("purchasing: cannot cancel a purchase order that has received goods, close it instead")
	}
	return nil
}

// CanEditLines reports whether lines may be added or removed. Only a DRAFT. Once a PO is ORDERED its line set
// is the agreement the three-way match measures against; editing it after the fact would silently redefine
// what "as ordered" means for bills already in flight.
func CanEditLines(status string) bool { return status == StatusDraft }

// CheckReceivable returns nil when a receipt may be recorded against a PO in this status.
func CheckReceivable(status string) error {
	switch status {
	case StatusOrdered, StatusPartial:
		return nil
	case StatusDraft:
		return conflict("purchasing: cannot receive against a DRAFT purchase order, place the order first")
	case StatusReceived:
		return conflict("purchasing: purchase order is already fully received")
	case StatusClosed:
		return conflict("purchasing: purchase order is closed")
	case StatusCancelled:
		return conflict("purchasing: purchase order was cancelled")
	default:
		return conflict("purchasing: cannot receive against a %s purchase order", status)
	}
}

// LineProgress is one line's ordered-vs-received pair, the only input StatusAfterReceipt needs.
type LineProgress struct {
	QtyOrderedBase  float64
	QtyReceivedBase float64
}

// StatusAfterReceipt is the receipt-driven status: RECEIVED when every line has landed in full, else PARTIAL.
//
// The epsilon matters. Quantities are DECIMAL(19,4) round-tripped through float64, so a line ordered at
// 20.0000 and received at 20.0000 can compare as 19.9999999999, and a PO that can never reach RECEIVED sits
// on the receiving worklist forever, and its GRNI never clears. A PO with no lines is PARTIAL, not RECEIVED:
// "everything arrived" is a false claim about nothing.
func StatusAfterReceipt(lines []LineProgress) string {
	if len(lines) == 0 {
		return StatusPartial
	}
	for _, l := range lines {
		if l.QtyReceivedBase < l.QtyOrderedBase-qtyEpsilon {
			return StatusPartial
		}
	}
	return StatusReceived
}

// CheckReceiptQty validates one requested receive quantity against the line's outstanding balance and returns
// the over-receipt refusal with both numbers in it. Over-receiving is a 409 rather than a clamp: the excess is
// either a vendor error or a counting error, and silently absorbing it into stock at the PO's price would put
// inventory Dibbits never agreed to buy onto the balance sheet at a price nobody quoted.
func CheckReceiptQty(lineNo int, qty, ordered, alreadyReceived float64) error {
	if qty <= 0 {
		return badReq("purchasing: receive quantity on line %d must be greater than zero", lineNo)
	}
	outstanding := ordered - alreadyReceived
	if qty > outstanding+qtyEpsilon {
		return conflict("purchasing: over-receipt on line %d, %s requested but only %s outstanding",
			lineNo, fmtQty(qty), fmtQty(outstanding))
	}
	return nil
}

func fmtQty(q float64) string { return fmt.Sprintf("%.4f", q) }
