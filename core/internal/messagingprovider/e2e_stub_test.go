package messagingprovider

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// e2e_stub_test.go, the FLAG-OFF end-to-end: a queued notice travels claim → handler → template →
// notifier and arrives, with no provider, no credentials, and no network. This is the acceptance
// guard that every walkthrough in this program passes with SMS switched off.
//
// It drives the REAL notify.Dispatcher over an in-memory store shaped exactly like the SQL one, so
// what is exercised is the shipped routing/retry machinery, not a hand-rolled loop. The database
// equivalent (same path, real sms_outbox rows) is in outbox_integration_test.go.

// memStore is an in-memory notify.OutboxStore: it hands rows to settle exactly as the SQL store does
// and records each row's final disposition so a test can assert PUBLISHED vs retried vs dead.
type memStore struct {
	rows      []notify.Event
	published []string
	dead      []string
	pending   []string
}

func (m *memStore) add(topic string, payload any) {
	b, _ := json.Marshal(payload)
	m.rows = append(m.rows, notify.Event{EventID: topic + "-" + itoa(len(m.rows)+1), Topic: topic, Payload: b})
}

func (m *memStore) ProcessBatch(ctx context.Context, n int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	processed := 0
	for len(m.rows) > 0 && processed < n {
		e := m.rows[0]
		m.rows = m.rows[1:]
		st := settle(ctx, e)
		switch string(st.Status) {
		case dispositionPublished:
			m.published = append(m.published, e.EventID)
		case dispositionDead:
			m.dead = append(m.dead, e.EventID)
		default:
			m.pending = append(m.pending, e.EventID)
		}
		processed++
	}
	return processed, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestStubModeEndToEndEmitsBothNotices is the acceptance guard for item 6's flag-off requirement.
func TestStubModeEndToEndEmitsBothNotices(t *testing.T) {
	// Flag off: no SMS_PROVIDER, no credentials, Select gives the stub.
	notifier, err := Select(Settings{}, nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	stub, ok := notifier.(*StubNotifier)
	if !ok {
		t.Fatalf("flag-off notifier = %T, want *StubNotifier", notifier)
	}

	recipients := NewFixtureRecipients().
		SeedOrder("07000000-0000-4000-9000-000000000001", Recipient{Name: "Sam Whitfield", Phone: "+16135550142"}).
		SeedInvoice("in-1", Recipient{Name: "Dana Cole", Phone: "+16135550188"})

	store := &memStore{}
	store.add(TopicDispatchNotice, DispatchNotice{
		TaskID: "t-1", OrderID: "07000000-0000-4000-9000-000000000001", OrderNo: "SO-2026-0001",
		BranchName: "Trenton", StageLocation: "Bay 3", StageRef: "STG-4KQ7RM",
	})
	store.add(TopicARNotice, ARNotice{InvoiceID: "in-1", InvoiceNo: "INV-2026-0042", AmountCents: 128_450, DaysPastDue: 45})

	d := notify.NewDispatcher(store, slog.Default())
	NewConsumer(notifier, recipients, slog.Default()).RegisterHandlers(d)

	if n := d.DrainOnce(context.Background()); n != 2 {
		t.Fatalf("drained %d rows, want 2", n)
	}
	if len(store.published) != 2 || len(store.dead) != 0 || len(store.pending) != 0 {
		t.Fatalf("dispositions: published=%v dead=%v pending=%v", store.published, store.dead, store.pending)
	}

	sent := stub.Sent()
	if len(sent) != 2 {
		t.Fatalf("stub delivered %d messages, want 2", len(sent))
	}
	dispatch, ar := sent[0], sent[1]
	if dispatch.Channel != notify.SMSChannel || dispatch.To != "+16135550142" {
		t.Fatalf("dispatch message = %+v", dispatch)
	}
	for _, want := range []string{"Hi Sam", "SO-2026-0001", "STG-4KQ7RM", "Trenton"} {
		if !strings.Contains(dispatch.Body, want) {
			t.Fatalf("dispatch body missing %q: %q", want, dispatch.Body)
		}
	}
	if ar.To != "+16135550188" {
		t.Fatalf("ar message to = %q", ar.To)
	}
	for _, want := range []string{"Hi Dana", "INV-2026-0042", "45 days past due", "$1,284.50"} {
		if !strings.Contains(ar.Body, want) {
			t.Fatalf("ar body missing %q: %q", want, ar.Body)
		}
	}
}

func TestNoticeWithNoRecipientIsSkippedNotDeadLettered(t *testing.T) {
	// The default resolver knows nobody. That must be quiet and terminal, not four retries and a DEAD
	// row per notice, otherwise the dead-letter pile fills with non-events and hides real failures.
	stub := NewStubNotifier(nil)
	store := &memStore{}
	store.add(TopicDispatchNotice, DispatchNotice{OrderID: "unknown-order", OrderNo: "SO-9"})

	d := notify.NewDispatcher(store, slog.Default())
	NewConsumer(stub, NewFixtureRecipients(), slog.Default()).RegisterHandlers(d)
	d.DrainOnce(context.Background())

	if len(store.published) != 1 {
		t.Fatalf("dispositions: published=%v dead=%v pending=%v", store.published, store.dead, store.pending)
	}
	if len(stub.Sent()) != 0 {
		t.Fatalf("stub sent %d messages for a notice with no recipient", len(stub.Sent()))
	}
}

func TestNoticeWithAMalformedRecipientIsSkipped(t *testing.T) {
	// A number that is not E.164 would be rejected by the provider AND billed as an attempt.
	stub := NewStubNotifier(nil)
	store := &memStore{}
	store.add(TopicDispatchNotice, DispatchNotice{OrderID: "o-1", To: "(613) 555-0142"})

	d := notify.NewDispatcher(store, slog.Default())
	NewConsumer(stub, NewFixtureRecipients(), slog.Default()).RegisterHandlers(d)
	d.DrainOnce(context.Background())

	if len(stub.Sent()) != 0 {
		t.Fatalf("a non-E.164 number was handed to the transport: %+v", stub.Sent())
	}
	if len(store.published) != 1 {
		t.Fatalf("dispositions: published=%v dead=%v pending=%v", store.published, store.dead, store.pending)
	}
}

func TestSeamWinsOverThePayloadRecipient(t *testing.T) { // UNION-W4 (D E-2): flipped, delivery re-resolves
	stub := NewStubNotifier(nil)
	store := &memStore{}
	store.add(TopicDispatchNotice, DispatchNotice{OrderID: "o-1", OrderNo: "SO-7", To: "+16135550111"})

	d := notify.NewDispatcher(store, slog.Default())
	NewConsumer(stub, NewFixtureRecipients().SeedOrder("o-1", Recipient{Phone: "+16135550999"}), nil).RegisterHandlers(d)
	d.DrainOnce(context.Background())

	sent := stub.Sent()
	if len(sent) != 1 || sent[0].To != "+16135550999" {
		t.Fatalf("sent = %+v, want the SEAM's resolution, the payload's To is a hint that is not trusted", sent)
	}
}

func TestUnreadablePayloadFailsRatherThanSendingNonsense(t *testing.T) {
	stub := NewStubNotifier(nil)
	store := &memStore{rows: []notify.Event{{EventID: "e1", Topic: TopicARNotice, Payload: []byte(`{ not json`)}}}

	d := notify.NewDispatcher(store, slog.Default())
	NewConsumer(stub, NewFixtureRecipients(), slog.Default()).RegisterHandlers(d)
	d.DrainOnce(context.Background())

	if len(stub.Sent()) != 0 {
		t.Fatalf("a malformed payload produced a message: %+v", stub.Sent())
	}
	if len(store.published) != 0 {
		t.Fatalf("a malformed payload was published as delivered")
	}
}

func TestRegisteringTheSameTopicTwicePanicsAtWireTime(t *testing.T) {
	// The shipped single-handler-per-topic invariant must still hold for these topics: a second
	// registration is a wiring bug that has to fail at boot, not route texts to the wrong renderer.
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate topic registration did not panic")
		}
	}()
	d := notify.NewDispatcher(&memStore{}, nil)
	NewConsumer(NewStubNotifier(nil), nil, nil).RegisterHandlers(d)
	NewConsumer(NewStubNotifier(nil), nil, nil).RegisterHandlers(d)
}
