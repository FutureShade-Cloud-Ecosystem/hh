package pos

// standing.go, the counter's ACCOUNT STANDING: does this customer owe money that is past due?
//
// WHERE THIS CAME FROM. Ryan Bostock, describing how he really does a price check: he starts a
// counter sale, because it gives him the price profile, the inventory and the lead time, "and it
// also gives me an opportunity to ask them to pay their bill." That last clause is the feature. The
// counter is already looking at the account; the one thing it could not see was the reason to ask.
//
// IT IS A FACT, NOT A NAG, AND NOT A GATE. This read answers a standing word, a number and a
// bucket. It never blocks, never warns, never returns a message, and it is deliberately incapable
// of doing so, the type has no verdict field to set. Whether a sale may proceed is the credit
// gate's question (billing/credit_gate.go, untouched here), and the counter's own refusal voice is
// already wired to it. Two systems both saying "this account is behind", one of them advisory, is
// how a counter person learns to ignore both.
//
// EVERY CASE ANSWERS DISTINCTLY. The first form of this read answered the same empty object for a
// walk-in, a clear account, a caller the policy refused and an AR read that failed, so a client
// could not tell "nothing to say" from "could not say". The Quote Desk needs the difference before
// it lets a quote become an order. The standing word names the case: walk_in (no customer named),
// clear (an account with nothing past due and no hold), past_due (money past the terms), on_hold
// (an active credit hold, whether or not anything is past due), and withheld (round 4, principal
// item 206: a caller the dealer's balance policy strips the figures from, whose answer never names
// a position). A caller the policy withholds is answered WITHOUT its money (round 3 of the UX-97
// visual review): the overdue figure and its bucket never enter the bytes, the same field absence
// the customer position answers with for limit, balance and available. The hold is the same for
// every caller, decided from the same sources, the aging-derived flag included, and it is the one
// case a withheld caller can still read. A branch the caller does not reach is a 403, an
// account that does not exist a 404, a malformed id a 400, a dealer who switched the surface off
// a 403, and a read that could not be made an error status: never an empty 200 that reads as
// "current". The three older fields stay exactly as they were and are present exactly when they
// were, so the Counter Till, which reads them by name, keeps working; it already treats any
// non-2xx answer as an empty standing.
//
// THE GATE IS AT THE MARSHAL LAYER (0181). A customer's outstanding balance is money about a third
// party; who may see it is the dealer's decision, taken in pos_policy.balance_visibility and
// applied HERE, by leaving the figures out of the bytes for a caller they are withheld from. Not
// filtered downstream, not hidden in CSS, not sent-and-suppressed: the figure is never put in the
// bytes.

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// The standing words. A closed set, so a client can switch on it.
const (
	StandingWalkIn   = "walk_in"
	StandingClear    = "clear"
	StandingPastDue  = "past_due"
	StandingOnHold   = "on_hold"
	StandingWithheld = "withheld"
)

// ErrStandingUnavailable answers a standing read that could not be made: a seam the composition
// root did not wire, a policy or AR or hold read that failed. The handler maps it to 503, never to
// an empty answer that would read as a clear account.
var ErrStandingUnavailable = errors.New("pos: the account standing could not be read")

// AccountStanding is one customer's AR position as the counter needs it, and no more of it than
// that. There is no invoice list, no per-bucket breakdown beyond the oldest one that is late, and
// no credit limit: the Customer step is answering "is there a reason to ask them to pay?", and
// every extra number on that card is a number the clerk has to read past mid-conversation.
type AccountStanding struct {
	// Standing names the case, one of the Standing words above. It is always present.
	Standing string `json:"standing"`
	// PastDueCents is what is past the account's terms right now. Absent (0, omitted) when the
	// account is current.
	PastDueCents int64 `json:"past_due_cents,omitempty"`
	// OldestBucket names how late the oldest past-due money is, in the aging vocabulary the AR
	// screens already use: "30", "60", "90+". Present only alongside a balance.
	OldestBucket string `json:"oldest_bucket,omitempty"`
	// OnHold mirrors the credit gate's own answer so the counter does not have to infer a hold from
	// a balance. It is still not a refusal here, the ring is where a hold is enforced.
	OnHold bool `json:"on_hold,omitempty"`
}

