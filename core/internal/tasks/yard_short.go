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
	"fmt"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Short pick reason codes, the picked_lines CHECK set (0369). Add, never rename.
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

// ErrPickClosed is a record onto a pick that is CANCELLED or DONE. Both are terminal in the module's
// matrix (allowedTransitions moves out of neither) and the module has no correction path for a done
// pick, so the record is refused before the order is told anything. → 409, and through sync that op's
// rejection.
var ErrPickClosed = errors.New("tasks: this pick is closed, its record cannot change")

// ErrNotHolder is a Stage or a pick record (Save pick) from a user who does not hold the pick: the claim is
// the holder's, and only the holder moves the pick on or changes what was pulled (UX-76, FROM-PRINCIPAL item
// 47). A shortage report is not guarded: a shortage seen is a fact anyone in the branch may report. The check
// runs inside the transaction that locks the task row, so a claim and a write cannot cross. → 409 with
// ErrNotHolderWords, and through sync that op's rejection.
var ErrNotHolder = errors.New("tasks: someone else is on this pick")

// ErrNotHolderWords is what the REST door answers and the screen shows.
const ErrNotHolderWords = "Someone else is on this pick."

// notHolder reports whether the pick is held by a person other than the actor. An unclaimed pick has no
// holder to protect, so anyone may write it; a held pick answers only to its holder.
func notHolder(claimedBy, actorID string) bool {
	return claimedBy != "" && claimedBy != actorID
}

// ErrNotPickTask is a pick record or skid count submitted against a task that is not a PICK task (for
// example a NEED_WEIGHT, DISPATCH, or OTHER task). The yard handlers, syncpick, and the fake store all
// refuse it before the order is told, because a pick record or a skid count on a non-PICK row would
// reprice an order line that nobody asked the yard to move. → 409, and through sync that op's
// rejection.
var ErrNotPickTask = errors.New("tasks: this is not a pick task")

// pickClosedError is ErrPickClosed with the status in the words.
type pickClosedError struct{ status string }

func (e pickClosedError) Error() string {
	if e.status == StatusCancelled {
		return "tasks: this pick was cancelled, its record cannot change"
	}
	return "tasks: this pick is done, its record cannot change"
}

func (e pickClosedError) Is(target error) bool { return target == ErrPickClosed }

// pickClosed is the refusal for a task in a terminal status, nil for any open one.
func pickClosed(status string) error {
	if status == StatusCancelled || status == StatusDone {
		return pickClosedError{status: status}
	}
	return nil
}

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
	// RecordSkids sets the order's skid count and returns what changed. The same count again is
	// Changed=false.
	RecordSkids(ctx context.Context, in YardSkidChange) (YardSkidResult, error)
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
	Revision       int
	// Reason and Note are what the order holds for the line after the pick (a blank re-save keeps
	// the stored ones, a full pick clears them); the recorded line and the notice read these.
	Reason string
	Note   string
}

