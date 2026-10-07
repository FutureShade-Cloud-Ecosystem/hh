package tasks

// yardshort_test.go: a short pick is recorded with a reason, and the shortfall reaches the order.
//
// The rules held here: recording fewer than ordered needs a reason code (and a note for OTHER); a
// full pick needs none; nobody can record more than was ordered; and the order module is told the
// quantity actually picked so the shortfall becomes a backorder and the invoice bills what goes out.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// fakeYardOrder records what the tasks module asked the order module to do.
type fakeYardOrder struct {
	picks []YardPickChange
	skids []YardSkidChange
	// restore marks every unchanged line as a restoration to full, and skidResult overrides the
	// skid answer; both are for the notice tests.
	restore    bool
	skidResult *YardSkidResult
	err        error
	// effects is what each call answers; the zero value answers "changed" for every short line.
	changed map[int]bool
	facts   YardOrderFacts
}

func (f *fakeYardOrder) ApplyPicks(_ context.Context, in YardPickChange) (YardPickResult, error) {
	f.picks = append(f.picks, in)
	if f.err != nil {
		return YardPickResult{}, f.err
	}
	res := YardPickResult{Order: f.facts}
	for _, l := range in.Lines {
		changed := true
		if f.changed != nil {
			changed = f.changed[l.LineNo]
		}
		ordered, _ := strconv.ParseFloat(l.QtyOrdered, 64)
		picked, _ := strconv.ParseFloat(l.QtyPicked, 64)
		res.Lines = append(res.Lines, YardLineEffect{
			LineNo: l.LineNo, Description: "Armour stone", UOM: l.UOM,
			QtyOrdered: l.QtyOrdered, QtyPicked: l.QtyPicked,
			QtyBackordered: strconv.FormatFloat(ordered-picked, 'f', 4, 64), Changed: changed || f.restore,
		})
	}
	return res, nil
}

func yardShortSvc(t *testing.T) (*Service, string, *fakeYardOrder) {
	t.Helper()
	svc, taskID := pickRecordSvc(t)
	fy := &fakeYardOrder{}
	svc.WithYardOrderChanges(fy)
	return svc, taskID, fy
}

func TestRecordPick_ShortNeedsAReason(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	_, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "3", UOM: "EA"},
	})
	if err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("a short pick with no reason must be refused naming the reason, got %v", err)
	}
	if len(fy.picks) != 0 {
		t.Fatal("a refused pick must not reach the order")
	}
}

func TestRecordPick_UnknownReasonRefusedAndOtherNeedsANote(t *testing.T) {
	svc, taskID, _ := yardShortSvc(t)
	ctx := context.Background()
	if _, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "3", UOM: "EA", ShortReason: "BECAUSE"},
	}); err == nil {
		t.Fatal("a reason outside the code list must be refused")
	}
	if _, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "3", UOM: "EA", ShortReason: ShortReasonOther},
	}); err == nil || !strings.Contains(err.Error(), "note") {
		t.Fatalf("OTHER with no note must be refused naming the note, got %v", err)
	}
}

func TestRecordPick_ShortWithReasonIsRecordedAndReachesTheOrder(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	got, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "3", UOM: "EA",
			ShortReason: ShortReasonCondensed, ShortNote: " two skids became one "},
	})
	if err != nil {
		t.Fatalf("a short pick with a reason must record: %v", err)
	}
	if got[0].ShortReason != ShortReasonCondensed || got[0].ShortNote != "two skids became one" {
		t.Fatalf("the reason and the trimmed note must be kept on the line: %+v", got[0])
	}
	if len(fy.picks) != 1 || fy.picks[0].OrderID != "aaaaaaaa-0000-4000-8000-000000000001" {
		t.Fatalf("the order must be told once, for the task's order: %+v", fy.picks)
	}
	l := fy.picks[0].Lines[0]
	if l.LineNo != 1 || l.QtyPicked != "3.0000" || l.Reason != ShortReasonCondensed {
		t.Fatalf("the order is told the picked quantity and the reason: %+v", l)
	}
}

func TestRecordPick_FullPickNeedsNoReason(t *testing.T) {
	svc, taskID, _ := yardShortSvc(t)
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "5", UOM: "EA"},
	}); err != nil {
		t.Fatalf("a full pick needs no reason: %v", err)
	}
}

func TestRecordPick_NoOneRaisesAQuantityAboveOrdered(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	_, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "6", UOM: "EA", ShortReason: ShortReasonCountCorrection},
	})
	if err == nil || !strings.Contains(err.Error(), "more than was ordered") {
		t.Fatalf("picking above ordered must be refused in words, got %v", err)
	}
	if len(fy.picks) != 0 {
		t.Fatal("a refused pick must not reach the order")
	}
}

func TestRecordPick_OrderRefusalStopsTheRecord(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	fy.err = ErrAboveOrdered
	_, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "3", UOM: "EA", ShortReason: ShortReasonDamaged},
	})
	if !errors.Is(err, ErrAboveOrdered) {
		t.Fatalf("the order's refusal must come back, got %v", err)
	}
	if got, _ := svc.ListPickedLines(context.Background(), taskID); len(got) != 0 {
		t.Fatalf("a pick the order refused must not be recorded: %+v", got)
	}
}
