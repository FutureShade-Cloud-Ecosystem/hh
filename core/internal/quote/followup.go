// followup.go, DIB-58: the quote follow-up cadence, and DIB-77's SMS quote delivery.
//
// -
// THE PROBLEM, IN THE WORDS OF THE PEOPLE WHO HAVE IT.
// A quote goes out. Nothing happens. Three weeks later somebody notices it expired and phones the
// customer, who bought the material somewhere else eleven days ago. Every branch already knows they
// should follow up on day 3 and day 7; nobody has a list of which quotes are on day 3 today.
//
// This is that list, made to act on itself: a per-quote plan (3/7/14 days after the quote was sent),
// a history of what actually went out, and one switch per quote to stop it.
//
// -
// WHAT MAKES IT SAFE TO POINT AT REAL CUSTOMERS.
//
//	IT STOPS ON ITS OWN. A quote that is ACCEPTED, CONVERTED, EXPIRED or LOST cancels its own plan on
//	the next sweep, with the reason recorded. The cadence is finite by construction, three steps, and
//	there is no fourth.
//
//	IT CANNOT SEND TWICE. Every step is claimed by an INSERT against a UNIQUE (quote_id, step_day,
//	channel) constraint (0110) in the SAME transaction as the enqueue. Re-running the sweep is a
//	provable no-op, which matters more here than anywhere else in the bundle: the job runner has no
//	lease and will happily start a second run of a job that is still going.
//
//	IT ASKS BEFORE IT TEXTS. Every SMS step re-checks consent and quiet hours at send time, not at
//	schedule time, so a customer who opts out on day 4 does not get the day-7 text.
//
//	IT DOES NOT SEND ANYTHING BY ITSELF. The schedule that drives it ships DISABLED (0110), the SMS
//	provider is the stub, and the email transport is the dev log notifier.
//
// -
// NO PAYMENT LINKS, EVER (CP-05). Not in the share message, not in a follow-up, not in the email. The
// payload types in this file have no field for one and the ruling is why.
package quote

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Channels a follow-up step can travel on.
const (
	ChannelEmail = "EMAIL"
	ChannelSMS   = "SMS"
	ChannelBoth  = "BOTH"
)

// Step statuses (0110 CHECK).
const (
	StepSent      = "SENT"
	StepSkipped   = "SKIPPED"
	StepCancelled = "CANCELLED"
)

// DefaultOffsets is the house cadence: day 3, day 7, day 14 after the quote was sent.
//
// Three touches, ending before the standard 30-day validity window closes, so the last message can
// still say something useful ("this expires on the 14th") rather than "this expired".
var DefaultOffsets = []int{3, 7, 14}

// FollowUpPlan is one quote's cadence setting (quote_followup_plans, 0110).
type FollowUpPlan struct {
	QuoteID     string     `json:"quote_id"`
	Enabled     bool       `json:"enabled"`
	Channel     string     `json:"channel"`
	OffsetsDays []int      `json:"offsets_days"`
	StartedAt   time.Time  `json:"started_at"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"`
	DisabledWhy string     `json:"disabled_why,omitempty"`
}

