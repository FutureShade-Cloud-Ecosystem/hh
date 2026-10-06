package order

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// qd5_completion_test.go, QD-5 review round 2 (P3-3): the fulfilment road's completion of the counter's
// invoice (consumer.go, completeCounterInvoiced) never fails the hand over, and never fails silently: a
// lookup or move that fails is logged with the order id and the event id, and a redelivered
// order.fulfilled (the order already FULFILLED) reaches the same completion.

// counterStore is the fake store plus the one read the completion needs, and a way to fail the move.
type counterStore struct {
	*fakeStore
	has        bool
	hasErr     error
	invoiceErr error // returned by the guarded write that targets INVOICED
}

func (c *counterStore) OrderHasCounterInvoice(context.Context, string) (bool, error) {
	return c.has, c.hasErr
}

func (c *counterStore) UpdateStatusFrom(ctx context.Context, orderID, newStatus string, allowedFrom []string) (string, error) {
	if newStatus == "INVOICED" && c.invoiceErr != nil {
		return "", c.invoiceErr
	}
	return c.fakeStore.UpdateStatusFrom(ctx, orderID, newStatus, allowedFrom)
}

func (c *counterStore) UpdateStatusFromChecked(ctx context.Context, orderID, newStatus string, allowedFrom []string, pre func(context.Context) error) (string, error) {
	if newStatus == "INVOICED" && c.invoiceErr != nil {
		return "", c.invoiceErr
	}
	return c.fakeStore.UpdateStatusFromChecked(ctx, orderID, newStatus, allowedFrom, pre)
}

// captureLog swaps the process logger for a buffer for the length of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prior) })
	return &buf
}

func completionFixture(t *testing.T, status string, cs *counterStore) *LifecycleConsumer {
	t.Helper()
	cs.fakeStore = newFakeStore(mkDetail(consOrder, consCust, status))
	svc := NewService(cs).WithAudit(&recordingRecorder{})
	return NewLifecycleConsumer(svc, &fakeInvoiceResolver{}, nil)
}

func TestCompletion_AFailedLookupIsLoggedWithTheOrderAndTheEvent(t *testing.T) {
	buf := captureLog(t)
	cs := &counterStore{hasErr: errors.New("lookup boom")}
	c := completionFixture(t, "READY", cs)
	if err := c.onFulfilled(context.Background(), evt("order.fulfilled", "evt-lookup", `{"order_id":"`+consOrder+`"}`)); err != nil {
		t.Fatalf("the hand over must not fail on the completion: %v", err)
	}
	if got := cs.details[consOrder].Status; got != "FULFILLED" {
		t.Fatalf("the hand over stands, order is %s", got)
	}
	for _, want := range []string{"lookup boom", consOrder, "evt-lookup"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("the failed completion must be logged with %q, log: %q", want, buf.String())
		}
	}
}

func TestCompletion_AFailedMoveIsLoggedWithTheOrderAndTheEvent(t *testing.T) {
	buf := captureLog(t)
	cs := &counterStore{has: true, invoiceErr: errors.New("move boom")}
	c := completionFixture(t, "READY", cs)
	if err := c.onFulfilled(context.Background(), evt("order.fulfilled", "evt-move", `{"order_id":"`+consOrder+`"}`)); err != nil {
		t.Fatalf("the hand over must not fail on the completion: %v", err)
	}
	for _, want := range []string{"move boom", consOrder, "evt-move"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("the failed completion must be logged with %q, log: %q", want, buf.String())
		}
	}
}

// TestCompletion_ARedeliveredHandOverReachesTheCompletion: the first delivery moved the order to
// FULFILLED and its completion failed; the redelivery finds the order already FULFILLED (no transition)
// and still completes the move, because the completion is idempotent.
func TestCompletion_ARedeliveredHandOverReachesTheCompletion(t *testing.T) {
	captureLog(t)
	cs := &counterStore{has: true, hasErr: errors.New("first try fails")}
	c := completionFixture(t, "READY", cs)
	e := evt("order.fulfilled", "evt-redeliver", `{"order_id":"`+consOrder+`"}`)
	if err := c.onFulfilled(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := cs.details[consOrder].Status; got != "FULFILLED" {
		t.Fatalf("after the failed completion the order is %s, want FULFILLED", got)
	}
	cs.hasErr = nil
	if err := c.onFulfilled(context.Background(), e); err != nil {
		t.Fatalf("the redelivery: %v", err)
	}
	if got := cs.details[consOrder].Status; got != "INVOICED" {
		t.Fatalf("the redelivery must complete the move, order is %s", got)
	}
	// A third delivery is the same event again: nothing moves, nothing fails.
	if err := c.onFulfilled(context.Background(), e); err != nil {
		t.Fatalf("a third delivery: %v", err)
	}
	if got := cs.details[consOrder].Status; got != "INVOICED" {
		t.Fatalf("order is %s", got)
	}
}
