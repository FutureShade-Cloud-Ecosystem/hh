package messaging

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestApprovalRef_JSONRoundTrip pins the typed card ref build/parse, one of the two seams the P2 warranty
// binds to, so its shape must be stable.
func TestApprovalRef_JSONRoundTrip(t *testing.T) {
	ref := ApprovalRef{
		ApprovalID:  "ar1",
		PolicyType:  "JE_OVER_THRESHOLD",
		SubjectRef:  "journal-1",
		BranchID:    "b1",
		AmountCents: 600000,
		RequestedBy: "maker",
		Detail:      "over threshold",
		DeepLink:    "/gl/journals/journal-1",
		Status:      "PENDING",
	}
	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ApprovalRef
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != ref {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, ref)
	}
}

// TestPostApprovalToUserChannels pins the fan-out: one card per approver channel, idempotent on re-notify,
// and a clean no-op for the empty-approver (zero-eligible) edge.
func TestPostApprovalToUserChannels(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	ref := ApprovalRef{ApprovalID: "ar1", PolicyType: "MARGIN_BELOW_MINIMUM"}

	if err := svc.PostApprovalToUserChannels(context.Background(), []string{"gm", "fc"}, ref); err != nil {
		t.Fatalf("fan-out: %v", err)
	}
	if len(f.approvalMsgs) != 2 {
		t.Fatalf("expected one card per approver (2), got %d", len(f.approvalMsgs))
	}
	// Re-notify is idempotent (per-channel event_key dedup), no new cards.
	if err := svc.PostApprovalToUserChannels(context.Background(), []string{"gm", "fc"}, ref); err != nil {
		t.Fatalf("re-fan: %v", err)
	}
	if len(f.approvalMsgs) != 2 {
		t.Fatalf("re-notify must not duplicate cards, got %d", len(f.approvalMsgs))
	}
	// Empty approver set is a clean no-op.
	if err := svc.PostApprovalToUserChannels(context.Background(), nil, ref); err != nil {
		t.Fatalf("empty fan-out should be a no-op, got %v", err)
	}
	if len(f.approvalMsgs) != 2 {
		t.Fatalf("empty fan-out must add nothing, got %d", len(f.approvalMsgs))
	}
}

// TestPostApprovalDecision pins the decision reflect: it stamps the status on the card copies and posts one
// echo per card channel, idempotently.
func TestPostApprovalDecision(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	// Seed a card for ar1 in one approver channel so the echo has a channel to land in.
	if err := svc.PostApprovalToUserChannels(context.Background(), []string{"gm"}, ApprovalRef{ApprovalID: "ar1", PolicyType: "MARGIN_BELOW_MINIMUM"}); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	f.displayNames = map[string]string{"gm": "Grace Manager"}

	if err := svc.PostApprovalDecision(context.Background(), "ar1", "APPROVED", "gm", "looks good"); err != nil {
		t.Fatalf("reflect decision: %v", err)
	}
	if f.stamped["ar1"] != "APPROVED" {
		t.Fatalf("card status should be stamped APPROVED, got %q", f.stamped["ar1"])
	}
	if len(f.events) != 1 {
		t.Fatalf("expected exactly one decision echo, got %d", len(f.events))
	}
	// Re-reflect is idempotent: the echo dedups, the stamp is a repeat UPDATE (no new echo rows).
	if err := svc.PostApprovalDecision(context.Background(), "ar1", "APPROVED", "gm", "looks good"); err != nil {
		t.Fatalf("re-reflect: %v", err)
	}
	if len(f.events) != 1 {
		t.Fatalf("re-reflect must not duplicate the echo, got %d", len(f.events))
	}
}

// fakeApprovalSource is a scripted reconcile source (the identity adapter's stand-in).
type fakeApprovalSource struct {
	plan  ReconcilePlan
	err   error
	calls int
}

func (f *fakeApprovalSource) ApprovalReconcile(_ context.Context) (ReconcilePlan, error) {
	f.calls++
	return f.plan, f.err
}

// TestReconcileSweep_Idempotent pins that a double-run of the sweep over unchanged state produces ZERO new
// rows (cards dedup on the card event_key; echoes dedup on the decided event_key; the stamp is a repeat
// UPDATE), the backfill safety net must be safe to run every 60s forever.
func TestReconcileSweep_Idempotent(t *testing.T) {
	f := newFakeStore()
	// Seed a pre-existing card for the decided request so its echo has a channel to land in.
	f.approvalCards = map[string]bool{"appch-gm|" + approvalCardKey("ar2"): true}
	svc := svcWith(f)
	src := &fakeApprovalSource{plan: ReconcilePlan{
		Pending: []PendingApproval{{
			Ref:         ApprovalRef{ApprovalID: "ar1", PolicyType: "JE_OVER_THRESHOLD", AmountCents: 600000},
			ApproverIDs: []string{"gm", "fc"},
		}},
		Decided: []DecidedApproval{{ApprovalID: "ar2", Status: "APPROVED", DecidedBy: "gm", RequestedBy: "clerk"}},
	}}
	w := NewReconcileWorker(svc, src, nil).WithInterval(time.Hour)

	w.ReconcileOnce(context.Background())
	cards1, echoes1 := len(f.approvalMsgs), len(f.events)
	if cards1 != 2 {
		t.Fatalf("first sweep should fan 2 cards (gm/fc) for the pending request, got %d", cards1)
	}
	if echoes1 != 1 {
		t.Fatalf("first sweep should post 1 decision echo for the decided request, got %d", echoes1)
	}
	if f.stamped["ar2"] != "APPROVED" {
		t.Fatalf("decided request should be stamped APPROVED, got %q", f.stamped["ar2"])
	}

	// Second identical sweep adds nothing new.
	w.ReconcileOnce(context.Background())
	if len(f.approvalMsgs) != cards1 {
		t.Fatalf("second sweep must add zero cards: %d -> %d", cards1, len(f.approvalMsgs))
	}
	if len(f.events) != echoes1 {
		t.Fatalf("second sweep must add zero echoes: %d -> %d", echoes1, len(f.events))
	}
}

// TestReconcileSweep_NilSourceNoop pins the kill switch: no source ⇒ the sweep does nothing.
func TestReconcileSweep_NilSourceNoop(t *testing.T) {
	f := newFakeStore()
	w := NewReconcileWorker(svcWith(f), nil, nil)
	if n := w.ReconcileOnce(context.Background()); n != 0 {
		t.Fatalf("nil-source sweep should be a no-op, touched %d", n)
	}
}