// YardOrderFacts is what the order module knows that a notice needs: who the order is for and whose
// it is, what its total was and is, and how it stands against its invoice.
type YardOrderFacts struct {
	OrderID          string
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

// ErrYardOrderUnwired is a yard order change asked for with no order port bound: a skid count has
// nowhere to live without the order, so it is refused rather than recorded and lost. Mapped to 503.
var ErrYardOrderUnwired = errors.New("tasks: the yard's order changes are not configured")

// YardSkidChange is a skid count the yard recorded for an order.
type YardSkidChange struct {
	OrderID   string
	ActorID   string
	SkidCount int
	Reason    string
	Note      string
}

// YardSkidResult is what the order module did with it. CountBefore is nil when no count had been
// recorded. For a skid change the facts' OwedBackCents and DueCents are the skid deposit's.
type YardSkidResult struct {
	Order              YardOrderFacts
	CountBefore        *int
	CountAfter         int
	DepositBeforeCents int64
	DepositAfterCents  int64
	Changed            bool
	ChangeID           string
}

// SkidInput is the yard's skid count request on one pick task.
type SkidInput struct {
	TaskID    string
	ActorID   string
	SkidCount int
	Reason    string
	Note      string
}

// maxSkids is the most skids one order can plausibly leave on; a larger number is a typo.
const maxSkids = 5000

// RecordSkids records or changes the skid count on the task's order, with a reason. Nobody may
// record a negative or implausible count, and every change needs a reason from the same list as a
// short pick, so the office reads one vocabulary.
func (s *Service) RecordSkids(ctx context.Context, in SkidInput) (YardSkidResult, error) {
	if in.TaskID == "" {
		return YardSkidResult{}, httpx.BadRequestf("task id is required")
	}
	if in.SkidCount < 0 {
		return YardSkidResult{}, httpx.BadRequestf("a skid count cannot be negative")
	}
	if in.SkidCount > maxSkids {
		return YardSkidResult{}, httpx.BadRequestf("that skid count is implausibly large")
	}
	in.Reason, in.Note = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Note)
	if len(in.Note) > maxShortNote {
		return YardSkidResult{}, httpx.BadRequestf("the note is too long (max %d)", maxShortNote)
	}
	if in.Reason == "" || !validShortReason(in.Reason) {
		return YardSkidResult{}, httpx.BadRequestf("give the reason for the skid count (%s)", strings.Join(ShortReasons, ", "))
	}
	if in.Reason == ShortReasonOther && in.Note == "" {
		return YardSkidResult{}, httpx.BadRequestf("say what happened in the note when the reason is OTHER")
	}
	if s.yardOrders == nil {
		return YardSkidResult{}, ErrYardOrderUnwired
	}
	task, err := s.store.GetTask(ctx, in.TaskID)
	if err != nil {
		return YardSkidResult{}, err
	}
	// A skid count is a yard-pick measurement: only a PICK task carries one. Recording a skid count
	// against a NEED_WEIGHT / DISPATCH / OTHER task would reprice an order line nobody asked the
	// yard to move (FROM-PRINCIPAL row 2122 item 5 / REVIEW-YS-opus section 4: the demo's NEED_WEIGHT
	// task on DEMO-PLM-O14 lost $8.02 to a stray pick record). Refuse before the order is told.
	if task.TaskType != TypePick {
		return YardSkidResult{}, ErrNotPickTask
	}
	change := YardSkidChange{
		OrderID: task.OrderID, ActorID: in.ActorID, SkidCount: in.SkidCount, Reason: in.Reason, Note: in.Note,
	}
	res, err := s.yardOrders.RecordSkids(ctx, change)
	if err != nil {
		return YardSkidResult{}, err
	}
	if res.Changed {
		key := fmt.Sprintf("yard.skids:%s:%s", task.OrderID, res.ChangeID)
		s.notifyOrderRep(ctx, task, res.Order, key, "yard.skid_change",
			skidMessage(change, res, true), skidMessage(change, res, false))
	}
	return res, nil
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

// cleanShortReason tidies a line's reason and note and holds them to the rule where this module is
// the one deciding: with no order port bound the ticket's quantities are all there is, so a short
// needs a reason, OTHER needs a note, a full pick carries none and nobody picks above the ordered
// quantity. With the port bound the ORDER LINE's own ordered quantity decides all of that (the
// ticket is rebuilt from the line after a short and understates what was ordered), so only the
// shape of what was sent is checked here.
func cleanShortReason(l *PickedLineInput, orderDecides bool) error {
	ordered, _ := strconv.ParseFloat(l.QtyOrdered, 64)
	picked, _ := strconv.ParseFloat(l.QtyPicked, 64)
	l.ShortReason = strings.TrimSpace(l.ShortReason)
	l.ShortNote = strings.TrimSpace(l.ShortNote)
	if len(l.ShortNote) > maxShortNote {
		return httpx.BadRequestf("line %d: the note is too long (max %d)", l.OrderLineNo, maxShortNote)
	}
	if l.ShortReason != "" && !validShortReason(l.ShortReason) {
		return httpx.BadRequestf("line %d: %q is not a short pick reason (%s)", l.OrderLineNo, l.ShortReason, strings.Join(ShortReasons, ", "))
	}
	if orderDecides {
		return nil
	}
	switch {
	case picked > ordered:
		return httpx.BadRequestf("line %d: %v", l.OrderLineNo, ErrAboveOrdered)
	case picked < ordered:
		if l.ShortReason == "" {
			return httpx.BadRequestf("line %d is short, give the reason (%s)", l.OrderLineNo, strings.Join(ShortReasons, ", "))
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
