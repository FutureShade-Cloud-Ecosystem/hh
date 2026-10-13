package tasks

// pickrecord_status_test.go: a pick that is CANCELLED or DONE takes no record. Both are terminal in
// the module's own matrix (allowedTransitions: no move out of either) and the module has no correction
// path for a done pick, so a record onto one is refused before the order is told anything.

import (
	"context"
	"strings"
	"testing"
)

func TestRecordPick_RefusesATerminalPickBeforeTheOrderMoves(t *testing.T) {
	for _, status := range []string{StatusCancelled, StatusDone} {
		t.Run(status, func(t *testing.T) {
			svc, store, _ := newSvc(t)
			yard := &fakeYardOrder{}
			svc.WithYardOrderChanges(yard)
			task, err := store.CreateTask(context.Background(), NewTaskInput{
				TaskType: TypePick, OrderID: "aaaaaaaa-0000-4000-8000-000000000001", BranchID: branchA, Source: "MANUAL",
			})
			if err != nil {
				t.Fatalf("seed task: %v", err)
			}
			fs := store
			closed := fs.tasks[task.ID]
			closed.Status = status
			fs.tasks[task.ID] = closed

			_, err = svc.RecordPick(context.Background(), task.ID, "", []PickedLineInput{{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}})
			if err == nil || !strings.Contains(err.Error(), "its record cannot change") {
				t.Fatalf("want the closed pick refusal in the module's words, got %v", err)
			}
			if len(yard.picks) != 0 {
				t.Fatalf("the order must not be told: %d calls", len(yard.picks))
			}
			if len(fs.picked[task.ID]) != 0 {
				t.Fatalf("nothing is recorded: %+v", fs.picked[task.ID])
			}
		})
	}
}
