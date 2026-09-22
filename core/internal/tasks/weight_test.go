package tasks

// weight_test.go, the DIB-324 round trip and the DIB-325 gate, against the in-memory store.
//
// What these pin, in order of how badly each would hurt:
//   - a weight typed in the wrong unit NEVER reaches the order line;
//   - a refused order write leaves the measurement recorded and the gap VISIBLE;
//   - the advance gate is OFF unless a rule says otherwise, and fires exactly once when it is on;
//   - one tap in the yard still walks the whole legal state machine, hop by hop.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// - the line seam fake -

type fakeLines struct {
	uom      string
	uomErr   error
	setErr   error
	setCalls []struct {
		orderID string
		lineNo  int
		qty     string
	}
}

func (f *fakeLines) LineUOM(context.Context, string, int) (string, error) {
	return f.uom, f.uomErr
}

func (f *fakeLines) SetLineQty(_ context.Context, orderID string, lineNo int, qty string) error {
	f.setCalls = append(f.setCalls, struct {
		orderID string
		lineNo  int
		qty     string
	}{orderID, lineNo, qty})
	return f.setErr
}

type fakeAdvancer struct {
	calls  []string // "order|target"
	refuse error
}

func (f *fakeAdvancer) AdvanceOrder(_ context.Context, orderID, target, _ string) error {
	if f.refuse != nil {
		return f.refuse
	}
	f.calls = append(f.calls, orderID+"|"+target)
	return nil
}

// weightFixture builds a service with a NEED_WEIGHT task already queued against line 3.
func weightFixture(t *testing.T) (*Service, *fakeStore, *fakeLines, string) {
	t.Helper()
	store := newFakeStore()
	lines := &fakeLines{uom: "TONNE"}
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Fulfillment: "PICKUP", Status: "CONFIRMED"},
		}}).
		WithOrderLineWriter(lines)
	task, err := svc.RequestWeight(context.Background(), WeightRequestInput{
		OrderID: knownOrder, OrderLineNo: 3, UOM: "TONNE", RequestedBy: "u-counter",
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return svc, store, lines, task.ID
}

// - the request half -

func TestRequestWeight_MintsAHighPriorityNeedWeightTaskOnTheLine(t *testing.T) {
	svc, store, _, id := weightFixture(t)
	_ = svc
	got := store.tasks[id]
	if got.TaskType != TypeNeedWeight {
		t.Fatalf("task_type = %q, want NEED_WEIGHT", got.TaskType)
	}
	if got.Priority != PriorityHigh {
		t.Fatalf("priority = %q, want HIGH, somebody is standing at a counter waiting for this", got.Priority)
	}
	if got.OrderLineNo == nil || *got.OrderLineNo != 3 {
		t.Fatalf("order_line_no = %v, want 3, a weight request names ONE line", got.OrderLineNo)
	}
	if got.BranchID != branchA {
		t.Fatalf("branch = %q, it must be copied from the ORDER", got.BranchID)
	}
}

func TestRequestWeight_RefusesAUnitTheLineDoesNotUse(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
		}}).
		WithOrderLineWriter(&fakeLines{uom: "TONNE"})
	_, err := svc.RequestWeight(context.Background(), WeightRequestInput{
		OrderID: knownOrder, OrderLineNo: 3, UOM: "KG",
	})
	if err == nil {
		t.Fatal("a request in a unit the line cannot take must be refused at the door")
	}
	if !strings.Contains(err.Error(), "TONNE") || !strings.Contains(err.Error(), "KG") {
		t.Fatalf("the refusal must name BOTH units, got %q", err)
	}
}

func TestRequestWeight_RefusesALineThatDoesNotExist(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
		}}).
		WithOrderLineWriter(&fakeLines{uomErr: errors.New("no such line")})
	if _, err := svc.RequestWeight(context.Background(), WeightRequestInput{
		OrderID: knownOrder, OrderLineNo: 99,
	}); err == nil {
		t.Fatal("a request against a line nobody can find would sit in the queue looking like work")
	}
}

func TestRequestWeight_RefusesAMissingLineNumber(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store).WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
		knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
	}})
	if _, err := svc.RequestWeight(context.Background(), WeightRequestInput{OrderID: knownOrder}); err == nil {
		t.Fatal("order_line_no is required, a weight request names ONE line")
	}
}

// - the answer half -

