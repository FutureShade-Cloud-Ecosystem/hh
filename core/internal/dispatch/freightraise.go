package dispatch

// freightraise.go, the second-truck gate: the INVARIANT, the parked proposal, and the one method
// that is allowed to put a freight figure on a payload.
//
// THE CLIENT'S SENTENCE, which is the whole specification: "If there is a situation where they're
// adding a second truck that's unforeseen and there's some cost that you're trying to pass on to the
// customer they weren't originally quoted on, we'll have some sort of approval gate for that."
//
// READ THAT SENTENCE FOR WHAT IT DOES *NOT* SAY. It does not say "approve every split". It does not
// say "approve every extra truck". The thing that needs a manager is the LAST CLAUSE, a cost the
// customer was not quoted on. Everything else is dispatch doing its job:
//
//   - splitting an order across three trucks and dividing the SAME freight three ways changes what
//     each load's paperwork says and changes nothing the customer pays. It sails through.
//   - adding a fourth truck at no extra charge is a yard decision about vehicles. It sails through.
//   - an order that never came from a quote has no baseline to be above. NULL is not zero, 0194 says
//     so at length, and an order with no baseline is NEVER gated.
//
// So the gate is on the INVARIANT, "the customer-facing freight would rise above what the customer
// was quoted", and not on any of the events that usually accompany it. An event-shaped gate ("gate
// the split") would refuse the ordinary work all day and still miss a raise typed in without one.

import (
	"strings"
	"time"
)

// FreightRaiseState is the parked proposal's lifecycle, mirroring delivery_freight_raises.state. The
// values are a MIRROR of a decision owned by permission_approval_requests, not a state machine this
// module drives: PARKED moves to APPLIED or REFUSED only inside the transaction that stamps the
// approval, and nothing in this package moves it on its own authority.
const (
	RaiseParked  = "PARKED"
	RaiseApplied = "APPLIED"
	RaiseRefused = "REFUSED"
)

// FreightRaise is one parked proposal, a rise of a customer's delivery charge that somebody has
// asked for and nobody has agreed to. It is the row, whole; the gated payload is FreightRaiseView.
type FreightRaise struct {
	ID           string
	OrderID      string
	BranchID     string
	OrderNo      string
	CustomerName string
	// The three figures, and the third is the one people leave out (0195's header argues it): where
	// the charge actually STOOD when the ask was made, so a re-split of an already-approved raise
	// does not ask an approver to decide the same money a second time.
	QuotedCents   int64
	CurrentCents  int64
	ProposedCents int64
	Loads         int
	// Amounts is the dispatcher's own per-load shares, or nil meaning "divide it evenly at apply
	// time". nil is NOT an empty slice: an even split has to be recomputed against the loads that
	// exist when it is applied, and freezing it here would be an arithmetic answer stored before the
	// question was final.
	Amounts         []int64
	RequestedBy     string
	RequestedByRole string
	RequestedAt     time.Time
	State           string
	ApprovalID      string
	DecidedBy       string
	DecidedAt       *time.Time
}

// GatesFreightRaise is THE INVARIANT, as a pure function of four numbers, so that it can be read,
// argued with, and tested without a database, a caller, or an HTTP request.
//
// It answers: would applying this split raise what the customer pays for delivery above what they
// were quoted, for the first time?
//
//	proposed     what the customer-facing freight would become
//	current      what it is now (orders.freight_cents, the opening actual, 0194)
//	quoted       what the customer was shown (orders.quoted_freight_cents, the frozen baseline)
//	quotedKnown  false when that baseline is SQL NULL
//
// THREE ARMS, EACH ONE AN ACCEPTANCE CLAUSE:
//
//  1. !quotedKnown → never gated. NULL means the order never came from a quote, so there is nothing
//     to be above. 0194 chose a NULLable column precisely so this case is expressible, and its
//     header forbids reading the column through a "<= 0 is unknown" rule, which is why this
//     function takes a separate boolean instead of a sentinel. A quoted-zero order (free delivery
//     the customer was promised) is quotedKnown=true, quoted=0, and IS gated the moment freight
//     appears on it. That is the case the gate exists for.
//
//  2. proposed <= quoted → never gated. Splitting the quoted freight over more trucks, in any
//     division, is the ordinary work. So is lowering it. The customer pays what they were told.
//
//  3. proposed <= current → never gated, and this is the arm that keeps approvers honest. If the
//     charge already stands above the quote, somebody already approved that rise: `current` can only
//     have got there through this gate (quote/convert.go stamps freight = quoted, and the apply path
//     below is the only other writer). Re-dividing that same money across the same trucks is not a
//     new ask, and gating it would put the identical decision in front of the identical approver a
//     second time, which is exactly how an approver learns to click through without reading.
//
// Everything else is a raise above the quote that nobody has agreed to yet.
func GatesFreightRaise(proposed, current, quoted int64, quotedKnown bool) bool {
	if !quotedKnown {
		return false
	}
	if proposed <= quoted {
		return false
	}
	return proposed > current
}

