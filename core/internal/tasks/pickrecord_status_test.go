package tasks

// pickrecord_status_test.go: a pick that is CANCELLED or DONE takes no record. Both are terminal in
// the module's own matrix (allowedTransitions: no move out of either) and the module has no correction
// path for a done pick, so a record onto one is refused before the order is told anything.

import (
	"context"
	"errors"
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

// A pick record or a skid count against a task that is not a PICK task is the same shape of refusal as
// a closed pick: the order must not move, the picked_lines table must not gain a row, the yard port
// must not be told, and the door must answer 409 with the module's words. The three non-PICK task
// types here are the ones the brief names (TypeNeedWeight, TypeDispatch, TypeOther) and the ones the
// demo box was originally asked to fix (FROM-PRINCIPAL row 2122 item 5 / REVIEW-YS-opus section 4:
// recording a pick against the NEED_WEIGHT task on DEMO-PLM-O14 repriced a line and took $8.02 off
// the order).
func TestRecordPick_RefusesANonPickTaskBeforeTheOrderMoves(t *testing.T) {
	for _, tt := range []string{TypeNeedWeight, TypeDispatch, TypeOther} {
		t.Run(tt, func(t *testing.T) {
			svc, store, _ := newSvc(t)
			yard := &fakeYardOrder{}
			svc.WithYardOrderChanges(yard)
			task, err := store.CreateTask(context.Background(), NewTaskInput{
				TaskType: tt, OrderID: "aaaaaaaa-0000-4000-8000-000000000001", BranchID: branchA, Source: "MANUAL",
			})
			if err != nil {
				t.Fatalf("seed task: %v", err)
			}
			fs := store

			// RecordPick must be refused at the door with ErrNotPickTask, the order untouched,
			// and the picked_lines map empty.
			_, err = svc.RecordPick(context.Background(), task.ID, "u-1",
				[]PickedLineInput{{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}})
			if !errors.Is(err, ErrNotPickTask) {
				t.Fatalf("RecordPick on %s: want ErrNotPickTask, got %v", tt, err)
			}
			if len(yard.picks) != 0 {
				t.Fatalf("RecordPick on %s: the order must not be told, got %d calls", tt, len(yard.picks))
			}
			if len(fs.picked[task.ID]) != 0 {
				t.Fatalf("RecordPick on %s: nothing recorded, got %+v", tt, fs.picked[task.ID])
			}

			// RecordSkids must be refused at the door with ErrNotPickTask, the order untouched,
			// and the picked_lines map still empty.
			_, err = svc.RecordSkids(context.Background(), SkidInput{
				TaskID: task.ID, SkidCount: 2, Reason: ShortReasonCondensed, Note: "two became one",
			})
			if !errors.Is(err, ErrNotPickTask) {
				t.Fatalf("RecordSkids on %s: want ErrNotPickTask, got %v", tt, err)
			}
			if len(yard.skids) != 0 {
				t.Fatalf("RecordSkids on %s: the order must not be told, got %d calls", tt, len(yard.skids))
			}
			if len(fs.picked[task.ID]) != 0 {
				t.Fatalf("RecordSkids on %s: nothing recorded, got %+v", tt, fs.picked[task.ID])
			}
		})
	}
}

// The direct door's class for "not a pick task" is the module's conflict class (409), the same as a
// closed pick or an order that cannot take the pick, with the module's words. Companion to
// TestWriteYardErr_AClosedPickIsAConflict so each refusal the door maps to 409 has its own witness.
func TestWriteYardErr_ANonPickTaskIsAConflict(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).writeYardErr(rec,
		httptest.NewRequest(http.MethodPut, "/api/v1/yard/picks/x/lines", nil), ErrNotPickTask)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 for ErrNotPickTask, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "this is not a pick task") {
		t.Fatalf("want the refusal's own words in the body, got %s", rec.Body.String())
	}
}
