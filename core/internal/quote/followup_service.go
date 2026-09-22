package quote

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// followup_service.go, the automation service: manual SMS quote delivery (DIB-77), the cadence sweep
// (DIB-58), the per-quote history, and the one-click disable.
//
// It is a SEPARATE type from StaffService for the same reason StaffService is separate from Service:
// different scoping, different guarantees. StaffService's every method proves a staff caller's branch
// reach. Half of this type's work is done by a scheduled job with NO caller at all, and mixing
// "guarded by the caller's branch" with "run by the machine at 10am" on one struct is how a guard
// eventually gets skipped. The manual path here delegates its guard to StaffService rather than
// re-implementing it.

// followUpStore is the persistence this service needs (*FollowUpRepository in production).
type followUpStore interface {
	Header(ctx context.Context, quoteID string) (QuoteCadenceHeader, error)
	EnsurePlan(ctx context.Context, quoteID, channel string, offsets []int, startedAt time.Time) (FollowUpPlan, error)
	Plan(ctx context.Context, quoteID string) (FollowUpPlan, error)
	EnabledPlans(ctx context.Context, limit int) ([]FollowUpPlan, error)
	SetEnabled(ctx context.Context, quoteID string, enabled bool, actorID, why string) error
	Events(ctx context.Context, quoteID string) ([]FollowUpEvent, error)
	ClaimStep(ctx context.Context, tx pgx.Tx, quoteID string, stepDay int, channel string, scheduledFor time.Time) (bool, error)
	SettleStep(ctx context.Context, tx pgx.Tx, quoteID string, stepDay int, channel, status, reason, eventID string) error
	CancelRemaining(ctx context.Context, plan FollowUpPlan, why string, now time.Time) (int, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// AutomationService is the quote-side automation surface.
type AutomationService struct {
	store followUpStore
	// staff performs the guarded mint for a MANUAL send: it re-proves the caller's branch reach, the
	// quote's shareability and its expiry, and moves DRAFT → SENT. Reimplementing any of that here
	// would produce a second, weaker copy of a guard that already exists.
	staff *StaffService
	queue NoticeQueue
	// contacts supplies the email address for the EMAIL channel. Nil → email steps are SKIPPED with a
	// reason rather than failing, because a cadence with no mail wiring must still run its SMS half.
	contacts ContactReader
	// shares lets the SWEEP mint a link with no staff caller (see mintForCadence).
	shares  shareStore
	baseURL string
	log     *slog.Logger
	now     func() time.Time
}

// NewAutomationService builds the automation surface over the cadence store.
func NewAutomationService(store followUpStore) *AutomationService {
	return &AutomationService{store: store, now: time.Now}
}

// WithStaff injects the guarded staff service used by the manual send path.
func (a *AutomationService) WithStaff(s *StaffService) *AutomationService { a.staff = s; return a }

// WithQueue injects the out-of-band delivery seam.
func (a *AutomationService) WithQueue(q NoticeQueue) *AutomationService { a.queue = q; return a }

// WithContacts injects the customer email/name reader.
func (a *AutomationService) WithContacts(c ContactReader) *AutomationService {
	a.contacts = c
	return a
}

// WithShares injects the share-link store the cadence mints from.
func (a *AutomationService) WithShares(s shareStore) *AutomationService { a.shares = s; return a }

// WithBaseURL sets the ERP origin share links are built against (ERP_BASE_URL).
func (a *AutomationService) WithBaseURL(u string) *AutomationService {
	a.baseURL = strings.TrimRight(strings.TrimSpace(u), "/")
	return a
}

// WithLogger attaches a logger.
func (a *AutomationService) WithLogger(l *slog.Logger) *AutomationService { a.log = l; return a }

// WithClock injects the clock. Every scheduling decision in this file reads it, so a cadence test
// pins "today" instead of waiting three days.
func (a *AutomationService) WithClock(f func() time.Time) *AutomationService {
	if f != nil {
		a.now = f
	}
	return a
}

// Wired reports whether the automation can do anything at all (the UI hides the controls otherwise).
func (a *AutomationService) Wired() bool { return a.store != nil && a.queue != nil }

// - DIB-77: send a quote's share link by SMS -

// SMSShareResult is what the staff caller gets back. It carries NO token: the raw link went into the
// message, and echoing it to the browser would put a live credential in a second place for no reason.
type SMSShareResult struct {
	QuoteID   string    `json:"quote_id"`
	QuoteNo   string    `json:"quote_no"`
	EventID   string    `json:"event_id"`
	NotBefore time.Time `json:"not_before"`
	// Queued is always true on success and is here so the UI can say the honest thing, "queued", not
	// "sent". With the stub provider nothing leaves the building, and a UI that claims otherwise is
	// lying to a salesperson who will tell the customer.
	Queued bool `json:"queued"`
}

// ShareBySMS texts a quote's public share link to the customer (DIB-77).
//
// ORDER OF OPERATIONS, AND WHY IT IS THIS ORDER:
//  1. Consent FIRST. If the customer has not agreed to be texted, we stop before minting anything -
//     a link minted for a message that cannot be sent is a live credential nobody will ever use, and
//     it moves a DRAFT quote to SENT as a side effect of an action that did not happen.
//  2. Mint through StaffService, which re-proves branch reach, shareability and expiry.
//  3. Enqueue, held until the customer's quiet hours allow it.
//
// NO PAYMENT LINK IS EVER INCLUDED (CP-05). The payload type has no field for one.
func (a *AutomationService) ShareBySMS(ctx context.Context, quoteIDOrNo string, ttl time.Duration) (SMSShareResult, error) {
	if a.queue == nil {
		return SMSShareResult{}, ErrNoticeQueueUnwired
	}
	if a.staff == nil {
		return SMSShareResult{}, ErrFollowUpNotWired
	}
	// Resolve + guard through the staff surface's own reader, so an unreachable quote is ErrNotFound
	// here exactly as it is everywhere else on this surface.
	view, err := a.staff.StaffQuoteRead(ctx, quoteIDOrNo)
	if err != nil {
		return SMSShareResult{}, err
	}
	now := a.now().UTC()

	ok, notBefore, err := a.queue.MaySendSMS(ctx, view.CustomerID, now)
	if err != nil {
		return SMSShareResult{}, err
	}
	if !ok {
		return SMSShareResult{}, ErrNoSMSConsent
	}

	minted, err := a.staff.MintShareLink(ctx, view.ID, ttl)
	if err != nil {
		return SMSShareResult{}, err
	}

	eventID, err := a.queue.EnqueueQuoteShare(ctx, ShareNotice{
		QuoteID:    view.ID,
		QuoteNo:    view.QuoteNo,
		CustomerID: view.CustomerID,
		TotalCents: view.TotalCents,
		ValidUntil: view.ValidUntil,
		ShareURL:   a.shareURL(minted.Token),
	}, notBefore)
	if err != nil {
		return SMSShareResult{}, err
	}

	// Sharing a quote starts its follow-up cadence. This is the natural moment: the customer now has
	// the document, and the clock the salesperson thinks in ("I sent it Tuesday") starts here. An
	// existing plan is left alone, EnsurePlan never restarts a running cadence.
	if _, perr := a.store.EnsurePlan(ctx, view.ID, ChannelBoth, DefaultOffsets, now); perr != nil && a.log != nil {
		a.log.Warn("quote: share sent but the follow-up plan could not be created", "quote_no", view.QuoteNo, "err", perr)
	}

	if a.log != nil {
		a.log.Info("quote: share link queued for SMS (nothing sent yet, the consumer delivers)",
			"quote_no", view.QuoteNo, "event_id", eventID, "not_before", notBefore.Format(time.RFC3339))
	}
	return SMSShareResult{
		QuoteID: view.ID, QuoteNo: view.QuoteNo, EventID: eventID,
		NotBefore: notBefore, Queued: true,
	}, nil
}

// shareURL builds the customer-facing link. The path matches the frontend's public quote page
// (quote.html?t=…), which is the ONE place a share token is meant to be redeemed.
func (a *AutomationService) shareURL(token string) string {
	base := a.baseURL
	if base == "" {
		base = "http://localhost:5173"
	}
	return base + "/quote.html?t=" + url.QueryEscape(token)
}

// - DIB-58: the cadence -

// SweepResult is what one cadence run did.
type SweepResult struct {
	PlansConsidered int
	Enqueued        int
	Skipped         int // a step deliberately not sent (no consent, no email), recorded with a reason
	Cancelled       int // steps voided because the quote reached a terminal state
}

// SweepFollowUps evaluates every enabled plan once. It is the job-runner body, and it is idempotent:
// a second run over unchanged data enqueues nothing.
func (a *AutomationService) SweepFollowUps(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	if a.store == nil {
		return res, ErrFollowUpNotWired
	}
	if a.queue == nil {
		return res, ErrNoticeQueueUnwired
	}
	now := a.now().UTC()

	plans, err := a.store.EnabledPlans(ctx, 0)
	if err != nil {
		return res, err
	}
	for _, plan := range plans {
		res.PlansConsidered++
		head, err := a.store.Header(ctx, plan.QuoteID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // the quote is gone; the plan cascades with it
			}
			return res, err
		}

		// A quote that has reached a terminal state ends its own cadence, with the reason recorded on
		// every step that will now never happen.
		if why := a.terminalReason(head, now); why != "" {
			n, err := a.store.CancelRemaining(ctx, plan, why, now)
			if err != nil {
				return res, err
			}
			res.Cancelled += n
			continue
		}

		for _, day := range plan.OffsetsDays {
			due := plan.StartedAt.AddDate(0, 0, day)
			if due.After(now) {
				continue // not yet, the step stays in Upcoming
			}
			for _, ch := range ChannelsOf(plan.Channel) {
				sent, skipped, err := a.runStep(ctx, plan, head, day, ch, due, now)
				if err != nil {
					return res, err
				}
				switch {
				case sent:
					res.Enqueued++
				case skipped:
					res.Skipped++
				}
			}
		}
	}
	return res, nil
}

// terminalReason names why a cadence must stop, or "" to continue.
//
// EXPIRY IS COMPUTED, NOT WAITED FOR. A quote whose valid_until has passed is treated as expired even
// though nothing in this program flips the status column on a timer, otherwise the cadence would keep
// following up on a document the customer can no longer accept.
func (a *AutomationService) terminalReason(h QuoteCadenceHeader, now time.Time) string {
	if terminalStatuses[h.Status] {
		return "quote is " + strings.ToLower(h.Status)
	}
	if h.ValidUntil != nil && now.After(h.ValidUntil.Add(24*time.Hour)) {
		return "quote expired on " + h.ValidUntil.Format("2006-01-02")
	}
	return ""
}

// runStep claims, decides and records ONE (step, channel) pair, in one transaction.
//
// The claim and the enqueue commit together. That is what makes re-running the sweep safe: either a
// step was claimed AND queued, or neither happened.
func (a *AutomationService) runStep(ctx context.Context, plan FollowUpPlan, head QuoteCadenceHeader, day int, channel string, due, now time.Time) (sent, skipped bool, err error) {
	tx, err := a.store.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	claimed, err := a.store.ClaimStep(ctx, tx, plan.QuoteID, day, channel, due)
	if err != nil {
		return false, false, err
	}
	if !claimed {
		return false, false, tx.Commit(ctx) // already decided, the idempotent path
	}

	validUntil := ""
	if head.ValidUntil != nil {
		validUntil = head.ValidUntil.Format("2006-01-02")
	}
	notice := FollowUpNotice{
		QuoteID: head.QuoteID, QuoteNo: head.QuoteNo, CustomerID: head.CustomerID,
		TotalCents: head.TotalCents, ValidUntil: validUntil, StepDay: day,
	}

	settle := func(status, reason, eventID string) error {
		if err := a.store.SettleStep(ctx, tx, plan.QuoteID, day, channel, status, reason, eventID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	switch channel {
	case ChannelSMS:
		ok, notBefore, err := a.queue.MaySendSMS(ctx, head.CustomerID, now)
		if err != nil {
			return false, false, err
		}
		if !ok {
			// Consent is re-checked HERE, at send time, not when the plan was created. A customer who
			// opted out on day 4 does not get the day-7 text, and the history says why.
			return false, true, settle(StepSkipped, "no SMS consent", "")
		}
		notice.ShareURL = a.mintForCadence(ctx, head)
		eventID, err := a.queue.EnqueueFollowUpSMS(ctx, notice, notBefore)
		if err != nil {
			return false, false, err
		}
		return true, false, settle(StepSent, "", eventID)

	default: // EMAIL
		name, email := "", ""
		if a.contacts != nil {
			name, email, _ = a.contacts.PrimaryContact(ctx, head.CustomerID)
		}
		_ = name
		if email == "" {
			return false, true, settle(StepSkipped, "no email address on file", "")
		}
		notice.Email = email
		notice.ShareURL = a.mintForCadence(ctx, head)
		eventID, err := a.queue.EnqueueFollowUpEmail(ctx, notice)
		if err != nil {
			return false, false, err
		}
		return true, false, settle(StepSent, "", eventID)
	}
}

// mintForCadence issues a fresh share link for a follow-up message, or "" if it cannot.
//
// WHY THIS DOES NOT GO THROUGH MintShareLink. That method proves a STAFF CALLER's branch reach, and
// the sweep has no caller, it is the machine acting on a plan a staff member turned on. So it writes
// the link directly, and the safety comes from where the link GOES rather than from who asked for it:
// the token is placed only in a message addressed to the customer of that quote (resolved through
// consent at delivery time) and is never returned to any HTTP caller. A failure to mint degrades the
// message to a link-free nudge rather than failing the step, the customer still hears from us.
func (a *AutomationService) mintForCadence(ctx context.Context, head QuoteCadenceHeader) string {
	if a.shares == nil {
		return ""
	}
	raw, hashed, err := newShareToken()
	if err != nil {
		return ""
	}
	if _, err := a.shares.CreateShareLink(ctx, head.QuoteID, hashed, a.now().Add(defaultShareTTL), ""); err != nil {
		if a.log != nil {
			a.log.Warn("quote: follow-up could not mint a share link, sending without one",
				"quote_no", head.QuoteNo, "err", err)
		}
		return ""
	}
	return a.shareURL(raw)
}

// - History + the one-click switch -

// History returns everything the quote surface renders for one quote's cadence.
func (a *AutomationService) History(ctx context.Context, quoteIDOrNo string) (FollowUpHistory, error) {
	quoteID, err := a.resolve(ctx, quoteIDOrNo)
	if err != nil {
		return FollowUpHistory{}, err
	}
	out := FollowUpHistory{QuoteID: quoteID, Events: []FollowUpEvent{}, Upcoming: []FollowUpStep{}}

	plan, err := a.store.Plan(ctx, quoteID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	if err == nil {
		p := plan
		out.Plan = &p
		out.Automated = plan.Enabled
	}
	events, err := a.store.Events(ctx, quoteID)
	if err != nil {
		return out, err
	}
	if events != nil {
		out.Events = events
	}

	// Upcoming is computed from the plan minus what already has a row. A scheduled step is arithmetic,
	// not a record, writing future rows would mean every cadence change had to rewrite them.
	if out.Plan != nil && plan.Enabled {
		taken := map[string]bool{}
		for _, e := range events {
			taken[fmt.Sprintf("%d/%s", e.StepDay, e.Channel)] = true
		}
		for _, d := range plan.OffsetsDays {
			for _, ch := range ChannelsOf(plan.Channel) {
				if taken[fmt.Sprintf("%d/%s", d, ch)] {
					continue
				}
				out.Upcoming = append(out.Upcoming, FollowUpStep{
					StepDay: d, Channel: ch,
					DueAt:     plan.StartedAt.AddDate(0, 0, d),
					Scheduled: true,
				})
			}
		}
	}
	return out, nil
}

// SetEnabled is the one-click disable (and re-enable) a salesperson uses from the quote surface.
func (a *AutomationService) SetEnabled(ctx context.Context, quoteIDOrNo string, enabled bool) (FollowUpHistory, error) {
	quoteID, err := a.resolve(ctx, quoteIDOrNo)
	if err != nil {
		return FollowUpHistory{}, err
	}
	actorID := ""
	why := ""
	if !enabled {
		why = "turned off by staff"
		if a.staff != nil {
			if caller, cerr := a.staff.caller(ctx); cerr == nil {
				actorID = caller.UserID
			}
		}
	}
	if err := a.store.SetEnabled(ctx, quoteID, enabled, actorID, why); err != nil {
		if errors.Is(err, ErrNotFound) && enabled {
			// Enabling a cadence for a quote that never had one is a create, not a 404, the
			// salesperson's intent is unambiguous.
			if _, perr := a.store.EnsurePlan(ctx, quoteID, ChannelBoth, DefaultOffsets, a.now().UTC()); perr != nil {
				return FollowUpHistory{}, perr
			}
		} else {
			return FollowUpHistory{}, err
		}
	}
	return a.History(ctx, quoteID)
}

// resolve turns an id-or-quote-number into an id, THROUGH the staff guard when one is wired. Every
// HTTP-reachable method here goes through it, so branch reach is proved on the automation surface by
// the same code that proves it on the quote surface.
func (a *AutomationService) resolve(ctx context.Context, quoteIDOrNo string) (string, error) {
	if a.store == nil {
		return "", ErrFollowUpNotWired
	}
	if a.staff == nil {
		return quoteIDOrNo, nil
	}
	view, err := a.staff.StaffQuoteRead(ctx, quoteIDOrNo)
	if err != nil {
		return "", err
	}
	return view.ID, nil
}
