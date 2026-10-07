package tasks

// yardnotice_test.go: every short pick and skid change tells the order's sales rep at once, naming
// the order, the line, ordered versus picked, the reason, and what the customer is owed when the
// order is already paid.

import (
	"context"
	"strings"
	"testing"
)

const noticeOrderNo = "#SO-9001"

func noticeSvc(t *testing.T) (*Service, string, *fakeYardOrder, *fakeNotifier) {
	t.Helper()
	svc, taskID, fy := yardShortSvc(t)
	n := &fakeNotifier{}
	svc.WithChatNotifier(n)
	fy.facts = YardOrderFacts{
		OrderNo: noticeOrderNo, CustomerID: "cccccccc-0000-4000-8000-000000000001",
		BranchID: branchA, SalespersonID: "dddddddd-0000-4000-8000-000000000001",
		TotalBeforeCents: 5650, TotalAfterCents: 3390,
	}
	return svc, taskID, fy, n
}

func shortLine() PickedLineInput {
	return PickedLineInput{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "3", UOM: "EA",
		ShortReason: ShortReasonCondensed, ShortNote: "two skids became one"}
}

func lastRepNotice(t *testing.T, n *fakeNotifier) TaskEventRef {
	t.Helper()
	for i := len(n.posts) - 1; i >= 0; i-- {
		if n.posts[i].Audience == AudienceOrderRep {
			return n.posts[i]
		}
	}
	t.Fatalf("no notice was addressed to the order's rep: %+v", n.posts)
	return TaskEventRef{}
}

func TestShortPick_TellsTheOrdersRepWithTheFacts(t *testing.T) {
	svc, taskID, _, n := noticeSvc(t)
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{shortLine()}); err != nil {
		t.Fatal(err)
	}
	ref := lastRepNotice(t, n)
	if ref.OrderID != "aaaaaaaa-0000-4000-8000-000000000001" || ref.CustomerID != "cccccccc-0000-4000-8000-000000000001" ||
		ref.RepUserID != "dddddddd-0000-4000-8000-000000000001" || ref.BranchID != branchA {
		t.Fatalf("the notice carries what routing needs (order, customer, the order's salesperson, branch): %+v", ref)
	}
	for _, want := range []string{noticeOrderNo, "line 1", "Armour stone", "ordered 5.0000 EA", "picked 3.0000 EA",
		"Skids condensed", "two skids became one", "$33.90"} {
		if !strings.Contains(ref.Summary, want) {
			t.Errorf("the notice must say %q:\n%s", want, ref.Summary)
		}
	}
	if strings.Contains(ref.Summary, "owed back") {
		t.Errorf("an order nobody has paid owes nothing back:\n%s", ref.Summary)
	}
}

func TestShortPick_PaidOrderSaysMoneyIsOwedBack(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.facts.InvoiceNo, fy.facts.InvoicedCents, fy.facts.PaidCents, fy.facts.OwedBackCents = "#INV-12", 5650, 5650, 2260
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{shortLine()}); err != nil {
		t.Fatal(err)
	}
	s := lastRepNotice(t, n).Summary
	for _, want := range []string{"already paid", "#INV-12", "$56.50", "$22.60", "owed back"} {
		if !strings.Contains(s, want) {
			t.Errorf("a paid order's notice must say %q:\n%s", want, s)
		}
	}
}

func TestShortPick_InvoicedButUnpaidSaysTheInvoiceNeedsBringingDown(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.facts.InvoiceNo, fy.facts.InvoicedCents = "#INV-12", 5650
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{shortLine()}); err != nil {
		t.Fatal(err)
	}
	s := lastRepNotice(t, n).Summary
	if !strings.Contains(s, "#INV-12") || !strings.Contains(s, "$56.50") || !strings.Contains(s, "$33.90") || strings.Contains(s, "owed back") {
		t.Errorf("an unpaid invoice raised for the ordered amount is named with both figures and nothing is owed back:\n%s", s)
	}
}

func TestShortPick_AFullPickAndARepeatAreNotAnnounced(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.changed = map[int]bool{1: false}
	full := PickedLineInput{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "5", UOM: "EA"}
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{full}); err != nil {
		t.Fatal(err)
	}
	for _, p := range n.posts {
		if p.Audience == AudienceOrderRep {
			t.Fatalf("a full pick that changed nothing is not news: %+v", p)
		}
	}
}

func TestShortPick_ARestoredLineIsAnnounced(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.restore = true
	full := PickedLineInput{OrderLineNo: 1, QtyOrdered: "5", QtyPicked: "5", UOM: "EA"}
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{full}); err != nil {
		t.Fatal(err)
	}
	if s := lastRepNotice(t, n).Summary; !strings.Contains(s, "in full") {
		t.Errorf("a line the yard finds the rest of is a change the rep hears about:\n%s", s)
	}
}