func TestSubmitWeight_WritesTheLineAndStampsTheSync(t *testing.T) {
	svc, store, lines, id := weightFixture(t)
	res, err := svc.SubmitWeight(context.Background(), WeightInput{
		TaskID: id, ActorID: "u-picker", Weight: "2.8700", UOM: "TONNE",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !res.OrderSynced {
		t.Fatalf("expected the order line to take the weight, got %q", res.OrderError)
	}
	if len(lines.setCalls) != 1 {
		t.Fatalf("SetLineQty calls = %d, want 1", len(lines.setCalls))
	}
	if lines.setCalls[0].qty != "2.8700" || lines.setCalls[0].lineNo != 3 {
		t.Fatalf("wrote %+v, want line 3 = 2.8700", lines.setCalls[0])
	}
	got := store.tasks[id]
	if got.Status != StatusDone {
		t.Fatalf("status = %q, want DONE, typing the weight IS completing the task", got.Status)
	}
	if got.WeightEntered != "2.8700" || got.WeightUOM != "TONNE" {
		t.Fatalf("weight not stamped on the task: %q %q", got.WeightEntered, got.WeightUOM)
	}
	if got.WeightSyncedAt == nil {
		t.Fatal("a successful sync must be stamped, the NULL is what marks a gap")
	}
	if got.WeightSyncError != "" {
		t.Fatalf("a successful sync must clear the message, got %q", got.WeightSyncError)
	}
}

func TestSubmitWeight_OneTapWalksTheWholeLegalStateMachine(t *testing.T) {
	// The task is QUEUED and the picker types one number. The matrix is untouched: the store still
	// walks QUEUED → ACCEPTED → IN_PROGRESS → DONE and records every hop.
	svc, store, _, id := weightFixture(t)
	if _, err := svc.SubmitWeight(context.Background(), WeightInput{
		TaskID: id, ActorID: "u-picker", Weight: "1.5", UOM: "TONNE",
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	want := []string{StatusAccepted, StatusInProgress, StatusDone}
	if len(store.weightHops) != len(want) {
		t.Fatalf("hops = %v, want %v, the audit trail must not disagree with the matrix", store.weightHops, want)
	}
	for i := range want {
		if store.weightHops[i] != want[i] {
			t.Fatalf("hops = %v, want %v", store.weightHops, want)
		}
	}
}

func TestSubmitWeight_AWrongUnitNeverReachesTheOrderLine(t *testing.T) {
	// The four-thousand-dollar test: 2.87 TONNE typed as 2.87 KG must be refused, not reinterpreted.
	svc, store, lines, id := weightFixture(t)
	_, err := svc.SubmitWeight(context.Background(), WeightInput{
		TaskID: id, ActorID: "u-picker", Weight: "2.87", UOM: "KG",
	})
	if !errors.Is(err, ErrWeightUOMMismatch) {
		t.Fatalf("err = %v, want ErrWeightUOMMismatch", err)
	}
	if len(lines.setCalls) != 0 {
		t.Fatal("nothing may be written to the order line when the unit is wrong")
	}
	if store.tasks[id].Status == StatusDone {
		t.Fatal("a refused submission must not complete the task")
	}
}

func TestSubmitWeight_ARefusedOrderWriteKeepsTheMeasurementAndShowsTheGap(t *testing.T) {
	svc, store, lines, id := weightFixture(t)
	lines.setErr = errors.New("order: this order's lines can no longer be edited (status FULFILLED)")

	res, err := svc.SubmitWeight(context.Background(), WeightInput{
		TaskID: id, ActorID: "u-picker", Weight: "3.1", UOM: "TONNE",
	})
	if err != nil {
		t.Fatalf("the yard's measurement must never be lost to the order's refusal: %v", err)
	}
	if res.OrderSynced {
		t.Fatal("the order refused, order_synced must say so")
	}
	if !strings.Contains(res.OrderError, "FULFILLED") {
		t.Fatalf("the order module's OWN sentence must be carried verbatim, got %q", res.OrderError)
	}
	got := store.tasks[id]
	if got.WeightEntered != "3.1000" {
		t.Fatalf("the measurement must be kept, got %q", got.WeightEntered)
	}
	if got.WeightSyncedAt != nil {
		t.Fatal("a refused write must leave weight_synced_at NULL, that NULL is the queryable gap")
	}
	if got.WeightSyncError == "" {
		t.Fatal("the refusal must be recorded on the task, not swallowed")
	}
}

func TestSubmitWeight_AnUnwiredSeamIsStatedNotHidden(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store).WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
		knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
	}})
	task, err := svc.RequestWeight(context.Background(), WeightRequestInput{OrderID: knownOrder, OrderLineNo: 1})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	res, err := svc.SubmitWeight(context.Background(), WeightInput{TaskID: task.ID, Weight: "1", UOM: "EA"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.OrderSynced || res.OrderError == "" {
		t.Fatal("an unwired seam must report that the weight went nowhere")
	}
}

func TestSubmitWeight_RefusesANonWeightTask(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store).WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
		knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
	}})
	pick, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.SubmitWeight(context.Background(), WeightInput{
		TaskID: pick.ID, Weight: "1", UOM: "EA",
	}); !errors.Is(err, ErrNotWeightTask) {
		t.Fatalf("err = %v, want ErrNotWeightTask", err)
	}
}

