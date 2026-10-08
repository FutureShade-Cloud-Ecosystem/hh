package order

// deposit_release_test.go, the deposit release trigger (the item after 9.5,
// and its review fixes): the release fires only on a fact of delivery, the
// explicit AdvanceFromDelivery road that the dispatch handoff, the will-call
// collection and the ring's order.fulfilled observer take. The plain event
// writer (AdvanceFromEvent, which a configurable task rule can reach) never
// releases and never records a delivery.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// releaserFake is the billing seam the order calls on a delivery.
type releaserFake struct {
	calls []string
	err   error
}

func (r *releaserFake) ReleaseDeposits(_ context.Context, orderID string) error {
	r.calls = append(r.calls, orderID)
	return r.err
}

// deliveryStoreFake is the fake store plus the delivery marker lane.
type deliveryStoreFake struct {
	*fakeStore
	marked  []string
	markErr error
}

func (f *deliveryStoreFake) MarkDelivered(_ context.Context, orderID string) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marked = append(f.marked, orderID)
	return nil
}

func (f *deliveryStoreFake) DeliveredOrderIDs(_ context.Context, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		for _, m := range f.marked {
			if m == id {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

func depositFixture(t *testing.T, status string) (*Service, *deliveryStoreFake, *releaserFake) {
	t.Helper()
	f := &deliveryStoreFake{fakeStore: newFakeStore(mkDetail(consOrder, consCust, status))}
	r := &releaserFake{}
	return NewService(f).WithAudit(&recordingRecorder{}).WithDepositReleaser(r), f, r
}

// A delivered order releases its deposit documents through the billing seam
// and records the delivery.
func TestDeliveryReleasesTheSupplierDeposit(t *testing.T) {
	svc, f, r := depositFixture(t, "READY")
	if err := svc.AdvanceFromDelivery(context.Background(), consOrder, "evt-rel-1"); err != nil {
		t.Fatalf("the delivery move: %v", err)
	}
	if len(r.calls) != 1 || r.calls[0] != consOrder {
		t.Fatalf("the delivery asked billing to release %v, want the one call for the order", r.calls)
	}
	if len(f.marked) != 1 {
		t.Fatalf("the delivery recorded %v, want the order marked delivered", f.marked)
	}
	if got := f.details[consOrder].Status; got != "FULFILLED" {
		t.Fatalf("the order is %s, want FULFILLED", got)
	}
}

// A redelivered delivery (the order already FULFILLED) re-reaches the release,
// the healing posture; the ledger's journal key keeps it exactly once.
func TestDeliveryRedeliveryReReachesTheRelease(t *testing.T) {
	svc, _, r := depositFixture(t, "FULFILLED")
	if err := svc.AdvanceFromDelivery(context.Background(), consOrder, "evt-rel-3"); !errors.Is(err, ErrNoTransition) {
		t.Fatalf("a redelivered delivery answers %v, want ErrNoTransition", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("the redelivery must re-reach the release, calls %v", r.calls)
	}
}

// The POS ring beat: the order sits INVOICED before the goods leave, so the
// ladder declines the move; the delivery is still a fact, so it releases.
func TestDeliveryReleasesWhenTheRingAlreadyInvoiced(t *testing.T) {
	svc, f, r := depositFixture(t, "INVOICED")
	if err := svc.AdvanceFromDelivery(context.Background(), consOrder, "dispatch:stop-1"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("the beat order answers %v, want ErrIllegalTransition", err)
	}
	if len(r.calls) != 1 || len(f.marked) != 1 {
		t.Fatalf("the beat order's delivery must release and record, calls %v marked %v", r.calls, f.marked)
	}
}

// A cancelled or closed order never releases or records a delivery.
func TestDeliveryReleasesNothingForACancelledOrder(t *testing.T) {
	svc, f, r := depositFixture(t, "CANCELLED")
	if err := svc.AdvanceFromDelivery(context.Background(), consOrder, "evt-rel-4"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("a cancelled order answers %v, want ErrIllegalTransition", err)
	}
	if len(r.calls) != 0 || len(f.marked) != 0 {
		t.Fatalf("a cancelled order released %v marked %v", r.calls, f.marked)
	}
}

// P2-2: the plain event writer, which a configurable task rule reaches, never
// releases and never records a delivery, from INVOICED or from anywhere else.
func TestATaskRuleMovingAnInvoicedOrderToFulfilledReleasesNothing(t *testing.T) {
	for _, from := range []string{"INVOICED", "READY"} {
		svc, f, r := depositFixture(t, from)
		_ = svc.AdvanceFromEvent(context.Background(), consOrder, "FULFILLED", "task-rule-1")
		if len(r.calls) != 0 || len(f.marked) != 0 {
			t.Fatalf("a task rule moving %s to FULFILLED released %v marked %v", from, r.calls, f.marked)
		}
	}
}

// A failed release never fails the delivery and never fails silently; the
// delivery stays recorded so the sweep can find it.
func TestAFailedReleaseIsLoggedNotRaised(t *testing.T) {
	buf := captureLog(t)
	svc, f, r := depositFixture(t, "READY")
	r.err = errors.New("release boom")
	if err := svc.AdvanceFromDelivery(context.Background(), consOrder, "evt-rel-5"); err != nil {
		t.Fatalf("the delivery move must stand: %v", err)
	}
	if len(f.marked) != 1 {
		t.Fatalf("the delivery must stay recorded for the sweep, marked %v", f.marked)
	}
	for _, want := range []string{"release boom", consOrder, "evt-rel-5"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("the failed release must be logged with %q, log: %q", want, buf.String())
		}
	}
}

// A failed delivery record is logged and the release is still attempted.
func TestAFailedDeliveryRecordIsLoggedAndTheReleaseStillRuns(t *testing.T) {
	buf := captureLog(t)
	svc, f, r := depositFixture(t, "READY")
	f.markErr = errors.New("mark boom")
	if err := svc.AdvanceFromDelivery(context.Background(), consOrder, "evt-rel-6"); err != nil {
		t.Fatalf("the delivery move must stand: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("the release must still run, calls %v", r.calls)
	}
	if !strings.Contains(buf.String(), "mark boom") {
		t.Fatalf("the failed record must be logged, log: %q", buf.String())
	}
}

// The sweep's read: which of these orders had a delivery recorded.
func TestDeliveredOrderIDsAnswersFromTheStore(t *testing.T) {
	svc, _, _ := depositFixture(t, "READY")
	_ = svc.AdvanceFromDelivery(context.Background(), consOrder, "evt-rel-7")
	got, err := svc.DeliveredOrderIDs(context.Background(), []string{consOrder, "other"})
	if err != nil || len(got) != 1 || got[0] != consOrder {
		t.Fatalf("delivered ids = %v, %v; want only the delivered order", got, err)
	}
}
