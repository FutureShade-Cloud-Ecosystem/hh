package billing

// ar_reminders_transport_test.go, FW1-06b: Remind never says sent when nothing was sent.
//
// AR-08: with no sender bound on the register (production, where no email transport is wired), the
// send still recorded an EMAIL REMINDER delivery per invoice and counted it in Sent, and the desk
// told the operator a reminder went out that never left the building. The legs here hold at the
// service, the layer the desk's words read.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSendReminder_NeverSaysSentWhenNoTransportIsWired(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	svc := newFakeService(t, store, time.Now())
	res, err := svc.SendReminder(context.Background(), ReminderInput{
		InvoiceIDs: []string{"i1"}, Tone: ToneFriendly, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("a missing transport is a skip with a reason, not an error: %v", err)
	}
	if res.Sent != 0 {
		t.Fatalf("no transport is wired and the send says %d sent: the desk would tell the operator a reminder went that never left the building", res.Sent)
	}
	if !strings.Contains(res.Skipped["INV-1"], "no email transport") {
		t.Fatalf("the skipped reason must name the missing transport, got %q", res.Skipped["INV-1"])
	}
	if len(store.sends) != 0 {
		t.Fatal("nothing may be recorded as a delivery when nothing was sent")
	}
}

// failingSender accepts nothing: every delivery fails at the transport.
type failingSender struct{ err error }

func (f failingSender) SendInvoiceMessage(context.Context, string, string, string) error {
	return f.err
}

// deliveringSender accepts everything: every delivery goes.
type deliveringSender struct{}

func (deliveringSender) SendInvoiceMessage(context.Context, string, string, string) error {
	return nil
}

func TestSendReminder_AFailedDeliveryRecordsNothingAndIsNotCountedAsSent(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	svc := newFakeService(t, store, time.Now()).WithSender(failingSender{err: errors.New("transport unreachable")})
	res, err := svc.SendReminder(context.Background(), ReminderInput{
		InvoiceIDs: []string{"i1"}, Tone: ToneFriendly, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("a transport failure is reported, not an error: %v", err)
	}
	if res.Sent != 0 {
		t.Fatalf("a delivery the transport refused says %d sent, want 0", res.Sent)
	}
	if !strings.Contains(res.Skipped["INV-1"], "delivery failed") {
		t.Fatalf("the skipped reason must name the failure, got %q", res.Skipped["INV-1"])
	}
	if len(store.sends) != 0 {
		t.Fatalf("a delivery the transport refused records %d rows; nothing went out, so nothing is recorded", len(store.sends))
	}
}

// S6's P2-1: the invoice Send button sits beside Remind and answered "1 sent" on the same unwired
// deployment, and when the transport refused. The same rule as the Remind run holds here.
func TestSendInvoice_NeverSaysSentWhenNoTransportIsWired(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	svc := newFakeService(t, store, time.Now())
	res, err := svc.SendInvoice(context.Background(), "i1", "send-key")
	if err != nil {
		t.Fatalf("a missing transport is a skip with a reason, not an error: %v", err)
	}
	if res.Sent != 0 {
		t.Fatalf("fw1-06b round 2 red: no transport is wired and the invoice send says %d sent", res.Sent)
	}
	if !strings.Contains(res.Skipped["INV-1"], "no email transport") {
		t.Fatalf("the skipped reason must name the missing transport, got %q", res.Skipped["INV-1"])
	}
	if len(store.sends) != 0 {
		t.Fatal("nothing may be recorded as a delivery when nothing was sent")
	}
}

func TestSendInvoice_AFailedDeliveryIsNotCountedAsSentAndRecordsNothing(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	svc := newFakeService(t, store, time.Now()).WithSender(failingSender{err: errors.New("transport unreachable")})
	res, err := svc.SendInvoice(context.Background(), "i1", "send-key")
	if err != nil {
		t.Fatalf("a transport failure is reported, not an error: %v", err)
	}
	if res.Sent != 0 {
		t.Fatalf("fw1-06b round 2 red: a delivery the transport refused says %d sent, want 0", res.Sent)
	}
	if !strings.Contains(res.Skipped["INV-1"], "delivery failed") {
		t.Fatalf("the skipped reason must name the failure, got %q", res.Skipped["INV-1"])
	}
	if len(store.sends) != 0 {
		t.Fatalf("a delivery the transport refused records %d rows; nothing went out, so nothing is recorded", len(store.sends))
	}
}

func TestSendInvoice_AnAcceptedDeliveryCountsAsSent(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	svc := newFakeService(t, store, time.Now()).WithSender(deliveringSender{})
	res, err := svc.SendInvoice(context.Background(), "i1", "send-key")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Sent != 1 {
		t.Fatalf("a delivery the transport accepted says %d sent, want 1", res.Sent)
	}
	if len(store.sends) != 1 {
		t.Fatalf("an accepted send records %d rows, want the one", len(store.sends))
	}
}

// countingSender counts what it was asked to deliver, so a leg can see a second message.
type countingSender struct{ n int }

func (c *countingSender) SendInvoiceMessage(context.Context, string, string, string) error {
	c.n++
	return nil
}

// The same key twice must deliver ONE message. The desk retries the same key when an answer was
// lost, and a customer who receives the same email twice is a relationship cost this system's whole
// collections cadence exists to avoid (the register's own note on the delivery log).
func TestSendInvoice_AReplayOfTheSameKeyNeverDeliversASecondMessage(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	sender := &countingSender{}
	svc := newFakeService(t, store, time.Now()).WithSender(sender)
	first, err := svc.SendInvoice(context.Background(), "i1", "send-key")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if first.Sent != 1 {
		t.Fatalf("the first send says %d sent, want 1", first.Sent)
	}
	second, err := svc.SendInvoice(context.Background(), "i1", "send-key")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Replayed != 1 || second.Sent != 0 {
		t.Fatalf("a replay says sent=%d replayed=%d, want sent 0 and replayed 1", second.Sent, second.Replayed)
	}
	if sender.n != 1 {
		t.Fatalf("a replay delivered %d messages, want 1: the customer is chased twice for one action", sender.n)
	}
	if len(store.sends) != 1 {
		t.Fatalf("a replay records %d delivery rows, want the one", len(store.sends))
	}
}

func TestSendReminder_AReplayOfTheSameKeyNeverDeliversASecondMessage(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	sender := &countingSender{}
	svc := newFakeService(t, store, time.Now()).WithSender(sender)
	in := ReminderInput{InvoiceIDs: []string{"i1"}, Tone: ToneFriendly, IdempotencyKey: "k"}
	first, err := svc.SendReminder(context.Background(), in)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if first.Sent != 1 {
		t.Fatalf("the first run says %d sent, want 1", first.Sent)
	}
	second, err := svc.SendReminder(context.Background(), in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Replayed != 1 || second.Sent != 0 {
		t.Fatalf("a replay says sent=%d replayed=%d, want sent 0 and replayed 1", second.Sent, second.Replayed)
	}
	if sender.n != 1 {
		t.Fatalf("a replay delivered %d messages, want 1: the customer is chased twice for one action", sender.n)
	}
	if len(store.sends) != 1 {
		t.Fatalf("a replay records %d delivery rows, want the one", len(store.sends))
	}
}
