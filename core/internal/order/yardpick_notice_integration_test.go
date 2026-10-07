//go:build integration

// The rep hears about every short, including one that follows an edit which cleared the line's
// earlier shortfall: the revision that keys the notice survives the clearing, so the second short
// is a second message and not a duplicate of the first. Run through the real tasks service, the real
// order module and the real messaging store.

package order

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

const noticeRep = "d7000000-0000-4000-9000-000000000003" // a seeded Trenton staff user

type orderAsYard struct{ svc *Service }

func (a orderAsYard) ApplyPicks(ctx context.Context, in tasks.YardPickChange) (tasks.YardPickResult, error) {
	oin := YardPickInput{OrderID: in.OrderID, ActorID: in.ActorID}
	for _, l := range in.Lines {
		oin.Lines = append(oin.Lines, YardPickLineInput{LineNo: l.LineNo, QtyPicked: l.QtyPicked, Reason: l.Reason, Note: l.Note})
	}
	out, err := a.svc.ApplyYardPicks(ctx, oin)
	if err != nil {
		return tasks.YardPickResult{}, err
	}
	res := tasks.YardPickResult{Order: tasks.YardOrderFacts(out.Order)}
	for _, l := range out.Lines {
		res.Lines = append(res.Lines, tasks.YardLineEffect(l))
	}
	return res, nil
}

func (a orderAsYard) RecordSkids(context.Context, tasks.YardSkidChange) (tasks.YardSkidResult, error) {
	return tasks.YardSkidResult{}, nil
}

// repNotifier is the composition root's rep routing in miniature: the ref's own key and text, to the
// order's rep through the real messaging service.
type repNotifier struct{ msg *messaging.Service }

func (n repNotifier) PostTaskEvent(ctx context.Context, ref tasks.TaskEventRef) error {
	_, err := n.msg.NotifyOrderRep(ctx, messaging.OrderRepNotice{
		CustomerID: ref.CustomerID, OrderID: ref.OrderID, OrderRepUserID: ref.RepUserID,
		EventKey: ref.EventKey, Body: ref.Summary,
		Ref: messaging.EventRef{Topic: ref.Topic, Entity: ref.Entity, Summary: ref.Summary},
	})
	return err
}

func TestIntegration_YardPick_AShortAfterAnEditIsAnnouncedAgain(t *testing.T) {
	pool := intPool(t)
	ord := seedTaxFreeYard(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE orders SET salesperson_id = $2 WHERE id = $1`, ord, noticeRep); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE event_key LIKE $1`, "yard.short:"+ord+":%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM tasks WHERE order_id = $1`, ord)
	})
	osvc, _ := editSvc(t, pool)
	var branch string
	if err := pool.QueryRow(ctx, `SELECT branch_id::text FROM orders WHERE id = $1`, ord).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	trepo := tasks.NewRepository(pool)
	task, err := trepo.CreateTask(ctx, tasks.NewTaskInput{OrderID: ord, BranchID: branch, TaskType: tasks.TypePick, Source: "MANUAL", Priority: tasks.PriorityNormal})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	tsvc := tasks.NewService(trepo).
		WithYardOrderChanges(orderAsYard{svc: osvc}).
		WithChatNotifier(repNotifier{msg: messaging.NewService(messaging.NewRepository(pool))})

	record := func(ordered, picked string) []tasks.PickedLine {
		t.Helper()
		got, err := tsvc.RecordPick(ctx, task.ID, noticeRep, []tasks.PickedLineInput{{
			OrderLineNo: 1, QtyOrdered: ordered, QtyPicked: picked, UOM: "SF",
			ShortReason: tasks.ShortReasonCondensed}})
		if err != nil {
			t.Fatalf("record %s of %s: %v", picked, ordered, err)
		}
		return got
	}
	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM msg_messages WHERE event_key LIKE $1`, "yard.short:"+ord+":%").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	record("5", "3")
	if n := count(); n != 1 {
		t.Fatalf("the first short is announced: %d", n)
	}
	mgr := identity.ContextWithStaff(ctx, edIntManagerCtx())
	if _, err := osvc.EditLineQty(mgr, ord, 1, "8"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	record("8", "6")
	if n := count(); n != 2 {
		t.Fatalf("the second short, after the edit cleared the first, is a second message with its own key: %d", n)
	}
	var keys int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT event_key) FROM msg_messages WHERE event_key LIKE $1`, "yard.short:"+ord+":%").Scan(&keys); err != nil || keys != 2 {
		t.Fatalf("two distinct keys: %d %v", keys, err)
	}
}
