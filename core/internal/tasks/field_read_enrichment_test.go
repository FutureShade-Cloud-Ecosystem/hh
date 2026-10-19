package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPickQueueAndDetail_PropagateRunNoDepartsAtAndLoadPosition(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	departs := time.Unix(1786004100, 0).UTC()

	src := NewStubPickSource().Seed(PickTicket{
		OrderID:      knownOrder,
		OrderNo:      "#SO-R5-3",
		BranchID:     branchA,
		CustomerName: "Harbour Builders",
		Fulfillment:  "DELIVERY",
		Priority:     PriorityHigh,
		Reference:    "stop-3",
		RunNo:        "R5",
		RunDepartsAt: &departs,
		LoadPosition: 1,
		Lines: []PickLine{
			{LineNo: 1, SKU: "PAV-1", Description: "Paver", QtyOrdered: "4.0000", UOM: "PLT"},
		},
	})
	svc.WithPickSource(src)

	task, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: knownOrder, CreatedBy: "picker"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	queue, err := svc.PickQueue(ctx, TaskFilter{BranchID: branchA})
	if err != nil {
		t.Fatalf("PickQueue: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("queue len = %d, want 1", len(queue))
	}
	entry := queue[0]
	if entry.RunNo != "R5" || entry.RunDepartsAt == nil || !entry.RunDepartsAt.Equal(departs) || entry.LoadPosition != 1 {
		t.Fatalf("queue entry truck fields = (run_no=%q, departs=%v, load_pos=%d), want (R5, %v, 1)",
			entry.RunNo, entry.RunDepartsAt, entry.LoadPosition, departs)
	}
	if entry.Ticket == nil || entry.Ticket.RunNo != "R5" || entry.Ticket.RunDepartsAt == nil || !entry.Ticket.RunDepartsAt.Equal(departs) || entry.Ticket.LoadPosition != 1 {
		t.Fatalf("queue ticket truck fields = %+v, want (R5, %v, 1)", entry.Ticket, departs)
	}

	detail, err := svc.PickDetail(ctx, task.ID)
	if err != nil {
		t.Fatalf("PickDetail: %v", err)
	}
	if detail.RunNo != "R5" || detail.RunDepartsAt == nil || !detail.RunDepartsAt.Equal(departs) || detail.LoadPosition != 1 {
		t.Fatalf("detail truck fields = (run_no=%q, departs=%v, load_pos=%d), want (R5, %v, 1)",
			detail.RunNo, detail.RunDepartsAt, detail.LoadPosition, departs)
	}

	// Verify unassigned Will Call omits run_no, run_departs_at, and load_position on the JSON wire.
	unassignedEntry := PickQueueEntry{
		Task:   task,
		Ticket: &PickTicket{OrderID: knownOrder, Fulfillment: "WILL_CALL", Lines: []PickLine{}},
	}
	raw, err := json.Marshal(unassignedEntry)
	if err != nil {
		t.Fatalf("marshal unassigned: %v", err)
	}
	for _, key := range []string{`"run_no"`, `"run_departs_at"`, `"load_position"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("unassigned entry leaked %s in JSON: %s", key, raw)
		}
	}
}
