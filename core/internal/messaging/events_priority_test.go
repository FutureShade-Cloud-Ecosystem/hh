package messaging

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - DIB-275: ack arithmetic (per-user, idempotent re-ack) -

// computeAckProgress is the arithmetic itself. Each row here is a rule someone could plausibly get
// wrong, so each is pinned rather than inferred from the SQL.
func TestComputeAckProgress_Arithmetic(t *testing.T) {
	cases := []struct {
		name            string
		required, acked int
		wantRemaining   int
		wantComplete    bool
	}{
		{"nobody acked yet", 3, 0, 3, false},
		{"partial", 3, 1, 2, false},
		{"everyone acked", 3, 3, 0, true},
		{"more acks than members (a member acked then left) floors at zero", 2, 3, 0, true},
		{"no members is NOT complete, it is un-acknowledgeable", 0, 0, 0, false},
		{"no members but a stray ack is still not complete", 0, 1, 0, false},
		{"negative inputs are clamped", -1, -1, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeAckProgress("m1", c.required, c.acked, nil)
			if got.Remaining != c.wantRemaining {
				t.Fatalf("remaining(required=%d acked=%d) = %d, want %d", c.required, c.acked, got.Remaining, c.wantRemaining)
			}
			if got.Complete != c.wantComplete {
				t.Fatalf("complete(required=%d acked=%d) = %v, want %v", c.required, c.acked, got.Complete, c.wantComplete)
			}
			if got.Acks == nil {
				t.Fatal("Acks must be a non-nil empty slice so the endpoint serializes [] not null")
			}
		})
	}
}

// Re-acking the SAME message by the SAME user must not move the arithmetic: the (message,user) primary
// key makes the second tap an UPDATE, never a second row. This is the "idempotent re-ack" acceptance.
func TestAckProgress_ReAckDoesNotInflateTheCount(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	f.setMember(chGen, uBob)
	f.w5().ackRequired[chGen] = 2
	svc := svcWith(f)
	ctx := context.Background()

	if _, err := svc.AckMessage(ctx, Caller{UserID: uAlice}, chGen, "m1", AckTap); err != nil {
		t.Fatalf("ack 1: %v", err)
	}
	p1, err := svc.AckProgress(ctx, Caller{UserID: uAlice}, chGen, "m1")
	if err != nil {
		t.Fatalf("progress 1: %v", err)
	}
	if p1.Acked != 1 || p1.Remaining != 1 || p1.Complete {
		t.Fatalf("after one ack want acked=1 remaining=1 incomplete, got %+v", p1)
	}

	// Same user taps again (and changes method), the count must NOT move.
	if _, err := svc.AckMessage(ctx, Caller{UserID: uAlice}, chGen, "m1", AckVoice); err != nil {
		t.Fatalf("re-ack: %v", err)
	}
	p2, err := svc.AckProgress(ctx, Caller{UserID: uAlice}, chGen, "m1")
	if err != nil {
		t.Fatalf("progress 2: %v", err)
	}
	if p2.Acked != 1 || p2.Remaining != 1 {
		t.Fatalf("a re-ack by the SAME user must not inflate the count, got %+v", p2)
	}

	// A DIFFERENT user completes it.
	if _, err := svc.AckMessage(ctx, Caller{UserID: uBob}, chGen, "m1", AckTap); err != nil {
		t.Fatalf("ack 2: %v", err)
	}
	p3, _ := svc.AckProgress(ctx, Caller{UserID: uAlice}, chGen, "m1")
	if p3.Acked != 2 || p3.Remaining != 0 || !p3.Complete {
		t.Fatalf("after both members ack want complete, got %+v", p3)
	}
}

func TestAckProgress_NonMemberForbidden(t *testing.T) {
	f := newFakeStore()
	f.exists[chGen] = true
	svc := svcWith(f)
	if _, err := svc.AckProgress(context.Background(), Caller{UserID: uBob}, chGen, "m1"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member ack-progress want ErrNotMember, got %v", err)
	}
}

// - DIB-217: the state key -

