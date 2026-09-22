//go:build integration

// consent_delivery_time_integration_test.go, W4-D item 6. The question put to this pass was:
// "W3-D's ARNotice.To pre-resolution became redundant belt-and-braces once the union wired
// delivery-time consent, confirm by test that delivery-time consent HOLDS (an opt-out between
// enqueue and drain is honoured)."
//
// It does NOT hold on the AR path, and the pre-resolution is not redundant. Consumer.resolve
// (consumer.go) short-circuits on a non-empty preset:
//
//	if preset != "" {
//	        r := Recipient{Phone: preset}
//	        if !r.Valid() { c.skip(e, "payload recipient is not E.164"); return Recipient{}, false, nil }
//	        return r, true, nil          // <-- returns WITHOUT calling the lookup
//	}
//	r, err := lookup()                   // <-- ARRecipient -> CustomerRecipient == the consent check
//
// and recipients_db.go says of CustomerRecipient: "the one place consent is evaluated. Every other
// method funnels through it." A preset does not funnel through it. Since the collections producer
// ALWAYS sets To (collections.go, the maybeQueue path), every sms.ar.notice it enqueues is delivered
// on enqueue-time consent alone.
//
// These two tests pin both arms so the difference is a fact rather than a reading of the code. The
// pre-resolved arm asserts TODAY'S behaviour and will fail the moment the bypass is closed, which is
// the intent: it is the tripwire that tells the union the drop landed.
package messagingprovider

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// captureNotifier records what actually went out, so "was this customer texted after they opted out?"
// is answered by the transport rather than by a return value.
type captureNotifier struct{ sent []notify.Message }

func (c *captureNotifier) Live() bool   { return false }
func (c *captureNotifier) Name() string { return "capture" }

func (c *captureNotifier) Send(_ context.Context, m notify.Message) error {
	c.sent = append(c.sent, m)
	return nil
}

// arEvent builds the event the dispatcher would hand handleAR for this fixture's customer.
func arEvent(t *testing.T, customerID, to string) notify.Event {
	t.Helper()
	payload, err := json.Marshal(ARNotice{
		InvoiceID: "", InvoiceNo: "INV-CONSENT-1", CustomerID: customerID,
		AmountCents: 25_000, DueDate: "2026-07-01", DaysPastDue: 31, To: to,
	})
	if err != nil {
		t.Fatalf("marshal ARNotice: %v", err)
	}
	return notify.Event{EventID: "consent-probe", Topic: TopicARNotice, Payload: payload}
}

// -- ARM A: To EMPTY, the pattern every other topic in this package uses ----------------------------
//
// With no preset the consumer funnels through ARRecipient -> CustomerRecipient at DRAIN time, so an
// opt-out that lands after the notice was queued is honoured and nothing is sent. This is the
// behaviour the union's delivery-time consent was wired to provide, and it genuinely works.
func TestIntegration_DeliveryTimeConsent_HoldsWhenRecipientIsNotPreResolved(t *testing.T) {
	c := newConsentFixture(t)
	ctx := context.Background()

	// Opted IN at enqueue time: the notice is legitimately queued.
	if err := c.prefs.Upsert(ctx, c.f.CustomerID, "+16135550143", true); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	e := arEvent(t, c.f.CustomerID, "") // To EMPTY, resolve at delivery time
	if r, err := c.res.CustomerRecipient(ctx, c.f.CustomerID); err != nil || r.Phone == "" {
		t.Fatalf("precondition: an opted-in customer must resolve at enqueue time (r=%+v err=%v)", r, err)
	}

	// ...and the customer replies STOP before the queue drains.
	if err := c.prefs.SetOptOut(ctx, c.f.CustomerID, true, "STOP reply"); err != nil {
		t.Fatalf("opt out: %v", err)
	}

	cap := &captureNotifier{}
	consumer := NewConsumer(cap, c.res, slog.Default())
	if err := consumer.handleAR(ctx, e); err != nil {
		t.Fatalf("handleAR: %v", err)
	}

	if len(cap.sent) != 0 {
		t.Fatalf("delivery-time consent did NOT hold: %d message(s) sent to a customer who opted out between enqueue and drain: %+v",
			len(cap.sent), cap.sent)
	}
}

// -- ARM B: To PRE-RESOLVED, the pattern the collections producer actually uses ---------------------
//
// Identical scenario, the ONLY difference being that To carries the number the producer resolved at
// enqueue time. UNION-W4 dropped the preset short-circuit (D's E-2): every delivery re-resolves -
// and therefore re-consents, at send time. This test was the characterization tripwire for the old
// bypass; per its own instructions it now asserts ZERO sends: an opt-out between enqueue and drain
// is honoured even when the producer pre-resolved To onto the payload.
func TestIntegration_DeliveryTimeConsent_HoldsEvenWhenRecipientIsPreResolved(t *testing.T) {
	c := newConsentFixture(t)
	ctx := context.Background()

	if err := c.prefs.Upsert(ctx, c.f.CustomerID, "+16135550143", true); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	// The producer resolves the number at enqueue time and puts it on the payload, exactly what
	// collections.go's maybeQueue does before EnqueueAtTx.
	preset, err := c.res.CustomerRecipient(ctx, c.f.CustomerID)
	if err != nil {
		t.Fatalf("precondition: resolve at enqueue time: %v", err)
	}
	e := arEvent(t, c.f.CustomerID, preset.Phone)

	// The customer replies STOP before the queue drains.
	if err := c.prefs.SetOptOut(ctx, c.f.CustomerID, true, "STOP reply"); err != nil {
		t.Fatalf("opt out: %v", err)
	}
	// The consent resolver itself is emphatic that this customer must not be texted...
	if _, err := c.res.CustomerRecipient(ctx, c.f.CustomerID); err == nil {
		t.Fatal("precondition: an opted-out customer must not resolve")
	}

	cap := &captureNotifier{}
	consumer := NewConsumer(cap, c.res, slog.Default())
	if err := consumer.handleAR(ctx, e); err != nil {
		t.Fatalf("handleAR: %v", err)
	}

	// The preset is deliberately not trusted: delivery re-resolves, the opt-out holds, nothing sends.
	if len(cap.sent) != 0 {
		t.Fatalf("delivery-time consent did NOT hold for a pre-resolved payload: %d message(s) sent to a "+
			"customer who opted out between enqueue and drain: %+v", len(cap.sent), cap.sent)
	}
}
