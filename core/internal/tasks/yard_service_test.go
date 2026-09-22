package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// yard_service_test.go, unit cover for the Wave 2 pick → STAGE → shortage flow against the in-memory
// store. These are the branch-fenced guards; the DB-backed equivalents live in
// yard_integration_test.go (build tag integration).

// inProgressPick drives a fresh task all the way to IN_PROGRESS, which is where a real picker is when
// they are standing in the yard with a cart.
func inProgressPick(t *testing.T, svc *Service) Task {
	t.Helper()
	ctx := context.Background()
	task, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: knownOrder, CreatedBy: "picker"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Claim(ctx, task.ID, "picker"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	got, err := svc.Accept(ctx, task.ID, "picker")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return got
}

// lastPost returns the most recent EVENT the fake notifier saw.
func lastPost(t *testing.T, n *fakeNotifier) TaskEventRef {
	t.Helper()
	if len(n.posts) == 0 {
		t.Fatal("no chat EVENT was posted")
	}
	return n.posts[len(n.posts)-1]
}

func TestStageMovesInProgressToStagedAndTellsTheCounter(t *testing.T) {
	svc, _, notifier := newSvc(t)
	task := inProgressPick(t, svc)

	staged, err := svc.Stage(context.Background(), StageInput{
		TaskID: task.ID, ActorID: "picker", Location: "Bay 3",
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if staged.Status != StatusStaged {
		t.Fatalf("status = %q, want STAGED", staged.Status)
	}
	if staged.StageLocation != "Bay 3" {
		t.Fatalf("stage location = %q", staged.StageLocation)
	}
	if staged.StagedAt == nil {
		t.Fatal("staged_at not stamped")
	}
	// No ref typed → a stable derived handle the counter can read out loud.
	if !strings.HasPrefix(staged.StageRef, "STG-") || len(staged.StageRef) != 10 {
		t.Fatalf("derived stage ref = %q, want STG-XXXXXX", staged.StageRef)
	}

	post := lastPost(t, notifier)
	if post.Audience != AudienceCounter {
		t.Fatalf("stage EVENT audience = %q, want COUNTER, the counter is who tells the customer", post.Audience)
	}
	if post.Topic != "task.staged" {
		t.Fatalf("stage EVENT topic = %q", post.Topic)
	}
	if !strings.Contains(post.Summary, "Bay 3") || !strings.Contains(post.Summary, staged.StageRef) {
		t.Fatalf("stage EVENT summary must name the location and ref, got %q", post.Summary)
	}
}

func TestStageDerivedRefIsStableForTheSameTask(t *testing.T) {
	// The counter and the yard say this handle to each other. If it changed between the screen the
	// picker looked at and the screen the counter looked at, they would be quoting different refs.
	a, b := shortRef("11111111-1111-4111-8111-111111111111"), shortRef("11111111-1111-4111-8111-111111111111")
	if a != b {
		t.Fatalf("shortRef is not deterministic: %q vs %q", a, b)
	}
	if a == shortRef("11111111-1111-4111-8111-111111111112") {
		t.Fatal("shortRef collides on adjacent ids")
	}
	for _, c := range a {
		if strings.ContainsRune("IO01", c) {
			t.Fatalf("shortRef %q contains a read-aloud-ambiguous character", a)
		}
	}
}

func TestStageRequiresALocation(t *testing.T) {
	svc, _, _ := newSvc(t)
	task := inProgressPick(t, svc)

	_, err := svc.Stage(context.Background(), StageInput{TaskID: task.ID, ActorID: "picker", Location: "   "})
	var br httpx.BadRequestError
	if !errors.As(err, &br) {
		t.Fatalf("blank location error = %v, want BadRequest, a staged pick nobody can find is worse than an unstaged one", err)
	}
}

func TestStageIsIllegalBeforeWorkStarts(t *testing.T) {
	svc, _, _ := newSvc(t)
	task, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.Stage(context.Background(), StageInput{TaskID: task.ID, Location: "Bay 3"})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("stage from QUEUED = %v, want ErrIllegalTransition", err)
	}
}

func TestStagedPickStillCompletes(t *testing.T) {
	svc, _, _ := newSvc(t)
	task := inProgressPick(t, svc)
	if _, err := svc.Stage(context.Background(), StageInput{TaskID: task.ID, Location: "Bay 3"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	done, err := svc.Done(context.Background(), task.ID, "counter", "")
	if err != nil {
		t.Fatalf("done from STAGED: %v", err)
	}
	if done.Status != StatusDone {
		t.Fatalf("status = %q", done.Status)
	}
}

func TestLegacyInProgressToDoneLaneSurvives0095(t *testing.T) {
	// 0095 INSERTED a state; it did not replace one. A will-call pick handed straight over the
	// counter never gets parked, and every task written before 0095 must still be closable.
	svc, _, _ := newSvc(t)
	task := inProgressPick(t, svc)
	done, err := svc.Done(context.Background(), task.ID, "picker", "")
	if err != nil {
		t.Fatalf("IN_PROGRESS → DONE must remain legal: %v", err)
	}
	if done.Status != StatusDone {
		t.Fatalf("status = %q", done.Status)
	}
}

func TestFlagShortageTellsTheCounterAndLeavesTheStatusAlone(t *testing.T) {
	svc, _, notifier := newSvc(t)
	task := inProgressPick(t, svc)

	sh, err := svc.FlagShortage(context.Background(), ShortageInput{
		TaskID: task.ID, Description: "Holland paver charcoal", QtyShort: "3", UOM: "PALLET",
		Note: "only 2 pallets on the yard", ReportedBy: "picker",
	})
	if err != nil {
		t.Fatalf("flag shortage: %v", err)
	}
	if sh.QtyShort != "3.0000" {
		t.Fatalf("qty_short = %q, want NUMERIC(19,4)-normalised", sh.QtyShort)
	}
	if !sh.Open() {
		t.Fatal("a freshly reported shortage must be open")
	}

	post := lastPost(t, notifier)
	if post.Audience != AudienceCounter {
		t.Fatalf("shortage EVENT audience = %q, want COUNTER, the yard already knows it is short", post.Audience)
	}
	if post.Topic != "task.shortage" {
		t.Fatalf("shortage EVENT topic = %q", post.Topic)
	}
	for _, want := range []string{"SHORTAGE", "3.0000", "PALLET", "Holland paver charcoal", "only 2 pallets on the yard"} {
		if !strings.Contains(post.Summary, want) {
			t.Fatalf("shortage EVENT summary missing %q: %q", want, post.Summary)
		}
	}
	if post.EventKey != "task:"+task.ID+":shortage:"+sh.ID {
		t.Fatalf("shortage EVENT key = %q, want one keyed on the shortage row (idempotent redelivery)", post.EventKey)
	}

	// A short line does not stop the rest of the pick.
	after, err := svc.PickDetail(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if after.Task.Status != StatusInProgress {
		t.Fatalf("status after shortage = %q, want IN_PROGRESS unchanged", after.Task.Status)
	}
	if after.Task.OpenShortages != 1 {
		t.Fatalf("open_shortages = %d, want 1", after.Task.OpenShortages)
	}
}

func TestFlagShortageRejectsUnusableInput(t *testing.T) {
	svc, _, _ := newSvc(t)
	task := inProgressPick(t, svc)
	ctx := context.Background()

	cases := []struct {
		name string
		in   ShortageInput
	}{
		{"no description", ShortageInput{TaskID: task.ID, QtyShort: "1"}},
		{"no qty", ShortageInput{TaskID: task.ID, Description: "pavers"}},
		{"zero qty", ShortageInput{TaskID: task.ID, Description: "pavers", QtyShort: "0"}},
		{"negative qty", ShortageInput{TaskID: task.ID, Description: "pavers", QtyShort: "-4"}},
		{"non-numeric qty", ShortageInput{TaskID: task.ID, Description: "pavers", QtyShort: "some"}},
		{"bad product id", ShortageInput{TaskID: task.ID, Description: "pavers", QtyShort: "1", ProductID: "nope"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.FlagShortage(ctx, c.in)
			var br httpx.BadRequestError
			if !errors.As(err, &br) {
				t.Fatalf("err = %v, want BadRequest", err)
			}
		})
	}
}

func TestResolveShortageIsAStatedOutcome(t *testing.T) {
	svc, _, notifier := newSvc(t)
	task := inProgressPick(t, svc)
	ctx := context.Background()
	sh, err := svc.FlagShortage(ctx, ShortageInput{TaskID: task.ID, Description: "HPB sand", QtyShort: "1.5", UOM: "TONNE"})
	if err != nil {
		t.Fatalf("flag: %v", err)
	}

	// A resolution with no account of HOW is how a customer gets told two different things.
	_, err = svc.ResolveShortage(ctx, ResolveShortageInput{TaskID: task.ID, ShortageID: sh.ID, Resolution: " "})
	var br httpx.BadRequestError
	if !errors.As(err, &br) {
		t.Fatalf("blank resolution = %v, want BadRequest", err)
	}

	got, err := svc.ResolveShortage(ctx, ResolveShortageInput{
		TaskID: task.ID, ShortageID: sh.ID, ActorID: "counter", Resolution: "substituted 1.5t washed sand",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Open() {
		t.Fatal("resolved shortage still reads open")
	}
	post := lastPost(t, notifier)
	if post.Audience != AudienceCounter || !strings.Contains(post.Summary, "substituted 1.5t washed sand") {
		t.Fatalf("resolution echo must carry the outcome to the counter: %+v", post)
	}

	// Second resolve is a conflict, never a silent overwrite of who cleared it first.
	_, err = svc.ResolveShortage(ctx, ResolveShortageInput{
		TaskID: task.ID, ShortageID: sh.ID, ActorID: "someone-else", Resolution: "credited",
	})
	if !errors.Is(err, ErrShortageResolved) {
		t.Fatalf("double resolve = %v, want ErrShortageResolved", err)
	}
}

func TestResolveUnknownShortageIsNotFound(t *testing.T) {
	svc, _, _ := newSvc(t)
	task := inProgressPick(t, svc)
	_, err := svc.ResolveShortage(context.Background(), ResolveShortageInput{
		TaskID: task.ID, ShortageID: "44444444-4444-4444-8444-444444444444", Resolution: "x",
	})
	if !errors.Is(err, ErrShortageNotFound) {
		t.Fatalf("err = %v, want ErrShortageNotFound", err)
	}
}

func TestPickQueueJoinsTicketsAndOpenShortagesInMemory(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	src := NewStrictStubPickSource().Seed(PickTicket{
		OrderID: knownOrder, OrderNo: "SO-2026-0001", BranchID: branchA,
		Lines: []PickLine{{LineNo: 1, SKU: "FIXTURE-PAVER-HOLLAND", Description: "pavers", QtyOrdered: "4.0000", UOM: "PALLET"}},
	})
	svc.WithPickSource(src)

	task := inProgressPick(t, svc)
	if _, err := svc.FlagShortage(ctx, ShortageInput{TaskID: task.ID, Description: "pavers", QtyShort: "1", UOM: "PALLET"}); err != nil {
		t.Fatalf("flag: %v", err)
	}

	entries, err := svc.PickQueue(ctx, TaskFilter{Limit: 20})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	// Cursor pagination (HH-3.2 B5): the total echo is gone with the offset
	// family; the page length is the assertion.
	if len(entries) != 1 {
		t.Fatalf("queue len=%d, want 1", len(entries))
	}
	e := entries[0]
	if e.Ticket == nil {
		t.Fatal("queue entry carries no ticket")
	}
	if e.Ticket.Source != "STUB" {
		t.Fatalf("ticket source = %q, a fixture-fed queue must say so on the wire", e.Ticket.Source)
	}
	if len(e.Shortages) != 1 || !e.Shortages[0].Open() {
		t.Fatalf("queue entry shortages = %+v, want one open", e.Shortages)
	}
}

func TestPickQueueStillShowsATaskWhoseFeedLostTheOrder(t *testing.T) {
	// Hiding it would turn a broken feed into an invisible one.
	svc, _, _ := newSvc(t)
	svc.WithPickSource(NewStrictStubPickSource()) // knows nothing
	if _, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder}); err != nil {
		t.Fatalf("create: %v", err)
	}
	entries, err := svc.PickQueue(context.Background(), TaskFilter{Limit: 20})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want the task shown anyway", len(entries))
	}
	if entries[0].Ticket != nil {
		t.Fatal("ticket must be nil when the source does not know the order")
	}
}

func TestPendingTicketsWithoutAWiredSourceIsAnHonestError(t *testing.T) {
	svc, _, _ := newSvc(t)
	if _, _, err := svc.PendingTickets(context.Background(), "", 10, 0); !errors.Is(err, ErrNoPickSource) {
		t.Fatalf("err = %v, want ErrNoPickSource (not an empty queue)", err)
	}
}

func TestSweepStalePicksNudgesOncePerDayAndChangesNothing(t *testing.T) {
	svc, store, notifier := newSvc(t)
	ctx := context.Background()
	task, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: knownOrder})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Age it past the window.
	aged := store.tasks[task.ID]
	aged.CreatedAt = time.Now().Add(-30 * time.Hour)
	store.tasks[task.ID] = aged

	before := len(notifier.posts)
	scanned, notified, err := svc.SweepStalePicks(ctx, 12*time.Hour, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if scanned != 1 || notified != 1 {
		t.Fatalf("sweep = (%d scanned, %d notified), want (1, 1)", scanned, notified)
	}
	post := lastPost(t, notifier)
	if post.Audience != AudienceYard || post.Topic != "task.stale" {
		t.Fatalf("stale nudge = %+v, want a YARD task.stale", post)
	}
	if !strings.Contains(post.EventKey, ":stale:"+time.Now().UTC().Format("2006-01-02")) {
		t.Fatalf("stale EventKey %q is not day-bucketed, a re-run would ping twice", post.EventKey)
	}
	if got := store.tasks[task.ID].Status; got != StatusQueued {
		t.Fatalf("sweep changed status to %q, it must only speak, never mutate", got)
	}
	if len(notifier.posts) != before+1 {
		t.Fatalf("sweep posted %d events for one task", len(notifier.posts)-before)
	}
}

func TestSweepIgnoresPicksInsideTheWindow(t *testing.T) {
	svc, _, notifier := newSvc(t)
	if _, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder}); err != nil {
		t.Fatalf("create: %v", err)
	}
	before := len(notifier.posts)
	scanned, notified, err := svc.SweepStalePicks(context.Background(), 12*time.Hour, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if scanned != 0 || notified != 0 || len(notifier.posts) != before {
		t.Fatalf("fresh task swept: scanned=%d notified=%d", scanned, notified)
	}
}
