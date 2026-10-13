package tasks

// pickrecord_status_test.go: a pick that is CANCELLED or DONE takes no record. Both are terminal in
// the module's own matrix (allowedTransitions: no move out of either) and the module has no correction
// path for a done pick, so a record onto one is refused before the order is told anything.

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// The direct door's class for a closed pick is the module's conflict class (409), the same as an
// order that cannot take the pick, with the module's words.
func TestWriteYardErr_AClosedPickIsAConflict(t *testing.T) {
	for status, words := range map[string]string{
		StatusCancelled: "this pick was cancelled, its record cannot change",
		StatusDone:      "this pick is done, its record cannot change",
	} {
		rec := httptest.NewRecorder()
		(&Handler{}).writeYardErr(rec, httptest.NewRequest(http.MethodPut, "/api/v1/yard/picks/x/lines", nil), pickClosed(status))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), words) {
			t.Fatalf("%s: want 409 with %q, got %d %s", status, words, rec.Code, rec.Body.String())
		}
	}
}