func TestSubmitWeight_RefusesAnImpossibleNumber(t *testing.T) {
	svc, _, _, id := weightFixture(t)
	for _, bad := range []string{"", "0", "-4", "heavy"} {
		if _, err := svc.SubmitWeight(context.Background(), WeightInput{
			TaskID: id, Weight: bad, UOM: "TONNE",
		}); err == nil {
			t.Fatalf("weight %q must be refused", bad)
		}
	}
}

func TestSubmitWeight_RefusesAMissingUnit(t *testing.T) {
	svc, _, _, id := weightFixture(t)
	if _, err := svc.SubmitWeight(context.Background(), WeightInput{TaskID: id, Weight: "2"}); err == nil {
		t.Fatal("a number with no unit is not a weight")
	}
}

// - pathToDone (the legality walk) -

func TestPathToDone_WalksTheMatrixAndNeverThroughCancelled(t *testing.T) {
	cases := map[string][]string{
		StatusQueued:     {StatusAccepted, StatusInProgress, StatusDone},
		StatusPushed:     {StatusAccepted, StatusInProgress, StatusDone},
		StatusAccepted:   {StatusInProgress, StatusDone},
		StatusInProgress: {StatusDone},
		StatusStaged:     {StatusDone},
	}
	for from, want := range cases {
		got := pathToDone(from)
		if len(got) != len(want) {
			t.Fatalf("pathToDone(%s) = %v, want %v", from, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("pathToDone(%s) = %v, want %v", from, got, want)
			}
		}
		for _, hop := range got {
			if hop == StatusCancelled {
				t.Fatalf("pathToDone(%s) routed through CANCELLED, that is data loss wearing a shortest-path hat", from)
			}
		}
	}
}

func TestPathToDone_TerminalStatusesHaveNoPath(t *testing.T) {
	for _, from := range []string{StatusDone, StatusCancelled} {
		if got := pathToDone(from); len(got) != 0 {
			t.Fatalf("pathToDone(%s) = %v, want nothing, it is terminal", from, got)
		}
	}
}

func TestPathToDone_EveryHopIsLegalOnTheSharedMatrix(t *testing.T) {
	// The anti-drift assertion: whatever path is produced, every edge on it must be one the SAME
	// canTransition the repository and the fake both enforce would accept.
	for from := range allowedTransitions {
		at := from
		for _, to := range pathToDone(from) {
			if !canTransition(at, to) {
				t.Fatalf("pathToDone(%s) proposed the illegal hop %s → %s", from, at, to)
			}
			at = to
		}
	}
}

// - the DIB-325 advance gate -

