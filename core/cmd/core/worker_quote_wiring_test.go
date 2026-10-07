package main

// worker_quote_wiring_test.go is the worker's sms topic-set pin (HH-188):
// the audit found the worker registering only the Wave 2 fulfilment
// notices on the sms dispatcher, so every quote share and follow-up notice
// the live producers enqueue on sms_outbox settled "no handler registered
// for topic" and dead-lettered, after the share link (a bearer credential
// for the public quote view and accept routes) had already moved the quote
// DRAFT to SENT and told the salesperson "queued". The module's e2e stub
// suite wires the quote handlers itself, so it could never see the gap;
// this file drives the registration set the worker actually builds, the
// way worker.go builds it, and asserts the quote topics have owners and
// the email transport is wired.
//
// It needs no database: the belt is the production constructor over a nil
// pool (scaffold mode, the stub notifier), the store captures settlements
// the way the messagingprovider e2e suite's does, and every delivery the
// handlers make either reaches the captured mail transport or takes the
// consent-skip path, both of which publish. The belt's resolver over a
// nil pool resolves nobody, which is exactly the safe scaffold answer.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// captureOutbox hands the dispatcher's settle one row per queued topic and
// keeps every settlement, so the test can read the disposition each topic
// actually received. notify.Settlement's status type is unexported, so the
// assertion switches on the wire strings, the same sanctioned move the
// sms_outbox store itself makes.
type captureOutbox struct {
	rows    []notify.Event
	settled []settledRow
}

// settledRow pairs one drained event with the settlement the dispatcher
// produced for it.
type settledRow struct {
	event notify.Event
	got   notify.Settlement
}

func (c *captureOutbox) add(topic string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		panic("worker wiring fixture: marshal: " + err.Error())
	}
	c.rows = append(c.rows, notify.Event{
		EventID: topic + "-" + strconv.Itoa(len(c.rows)+1),
		Topic:   topic,
		Payload: b,
	})
}

func (c *captureOutbox) ProcessBatch(ctx context.Context, n int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	processed := 0
	for processed < n && len(c.rows) > 0 {
		e := c.rows[0]
		c.rows = c.rows[1:]
		c.settled = append(c.settled, settledRow{event: e, got: settle(ctx, e)})
		processed++
	}
	return processed, nil
}

// captureNotifier records every Send it is handed, the email-transport
// stand-in for the WithEmailNotifier leg.
type captureNotifier struct{ sent []notify.Message }

func (c *captureNotifier) Name() string { return "capture" }
func (c *captureNotifier) Live() bool   { return false }
func (c *captureNotifier) Send(_ context.Context, m notify.Message) error {
	c.sent = append(c.sent, m)
	return nil
}

