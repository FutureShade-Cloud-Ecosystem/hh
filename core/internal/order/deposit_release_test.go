package order

// deposit_release_test.go, the deposit release emission (the item after 9.5):
// when an order carrying a supplier deposit document reaches its fulfilment,
// the order service tells the outbox, one deposit.released event per live
// deposit document, so the GL consumer can post DR 2300 / CR revenue. The
// tests here drive the choke point (AdvanceFromEvent) exactly as the
// fulfilment roads do (the dispatch handoff, the will-call collection, the
// pos order.fulfilled observer).

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// outboxEmit is one captured outbox write.
type outboxEmit struct {
	topic   string
	payload map[string]any
}

// depositReleaseStoreFake is the fake store plus the deposit release lane's
// two reads: the order's live deposit documents, and the outbox write.
type depositReleaseStoreFake struct {
	*fakeStore
	ids     []string
	idsErr  error
	emitErr error
	emitted []outboxEmit
}

func (f *depositReleaseStoreFake) DepositDocumentIDs(context.Context, string) ([]string, error) {
	return f.ids, f.idsErr
}

func (f *depositReleaseStoreFake) EmitEvent(_ context.Context, topic string, payload any) error {
	if f.emitErr != nil {
		return f.emitErr
	}
	m, _ := payload.(map[string]any)
	f.emitted = append(f.emitted, outboxEmit{topic: topic, payload: m})
	return nil
}

func depositFixture(t *testing.T, status string, ids ...string) (*Service, *depositReleaseStoreFake) {
	t.Helper()
	f := &depositReleaseStoreFake{fakeStore: newFakeStore(mkDetail(consOrder, consCust, status)), ids: ids}
	return NewService(f).WithAudit(&recordingRecorder{}), f
}

// A fulfilled order with one deposit document emits exactly one
// deposit.released event naming that document.
func TestFulfilmentReleasesTheSupplierDeposit(t *testing.T) {
	svc, f := depositFixture(t, "READY", "isd-1")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "evt-rel-1"); err != nil {
		t.Fatalf("the fulfilment move: %v", err)
	}
	if len(f.emitted) != 1 {
		t.Fatalf("the fulfilment emitted %d event(s), want the one deposit.released: %+v", len(f.emitted), f.emitted)
	}
	e := f.emitted[0]
	if e.topic != "deposit.released" {
		t.Fatalf("topic = %q, want deposit.released", e.topic)
	}
	if e.payload["invoice_id"] != "isd-1" || e.payload["order_id"] != consOrder {
		t.Fatalf("payload = %v, want the deposit document and its order", e.payload)
	}
}

// An order with no deposit document emits nothing: the lane costs one query
// and no event.
func TestFulfilmentReleasesNothingWithoutADepositDocument(t *testing.T) {
	svc, f := depositFixture(t, "READY")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "evt-rel-2"); err != nil {
		t.Fatalf("the fulfilment move: %v", err)
	}
	if len(f.emitted) != 0 {
		t.Fatalf("an order with no deposit emitted %+v", f.emitted)
	}
}

// A redelivered fulfilment (the order already FULFILLED) re-reaches the
// emission, the healing posture of the counter-invoice completion beside it;
// the ledger's journal key is what keeps the release exactly once.
func TestFulfilmentRedeliveryReReachesTheRelease(t *testing.T) {
	svc, f := depositFixture(t, "FULFILLED", "isd-1")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "evt-rel-3"); !errors.Is(err, ErrNoTransition) {
		t.Fatalf("a redelivered fulfilment answers %v, want ErrNoTransition", err)
	}
	if len(f.emitted) != 1 {
		t.Fatalf("the redelivery must re-reach the release, emitted %+v", f.emitted)
	}
}

// The POS ring beat: an on-account sale's invoice finalises at the ring and
// the order sits INVOICED before the goods leave, so the yard's MarkFulfilled
// is declined by the ladder. The material still left, so the release fires
// anyway.
func TestFulfilmentReleasesWhenTheRingAlreadyInvoiced(t *testing.T) {
	svc, f := depositFixture(t, "INVOICED", "isd-1")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "dispatch:stop-1"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("the beat order answers %v, want ErrIllegalTransition", err)
	}
	if len(f.emitted) != 1 {
		t.Fatalf("the beat order's fulfilment must still release the deposit, emitted %+v", f.emitted)
	}
}

// A cancelled or closed order never releases: the deposit of an order that
// will not be fulfilled is the reversal follow up's money, not revenue.
func TestFulfilmentReleasesNothingForACancelledOrder(t *testing.T) {
	svc, f := depositFixture(t, "CANCELLED", "isd-1")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "evt-rel-4"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("a cancelled order answers %v, want ErrIllegalTransition", err)
	}
	if len(f.emitted) != 0 {
		t.Fatalf("a cancelled order emitted %+v", f.emitted)
	}
}

// A move that is not a fulfilment never consults the lane.
func TestNonFulfilmentMovesReleaseNothing(t *testing.T) {
	svc, f := depositFixture(t, "CONFIRMED", "isd-1")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "INVOICED", "evt-inv-1"); err != nil {
		t.Fatalf("the invoiced move: %v", err)
	}
	if len(f.emitted) != 0 {
		t.Fatalf("an invoiced move emitted %+v", f.emitted)
	}
}

// A failed emission never fails the fulfilment, and never fails silently:
// the order id and the error are logged, the queryable gap.
func TestAFailedReleaseEmissionIsLoggedNotRaised(t *testing.T) {
	buf := captureLog(t)
	svc, f := depositFixture(t, "READY", "isd-1")
	f.emitErr = errors.New("outbox boom")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "evt-rel-5"); err != nil {
		t.Fatalf("the fulfilment move must stand: %v", err)
	}
	if got := f.details[consOrder].Status; got != "FULFILLED" {
		t.Fatalf("the order is %s, want FULFILLED", got)
	}
	for _, want := range []string{"outbox boom", consOrder, "evt-rel-5"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("the failed emission must be logged with %q, log: %q", want, buf.String())
		}
	}
}

// An unreadable deposit read is the same posture: the move stands, the gap is
// logged with the order id and the event id.
func TestAnUnreadableDepositReadIsLoggedNotRaised(t *testing.T) {
	buf := captureLog(t)
	svc, f := depositFixture(t, "READY", "isd-1")
	f.idsErr = errors.New("deposit read boom")
	if err := svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "evt-rel-6"); err != nil {
		t.Fatalf("the fulfilment move must stand: %v", err)
	}
	for _, want := range []string{"deposit read boom", consOrder, "evt-rel-6"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("the failed read must be logged with %q, log: %q", want, buf.String())
		}
	}
}
