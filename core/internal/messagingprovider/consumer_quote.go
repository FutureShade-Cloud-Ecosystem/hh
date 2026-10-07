package messagingprovider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// consumer_quote.go, the Wave 3 handlers: quote share delivery (DIB-77) and the follow-up cadence's
// two channels (DIB-58). They are registered on the SAME dispatcher as the Wave 2 topics by
// RegisterHandlers, so retry, dead-lettering and the single-handler-per-topic invariant are unchanged.

// WithEmailNotifier gives the consumer a transport for the follow-up cadence's EMAIL step.
//
// It is a SECOND notifier, not a replacement: the SMS notifier is the stub or Twilio, and email is
// whatever the composition root already wired for the portal's mail (the dev log stub by default).
// One dispatcher, two transports, chosen per topic, which is cheaper and far more legible than a
// second dispatcher over a second table for the sake of a channel enum.
//
// A nil email notifier makes the email topic a hard, retryable failure rather than a silent success,
// because "the operator wired a follow-up cadence with an email channel and no mailer" must be visible.
func (c *Consumer) WithEmailNotifier(n notify.Notifier) *Consumer {
	c.email = n
	return c
}

// WithResolver replaces the recipient seam after construction (the composition root builds the
// consent resolver from the same pool it builds the outbox from).
func (c *Consumer) WithResolver(r RecipientResolver) *Consumer {
	if r != nil {
		c.recipient = r
	}
	return c
}

// QuoteStatusReader answers a quote's current status at delivery time. An unknown quote answers "".
type QuoteStatusReader interface {
	QuoteStatus(ctx context.Context, quoteID string) (string, error)
}

// WithQuoteStatus gives the follow-up handlers the means to re-read a quote when a notice is
// delivered. A cadence step is enqueued by the sweep and delivered later (a text waits for the
// quiet hours to end), and the customer may answer in between, through the link or by phone with
// staff recording it; the cancel at that moment cannot reach a step already queued. Nil checks
// nothing, the behaviour before this seam existed.
func (c *Consumer) WithQuoteStatus(r QuoteStatusReader) *Consumer {
	c.quotes = r
	return c
}

// quoteStillOpen is the delivery-time check for a follow-up notice: only a SENT quote is still being
// followed up. A closed, lost, expired, converted or vanished quote's notice is skipped (published
// with a reason, never retried), and a read failure is returned so the row retries rather than
// sending on an unknown.
func (c *Consumer) quoteStillOpen(ctx context.Context, e notify.Event, quoteID string) (bool, error) {
	if c.quotes == nil || quoteID == "" {
		return true, nil
	}
	status, err := c.quotes.QuoteStatus(ctx, quoteID)
	if err != nil {
		return false, fmt.Errorf("quote follow-up: reading the quote's status: %w", err)
	}
	if status != "SENT" {
		c.skip(e, "the quote is no longer open ("+status+"), follow-up dropped")
		return false, nil
	}
	return true, nil
}

// RegisterQuoteHandlers claims the Wave 3 topics. Kept separate from RegisterHandlers so a deployment
// that wants only the Wave 2 fulfilment notices does not silently acquire a quote cadence, and so the
// Wave 2 registration line in main.go stays untouched.
func (c *Consumer) RegisterQuoteHandlers(d *notify.Dispatcher) {
	// The share notice's payload carries a live share URL, a bearer credential for one quote. It is
	// registered SECRET so the row's payload is redacted the moment it is published, the same treatment
	// the invite and reset topics get. Without this the token would sit in the queue table indefinitely
	// after it had been used.
	d.RegisterSecret(TopicQuoteShare, c.handleQuoteShare)
	d.RegisterSecret(TopicQuoteFollowupSMS, c.handleQuoteFollowupSMS)
	d.RegisterSecret(TopicQuoteFollowupEmail, c.handleQuoteFollowupEmail)
}

// handleQuoteShare delivers a quote's share link by SMS.
func (c *Consumer) handleQuoteShare(ctx context.Context, e notify.Event) error {
	var n QuoteShareNotice
	if err := json.Unmarshal(e.Payload, &n); err != nil {
		return fmt.Errorf("quote share notice: unreadable payload (permanent): %w", err)
	}
	if n.ShareURL == "" {
		// A share notice with no link is a message that says "your quote is ready" and nothing else.
		// Skipping is better than sending it: the salesperson can re-send, and a retry cannot conjure a
		// token that was never minted.
		c.skip(e, "no share URL on the notice")
		return nil
	}
	r, ok, err := c.resolveCustomer(ctx, e, n.To, n.CustomerID)
	if err != nil || !ok {
		return err
	}
	return c.notifier.Send(ctx, RenderQuoteShare(n, r))
}

// handleQuoteFollowupSMS delivers one cadence step by text.
func (c *Consumer) handleQuoteFollowupSMS(ctx context.Context, e notify.Event) error {
	var n QuoteFollowupNotice
	if err := json.Unmarshal(e.Payload, &n); err != nil {
		return fmt.Errorf("quote follow-up notice: unreadable payload (permanent): %w", err)
	}
	if open, err := c.quoteStillOpen(ctx, e, n.QuoteID); err != nil || !open {
		return err
	}
	r, ok, err := c.resolveCustomer(ctx, e, n.To, n.CustomerID)
	if err != nil || !ok {
		return err
	}
	return c.notifier.Send(ctx, RenderQuoteFollowupSMS(n, r))
}

// handleQuoteFollowupEmail delivers one cadence step by email.
//
// Consent (0109) is an SMS concept and is deliberately NOT consulted here: a customer who asked us to
// stop texting has not asked us to stop emailing, and conflating the two would quietly break the
// email cadence for everyone who ever replied STOP. The email cadence's own off switch is the quote's
// follow-up plan.
func (c *Consumer) handleQuoteFollowupEmail(ctx context.Context, e notify.Event) error {
	var n QuoteFollowupNotice
	if err := json.Unmarshal(e.Payload, &n); err != nil {
		return fmt.Errorf("quote follow-up email: unreadable payload (permanent): %w", err)
	}
	if open, err := c.quoteStillOpen(ctx, e, n.QuoteID); err != nil || !open {
		return err
	}
	if n.Email == "" {
		c.skip(e, "no email address on the notice")
		return nil
	}
	if c.email == nil {
		return fmt.Errorf("quote follow-up email: no email notifier is wired")
	}
	return c.email.Send(ctx, RenderQuoteFollowupEmail(n, ""))
}

// resolveCustomer resolves a recipient from a customer id, honouring a pre-resolved number on the
// payload. It is the customer-keyed sibling of resolve(): the Wave 2 topics key off an invoice or an
// order, the Wave 3 quote topics already know the customer.
//
// A resolver that does not implement CustomerRecipient (the Wave 2 fixture) yields a SKIP rather than
// a failure, an old resolver wired against new topics sends nothing, loudly, instead of retrying
// into the dead-letter pile.
func (c *Consumer) resolveCustomer(ctx context.Context, e notify.Event, preset, customerID string) (Recipient, bool, error) {
	type customerResolver interface {
		CustomerRecipient(ctx context.Context, customerID string) (Recipient, error)
	}
	cr, ok := c.recipient.(customerResolver)
	if preset == "" && !ok {
		c.skip(e, "resolver cannot resolve a customer id")
		return Recipient{}, false, nil
	}
	return c.resolve(ctx, e, preset, func() (Recipient, error) {
		return cr.CustomerRecipient(ctx, customerID)
	})
}
