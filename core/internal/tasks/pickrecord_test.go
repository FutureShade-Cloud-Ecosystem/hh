package tasks

// pickrecord_test.go, item 1 (session A): what the yard ACTUALLY pulled, per line (migration 0187).
//
// THE DISTINCTION THESE TESTS EXIST TO HOLD. task_shortages answers "what is missing, and who has
// been told", an exception, append-only, with a resolution the counter owns. task_pick_lines answers
// "what came off the shelf", the normal case, one live number per line, rewritten as a picker
// corrects themselves. A short pick recorded only as a shortage tells the counter a number is missing
// and never tells anybody what the yard DID load, which is the number the driver hands over and the
// number the customer counts.

import (
	"context"
	"strings"
	"testing"
)

func pickRecordSvc(t *testing.T) (*Service, string) {
	t.Helper()
	svc, store, _ := newSvc(t)
	task, err := store.CreateTask(context.Background(), NewTaskInput{
		TaskType: TypePick, OrderID: "aaaaaaaa-0000-4000-8000-000000000001",
		BranchID: branchA, Source: "MANUAL",
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return svc, task.ID
}

// TestRecordPick_ZeroIsAnAnswerAndBlankIsNot is the rule that separates a recorded pick from a
// shortage report, and the reason parseNonNegativeQty exists beside parsePositiveQty.
//
// "We went to pick it and there was none" is a real outcome and the one a counter most needs to hear
// about. Refusing a zero would force a picker to either lie or say nothing, and saying nothing is
// what leaves a ticket looking un-picked when somebody did in fact walk out to the bay.
func TestRecordPick_ZeroIsAnAnswerAndBlankIsNot(t *testing.T) {
	svc, taskID := pickRecordSvc(t)
	ctx := context.Background()

	got, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, Description: "Armour stone", QtyOrdered: "10", QtyPicked: "0", UOM: "EA", ShortReason: ShortReasonNotInYard},
	})
	if err != nil {
		t.Fatalf("a pick of nothing must be recordable: %v", err)
	}
	if len(got) != 1 || got[0].QtyPicked != "0.0000" {
		t.Fatalf("zero must survive the round trip as a number: %+v", got)
	}
	if !got[0].Short() {
		t.Fatal("nothing pulled against ten ordered is short")
	}

	if _, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "", UOM: "EA"},
	}); err == nil {
		t.Fatal("a BLANK quantity is not an answer and must be refused")
	} else if !strings.Contains(err.Error(), "zero is an answer") {
		t.Fatalf("the refusal must say what to type instead: %v", err)
	}

	if _, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "-2", UOM: "EA"},
	}); err == nil {
		t.Fatal("a negative pick is not a fact about a yard")
	}
}

// TestRecordPick_ACorrectionReplacesTheNumberRatherThanAppendingASecond pins the upsert.
//
// This is a MEASUREMENT. Two live measurements of one line is the ambiguity the UNIQUE constraint
// removes at the database, and this asserts the service agrees: a picker who re-counts leaves one
// number, not a history to reconcile at the counter.
func TestRecordPick_ACorrectionReplacesTheNumberRatherThanAppendingASecond(t *testing.T) {
	svc, taskID := pickRecordSvc(t)
	ctx := context.Background()

	if _, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "7", UOM: "EA", ShortReason: ShortReasonCountCorrection},
	}); err != nil {
		t.Fatalf("first record: %v", err)
	}
	got, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "9", UOM: "EA", ShortReason: ShortReasonCountCorrection},
	})
	if err != nil {
		t.Fatalf("correction: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a correction must leave ONE number for the line, not two: %+v", got)
	}
	if got[0].QtyPicked != "9.0000" {
		t.Fatalf("the correction must win: %+v", got[0])
	}
}

// TestRecordPick_RefusesTwoNumbersForOneLineInOneCall is the same rule inside a single request, where
// the database's UNIQUE would report it as an opaque conflict rather than as something a picker can
// act on.
func TestRecordPick_RefusesTwoNumbersForOneLineInOneCall(t *testing.T) {
	svc, taskID := pickRecordSvc(t)
	_, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 2, QtyOrdered: "5", QtyPicked: "5", UOM: "EA"},
		{OrderLineNo: 2, QtyOrdered: "5", QtyPicked: "3", UOM: "EA"},
	})
	if err == nil {
		t.Fatal("two numbers for one line must be refused")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("the refusal must name the line it is about: %v", err)
	}
}

// TestRecordPick_AnEmptyRecordIsRefusedInWords. An empty PUT is ambiguous between "I picked nothing"
// and "I pressed the wrong thing", and those deserve different outcomes. A pick of nothing is
// recorded as a LINE with zero on it (the first test above); an empty set is refused.
func TestRecordPick_AnEmptyRecordIsRefusedInWords(t *testing.T) {
	svc, taskID := pickRecordSvc(t)
	_, err := svc.RecordPick(context.Background(), taskID, "u-1", nil)
	if err == nil {
		t.Fatal("an empty record must be refused")
	}
	if !strings.Contains(err.Error(), "pick of nothing") {
		t.Fatalf("the refusal must name the difference it is protecting: %v", err)
	}
}

// TestRecordPick_DoesNotFileAShortage is the boundary between the measurement and the message.
//
// Recording 7 of 10 does NOT tell the counter. Telling the counter is a separate, deliberate act with
// a note and a resolution attached, because a counter told about every mid-pick correction is a
// counter who reads nothing.
func TestRecordPick_DoesNotFileAShortage(t *testing.T) {
	svc, taskID := pickRecordSvc(t)
	ctx := context.Background()

	if _, err := svc.RecordPick(ctx, taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "7", UOM: "EA", ShortReason: ShortReasonDamaged},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	shortages, err := svc.ListShortages(ctx, taskID, false)
	if err != nil {
		t.Fatalf("shortages: %v", err)
	}
	if len(shortages) != 0 {
		t.Fatalf(`recording a short pick must not file a shortage by itself, the counter is told
deliberately, with a note, or not at all. Got %d shortage(s).`, len(shortages))
	}
}

// TestPickedLine_ShortIsTheOnePlaceThatDecidesWhatShortMeans. Shortness is DERIVED, never stored, and
// derived in exactly one function so two callers cannot disagree with a different string comparison.
func TestPickedLine_ShortIsTheOnePlaceThatDecidesWhatShortMeans(t *testing.T) {
	cases := []struct {
		ordered, picked string
		want            bool
		why             string
	}{
		{"10.0000", "7.0000", true, "less came off the shelf than the ticket asked for"},
		{"10.0000", "10.0000", false, "an exact pick is not short"},
		{"10.0000", "12.0000", false, "an OVER-pick is not short, it is a different conversation"},
		{"10.0000", "0.0000", true, "nothing pulled against ten ordered is short"},
		{"0.0000", "0.0000", false, "a line that asked for nothing cannot be short of it"},
		{"10.0000", "seven", false, "an unparseable quantity is not evidence of a shortfall"},
		{"", "7.0000", false, "nor is a missing one"},
	}
	for _, c := range cases {
		got := PickedLine{QtyOrdered: c.ordered, QtyPicked: c.picked}.Short()
		if got != c.want {
			t.Errorf("Short(ordered=%q picked=%q) = %v, want %v, %s", c.ordered, c.picked, got, c.want, c.why)
		}
	}
}
