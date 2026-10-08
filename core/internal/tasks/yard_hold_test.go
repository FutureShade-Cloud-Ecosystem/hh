package tasks

// yard_hold_test.go, item 9 Opus P2-1: the yard cannot stage or claim a held
// order's task. The pick feed marks the held rows (the ticket carries the
// mark); these legs are the doors: Stage and Claim refuse an order held from
// picking under a pay before release shape, with the mandated sentence.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeHoldCheck is the fake over the pick hold seam: canned facts, one call
// shape.
type fakeHoldCheck struct {
	held bool
	owed int64
	err  error
}

func (f fakeHoldCheck) OrderHeldFromPicking(context.Context, string) (bool, int64, error) {
	return f.held, f.owed, f.err
}

// heldSvc is a service whose hold check answers the canned facts.
func heldSvc(t *testing.T, check PickHoldCheck) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	notifier := &fakeNotifier{}
	orders := fakeOrderReader{orders: map[string]OrderBrief{
		knownOrder: {ID: knownOrder, BranchID: branchA, Fulfillment: "PICKUP", Status: "CONFIRMED"},
	}}
	svc := NewService(store).WithOrderReader(orders).WithChatNotifier(notifier).WithPickHoldCheck(check)
	return svc, store
}

// Stage refuses a held order's task with the mandated sentence. The task is
// driven to IN_PROGRESS before the hold lands, which is the real shape: an
// NSF re-owes an order that was clear when the picker claimed it.
func TestStage_HeldOrderRefusesWithTheWords(t *testing.T) {
	svc, store := heldSvc(t, nil)
	task := inProgressPick(t, svc)
	svc.WithPickHoldCheck(fakeHoldCheck{held: true, owed: 8800})

	_, err := svc.Stage(context.Background(), StageInput{TaskID: task.ID, ActorID: "picker", Location: "Bay 3"})
	if !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("a held order staged: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot reach picking") {
		t.Fatalf("the refusal's words: %v", err)
	}
	if got, gerr := store.GetTask(context.Background(), task.ID); gerr != nil || got.Status != StatusInProgress {
		t.Fatalf("the refused stage must not move the task: %s %v", got.Status, gerr)
	}
}

// A hold read that cannot be made refuses the stage (money fails closed).
func TestStage_UnreadableHoldRefuses(t *testing.T) {
	svc, _ := heldSvc(t, nil)
	task := inProgressPick(t, svc)
	svc.WithPickHoldCheck(fakeHoldCheck{err: context.DeadlineExceeded})

	_, err := svc.Stage(context.Background(), StageInput{TaskID: task.ID, ActorID: "picker", Location: "Bay 3"})
	if !errors.Is(err, ErrHoldUnreadable) {
		t.Fatalf("an unreadable hold must refuse the stage: %v", err)
	}
}

// A nil hold check allows: the additive lane, the boot fail-fast's counterpart.
func TestStage_NilHoldCheckAllows(t *testing.T) {
	svc, _ := heldSvc(t, nil)
	task := inProgressPick(t, svc)

	staged, err := svc.Stage(context.Background(), StageInput{TaskID: task.ID, ActorID: "picker", Location: "Bay 3"})
	if err != nil || staged.Status != StatusStaged {
		t.Fatalf("a nil hold check must not refuse the stage: %v %s", err, staged.Status)
	}
}

// Claim refuses a held order's task before the picker can pull it.
func TestClaim_HeldOrderRefusesWithTheWords(t *testing.T) {
	svc, store := heldSvc(t, fakeHoldCheck{held: true, owed: 8800})
	task, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder, CreatedBy: "picker"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Claim(context.Background(), task.ID, "picker")
	if !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("a held order's task was claimed: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot reach picking") {
		t.Fatalf("the refusal's words: %v", err)
	}
	if after, gerr := store.GetTask(context.Background(), task.ID); gerr != nil || after.Status != StatusQueued || after.ClaimedBy != "" {
		t.Fatalf("the refused claim must not move the task: %+v %v", after, gerr)
	}
	_ = got
}

func TestClaim_UnreadableHoldRefuses(t *testing.T) {
	svc, _ := heldSvc(t, fakeHoldCheck{err: context.DeadlineExceeded})
	task, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder, CreatedBy: "picker"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Claim(context.Background(), task.ID, "picker"); !errors.Is(err, ErrHoldUnreadable) {
		t.Fatalf("an unreadable hold must refuse the claim: %v", err)
	}
}

// A nil hold check allows the claim (the additive lane).
func TestClaim_NilHoldCheckAllows(t *testing.T) {
	svc, _ := heldSvc(t, nil)
	task, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder, CreatedBy: "picker"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.Claim(context.Background(), task.ID, "picker")
	if err != nil || got.Status != StatusAccepted {
		t.Fatalf("a nil hold check must not refuse the claim: %v %s", err, got.Status)
	}
}

// The service reports its hold check wired exactly when one was given (the
// composition root's boot assertion's fact).
func TestPickHoldCheckWired(t *testing.T) {
	svc, _, _ := newSvc(t)
	if svc.PickHoldCheckWired() {
		t.Fatal("a bare service reports its hold check wired")
	}
	if !svc.WithPickHoldCheck(fakeHoldCheck{}).PickHoldCheckWired() {
		t.Fatal("a wired hold check reports itself unwired")
	}
}
