package tasks

// yard_short.go, a short pick is a recorded fact with a reason, and it reaches the order.
//
// THE RULE. Recording fewer than the ticket asked for needs a reason (a short list of codes, plus a
// note when the code is OTHER). The shortfall then becomes a backorder on the order line and the
// order's quantity to fulfil and money drop with it, so the invoice bills what went out. The tasks
// module owns none of that: it asks the order module through the narrow port below, which the
// composition root satisfies (the OrderLineWriter precedent in weight.go).
//
// NOBODY RAISES A QUANTITY. A picked quantity above the ordered one is refused here against the
// ticket's number and again in the order module against the order line's own, so a client that sends
// a doctored ticket quantity still cannot talk its way past the order.

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Short pick reason codes, the picked_lines CHECK set (0365). Add, never rename.
const (
	ShortReasonCondensed       = "CONDENSED_SKIDS"
	ShortReasonDamaged         = "DAMAGED"
	ShortReasonNotInYard       = "NOT_IN_YARD"
	ShortReasonCountCorrection = "COUNT_CORRECTION"
	ShortReasonOther           = "OTHER"
)

// ShortReasons is the closed list, in the order a screen shows it.
var ShortReasons = []string{
	ShortReasonCondensed, ShortReasonDamaged, ShortReasonNotInYard, ShortReasonCountCorrection, ShortReasonOther,
}

const maxShortNote = 400

// ErrOrderRefused wraps an order module refusal that is a state of the order, not a malformed
// request: the order cannot take a pick in its status, or the line has already gone out. Mapped to 409.
var ErrOrderRefused = errors.New("tasks: the order cannot take that pick")

// ErrAboveOrdered is a picked quantity above what was ordered. The yard may record less, never more.
var ErrAboveOrdered = errors.New("tasks: that is more than was ordered, the yard cannot raise a quantity")

// YardOrderChanges is the consumer-declared port over the order module's yard effects. Un-wired, a
// short pick is still recorded with its reason and simply does not reach the order, the gap the
// composition root asserts at boot.
type YardOrderChanges interface {
	// ApplyPicks sets the quantity to fulfil on each named order line to the quantity picked (the
	// ABSOLUTE number, so a replay converges) and holds the rest as a backorder. It refuses a pick
	// above the ordered quantity with ErrAboveOrdered.
	ApplyPicks(ctx context.Context, in YardPickChange) (YardPickResult, error)
}

// YardPickChange is what the yard recorded, for the order module to act on.
type YardPickChange struct {
	OrderID string
	ActorID string
	Lines   []YardPickLine
}

// YardPickLine is one recorded line. Quantities are decimal strings in the ticket line's own unit.
type YardPickLine struct {
	LineNo     int
	QtyOrdered string
	QtyPicked  string
	UOM        string
	Reason     string
	Note       string
}

// YardPickResult answers what the order module changed, line by line, and the order's own facts a
// notice needs.
type YardPickResult struct {
	Order YardOrderFacts
	Lines []YardLineEffect
}

// YardLineEffect is one line's outcome. Changed is false for a record that matched what the order
// already held, so a repeat is never announced twice.
type YardLineEffect struct {
	LineNo         int
	Description    string
	UOM            string
	QtyOrdered     string
	QtyPicked      string
	QtyBackordered string
	Changed        bool
}

// YardOrderFacts is what the order module knows that a notice needs: who the order is for and whose
// it is, what its total was and is, and how it stands against its invoice.
type YardOrderFacts struct {
	OrderNo          string
	CustomerID       string
	BranchID         string
	SalespersonID    string
	TotalBeforeCents int64
	TotalAfterCents  int64
	InvoiceNo        string
	InvoicedCents    int64
	PaidCents        int64
	// OwedBackCents is money the customer has paid that the order no longer asks for; DueCents is
	// money the order now asks for beyond what the customer has paid on an invoiced order.
	OwedBackCents int64
	DueCents      int64
}

// WithYardOrderChanges injects the order port (main.go wires the order service adapter).
func (s *Service) WithYardOrderChanges(p YardOrderChanges) *Service { s.yardOrders = p; return s }

// YardOrderChangesWired reports whether the port is bound, asserted at boot.
func (s *Service) YardOrderChangesWired() bool { return s.yardOrders != nil }

// validShortReason says whether a code is on the list.
func validShortReason(code string) bool {
	for _, c := range ShortReasons {
		if c == code {
			return true
		}
	}
	return false
}

// cleanShortReason holds a line to the rule: short needs a reason, OTHER needs a note, a full pick
// carries none, and nobody picks above the ordered quantity.
func cleanShortReason(l *PickedLineInput) error {
	ordered, _ := strconv.ParseFloat(l.QtyOrdered, 64)
	picked, _ := strconv.ParseFloat(l.QtyPicked, 64)
	l.ShortReason = strings.TrimSpace(l.ShortReason)
	l.ShortNote = strings.TrimSpace(l.ShortNote)
	if len(l.ShortNote) > maxShortNote {
		return httpx.BadRequestf("line %d: the note is too long (max %d)", l.OrderLineNo, maxShortNote)
	}
	switch {
	case picked > ordered:
		return httpx.BadRequestf("line %d: %v", l.OrderLineNo, ErrAboveOrdered)
	case picked < ordered:
		if l.ShortReason == "" {
			return httpx.BadRequestf("line %d is short, give the reason (%s)", l.OrderLineNo, strings.Join(ShortReasons, ", "))
		}
		if !validShortReason(l.ShortReason) {
			return httpx.BadRequestf("line %d: %q is not a short pick reason (%s)", l.OrderLineNo, l.ShortReason, strings.Join(ShortReasons, ", "))
		}
		if l.ShortReason == ShortReasonOther && l.ShortNote == "" {
			return httpx.BadRequestf("line %d: say what happened in the note when the reason is OTHER", l.OrderLineNo)
		}
	default:
		// A full pick carries no reason: a stale one from an earlier short is cleared, not kept.
		l.ShortReason, l.ShortNote = "", ""
	}
	return nil
}
