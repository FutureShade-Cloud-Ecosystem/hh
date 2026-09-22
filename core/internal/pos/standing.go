package pos

// standing.go, the counter's ACCOUNT STANDING: does this customer owe money that is past due?
//
// WHERE THIS CAME FROM. Ryan Bostock, describing how he really does a price check: he starts a
// counter sale, because it gives him the price profile, the inventory and the lead time, "and it
// also gives me an opportunity to ask them to pay their bill." That last clause is the feature. The
// counter is already looking at the account; the one thing it could not see was the reason to ask.
//
// IT IS A FACT, NOT A NAG, AND NOT A GATE. This read answers a number and a bucket. It never
// blocks, never warns, never returns a message, and it is deliberately incapable of doing so, the
// type has no verdict field to set. Whether a sale may proceed is the credit gate's question
// (billing/credit_gate.go, untouched here), and the counter's own refusal voice is already wired to
// it. Two systems both saying "this account is behind", one of them advisory, is how a counter
// person learns to ignore both.
//
// AND IT IS NOTHING AT ALL FOR MOST TICKETS. A walk-in has no account. An account that is current
// answers with nothing to say. The zero value marshals as `{}`, no fields, so no chip, so no
// pixel spent on the ordinary case.
//
// THE GATE IS AT THE MARSHAL LAYER (0181). A customer's outstanding balance is money about a third
// party; who may see it is the dealer's decision, taken in pos_policy.balance_visibility and
// applied HERE, by returning a value with no fields in it. Not filtered downstream, not hidden in
// CSS, not sent-and-suppressed: absent from the bytes.

import (
	"context"
	"net/http"
)

// AccountStanding is one customer's AR position as the counter needs it, and no more of it than
// that. There is no invoice list, no per-bucket breakdown beyond the oldest one that is late, and
// no credit limit: the Customer step is answering "is there a reason to ask them to pay?", and
// every extra number on that card is a number the clerk has to read past mid-conversation.
type AccountStanding struct {
	// PastDueCents is what is past the account's terms right now. Absent (0, omitted) when the
	// account is current, which is why the whole struct can be empty and mean "nothing to say".
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

// balanceAllowed answers whether THIS caller may receive the balance fields under the branch
// policy. Same shape as marginAllowed by design, one idea about visibility, applied twice.
func (s *Service) balanceAllowed(ctx context.Context, branchID string) bool {
	caller, err := s.caller(ctx)
	if err != nil {
		return false
	}
	p, err := s.store.ReadPosPolicy(ctx, branchID)
	if err != nil {
		return false
	}
	switch p.BalanceVisibility {
	case BalanceVisibilityManagerUp:
		return caller.IsManager()
	case BalanceVisibilityHidden:
		return false
	default: // ALL_STAFF, the column default, and what a pre-0181 fake resolves to
		return true
	}
}

// AccountStandingInput is GET /api/v1/pos/account-standing.
type AccountStandingInput struct {
	BranchID   string
	CustomerID string
}

// Standing answers the Customer step's one question about money owed.
//
// Every path that cannot answer returns the EMPTY value, and the empty value renders nothing: no
// customer, no reader wired, no AR row, an AR read that failed, a caller the dealer's policy does
// not allow. A counter must never be told a customer is current because a query timed out, and it
// must never be shown a fault banner over a fact it did not ask for, so the failure and the
// good-standing case deliberately look identical from here, and the ONE case that differs is the
// one worth a clerk's attention.
func (s *Service) Standing(ctx context.Context, in AccountStandingInput) (AccountStanding, error) {
	if in.BranchID == "" {
		return AccountStanding{}, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	if in.CustomerID == "" {
		return AccountStanding{}, nil // a walk-in has no account and no balance
	}
	if !s.balanceAllowed(ctx, in.BranchID) {
		// THE GATE: byte-absent. The zero value marshals as `{}`.
		return AccountStanding{}, nil
	}
	if s.aging == nil {
		return AccountStanding{}, nil
	}
	due, err := s.aging.CustomerAging(ctx, in.CustomerID)
	if err != nil || due.PastDueCents <= 0 {
		return AccountStanding{}, nil
	}
	return AccountStanding{
		PastDueCents: due.PastDueCents,
		OldestBucket: due.OldestBucket,
		OnHold:       due.OnHold,
	}, nil
}