func TestStateKeyFor_PrefersTheNarrowestRecord(t *testing.T) {
	cases := []struct {
		name string
		p    eventPayload
		want string
	}{
		{"order id wins over customer", eventPayload{OrderID: "o1", CustomerID: "c1"}, "order:o1"},
		{"order id wins over order no", eventPayload{OrderID: "o1", OrderNo: "SO-1"}, "order:o1"},
		{"order no when no id", eventPayload{OrderNo: "SO-1", CustomerID: "c1"}, "order#SO-1"},
		{"customer when no order at all", eventPayload{CustomerID: "c1"}, "customer:c1"},
		{"nothing identifiable is NOT a state assertion", eventPayload{SKU: "PAVER"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stateKeyFor(c.p); got != c.want {
				t.Fatalf("stateKeyFor(%+v) = %q, want %q", c.p, got, c.want)
			}
		})
	}
}

// eventOptsFor is the single place topic → priority/ack is decided; HIGH must always imply an ack.
func TestEventOptsFor_HighPriorityImpliesForcedAck(t *testing.T) {
	high := eventOptsFor(TopicOrderBlocked, eventPayload{OrderID: "o1"})
	if high.Priority != PriorityHigh || !high.RequiresAck {
		t.Fatalf("a blocked order must be HIGH + forced-ack, got %+v", high)
	}
	if high.StateKey != "order:o1" {
		t.Fatalf("a high-priority event must carry its state key, got %q", high.StateKey)
	}
	normal := eventOptsFor(TopicOrderReleased, eventPayload{OrderID: "o1"})
	if normal.Priority != PriorityNormal || normal.RequiresAck {
		t.Fatalf("an ordinary release must be NORMAL + non-blocking, got %+v", normal)
	}
	// …but it STILL carries the state key, which is what lets it resolve the blocked bubble.
	if normal.StateKey != "order:o1" {
		t.Fatalf("an ordinary event must still carry its state key (that is what supersedes), got %q", normal.StateKey)
	}
}

// A caller cannot hand-craft a HIGH priority without an ack, normalize repairs the pair.
func TestEventOpts_NormalizeRepairsThePair(t *testing.T) {
	got := EventOpts{Priority: PriorityHigh, RequiresAck: false}.normalize()
	if !got.RequiresAck {
		t.Fatal("HIGH priority must imply RequiresAck, the two cannot disagree")
	}
	if bad := (EventOpts{Priority: "URGENT"}).normalize(); bad.Priority != PriorityNormal {
		t.Fatalf("an unknown priority must clamp to NORMAL, got %q", bad.Priority)
	}
}

// - DIB-217: the supersede rule (LAST-WRITE-WINS), end to end -

// supersedeFixture posts a forced-ack "order blocked" and returns the service, store, and that
// message's id.
func supersedeFixture(t *testing.T) (*Service, *fakeStore, string) {
	t.Helper()
	f := newFakeStore()
	f.slugs["yard-trenton"] = "ch-yard-trt"
	f.yard[branchTRT] = "yard-trenton"
	svc := svcWith(f)
	c := NewConsumer(svc, nil)
	ev := mkEvent(t, TopicOrderBlocked, eventPayload{
		EventID: "e-blocked", BranchID: branchTRT, OrderID: "o-1042", Reason: "credit hold",
	})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("post blocked: %v", err)
	}
	if len(f.events) != 1 || !f.events[0].RequiresAck {
		t.Fatalf("precondition: the blocked event must be a forced-ack post, got %+v", f.events)
	}
	return svc, f, f.events[0].ID
}

// THE RULING: a newer fact about the SAME record auto-resolves the older un-acked bubble.
func TestSupersede_NewerEventAutoResolvesTheStaleBubble(t *testing.T) {
	svc, f, blockedID := supersedeFixture(t)
	c := NewConsumer(svc, nil)

	// The order is released a moment later, a NEWER fact about the SAME record.
	time.Sleep(time.Millisecond)
	rel := mkEvent(t, TopicOrderReleased, eventPayload{
		EventID: "e-released", BranchID: branchTRT, OrderID: "o-1042",
	})
	if err := c.handle(context.Background(), rel); err != nil {
		t.Fatalf("post released: %v", err)
	}
	if !f.isSuperseded(blockedID) {
		t.Fatal("a newer fact about the same order must auto-resolve the older un-acked bubble, " +
			"a user must never ack a stale fact presented as current")
	}
	// Supersession RESOLVES; it does not ACK. The ack ledger stays honest about who actually saw it.
	if f.hasAnyAck(blockedID) {
		t.Fatal("supersession must not fabricate an ack, nobody read it, and that is the honest record")
	}
}