// FollowUpEvent is one recorded step (quote_followup_events, 0110). A SKIPPED row with a reason is a
// deliberate part of the history: "we did not text them on day 7 because they had opted out" is
// something a salesperson needs to see before they phone.
type FollowUpEvent struct {
	StepDay      int       `json:"step_day"`
	Channel      string    `json:"channel"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason,omitempty"`
	EventID      string    `json:"event_id,omitempty"`
	ScheduledFor time.Time `json:"scheduled_for"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// FollowUpStep is a step that has NOT happened yet, computed, not stored. The UI shows past and
// future in one list, and a scheduled step is an arithmetic fact about the plan rather than a row
// somebody has to remember to write.
type FollowUpStep struct {
	StepDay   int       `json:"step_day"`
	Channel   string    `json:"channel"`
	DueAt     time.Time `json:"due_at"`
	Scheduled bool      `json:"scheduled"` // always true; present so the UI can render one list
}

// FollowUpHistory is what the quote surface renders: the plan, what happened, and what is coming.
type FollowUpHistory struct {
	QuoteID   string          `json:"quote_id"`
	Plan      *FollowUpPlan   `json:"plan,omitempty"`
	Events    []FollowUpEvent `json:"events"`
	Upcoming  []FollowUpStep  `json:"upcoming"`
	Automated bool            `json:"automated"` // a plan exists AND is enabled
}

// NormaliseOffsets is the single definition of a valid cadence: strictly positive days, ascending,
// de-duplicated, and no more than the house maximum.
//
// It lives in Go rather than in a CHECK constraint because Postgres forbids a subquery in a CHECK and
// "every element of this array is > 0" needs one (0110 says so at the constraint). Enforcing it in one
// function with one named test is better than half-enforcing it in two places that can disagree.
func NormaliseOffsets(in []int) ([]int, error) {
	const maxSteps = 6
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, d := range in {
		if d <= 0 {
			return nil, fmt.Errorf("quote: follow-up offset %d is not a positive number of days", d)
		}
		if d > 365 {
			return nil, fmt.Errorf("quote: follow-up offset %d days is past any quote's life", d)
		}
		if seen[d] {
			continue // a duplicated day is a typo, not a request for two messages on one day
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) > maxSteps {
		return nil, fmt.Errorf("quote: %d follow-up steps is a campaign, not a cadence (max %d)", len(out), maxSteps)
	}
	sort.Ints(out)
	return out, nil
}

// ChannelsOf expands a plan channel into the transports one step travels on.
func ChannelsOf(planChannel string) []string {
	switch planChannel {
	case ChannelSMS:
		return []string{ChannelSMS}
	case ChannelBoth:
		// Email first: it is the channel that always works, and if the SMS step is skipped for consent
		// the customer has still been contacted.
		return []string{ChannelEmail, ChannelSMS}
	default:
		return []string{ChannelEmail}
	}
}

// terminalStatuses are the quote states that END a cadence. A quote in any of them must never receive
// another follow-up: three of the four mean the conversation is over, and the fourth (EXPIRED) means
// the thing we would be following up about no longer exists.
var terminalStatuses = map[string]bool{
	"ACCEPTED": true, "CONVERTED": true, "EXPIRED": true, "LOST": true,
}

// - Seams -
//
// Both are consumer-declared here and satisfied at the composition root, so this package imports
// neither internal/messagingprovider nor internal/customer. That is the same shape as
// quote.CreditGate and tasks.OrderReader.

// ShareNotice is a request to text a customer a quote's share link (DIB-77).
//
// NOTE THE ABSENT FIELD. There is no payment link and no amount-due, deliberately: CP-05 is the portal
// fork and that ruling stands. A struct with no field for a thing cannot be made to carry it by a
// later one-line change.
type ShareNotice struct {
	QuoteID    string
	QuoteNo    string
	CustomerID string
	TotalCents int64
	ValidUntil string
	ShareURL   string
}

// FollowUpNotice is one cadence step's payload.
type FollowUpNotice struct {
	QuoteID    string
	QuoteNo    string
	CustomerID string
	TotalCents int64
	ValidUntil string
	StepDay    int
	ShareURL   string
	Email      string
}

// NoticeQueue is the out-of-band delivery seam. main.go satisfies it over messagingprovider's outbox
// and consent resolver.
//
// SEAM CONTRACT:
//   - Enqueue* return the queue row's id, so a follow-up event can point at the notice it produced.
//   - MaySendSMS answers BOTH consent and quiet hours: ok=false means "never" (no consent, opted out),
//     and ok=true carries the earliest instant the customer may be disturbed. A caller must not treat
//     the zero time as "now" when ok is false.
//   - Nothing here sends. Everything here queues.
type NoticeQueue interface {
	MaySendSMS(ctx context.Context, customerID string, at time.Time) (ok bool, notBefore time.Time, err error)
	EnqueueQuoteShare(ctx context.Context, n ShareNotice, notBefore time.Time) (eventID string, err error)
	EnqueueFollowUpSMS(ctx context.Context, n FollowUpNotice, notBefore time.Time) (eventID string, err error)
	EnqueueFollowUpEmail(ctx context.Context, n FollowUpNotice) (eventID string, err error)
}

// ContactReader supplies the customer's email address and display name for the email step. Satisfied
// at the composition root over internal/customer.
type ContactReader interface {
	PrimaryContact(ctx context.Context, customerID string) (name, email string, err error)
}

// - Errors -

// ErrNoSMSConsent, this customer has not agreed to receive texts (or has opted out). It is returned
// to the STAFF caller of a manual send so the salesperson learns why nothing happened, and it is a
// 409, not a 500: the request was well-formed and the answer is no.
var ErrNoSMSConsent = errors.New("quote: customer has no SMS consent on file (or has opted out)")

// ErrNoticeQueueUnwired, the automation seam is not wired. Manual sends fail loudly; the sweep
// reports SKIPPED.
var ErrNoticeQueueUnwired = errors.New("quote: no notice queue is wired")

// ErrFollowUpNotWired, the follow-up store is not wired.
var ErrFollowUpNotWired = errors.New("quote: follow-up automation is not wired")