func TestShortPick_ARepeatedShortRecordKeepsTheSameKeySoItIsNeverPostedTwice(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.changed = map[int]bool{1: false}
	for i := 0; i < 2; i++ {
		if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{shortLine()}); err != nil {
			t.Fatal(err)
		}
	}
	var keys []string
	for _, p := range n.posts {
		if p.Audience == AudienceOrderRep {
			keys = append(keys, p.EventKey)
		}
	}
	if len(keys) != 2 || keys[0] != keys[1] {
		t.Fatalf("a repeat of a short record is re-sent under one key (a lost notice is retried, a delivered one deduplicates): %v", keys)
	}
}

func TestShortPick_ANoticeFailureDoesNotFailTheRecord(t *testing.T) {
	svc, taskID, _, n := noticeSvc(t)
	n.err = context.DeadlineExceeded
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{shortLine()}); err != nil {
		t.Fatalf("a messaging blip must never fail a yard write: %v", err)
	}
}

func TestSkidChange_TellsTheRepBeforeAndAfter(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.facts.InvoiceNo, fy.facts.InvoicedCents, fy.facts.PaidCents, fy.facts.OwedBackCents = "#INV-12", 5650, 5650, 5000
	three := 3
	fy.skidResult = &YardSkidResult{CountBefore: &three, CountAfter: 2, DepositBeforeCents: 15000, DepositAfterCents: 10000, Changed: true}
	if _, err := svc.RecordSkids(context.Background(), SkidInput{TaskID: taskID, ActorID: "u-1", SkidCount: 2,
		Reason: ShortReasonCondensed, Note: "three became two"}); err != nil {
		t.Fatal(err)
	}
	ref := lastRepNotice(t, n)
	for _, want := range []string{noticeOrderNo, "skids", "from 3 to 2", "Skids condensed", "three became two", "already paid", "$50.00", "owed back"} {
		if !strings.Contains(ref.Summary, want) {
			t.Errorf("the skid notice must say %q:\n%s", want, ref.Summary)
		}
	}
}

func TestSkidChange_DueWhenThePaidOrderGainsASkid(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.facts.InvoiceNo, fy.facts.PaidCents, fy.facts.DueCents = "#INV-12", 5650, 5000
	two := 2
	fy.skidResult = &YardSkidResult{CountBefore: &two, CountAfter: 3, DepositBeforeCents: 10000, DepositAfterCents: 15000, Changed: true}
	if _, err := svc.RecordSkids(context.Background(), SkidInput{TaskID: taskID, SkidCount: 3, Reason: ShortReasonCountCorrection}); err != nil {
		t.Fatal(err)
	}
	if s := lastRepNotice(t, n).Summary; !strings.Contains(s, "$50.00") || !strings.Contains(s, "now due") {
		t.Errorf("a paid order that gains a skid owes the deposit:\n%s", s)
	}
}

func TestSkidChange_FirstRecordAndNoChange(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.skidResult = &YardSkidResult{CountAfter: 4, Changed: true}
	if _, err := svc.RecordSkids(context.Background(), SkidInput{TaskID: taskID, SkidCount: 4, Reason: ShortReasonCountCorrection}); err != nil {
		t.Fatal(err)
	}
	if s := lastRepNotice(t, n).Summary; !strings.Contains(s, "set to 4") {
		t.Errorf("a first count has no before:\n%s", s)
	}
	n.posts = nil
	fy.skidResult = &YardSkidResult{CountAfter: 4, Changed: false}
	if _, err := svc.RecordSkids(context.Background(), SkidInput{TaskID: taskID, SkidCount: 4, Reason: ShortReasonCountCorrection}); err != nil {
		t.Fatal(err)
	}
	if len(n.posts) != 0 {
		t.Fatalf("the same count again is not told: %+v", n.posts)
	}
}

func TestShortPick_NeverReadsAnEmptyReason(t *testing.T) {
	svc, taskID, fy, n := noticeSvc(t)
	fy.stored = map[int]string{}
	line := shortLine()
	line.ShortReason, line.ShortNote = ShortReasonDamaged, ""
	if _, err := svc.RecordPick(context.Background(), taskID, "u-1", []PickedLineInput{line}); err != nil {
		t.Fatal(err)
	}
	if s := lastRepNotice(t, n).Summary; strings.Contains(s, "Reason: .") || strings.Contains(s, "Reason: ,") {
		t.Errorf("a notice must not read an empty reason:\n%s", s)
	}
	if got := shortPickMessage(YardOrderFacts{OrderNo: "#X", TotalAfterCents: 100, TotalBeforeCents: 100},
		YardLineEffect{LineNo: 1, Description: "d", UOM: "EA", QtyOrdered: "5.0000", QtyPicked: "3.0000", QtyBackordered: "2.0000"}); strings.Contains(got, "Reason") || strings.Contains(got, "(was") {
		t.Errorf("no reason means no reason sentence, and an unchanged total has no (was ...):\n%s", got)
	}
}