func TestAdvanceGate_IsOffByDefaultAndNeverCallsTheSeam(t *testing.T) {
	store := newFakeStore() // no rules configured, exactly what 0108 seeds
	adv := &fakeAdvancer{}
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
		}}).
		WithOrderAdvancer(adv)

	task, _ := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	if _, err := svc.Claim(context.Background(), task.ID, "u"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.Accept(context.Background(), task.ID, "u"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := svc.Done(context.Background(), task.ID, "u", ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	if len(adv.calls) != 0 {
		t.Fatalf("the gate is OFF by default, the order seam must not be called, got %v", adv.calls)
	}
}

func TestAdvanceGate_FiresExactlyOnceWhenTheRuleIsOn(t *testing.T) {
	store := newFakeStore()
	store.rules[TypePick] = AdvanceRule{Enabled: true, TargetStatus: "FULFILLED"}
	adv := &fakeAdvancer{}
	audit := &recordingAudit{}
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
		}}).
		WithOrderAdvancer(adv).
		WithAudit(audit)

	task, _ := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	_, _ = svc.Claim(context.Background(), task.ID, "u")
	_, _ = svc.Accept(context.Background(), task.ID, "u")
	if _, err := svc.Done(context.Background(), task.ID, "u", ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	if len(adv.calls) != 1 || adv.calls[0] != knownOrder+"|FULFILLED" {
		t.Fatalf("advance calls = %v, want one FULFILLED advance", adv.calls)
	}
	if len(audit.actions) != 1 || audit.actions[0] != "TASK_ADVANCED_ORDER" {
		t.Fatalf("audit = %v, an automatic status change with no attribution is untrustworthy", audit.actions)
	}
}

func TestAdvanceGate_AnEnabledRuleWithNoTargetDoesNothing(t *testing.T) {
	store := newFakeStore()
	store.rules[TypePick] = AdvanceRule{Enabled: true, TargetStatus: "  "}
	adv := &fakeAdvancer{}
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
		}}).
		WithOrderAdvancer(adv)
	task, _ := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	_, _ = svc.Claim(context.Background(), task.ID, "u")
	_, _ = svc.Accept(context.Background(), task.ID, "u")
	_, _ = svc.Done(context.Background(), task.ID, "u", "")
	if len(adv.calls) != 0 {
		t.Fatal("a switch wired to nothing must do nothing")
	}
}

func TestAdvanceGate_ADeclinedAdvanceIsNotAudited(t *testing.T) {
	// The order module says no (already past the target). That is a normal outcome, not an event -
	// auditing it would fill the trail with rows describing things that did not happen.
	store := newFakeStore()
	store.rules[TypePick] = AdvanceRule{Enabled: true, TargetStatus: "FULFILLED"}
	adv := &fakeAdvancer{refuse: errors.New("order: already past FULFILLED")}
	audit := &recordingAudit{}
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
		}}).
		WithOrderAdvancer(adv).
		WithAudit(audit)
	task, _ := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	_, _ = svc.Claim(context.Background(), task.ID, "u")
	_, _ = svc.Accept(context.Background(), task.ID, "u")
	if _, err := svc.Done(context.Background(), task.ID, "u", ""); err != nil {
		t.Fatalf("a declined advance must never fail the task: %v", err)
	}
	if len(audit.actions) != 0 {
		t.Fatalf("a declined advance must not be audited as a firing, got %v", audit.actions)
	}
}

func TestAdvanceGate_AnUnreadableRuleDoesNotAdvanceAndDoesNotFailTheTask(t *testing.T) {
	store := newFakeStore()
	store.advanceErr = errors.New("advance rules table is unavailable")
	adv := &fakeAdvancer{}
	svc := NewService(store).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			knownOrder: {ID: knownOrder, BranchID: branchA, Status: "CONFIRMED"},
		}}).
		WithOrderAdvancer(adv)
	task, _ := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	_, _ = svc.Claim(context.Background(), task.ID, "u")
	_, _ = svc.Accept(context.Background(), task.ID, "u")
	if _, err := svc.Done(context.Background(), task.ID, "u", ""); err != nil {
		t.Fatalf("an unreadable rule must not fail a completed pick: %v", err)
	}
	if len(adv.calls) != 0 {
		t.Fatal("a rule that could not be read must NOT be treated as enabled")
	}
}

func TestAdvanceRuleFires(t *testing.T) {
	cases := map[AdvanceRule]bool{
		{Enabled: true, TargetStatus: "FULFILLED"}:  true,
		{Enabled: false, TargetStatus: "FULFILLED"}: false,
		{Enabled: true, TargetStatus: ""}:           false,
		{Enabled: true, TargetStatus: "   "}:        false,
	}
	for rule, want := range cases {
		if got := rule.Fires(); got != want {
			t.Fatalf("%+v.Fires() = %v, want %v", rule, got, want)
		}
	}
}

// recordingAudit is the minimal audit.Recorder the advance path needs.
type recordingAudit struct{ actions []string }

func (r *recordingAudit) Record(_ context.Context, ev audit.SensitiveAccess) error {
	r.actions = append(r.actions, ev.Action)
	return nil
}