// - the payload, and the gate on it -

// FreightRaiseMoney is the money on a freight raise, and it exists as its OWN TYPE for the reason
// LoadStop exists (model.go says it at length): a gated figure that rides on a general payload is a
// figure somebody has to remember to blank in every read path, and the one they forget is the leak.
//
// Nothing constructs this type except raiseView below.
type FreightRaiseMoney struct {
	QuotedCents   int64 `json:"quoted_freight_cents"`
	CurrentCents  int64 `json:"current_freight_cents"`
	ProposedCents int64 `json:"proposed_freight_cents"`
	// DeltaCents is proposed − QUOTED: how far above the delivery the customer was shown this charge
	// would land. That is the figure the client's sentence names, "some cost that you're trying to
	// pass on to the customer they weren't originally quoted on", and it is the question the approver
	// is actually being asked, which is why it is this subtraction and not proposed − current.
	//
	// It is computed on the server because a subtraction done in a browser is a subtraction that can
	// disagree with the one the record was written from.
	DeltaCents int64 `json:"delta_cents"`
}

// FreightRaiseView is a parked proposal as a surface sees it. Every field on it is safe for any
// caller who may see the load board at all, EXCEPT Money, which is a gated pointer and is absent by
// TYPE for a caller the dealer's load-value policy refuses. Absent: no key, no zero, no null.
type FreightRaiseView struct {
	ID           string `json:"id"`
	OrderID      string `json:"order_id"`
	OrderNo      string `json:"order_no,omitempty"`
	CustomerName string `json:"customer_name,omitempty"`
	BranchID     string `json:"branch_id"`
	Loads        int    `json:"loads"`
	// AskedBy is a WORD, never a role code: "a dispatch manager", "the general manager". The same
	// actorWord the policy trail uses, so the two surfaces name the same person the same way and no
	// screen has to dress a role enum itself.
	AskedBy string    `json:"asked_by"`
	AskedAt time.Time `json:"asked_at"`
	// State is the mirrored decision. It is an enum on the wire and the surface maps it to a word;
	// nothing in this package renders it.
	State      string `json:"state"`
	ApprovalID string `json:"approval_id,omitempty"`

	// THE MONEY. One pointer, one producer, one policy question.
	Money *FreightRaiseMoney `json:"money,omitempty"`
}

// raiseDetail is the sentence that goes on the APPROVAL ROW, and every word it does not contain is
// deliberate: it carries no figures.
//
// The approvals list (GET /api/v1/staff/approvals) is open to every authenticated staff member and
// has no money gate of its own, it was built for margin overrides and journal thresholds, which are
// internal numbers. A customer's delivery charge is not. So the queue gets a subject and a reason,
// the figures stay behind dispatch's own visibility policy, and a card that wants to show them asks
// for them separately and is refused if it may not have them.
func raiseDetail(orderNo, customerName string) string {
	subject := strings.TrimSpace(orderNo)
	if subject == "" {
		subject = "a delivery"
	}
	if who := strings.TrimSpace(customerName); who != "" {
		subject += " for " + who
	}
	return "Freight on " + subject + " is being raised above the delivery the customer was quoted."
}

// sameAsk reports whether a fresh proposal is BYTE-FOR-BYTE the ask that is already parked on this
// order. It exists so that pressing the button twice, or a client retrying a request whose response
// was lost, converges on the one parked raise instead of colliding with the unique index and
// showing the dispatcher a refusal for something that already worked.
//
// It compares the ASK and not the row: an identical proposal filed under a different approver, or
// after the customer's name was edited, is still the same ask.
func sameAsk(parked FreightRaise, proposed int64, loads int, amounts []int64) bool {
	if parked.ProposedCents != proposed || parked.Loads != loads {
		return false
	}
	if len(parked.Amounts) != len(amounts) {
		return false
	}
	for i := range amounts {
		if parked.Amounts[i] != amounts[i] {
			return false
		}
	}
	return true
}
