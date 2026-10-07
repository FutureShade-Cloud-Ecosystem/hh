package tasks

// yardskids_test.go: the yard records or changes the skid count on an order, with a reason.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func (f *fakeYardOrder) RecordSkids(_ context.Context, in YardSkidChange) (YardSkidResult, error) {
	f.skids = append(f.skids, in)
	if f.err != nil {
		return YardSkidResult{}, f.err
	}
	return YardSkidResult{Order: f.facts, CountAfter: in.SkidCount, Changed: true}, nil
}

func TestRecordSkids_NeedsAReasonAndASaneCount(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		in    SkidInput
		wants string
	}{
		{"negative", SkidInput{SkidCount: -1, Reason: ShortReasonCondensed}, "negative"},
		{"no reason", SkidInput{SkidCount: 2}, "reason"},
		{"unknown reason", SkidInput{SkidCount: 2, Reason: "BECAUSE"}, "reason"},
		{"other without a note", SkidInput{SkidCount: 2, Reason: ShortReasonOther}, "note"},
		{"absurd", SkidInput{SkidCount: 100000, Reason: ShortReasonCondensed}, "implausibly"},
	}
	for _, c := range cases {
		c.in.TaskID = taskID
		if _, err := svc.RecordSkids(ctx, c.in); err == nil || !strings.Contains(err.Error(), c.wants) {
			t.Errorf("%s: want a refusal naming %q, got %v", c.name, c.wants, err)
		}
	}
	if len(fy.skids) != 0 {
		t.Fatal("a refused record must not reach the order")
	}
}

func TestRecordSkids_ReachesTheTasksOrder(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	res, err := svc.RecordSkids(context.Background(), SkidInput{
		TaskID: taskID, ActorID: "u-1", SkidCount: 2, Reason: ShortReasonCondensed, Note: " three became two "})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.CountAfter != 2 || !res.Changed {
		t.Fatalf("the result carries the new count: %+v", res)
	}
	got := fy.skids[0]
	if got.OrderID != "aaaaaaaa-0000-4000-8000-000000000001" || got.SkidCount != 2 || got.Note != "three became two" || got.ActorID != "u-1" {
		t.Fatalf("the order is told the task's order, the count, the actor and the trimmed note: %+v", got)
	}
}

func TestRecordSkids_NeedsTheOrderPortAndTheOrderCanRefuse(t *testing.T) {
	svc, taskID := pickRecordSvc(t)
	if _, err := svc.RecordSkids(context.Background(), SkidInput{TaskID: taskID, SkidCount: 1, Reason: ShortReasonDamaged}); !errors.Is(err, ErrYardOrderUnwired) {
		t.Fatalf("with no order port a skid count cannot be recorded, got %v", err)
	}
	svc2, taskID2, fy := yardShortSvc(t)
	fy.err = ErrOrderRefused
	if _, err := svc2.RecordSkids(context.Background(), SkidInput{TaskID: taskID2, SkidCount: 1, Reason: ShortReasonDamaged}); !errors.Is(err, ErrOrderRefused) {
		t.Fatalf("the order's refusal comes back, got %v", err)
	}
}