// TestWorkerSmsDispatcherOwnsTheQuoteTopics, HH-188: every topic the live
// producers write into sms_outbox must have an owner on the worker's sms
// dispatcher, and the follow-up email topic must reach a wired mail
// transport. Every quote topic (the share and both follow-ups) must also
// settle with the redact flag set: their payloads carry the share link or
// the customer's address, and the dispatcher must treat them as secret no
// matter who wired them.
func TestWorkerSmsDispatcherOwnsTheQuoteTopics(t *testing.T) {
	t.Setenv("SMS_PROVIDER", "stub")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	belt := buildSmsBelt(nil, log)
	mail := &captureNotifier{}
	store := &captureOutbox{}
	d := notify.NewDispatcher(store, log)
	registerSmsConsumers(belt, mail, d, log)

	// The Wave 3 payloads: a share notice whose link a customer could open,
	// a follow-up text, and a follow-up email with an address on file.
	store.add(messagingprovider.TopicQuoteShare, messagingprovider.QuoteShareNotice{
		QuoteID: "fx-q1", QuoteNo: "Q-FX-1", CustomerID: "fx-cust-1",
		ShareURL: "https://erp.fx.invalid/quote.html?t=fx-share-token",
	})
	store.add(messagingprovider.TopicQuoteFollowupSMS, messagingprovider.QuoteFollowupNotice{
		QuoteID: "fx-q1", QuoteNo: "Q-FX-1", CustomerID: "fx-cust-1", StepDay: 3,
	})
	store.add(messagingprovider.TopicQuoteFollowupEmail, messagingprovider.QuoteFollowupNotice{
		QuoteID: "fx-q1", QuoteNo: "Q-FX-1", CustomerID: "fx-cust-1", StepDay: 7,
		Email: "customer@fx.invalid",
	})
	// The Wave 2 fulfilment notices must keep their owners through the same
	// registration set (the resolver over a nil pool resolves nobody, so
	// both take the consent-skip path, which publishes).
	store.add(messagingprovider.TopicARNotice, messagingprovider.ARNotice{
		InvoiceID: "fx-inv-1", CustomerID: "fx-cust-1",
	})
	store.add(messagingprovider.TopicDispatchNotice, messagingprovider.DispatchNotice{
		OrderID: "fx-order-1", To: "+16135550142",
	})

	if n := d.DrainOnce(context.Background()); n != 5 {
		t.Fatalf("DrainOnce processed %d rows, want 5", n)
	}

	byTopic := map[string]settledRow{}
	for _, s := range store.settled {
		byTopic[s.event.Topic] = s
	}
	for _, topic := range []string{
		messagingprovider.TopicQuoteShare,
		messagingprovider.TopicQuoteFollowupSMS,
		messagingprovider.TopicQuoteFollowupEmail,
		messagingprovider.TopicARNotice,
		messagingprovider.TopicDispatchNotice,
	} {
		s, ok := byTopic[topic]
		if !ok {
			t.Fatalf("topic %s was never offered a settlement", topic)
		}
		if string(s.got.Status) != "PUBLISHED" {
			t.Errorf("topic %s settled %q (last_error %q), want PUBLISHED: the producers enqueue this topic, a worker that does not own it dead-letters every notice",
				topic, string(s.got.Status), s.got.LastError)
		}
	}
	for _, topic := range []string{
		messagingprovider.TopicQuoteShare,
		messagingprovider.TopicQuoteFollowupSMS,
		messagingprovider.TopicQuoteFollowupEmail,
	} {
		if s := byTopic[topic]; string(s.got.Status) == "PUBLISHED" && !s.got.Redact {
			t.Errorf("topic %s must settle as SECRET: its payload carries the share link or the customer's address, a live credential", topic)
		}
	}

	// The email leg is only honest if a transport is wired: a follow-up
	// email row that publishes because no mailer exists is the silent
	// nothing the cadence must never report as sent.
	if len(mail.sent) != 1 {
		t.Fatalf("the mail transport recorded %d message(s), want exactly the one follow-up email", len(mail.sent))
	}
	if m := mail.sent[0]; m.Channel != notify.EmailChannel || m.To != "customer@fx.invalid" {
		t.Fatalf("the mail transport recorded %+v, want one EMAIL to customer@fx.invalid", m)
	}
}

type fxQuoteStatus map[string]string

func (w fxQuoteStatus) QuoteStatus(_ context.Context, id string) (string, error) { return w[id], nil }

// The worker's registration set carries the belt's quote status reader into the follow-up handlers:
// a follow-up email queued for a quote that has since been accepted or lost is dropped at delivery
// (published, nothing mailed), and one for a still open quote is mailed.
func TestWorkerSmsDispatcherDropsFollowupsForClosedQuotes(t *testing.T) {
	t.Setenv("SMS_PROVIDER", "stub")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	belt := buildSmsBelt(nil, log)
	belt.quoteStatus = fxQuoteStatus{"fx-open": "SENT", "fx-acc": "ACCEPTED", "fx-lost": "LOST"}
	mail := &captureNotifier{}
	store := &captureOutbox{}
	d := notify.NewDispatcher(store, log)
	registerSmsConsumers(belt, mail, d, log)

	for _, q := range []string{"fx-acc", "fx-lost", "fx-open"} {
		store.add(messagingprovider.TopicQuoteFollowupEmail, messagingprovider.QuoteFollowupNotice{
			QuoteID: q, CustomerID: "fx-cust-1", StepDay: 7, Email: "customer@fx.invalid",
		})
	}
	if n := d.DrainOnce(context.Background()); n != 3 {
		t.Fatalf("DrainOnce processed %d rows, want 3", n)
	}
	for _, s := range store.settled {
		if string(s.got.Status) != "PUBLISHED" {
			t.Errorf("a dropped notice must publish, not retry or dead-letter: %q (%q)", string(s.got.Status), s.got.LastError)
		}
	}
	if len(mail.sent) != 1 {
		t.Fatalf("the mail transport recorded %d message(s), want only the open quote's one", len(mail.sent))
	}
}
