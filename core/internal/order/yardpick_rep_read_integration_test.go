//go:build integration

// The Phase 0 item 6 proof, at source: a short pick recorded with a reason through the yard's own
// door (the tasks service over the real order module and the real messaging store) backorders the
// order line, and the order's rep is told in a message the rep's own read answers (the DM
// channel's message list), which is the leg the roadmap proves "through the rep's API read" until
// House Messages is live. Nothing here peeks a table the rep cannot see: the line comes back
// through the order's own read shape and the notice through the rep's channel read.

package order

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

func TestIntegration_YardPick_ShortWithReasonBackordersTheLineAndReachesTheRep(t *testing.T) {
	pool := intPool(t)
	ord := seedTaxFreeYard(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE orders SET salesperson_id = $2 WHERE id = $1`, ord, noticeRep); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM msg_messages WHERE event_key LIKE $1`, "yard.short:"+ord+":%")
		_, _ = pool.Exec(c, `DELETE FROM tasks WHERE order_id = $1`, ord)
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
	msgRepo := messaging.NewRepository(pool)
	tsvc := tasks.NewService(trepo).
		WithYardOrderChanges(orderAsYard{svc: osvc}).
		WithChatNotifier(repNotifier{msg: messaging.NewService(msgRepo)})

	picked, err := tsvc.RecordPick(ctx, task.ID, noticeRep, []tasks.PickedLineInput{{
		OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "3", UOM: "SF", ShortReason: tasks.ShortReasonCondensed}})
	if err != nil {
		t.Fatalf("record the short pick: %v", err)
	}
	if len(picked) != 1 || picked[0].QtyPicked != "3.0000" || picked[0].ShortReason != tasks.ShortReasonCondensed {
		t.Fatalf("the recorded line is %+v, want 3 of 5 picked short with the reason", picked)
	}

	// The line: 2 of 5 held as the line's backorder (0369's invariant, ordered = qty + backorder),
	// read through the same read the edit door holds.
	if b := liveBackorder(t, pool, ord); b.entered != "2.0000" || b.base != "2.0000" {
		t.Fatalf("the line holds backorder %s/%s, want 2 of 5 on backorder", b.entered, b.base)
	}
	var qty, ordered, shortfallOrdered, reason string
	if err := pool.QueryRow(ctx, `
SELECT ol.qty_entered::text, (ol.qty_entered + ol.backorder_entered)::text,
       sf.ordered_entered::text, sf.reason
  FROM order_lines ol JOIN order_line_shortfalls sf ON sf.order_line_id = ol.id
 WHERE ol.order_id = $1 AND ol.line_no = 1`, ord).Scan(&qty, &ordered, &shortfallOrdered, &reason); err != nil {
		t.Fatalf("the shortfall row: %v", err)
	}
	if qty != "3.0000" || ordered != "5.0000" || shortfallOrdered != "5.0000" || reason != "CONDENSED_SKIDS" {
		t.Fatalf("the line reads qty %s of ordered %s (shortfall froze %s, reason %s), want 3 of 5 frozen with the reason", qty, ordered, shortfallOrdered, reason)
	}

	// The rep's read: the DM channel the rep's screen lists carries the notice, with the reason in
	// words and the backorder figure the line now holds.
	channel, err := msgRepo.EnsureRepDMChannel(ctx, noticeRep)
	if err != nil {
		t.Fatalf("the rep's channel: %v", err)
	}
	page, err := msgRepo.ListMessages(ctx, channel, "", 50)
	if err != nil {
		t.Fatalf("the rep's message list: %v", err)
	}
	var notice *string
	for i := range page.Messages {
		m := &page.Messages[i]
		if m.Kind == "EVENT" && strings.Contains(string(m.Ref), `"yard.short_pick"`) &&
			strings.Contains(string(m.Ref), `"order:`+ord+`"`) {
			body := m.Body
			notice = &body
			break
		}
	}
	if notice == nil {
		t.Fatalf("the rep's channel holds no short pick notice for the order: %+v", page.Messages)
	}
	for _, want := range []string{"2.0000 T on backorder", "Skids condensed"} {
		if !strings.Contains(*notice, want) {
			t.Fatalf("the notice %q does not carry %q", *notice, want)
		}
	}
}
