package messagingprovider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// e2e_quote_stub_test.go, the FLAG-OFF end-to-end for the Wave 3 topics: a queued quote notice travels
// claim → handler → consent → template → notifier and arrives at the STUB, with no provider, no
// credentials and no network. This is the acceptance guard for items 3 and 4 (DIB-77, DIB-58).
//
// It drives the REAL notify.Dispatcher over the same in-memory store the Wave 2 guard uses, so what is
// exercised is the shipped routing machinery rather than a hand-rolled loop.

// stubConsent is a RecipientResolver with a CustomerRecipient method, built from a map, the Wave 3
// topics key off a customer id rather than an invoice or an order.
type stubConsent struct {
	byCustomer map[string]Recipient
}

func (s stubConsent) ARRecipient(_ context.Context, _, customerID string) (Recipient, error) {
	return s.CustomerRecipient(context.Background(), customerID)
}
func (s stubConsent) DispatchRecipient(_ context.Context, _ string) (Recipient, error) {
	return Recipient{}, ErrNoRecipient
}
func (s stubConsent) CustomerRecipient(_ context.Context, customerID string) (Recipient, error) {
	if r, ok := s.byCustomer[customerID]; ok {
		return r, nil
	}
	return Recipient{}, ErrNoRecipient
}

func quoteStubRig(t *testing.T, consent RecipientResolver) (*memStore, *StubNotifier, *StubNotifier, *notify.Dispatcher) {
	t.Helper()
	// Flag off: no SMS_PROVIDER, no credentials.
	notifier, err := Select(Settings{}, nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	sms, ok := notifier.(*StubNotifier)
	if !ok {
		t.Fatalf("flag-off notifier = %T, want *StubNotifier", notifier)
	}
	if sms.Live() {
		t.Fatalf("the default notifier reports Live(), this bundle must never send for real by default")
	}
	mail := NewStubNotifier(nil)

	store := &memStore{}
	d := notify.NewDispatcher(store, nil)
	NewConsumer(sms, consent, nil).WithEmailNotifier(mail).RegisterQuoteHandlers(d)
	return store, sms, mail, d
}

// TestStubMode_QuoteShareAndFollowupsTravelEndToEnd, all three Wave 3 topics deliver to the stub.
func TestStubMode_QuoteShareAndFollowupsTravelEndToEnd(t *testing.T) {
	consent := stubConsent{byCustomer: map[string]Recipient{
		"cust-1": {Name: "Mark Quinte", Phone: "+16135551001"},
	}}
	store, sms, mail, d := quoteStubRig(t, consent)

	store.add(TopicQuoteShare, QuoteShareNotice{
		QuoteID: "q-1", QuoteNo: "Q-2026-0001", CustomerID: "cust-1",
		TotalCents: 481250, ValidUntil: "2026-09-04",
		ShareURL: "https://erp.example/quote.html?t=tok",
	})
	store.add(TopicQuoteFollowupSMS, QuoteFollowupNotice{
		QuoteID: "q-1", QuoteNo: "Q-2026-0001", CustomerID: "cust-1", StepDay: 3,
	})
	store.add(TopicQuoteFollowupEmail, QuoteFollowupNotice{
		QuoteID: "q-1", QuoteNo: "Q-2026-0001", CustomerID: "cust-1", StepDay: 7,
		Email: "mark@example.test",
	})

	if n := d.DrainOnce(context.Background()); n != 3 {
		t.Fatalf("drained %d rows, want 3", n)
	}
	if len(store.published) != 3 || len(store.dead) != 0 {
		t.Fatalf("dispositions: published=%v dead=%v pending=%v, all three must publish",
			store.published, store.dead, store.pending)
	}

	sent := sms.Sent()
	if len(sent) != 2 {
		t.Fatalf("the SMS stub recorded %d messages, want 2 (share + follow-up)", len(sent))
	}
	for _, m := range sent {
		if m.Channel != notify.SMSChannel || m.To != "+16135551001" {
			t.Fatalf("SMS message went to %q on %s", m.To, m.Channel)
		}
	}
	if !strings.Contains(sent[0].Body, "https://erp.example/quote.html?t=tok") {
		t.Fatalf("the share message must carry the link, got %q", sent[0].Body)
	}

	mailed := mail.Sent()
	if len(mailed) != 1 || mailed[0].Channel != notify.EmailChannel || mailed[0].To != "mark@example.test" {
		t.Fatalf("the email stub recorded %+v, want one EMAIL to mark@example.test", mailed)
	}
}

// TestStubMode_OptedOutCustomerIsSkipped_NotRetried. A customer we may not text is a SKIP: the row is
// published with a log line, nothing is sent, and it does not walk to the dead-letter pile, otherwise
// every consent refusal becomes noise that hides a real provider outage.
func TestStubMode_OptedOutCustomerIsSkipped_NotRetried(t *testing.T) {
	store, sms, _, d := quoteStubRig(t, stubConsent{byCustomer: map[string]Recipient{}})

	store.add(TopicQuoteShare, QuoteShareNotice{
		QuoteID: "q-2", QuoteNo: "Q-2026-0002", CustomerID: "opted-out",
		ShareURL: "https://erp.example/quote.html?t=tok",
	})
	store.add(TopicQuoteFollowupSMS, QuoteFollowupNotice{
		QuoteID: "q-2", QuoteNo: "Q-2026-0002", CustomerID: "opted-out", StepDay: 3,
	})

	if n := d.DrainOnce(context.Background()); n != 2 {
		t.Fatalf("drained %d rows, want 2", n)
	}
	if len(sms.Sent()) != 0 {
		t.Fatalf("a customer with no consent received %d messages, want 0", len(sms.Sent()))
	}
	if len(store.published) != 2 {
		t.Fatalf("a consent skip must PUBLISH the row (published=%v, dead=%v, pending=%v)",
			store.published, store.dead, store.pending)
	}
}

// TestStubMode_ShareNoticeWithNoLinkIsSkipped, "your quote is ready" and nothing else is worse than
// silence, and a retry cannot conjure a token that was never minted.
func TestStubMode_ShareNoticeWithNoLinkIsSkipped(t *testing.T) {
	consent := stubConsent{byCustomer: map[string]Recipient{"cust-1": {Phone: "+16135551001"}}}
	store, sms, _, d := quoteStubRig(t, consent)

	store.add(TopicQuoteShare, QuoteShareNotice{QuoteID: "q-3", QuoteNo: "Q-3", CustomerID: "cust-1"})
	d.DrainOnce(context.Background())

	if len(sms.Sent()) != 0 {
		t.Fatalf("a share notice with no URL sent a message: %+v", sms.Sent())
	}
	if len(store.published) != 1 {
		t.Fatalf("a link-less share notice must be skipped-and-published, not retried")
	}
}

// TestStubMode_FollowupEmailWithNoMailerFails_Loudly. An operator who configured an email cadence and
// no mailer must see it; silently succeeding would mean a cadence that reports SENT and delivers
// nothing.
func TestStubMode_FollowupEmailWithNoMailerFails_Loudly(t *testing.T) {
	notifier, _ := Select(Settings{}, nil)
	store := &memStore{}
	d := notify.NewDispatcher(store, nil)
	// No WithEmailNotifier.
	NewConsumer(notifier, stubConsent{byCustomer: map[string]Recipient{}}, nil).RegisterQuoteHandlers(d)

	store.add(TopicQuoteFollowupEmail, QuoteFollowupNotice{
		QuoteID: "q-4", QuoteNo: "Q-4", CustomerID: "cust-1", StepDay: 3, Email: "a@b.test",
	})
	d.DrainOnce(context.Background())

	if len(store.published) != 0 {
		t.Fatalf("an email step with no mailer must NOT publish, it published %v", store.published)
	}
}

// redactStore is a one-row store that keeps the Settlement the dispatcher produced, so a test can
// assert the REDACT flag. notify.Settlement's status type is unexported, so a store outside package
// notify cannot construct one, but it can keep the one it was handed, which is all this needs.
type redactStore struct {
	event notify.Event
	got   notify.Settlement
	done  bool
}

func (r *redactStore) ProcessBatch(ctx context.Context, n int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	if r.done {
		return 0, nil
	}
	r.done = true
	r.got = settle(ctx, r.event)
	return 1, nil
}

// TestRegisterQuoteHandlers_ShareTopicIsSecret, the share payload carries a live bearer credential
// (the raw share token, which 0086 stores only as a hash and can never recover). Registering it as an
// ordinary topic would leave that token sitting in the queue table indefinitely after it had been
// used; registering it SECRET makes the dispatcher redact the published row's payload.
func TestRegisterQuoteHandlers_ShareTopicIsSecret(t *testing.T) {
	payload, err := json.Marshal(QuoteShareNotice{
		QuoteID: "q-5", QuoteNo: "Q-5", CustomerID: "cust-1",
		ShareURL: "https://erp.example/quote.html?t=secret",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	store := &redactStore{event: notify.Event{EventID: "e-1", Topic: TopicQuoteShare, Payload: payload}}
	d := notify.NewDispatcher(store, nil)
	NewConsumer(NewStubNotifier(nil), stubConsent{byCustomer: map[string]Recipient{
		"cust-1": {Phone: "+16135551001"},
	}}, nil).RegisterQuoteHandlers(d)

	if n := d.DrainOnce(context.Background()); n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
	if !store.got.Redact {
		t.Fatalf("the quote share topic must be registered SECRET so the raw share token does not linger at rest")
	}
}

type statusMap map[string]string

func (s statusMap) QuoteStatus(_ context.Context, id string) (string, error) { return s[id], nil }

// A follow-up notice queued while the quote was open is dropped at delivery once the quote is no
// longer open: the customer accepted or declined (here, or by phone with staff recording it) after the
// sweep enqueued the step. The row is published with a skip, never dead-lettered, and nothing is sent
// by either channel. A notice for a still open quote goes out as before.
func TestStubMode_FollowupForAClosedQuoteIsDroppedAtDelivery(t *testing.T) {
	consent := stubConsent{byCustomer: map[string]Recipient{"cust-1": {Name: "Mark Quinte", Phone: "+16135551001"}}}
	notifier, _ := Select(Settings{}, nil)
	sms := notifier.(*StubNotifier)
	mail := NewStubNotifier(nil)
	store := &memStore{}
	d := notify.NewDispatcher(store, nil)
	NewConsumer(sms, consent, nil).WithEmailNotifier(mail).
		WithQuoteStatus(statusMap{"q-open": "SENT", "q-acc": "ACCEPTED", "q-lost": "LOST", "q-conv": "CONVERTED", "q-exp": "EXPIRED"}).
		RegisterQuoteHandlers(d)

	for _, q := range []string{"q-acc", "q-lost", "q-conv", "q-exp", "q-gone"} {
		store.add(TopicQuoteFollowupSMS, QuoteFollowupNotice{QuoteID: q, CustomerID: "cust-1", StepDay: 3})
		store.add(TopicQuoteFollowupEmail, QuoteFollowupNotice{QuoteID: q, CustomerID: "cust-1", StepDay: 7, Email: "mark@example.test"})
	}
	store.add(TopicQuoteFollowupSMS, QuoteFollowupNotice{QuoteID: "q-open", CustomerID: "cust-1", StepDay: 3})
	store.add(TopicQuoteFollowupEmail, QuoteFollowupNotice{QuoteID: "q-open", CustomerID: "cust-1", StepDay: 7, Email: "mark@example.test"})

	if n := d.DrainOnce(context.Background()); n != 12 {
		t.Fatalf("drained %d rows, want 12", n)
	}
	if len(store.dead) != 0 || len(store.published) != 12 {
		t.Fatalf("a dropped notice is published with a skip: published=%d dead=%v pending=%v", len(store.published), store.dead, store.pending)
	}
	if got := len(sms.Sent()); got != 1 {
		t.Errorf("only the open quote's text may go out, %d texts sent", got)
	}
	if got := len(mail.Sent()); got != 1 {
		t.Errorf("only the open quote's email may go out, %d emails sent", got)
	}
}
