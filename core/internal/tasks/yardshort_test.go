package tasks

// yardshort_test.go: a short pick is recorded with a reason, and the shortfall reaches the order.
//
// The rules held here: recording fewer than ordered needs a reason code (and a note for OTHER); a
// full pick needs none; nobody can record more than was ordered; and the order module is told the
// quantity actually picked so the shortfall becomes a backorder and the invoice bills what goes out.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
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
	// truth is the order line's own ordered quantity per line (the frozen original), which decides
	// short against full whatever the ticket says; unset means the ticket's number. stored is the
	// reason already held on a short line, kept when a re-save sends none.
	truth  map[int]string
	stored map[int]string
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
		orderedText := l.QtyOrdered
		if t, ok := f.truth[l.LineNo]; ok {
			orderedText = t
		}
		ordered, _ := strconv.ParseFloat(orderedText, 64)
		picked, _ := strconv.ParseFloat(l.QtyPicked, 64)
		if picked > ordered {
			return YardPickResult{}, fmt.Errorf("line %d: %w", l.LineNo, ErrAboveOrdered)
		}
		reason, note := l.Reason, l.Note
		if reason == "" {
			reason = f.stored[l.LineNo]
		}
		if picked < ordered && reason == "" {
			return YardPickResult{}, httpx.BadRequestf("line %d is short, give the reason", l.LineNo)
		}
		if picked < ordered && reason == ShortReasonOther && note == "" {
			return YardPickResult{}, httpx.BadRequestf("line %d: say what happened in the note when the reason is OTHER", l.LineNo)
		}
		if picked >= ordered {
			reason, note = "", ""
		}
		res.Lines = append(res.Lines, YardLineEffect{
			LineNo: l.LineNo, Description: "Armour stone", UOM: l.UOM,
			QtyOrdered: strconv.FormatFloat(ordered, 'f', 4, 64), QtyPicked: l.QtyPicked,
			QtyBackordered: strconv.FormatFloat(ordered-picked, 'f', 4, 64), Changed: changed || f.restore,
			Reason: reason, Note: note,
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
	if got, _ := svc.ListPickedLines(context.Background(), taskID); len(got) != 0 {
		t.Fatalf("a refused pick records nothing: %+v", got)
	}
	_ = fy
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
	if got, _ := svc.ListPickedLines(context.Background(), taskID); len(got) != 0 {
		t.Fatalf("a refused pick records nothing: %+v", got)
	}
	_ = fy
}

// The ticket is rebuilt from the order line after a short, so it shows the quantity to fulfil (3),
// not the ordered 5. An honest "found the rest, picked 5" must restore the line: the order line's
// own ordered quantity decides, never the ticket's.
func TestRecordPick_FoundTheRestRestoresAgainstTheOrdersOwnQuantity(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	fy.truth = map[int]string{1: "5"}
	got, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "3", QtyPicked: "5", UOM: "EA"},
	})
	if err != nil {
		t.Fatalf("5 of 5 against an order of 5 is a full pick whatever the ticket said: %v", err)
	}
	if got[0].QtyOrdered != "5.0000" || got[0].ShortReason != "" {
		t.Fatalf("the recorded line carries the order's ordered quantity and no reason: %+v", got[0])
	}
}

// A ticket quantity cannot create a short without a reason: 3 of a doctored 3 on an order of 5.
func TestRecordPick_ADoctoredTicketQuantityCannotHideAShort(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	fy.truth = map[int]string{1: "5"}
	_, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "3", QtyPicked: "3", UOM: "EA"},
	})
	if err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("a short against the order's own 5 needs a reason, got %v", err)
	}
	if got, _ := svc.ListPickedLines(context.Background(), taskID); len(got) != 0 {
		t.Fatalf("and records nothing: %+v", got)
	}
}

// Re-saving the pick as it stands (3 of 3, no reason) keeps the reason and note the order holds.
func TestRecordPick_ResavingNeverBlanksAStoredReason(t *testing.T) {
	svc, taskID, fy := yardShortSvc(t)
	fy.truth = map[int]string{1: "5"}
	fy.stored = map[int]string{1: ShortReasonDamaged}
	got, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{
		{OrderLineNo: 1, QtyOrdered: "3", QtyPicked: "3", UOM: "EA"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ShortReason != ShortReasonDamaged || got[0].QtyOrdered != "5.0000" {
		t.Fatalf("the recorded line keeps the order's reason and ordered quantity: %+v", got[0])
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
