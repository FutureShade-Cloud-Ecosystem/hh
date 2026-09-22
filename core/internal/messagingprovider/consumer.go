package messagingprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// consumer.go, the handlers that turn a queued notice into a delivered message. They are ordinary
// notify.Handler funcs registered on a notify.Dispatcher, so retry, dead-lettering and the
// single-handler-per-topic invariant are the shipped ones; nothing about delivery policy is
// reinvented here.

// Consumer owns the two topics on sms_outbox. It holds the active notifier (stub by default, Twilio
// only when the environment says so) and the recipient seam.
type Consumer struct {
	notifier  notify.Notifier
	recipient RecipientResolver
	log       *slog.Logger
	// email is the transport for the follow-up cadence's EMAIL step (Wave 3, DIB-58). Nil unless
	// WithEmailNotifier was called, see consumer_quote.go for why one dispatcher carries two
	// transports rather than one queue carrying one channel.
	email notify.Notifier
}

// NewConsumer builds the consumer. A nil resolver falls back to the empty fixture, which resolves
// nobody, so an unwired deployment queues notices and sends none, rather than guessing a number.
func NewConsumer(n notify.Notifier, r RecipientResolver, log *slog.Logger) *Consumer {
	if r == nil {
		r = NewFixtureRecipients()
	}
	return &Consumer{notifier: n, recipient: r, log: log}
}

// Notifier exposes the active transport (used by the boot check and by tests asserting stub mode).
func (c *Consumer) Notifier() notify.Notifier { return c.notifier }

// RegisterHandlers claims this package's two topics as PRIMARY owner. They live on sms_outbox, which
// no other dispatcher drains, so there is no contention with the ERP's financial topics, and the
// dispatcher's duplicate-registration panic still protects against wiring the same topic twice here.
func (c *Consumer) RegisterHandlers(d *notify.Dispatcher) {
	d.Register(TopicARNotice, c.handleAR)
	d.Register(TopicDispatchNotice, c.handleDispatch)
}

// handleAR renders and sends an accounts-receivable notice.
func (c *Consumer) handleAR(ctx context.Context, e notify.Event) error {
	var n ARNotice
	if err := json.Unmarshal(e.Payload, &n); err != nil {
		// A malformed payload will never parse on a retry. Returning an error still burns four more
		// attempts before dead-lettering, which is the dispatcher's policy and not this handler's to
		// override, but the message says plainly that it is permanent.
		return fmt.Errorf("ar notice: unreadable payload (permanent): %w", err)
	}
	r, ok, err := c.resolve(ctx, e, n.To, func() (Recipient, error) {
		return c.recipient.ARRecipient(ctx, n.InvoiceID, n.CustomerID)
	})
	if err != nil || !ok {
		return err
	}
	return c.notifier.Send(ctx, RenderAR(n, r))
}

// handleDispatch renders and sends a fulfilment notice (the yard staged a pick).
func (c *Consumer) handleDispatch(ctx context.Context, e notify.Event) error {
	var n DispatchNotice
	if err := json.Unmarshal(e.Payload, &n); err != nil {
		return fmt.Errorf("dispatch notice: unreadable payload (permanent): %w", err)
	}
	r, ok, err := c.resolve(ctx, e, n.To, func() (Recipient, error) {
		return c.recipient.DispatchRecipient(ctx, n.OrderID)
	})
	if err != nil || !ok {
		return err
	}
	return c.notifier.Send(ctx, RenderDispatch(n, r))
}

// resolve produces the recipient for one notice and decides whether there is anything to send.
//
// Returns (recipient, send, err):
//   - send=false, err=nil  → SKIP. Nobody to text. The row is PUBLISHED with a log line rather than
//     retried, because "this customer has no mobile number" is a permanent, expected, uninteresting
//     state and four retries plus a DEAD row would turn it into noise that hides real failures.
//   - err != nil           → a genuine failure of the lookup; the dispatcher retries.
//
// A pre-resolved number on the payload wins over the seam (a producer that already knew should not
// force a second lookup), but it is shape-checked exactly the same way.
func (c *Consumer) resolve(ctx context.Context, e notify.Event, preset string, lookup func() (Recipient, error)) (Recipient, bool, error) {
	// UNION-W4 (D E-2, HIGH): the preset fast path is GONE. A pre-resolved number on the payload
	// skipped CustomerRecipient, "the one place consent is evaluated", so an AR notice enqueued
	// before a STOP shipped anyway after the quiet-hours hold. Every delivery now re-resolves, and
	// therefore re-consents, at send time; the payload's To is a hint that is deliberately not trusted.
	_ = preset
	r, err := lookup()
	if errors.Is(err, ErrNoRecipient) {
		c.skip(e, "no recipient on file")
		return Recipient{}, false, nil
	}
	if err != nil {
		return Recipient{}, false, fmt.Errorf("resolve recipient: %w", err)
	}
	if !r.Valid() {
		c.skip(e, "resolved recipient is not E.164")
		return Recipient{}, false, nil
	}
	_ = ctx
	return r, true, nil
}

// skip logs a non-delivery that is NOT a failure. It never logs the number itself.
func (c *Consumer) skip(e notify.Event, why string) {
	if c.log != nil {
		c.log.Info("sms: notice skipped (nothing sent)", "topic", e.Topic, "event_id", e.EventID, "why", why)
	}
}