// An event about a DIFFERENT record must leave the bubble alone.
func TestSupersede_DifferentRecordDoesNotResolve(t *testing.T) {
	svc, f, blockedID := supersedeFixture(t)
	c := NewConsumer(svc, nil)
	time.Sleep(time.Millisecond)
	other := mkEvent(t, TopicOrderReleased, eventPayload{
		EventID: "e-other", BranchID: branchTRT, OrderID: "o-9999",
	})
	if err := c.handle(context.Background(), other); err != nil {
		t.Fatalf("post other: %v", err)
	}
	if f.isSuperseded(blockedID) {
		t.Fatal("an event about a DIFFERENT order must never resolve this order's bubble")
	}
}

// An ALREADY-ACKED message is never superseded: someone read it and said so, and that record is not
// ours to overwrite.
func TestSupersede_AlreadyAckedMessageIsLeftAlone(t *testing.T) {
	svc, f, blockedID := supersedeFixture(t)
	f.setMember("ch-yard-trt", uAlice)
	if _, err := svc.AckMessage(context.Background(), Caller{UserID: uAlice}, "ch-yard-trt", blockedID, AckTap); err != nil {
		t.Fatalf("ack: %v", err)
	}
	c := NewConsumer(svc, nil)
	time.Sleep(time.Millisecond)
	rel := mkEvent(t, TopicOrderReleased, eventPayload{
		EventID: "e-released-2", BranchID: branchTRT, OrderID: "o-1042",
	})
	if err := c.handle(context.Background(), rel); err != nil {
		t.Fatalf("post released: %v", err)
	}
	if f.isSuperseded(blockedID) {
		t.Fatal("an already-ACKED message must keep its ack, supersession must not rewrite ack history")
	}
}

// A redelivery of the SAME event is the event_key no-op, not a supersede of itself.
func TestSupersede_RedeliveryDoesNotSupersedeItself(t *testing.T) {
	svc, f, blockedID := supersedeFixture(t)
	c := NewConsumer(svc, nil)
	ev := mkEvent(t, TopicOrderBlocked, eventPayload{
		EventID: "e-blocked", BranchID: branchTRT, OrderID: "o-1042", Reason: "credit hold",
	})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if f.isSuperseded(blockedID) {
		t.Fatal("a redelivered event must be an idempotent no-op, never a self-supersede")
	}
	if len(f.events) != 1 {
		t.Fatalf("redelivery must not create a second post, got %d", len(f.events))
	}
}

// An event with NO state key supersedes nothing, the safe default (a spurious supersede would
// silently dismiss a live alert).
func TestSupersede_KeylessEventResolvesNothing(t *testing.T) {
	svc, f, blockedID := supersedeFixture(t)
	sup, err := svc.supersede(context.Background(), "", f.events[0].ID)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if len(sup) != 0 || f.isSuperseded(blockedID) {
		t.Fatal("an event with no state key must resolve nothing")
	}
}

// - DIB-269: the priority tier feeds the un-mutable escalation channel -