// AgingReader is the billing module's single-account AR read, as POS needs it. Declared as an
// interface (not imported concretely) for the reason every seam in this package is: a unit test
// answers one without a database, and an unwired composition root leaves the read absent rather
// than panicking.
//
// The adapter behind it in the composition root does NOT compute anything, it calls
// billing.CreditService.CustomerAging and reads AgingRow.OverdueCents() / OldestOverdueBucket().
// billing/customer_ar_read.go states at length why one definition of "past due" exists in this
// system and why a second, smaller, equivalent query is the mistake to avoid; a number a clerk
// reads out loud to a customer is the last place that drift is affordable.
type AgingReader interface {
	CustomerAging(ctx context.Context, customerID string) (PastDue, error)
}

// PastDue is the aging reader's answer, in this package's own words so the seam does not drag the
// whole billing model across it.
type PastDue struct {
	PastDueCents int64
	OldestBucket string
	OnHold       bool
}

// WithAgingReader wires (or overrides) the AR read. An unwired reader is not an error: the counter
// simply has nothing to say about standing, which is exactly what it says today.
func (s *Service) WithAgingReader(a AgingReader) *Service { s.aging = a; return s }

// HoldReader answers whether a customer carries an ACTIVE credit hold: the very fact the credit
// gate refuses an ACCOUNT tender on (till.Repository.HasActiveHold satisfies it). It is read
// beside the aging read, not out of it, because the aging read drops an account with no open
// invoices, and a hold placed on such an account must still show.
type HoldReader interface {
	HasActiveHold(ctx context.Context, customerID string) (bool, error)
}

// AccountChecker answers whether a customer account exists AND is reachable by the caller on the
// context, so an unknown id and an account at a branch the caller cannot reach are the same 404 and
// never a "clear" account or a hold word. The customer module owns the table and the reach fence;
// the composition root adapts it.
type AccountChecker interface {
	AccountExists(ctx context.Context, customerID string) (bool, error)
}

// WithHoldReader wires the hold read behind the standing read.
func (s *Service) WithHoldReader(h HoldReader) *Service { s.holds = h; return s }

// WithAccountChecker wires the account existence check behind the standing read.
func (s *Service) WithAccountChecker(a AccountChecker) *Service { s.accounts = a; return s }

// PositionGate reports whether the caller on ctx may read the customer's account position figures
// (credit limit, open balance, available credit, past due totals and aging bucket) at branchID.
// Satisfied by *billing.AccountPositionGate. A nil gate fails closed.
type PositionGate interface {
	AccountPositionAllowed(ctx context.Context, branchID string) bool
}

// WithPositionGate binds the dealer's account position policy gate. A nil gate fails closed.
func (s *Service) WithPositionGate(g PositionGate) *Service { s.position = g; return s }

func (s *Service) positionAllowed(ctx context.Context, branchID string) bool {
	if s == nil || s.position == nil {
		return false
	}
	return s.position.AccountPositionAllowed(ctx, branchID)
}

// balanceGate is what the dealer's policies say about THIS caller on THIS branch.
type balanceGate int

const (
	// balanceOpen: the counter standing surface is enabled and the caller is admitted by the
	// dealer's account position policy (ar_invoice_policy.account_position_visibility). The read
	// answers in full.
	balanceOpen balanceGate = iota
	// balanceFiguresWithheld: the counter standing surface is enabled, and the dealer's account
	// position policy withholds figures from this caller. The read answers the hold state or the
	// withheld word, and never puts the overdue figure, its bucket, or the clear/past_due word in
	// the bytes.
	balanceFiguresWithheld
	// balanceOff: pos_policy.balance_visibility is HIDDEN. The surface is off for everyone, and
	// the answer is a 403.
	balanceOff
)

