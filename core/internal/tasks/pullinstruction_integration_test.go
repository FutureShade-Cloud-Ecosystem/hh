//go:build integration

// pullinstruction_integration_test.go, the yard-side half of "the rack tag follows the pick".
//
//	DATABASE_URL=... go test -tags integration -count=1 -run OrderPickStatus ./internal/tasks/
//
// 0092 shipped will-call on the sentence "staff mint a pickup token when the order goes on the rack",
// which assumed somebody had already pulled the material with nothing recording that they had.
// dispatch now asks this module first, through a seam the composition root satisfies over
// Repository.OrderPickStatus. This file pins what that read answers, against a real board.
//
// FIXTURES RESTORE WHAT THEY TOUCH, newYardFixture's cleanup removes every task, event and shortage
// bound to the synthetic order.

package tasks

import (
	"context"
	"testing"
)

// TestOrderPickStatus_TracksTheLivePickThroughItsLifecycle is the read as dispatch will consume it.
// The transitions are driven through the real service, so what is pinned is the board's own state
// machine rather than a hand-written row.
func TestOrderPickStatus_TracksTheLivePickThroughItsLifecycle(t *testing.T) {
	svc, _, pool, orderID, _ := newYardFixture(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	// An order with no pick at all is an ABSENCE, not an error. dispatch turns this into "nothing has
	// been picked for this order yet" rather than "the yard could not be asked".
	status, err := repo.OrderPickStatus(ctx, orderID)
	if err != nil {
		t.Fatalf("an order with no pick must not error: %v", err)
	}
	if status != "" {
		t.Fatalf("status = %q, want empty for an order with no pick", status)
	}

	task := pickToInProgress(t, svc, orderID)
	if status, err = repo.OrderPickStatus(ctx, orderID); err != nil || status != StatusInProgress {
		t.Fatalf("status = %q (err %v), want %q, a pick somebody is halfway through is a promise, not a pallet", status, err, StatusInProgress)
	}

	if _, err := svc.Stage(ctx, StageInput{TaskID: task.ID, Location: "Bay 3", Ref: "STG-PULL1"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if status, err = repo.OrderPickStatus(ctx, orderID); err != nil || status != StatusStaged {
		t.Fatalf("status = %q (err %v), want %q, this is the state that mints a tag", status, err, StatusStaged)
	}

	if _, err := svc.Done(ctx, task.ID, "", ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	if status, err = repo.OrderPickStatus(ctx, orderID); err != nil || status != StatusDone {
		t.Fatalf("status = %q (err %v), want %q, a handed-over pick still proves the material was pulled", status, err, StatusDone)
	}
}

// TestOrderPickStatus_ACancelledPickIsNotAState, a cancelled pick followed by a re-queued one is a
// normal day in a yard. "Has this order been picked" is a question about the pick that is live now.
func TestOrderPickStatus_ACancelledPickIsNotAState(t *testing.T) {
	svc, _, pool, orderID, _ := newYardFixture(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	task, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: orderID, Source: SourceManual})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Cancel(ctx, task.ID, "", "customer changed their mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	status, err := repo.OrderPickStatus(ctx, orderID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "" {
		t.Fatalf("status = %q, a cancelled pick is not a state anybody is waiting on", status)
	}

	// The re-queued pick is the one that answers.
	if _, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: orderID, Source: SourceManual}); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if status, err = repo.OrderPickStatus(ctx, orderID); err != nil || status != StatusQueued {
		t.Fatalf("status = %q (err %v), want %q from the live pick", status, err, StatusQueued)
	}
}

// TestOrderPickStatus_AGarbageOrderIsAnAbsenceNotAnError, the id reaches this read straight off a
// caller-supplied reference. An unparseable one must not arrive at Postgres and come back as an
// internal error, which is what would make a will-call mint refuse with "the yard could not be asked".
func TestOrderPickStatus_AGarbageOrderIsAnAbsenceNotAnError(t *testing.T) {
	pool := yardPool(t)
	status, err := NewRepository(pool).OrderPickStatus(context.Background(), "the blue pallets")
	if err != nil {
		t.Fatalf("an unparseable order id must not error: %v", err)
	}
	if status != "" {
		t.Fatalf("status = %q, want empty", status)
	}
}