func TestPriorityEvent_MirrorsIntoEscalations(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	f.slugs[escalationSlug] = "ch-escalations"
	svc := svcWith(f)
	c := NewConsumer(svc, nil)

	ev := mkEvent(t, TopicCreditHold, eventPayload{EventID: "e-hold", Customer: "Acme", CustomerID: "c-1", Reason: "NSF"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	var home, mirror *Message
	for i := range f.events {
		switch f.events[i].ChannelID {
		case "ch-finance":
			home = &f.events[i]
		case "ch-escalations":
			mirror = &f.events[i]
		}
	}
	if home == nil || !home.RequiresAck {
		t.Fatalf("the home post must be the forced-ack one, got %+v", home)
	}
	if mirror == nil {
		t.Fatal("a HIGH-priority event must be mirrored into the un-mutable escalation channel")
	}
	// The mirror must NOT force a second ack, acking happens once, on the home message, or the ack
	// arithmetic double-counts and everyone taps twice for one fact.
	if mirror.RequiresAck {
		t.Fatal("the escalation mirror must NOT force its own ack, one fact, one ack")
	}
}

// REGRESSION (found by the Chromium walkthrough, not by a unit test): the escalation MIRROR carries
// the same state_key as its source and is written a moment LATER. Without the Mirror flag the sweep
// read it as a NEWER fact about the record and auto-resolved the home channel's forced-ack bubble -
// the escalation copy silently dismissed the very alert it exists to amplify, and a blocking alert
// vanished with nobody acknowledging it.
//
// A COPY never supersedes its ORIGINAL.
func TestPriorityEvent_MirrorDoesNotSupersedeItsOwnSource(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	f.slugs[escalationSlug] = "ch-escalations"
	c := NewConsumer(svcWith(f), nil)

	ev := mkEvent(t, TopicCreditHold, eventPayload{
		EventID: "e-hold-mirror", Customer: "Acme", CustomerID: "c-1", OrderID: "o-77", Reason: "NSF",
	})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	var homeID string
	for _, m := range f.events {
		if m.ChannelID == "ch-finance" {
			homeID = m.ID
		}
	}
	if homeID == "" {
		t.Fatal("precondition: the home post must exist")
	}
	if f.isSuperseded(homeID) {
		t.Fatal("the escalation mirror must NEVER supersede the alert it mirrors, " +
			"a copy is not a newer fact, and a bubble must not vanish un-acked")
	}
}

// An ordinary event is NOT mirrored: the escalation channel is the "must never be missed" tier, and
// filling it with routine traffic is how a firehose becomes noise nobody reads.
func TestNormalEvent_IsNotMirroredIntoEscalations(t *testing.T) {
	f := newFakeStore()
	f.yard[branchTRT] = "yard-trenton"
	f.slugs["yard-trenton"] = "ch-yard-trt"
	f.slugs[escalationSlug] = "ch-escalations"
	c := NewConsumer(svcWith(f), nil)
	ev := mkEvent(t, TopicOrderReleased, eventPayload{EventID: "e-rel", BranchID: branchTRT, OrderNo: "SO-1"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	for _, m := range f.events {
		if m.ChannelID == "ch-escalations" {
			t.Fatal("an ordinary event must not reach the escalation tier")
		}
	}
}

// - DIB-269: the escalation channel CANNOT be muted (the named test) -

func TestSetNotifyPrefs_EscalationChannelCannotBeMuted(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	f.tiers[chGen] = TierEscalation
	svc := svcWith(f)
	ctx := context.Background()

	// Route 1: the three-way mode.
	muted := ModeMuted
	if _, err := svc.SetNotifyPrefs(ctx, Caller{UserID: uAlice}, chGen, PrefsPatch{Mode: &muted}); !errors.Is(err, ErrChannelUnmutable) {
		t.Fatalf("muting an ESCALATION channel via mode must be refused, got %v", err)
	}
	// Route 2: the legacy boolean. Refusing one and not the other would leave an open door to the same
	// outcome.
	if _, err := svc.SetNotifyPrefs(ctx, Caller{UserID: uAlice}, chGen, PrefsPatch{Muted: boolp(true)}); !errors.Is(err, ErrChannelUnmutable) {
		t.Fatalf("muting an ESCALATION channel via the legacy boolean must be refused, got %v", err)
	}
	// A refused write must leave NOTHING behind.
	if len(f.prefs) != 0 {
		t.Fatalf("a refused mute must not persist any prefs row, got %d", len(f.prefs))
	}
	// Non-silencing prefs on the SAME channel are still allowed, the refusal is surgical.
	mentions := ModeMentions
	if _, err := svc.SetNotifyPrefs(ctx, Caller{UserID: uAlice}, chGen, PrefsPatch{Mode: &mentions}); err != nil {
		t.Fatalf("mention-only on an escalation channel is not a mute and must be allowed, got %v", err)
	}
	if _, err := svc.SetNotifyPrefs(ctx, Caller{UserID: uAlice}, chGen, PrefsPatch{DesktopEnabled: boolp(false)}); err != nil {
		t.Fatalf("turning off the desktop toast is not a mute and must be allowed, got %v", err)
	}
}

// A STANDARD channel is freely mutable, the refusal must not have leaked into every channel.
func TestSetNotifyPrefs_StandardChannelStillMutable(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	muted := ModeMuted
	got, err := svc.SetNotifyPrefs(context.Background(), Caller{UserID: uAlice}, chGen, PrefsPatch{Mode: &muted})
	if err != nil {
		t.Fatalf("muting a STANDARD channel must be allowed, got %v", err)
	}
	if got.Mode != ModeMuted || !got.Muted {
		t.Fatalf("a mute must set BOTH mode and its boolean shadow, got %+v", got)
	}
}

// The FIREHOSE channel keeps its SHIPPED, softer contract (mutable, with the escalation_override
// bypass). This test exists so a future "make everything loud unmutable" edit is a deliberate,
// visible choice rather than an accident.
func TestSetNotifyPrefs_FirehoseKeepsItsShippedMutableContract(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	f.tiers[chGen] = TierFirehose
	svc := svcWith(f)
	if _, err := svc.SetNotifyPrefs(context.Background(), Caller{UserID: uAlice}, chGen, PrefsPatch{Muted: boolp(true)}); err != nil {
		t.Fatalf("FIREHOSE shipped as mutable (with the override bypass); refusing it would break a shipped contract: %v", err)
	}
}

// The refusal maps to HTTP 409 through the full handler chain, not 403 and not 500.
func TestHandler_MuteEscalation_Returns409(t *testing.T) {
	f := newFakeStore()
	f.setMember(chUUID, uAlice)
	f.tiers[chUUID] = TierEscalation
	h := NewHandler(svcWith(f), nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/messaging/channels/"+chUUID+"/notify-prefs",
		strings.NewReader(`{"mode":"MUTED"}`))
	req.SetPathValue("id", chUUID)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	h.putNotifyPrefs(rr, req, guards.StaffCaller{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("muting an escalation channel want 409, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// - DIB-268: the mode merge -

func TestMergePrefs_ModeWinsOverTheLegacyBoolean(t *testing.T) {
	cur := DefaultNotificationPrefs(chGen, uAlice)
	mentions := ModeMentions
	got, ok := mergePrefs(cur, PrefsPatch{Mode: &mentions, Muted: boolp(false)})
	if !ok || got.Mode != ModeMentions {
		t.Fatalf("mode is the more specific instruction and must win, got %+v (ok=%v)", got, ok)
	}
	if got.Muted {
		t.Fatal("mention-only is not muted, the boolean shadow must follow the mode")
	}
	// muted=true with no mode still mutes (the legacy path keeps working).
	got2, _ := mergePrefs(cur, PrefsPatch{Muted: boolp(true)})
	if got2.Mode != ModeMuted || !got2.Muted {
		t.Fatalf("legacy muted=true must map to mode MUTED, got %+v", got2)
	}
	// muted=false un-mutes a previously muted row without clobbering a MENTIONS setting.
	mentionsRow := applyMode(cur, ModeMentions)
	got3, _ := mergePrefs(mentionsRow, PrefsPatch{Muted: boolp(false)})
	if got3.Mode != ModeMentions {
		t.Fatalf("muted=false must not downgrade MENTIONS to ALL, got %+v", got3)
	}
	if _, ok := mergePrefs(cur, PrefsPatch{Mode: strp("LOUD")}); ok {
		t.Fatal("an unknown mode must be rejected at the service boundary, not sent to the CHECK")
	}
}

func strp(s string) *string { return &s }

// ModeOf derives the mode from the legacy boolean for a pre-0137 row, so a user who had muted a
// channel is not silently un-muted.
func TestModeOf_DerivesFromLegacyBoolean(t *testing.T) {
	if got := ModeOf(NotificationPrefs{Muted: true}); got != ModeMuted {
		t.Fatalf("a legacy muted row must read as MUTED, got %q", got)
	}
	if got := ModeOf(NotificationPrefs{}); got != ModeAll {
		t.Fatalf("an unset row must read as ALL, got %q", got)
	}
	if got := ModeOf(NotificationPrefs{Mode: ModeMentions, Muted: false}); got != ModeMentions {
		t.Fatalf("an explicit mode must win, got %q", got)
	}
}
