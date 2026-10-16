package tasks

// pickrecord_type_test.go: the record door takes a PICK task and nothing else. A NEED_WEIGHT task's
// line is measured by the weight round trip, not by a pick record, and a record onto one would
// backorder an order line and reprice the order for material the yard never picked (the review's
// section 4 money finding: the door checked the status only, never the type). The guard lives where
// the row is locked, in the store, so every door that reaches the record body gets it.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRecordPick_RefusesATaskThatIsNotAPick: a record against a task whose type is not PICK is
// refused as a domain error, the order is never told, and nothing is written. A PICK task through
// the same door and the same lines records fine, so the guard refuses only the types it names.
func TestRecordPick_RefusesATaskThatIsNotAPick(t *testing.T) {
	for _, taskType := range []string{TypeNeedWeight, TypeDispatch, TypeOther} {
		t.Run(taskType, func(t *testing.T) {
			svc, store, _ := newSvc(t)
			yard := &fakeYardOrder{}
			svc.WithYardOrderChanges(yard)
			task, err := store.CreateTask(context.Background(), NewTaskInput{
				TaskType: taskType, OrderID: "aaaaaaaa-0000-4000-8000-000000000001", BranchID: branchA, Source: "MANUAL",
			})
			if err != nil {
				t.Fatalf("seed task: %v", err)
			}

			_, err = svc.RecordPick(context.Background(), task.ID, "u-1", []PickedLineInput{
				{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"},
			})
			if err == nil {
				t.Fatalf("a %s task must not take a pick record", taskType)
			}
			if !errors.Is(err, ErrNotAPick) {
				t.Fatalf("want the not-a-pick domain refusal, got %v", err)
			}
			if !strings.Contains(err.Error(), taskType) {
				t.Fatalf("the refusal must name the type it refused: %v", err)
			}
			if len(yard.picks) != 0 {
				t.Fatalf("the order must not be told: %d calls", len(yard.picks))
			}
			if len(store.picked[task.ID]) != 0 {
				t.Fatalf("nothing is recorded: %+v", store.picked[task.ID])
			}
		})
	}
}

// TestRecordPick_APickTaskStillRecords: the control. The guard is a fence around the door, not a
// door shut: the same record against a PICK task still goes through and reaches the order.
func TestRecordPick_APickTaskStillRecords(t *testing.T) {
	svc, store, _ := newSvc(t)
	yard := &fakeYardOrder{}
	svc.WithYardOrderChanges(yard)
	task, err := store.CreateTask(context.Background(), NewTaskInput{
		TaskType: TypePick, OrderID: "aaaaaaaa-0000-4000-8000-000000000001", BranchID: branchA, Source: "MANUAL",
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	got, err := svc.RecordPick(context.Background(), task.ID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"},
	})
	if err != nil {
		t.Fatalf("a PICK task still records: %v", err)
	}
	if len(got) != 1 || got[0].QtyPicked != "10.0000" {
		t.Fatalf("the recorded line is wrong: %+v", got)
	}
	if len(yard.picks) != 1 {
		t.Fatalf("the order must still be told for a PICK task: %d calls", len(yard.picks))
	}
}

// The direct door's class for a not-a-pick refusal is the module's conflict class (409), the same
// family as a closed pick, with the type in the words: a 4xx domain error, never a 500.
func TestWriteYardErr_ANonPickTaskIsAConflict(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).writeYardErr(rec,
		httptest.NewRequest(http.MethodPut, "/api/v1/yard/picks/x/lines", nil),
		notAPick(TypeNeedWeight))
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), TypeNeedWeight) {
		t.Fatalf("the body must name the type it refused: %s", rec.Body.String())
	}
}