// balanceGateFor resolves the counter standing surface switch (pos_policy.balance_visibility =
// HIDDEN is a 403 for everyone) and the dealer's one account position policy gate
// (ar_invoice_policy.account_position_visibility, MANAGER_UP by default) behind the branch wall
// (X2: a branch the caller's resolved authority does not reach is REFUSED with the wall's 403,
// never answered with an empty card). A caller who cannot be resolved and a policy that cannot be
// read are errors, not a quiet "no": the read is refused or fails, it is never answered empty.
func (s *Service) balanceGateFor(ctx context.Context, branchID string) (balanceGate, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return balanceOff, err // the 401 an unresolved caller earns, never an empty card
	}
	if err := requireBranchReach(caller, branchID); err != nil {
		return balanceOff, err
	}
	p, err := s.store.ReadPosPolicy(ctx, branchID)
	if err != nil {
		return balanceOff, errors.Join(ErrStandingUnavailable, err)
	}
	if p.BalanceVisibility == BalanceVisibilityHidden {
		return balanceOff, nil
	}
	if s.positionAllowed(ctx, branchID) {
		return balanceOpen, nil
	}
	return balanceFiguresWithheld, nil
}

// AccountStandingInput is GET /api/v1/pos/account-standing.
type AccountStandingInput struct {
	BranchID   string
	CustomerID string
}

// Standing answers the Customer step's one question about money owed.
//
// Every case answers with its own word or its own error status, see the file header. The order of
// the checks is the order of what the caller is owed: a branch is required (400); a caller who
// does not reach the branch is refused (403) before any account is touched, so a wall refusal
// never confirms an account exists, and a dealer who switched the surface off is refused with it;
// then the account itself (400 malformed, 404 unknown); then the facts, any of which failing is a
// 503. A caller whom the policy strips the figures from still gets the hold state, under the word
// `withheld` unless the account is on hold, and never the overdue figure or its bucket: those
// bytes are simply never written.
func (s *Service) Standing(ctx context.Context, in AccountStandingInput) (AccountStanding, error) {
	if in.BranchID == "" {
		return AccountStanding{}, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	if in.CustomerID != "" {
		if _, perr := uuid.Parse(in.CustomerID); perr != nil {
			return AccountStanding{}, clientErr(http.StatusBadRequest, "pos: customer_id must be a uuid")
		}
	}
	gate, err := s.balanceGateFor(ctx, in.BranchID)
	if err != nil {
		// The branch wall (X2) and the unreadable policy: a refusal or a failure, never an empty card.
		return AccountStanding{}, err
	}
	if gate == balanceOff {
		return AccountStanding{}, clientErr(http.StatusForbidden, "pos: this caller may not see account balances")
	}
	if in.CustomerID == "" {
		return AccountStanding{Standing: StandingWalkIn}, nil // a walk-in has no account and no balance
	}
	if s.aging == nil || s.holds == nil || s.accounts == nil {
		return AccountStanding{}, errors.Join(ErrStandingUnavailable, errors.New("a standing seam is not wired"))
	}
	exists, err := s.accounts.AccountExists(ctx, in.CustomerID)
	if err != nil {
		return AccountStanding{}, errors.Join(ErrStandingUnavailable, err)
	}
	if !exists {
		return AccountStanding{}, errCustomerNotFound // the one customer answer (customerFor): unreachable and unknown alike
	}
	held, err := s.holds.HasActiveHold(ctx, in.CustomerID)
	if err != nil {
		return AccountStanding{}, errors.Join(ErrStandingUnavailable, err)
	}
	if gate == balanceFiguresWithheld {
		// The policy withholds this caller's money, and "clear" is a position like any other: the
		// answer is `withheld` unless the account is on hold, and the hold is decided from the SAME
		// sources an admitted caller's is, the aging-derived flag included (round 4, principal item
		// 206). The aging read runs for that flag alone: no figure and no bucket it returns ever
		// enters the answer's bytes.
		due, err := s.aging.CustomerAging(ctx, in.CustomerID)
		if err != nil {
			return AccountStanding{}, errors.Join(ErrStandingUnavailable, err)
		}
		out := AccountStanding{Standing: StandingWithheld}
		if held || due.OnHold {
			out.Standing = StandingOnHold
			out.OnHold = true
		}
		return out, nil
	}
	due, err := s.aging.CustomerAging(ctx, in.CustomerID)
	if err != nil {
		return AccountStanding{}, errors.Join(ErrStandingUnavailable, err)
	}
	out := AccountStanding{Standing: StandingClear}
	if due.PastDueCents > 0 {
		out.Standing = StandingPastDue
		out.PastDueCents = due.PastDueCents
		out.OldestBucket = due.OldestBucket
	}
	if held || due.OnHold {
		out.Standing = StandingOnHold
		out.OnHold = true
	}
	return out, nil
}
