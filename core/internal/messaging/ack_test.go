package messaging

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - AckMessage: membership authz + method validation + upsert (service, DB-free) -

func TestAckMessage_NonMemberForbidden(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice) // Alice is a member; Bob is not
	svc := svcWith(f)

	// A member can ack.
	if _, err := svc.AckMessage(context.Background(), Caller{UserID: uAlice}, chGen, "m1", AckTap); err != nil {
		t.Fatalf("member ack should succeed: %v", err)
	}
	// A non-member is forbidden (the channel EXISTS, so 403 not 404), same gate as MarkRead.
	if _, err := svc.AckMessage(context.Background(), Caller{UserID: uBob}, chGen, "m1", AckTap); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member ack want ErrNotMember, got %v", err)
	}
}

func TestAckMessage_DefaultsToTap(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	ack, err := svc.AckMessage(context.Background(), Caller{UserID: uAlice}, chGen, "m1", "")
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if ack.Method != AckTap {
		t.Fatalf("empty method must default to TAP, got %q", ack.Method)
	}
}

func TestAckMessage_RejectsBadMethod(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	for _, bad := range []string{"SHOUT", "tap", "email", "'; DROP"} {
		if _, err := svc.AckMessage(context.Background(), Caller{UserID: uAlice}, chGen, "m1", bad); !errors.Is(err, ErrBadAckMethod) {
			t.Fatalf("bad method %q want ErrBadAckMethod, got %v", bad, err)
		}
	}
	// A non-member must be rejected on membership BEFORE the method is even inspected (authz first).
	if _, err := svc.AckMessage(context.Background(), Caller{UserID: uBob}, chGen, "m1", "SHOUT"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member with a bad method should still be ErrNotMember, got %v", err)
	}
}

func TestAckMessage_UpsertIsIdempotent(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	// Two acks of the SAME message by the SAME user must collapse to one row (upsert), and the second
	// may change the method, mirroring the repository's ON CONFLICT DO UPDATE.
	if _, err := svc.AckMessage(context.Background(), Caller{UserID: uAlice}, chGen, "m1", AckTap); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	if _, err := svc.AckMessage(context.Background(), Caller{UserID: uAlice}, chGen, "m1", AckVoice); err != nil {
		t.Fatalf("second ack: %v", err)
	}
	if len(f.acks) != 1 {
		t.Fatalf("re-acking the same message must upsert (one row), got %d", len(f.acks))
	}
	if got := f.acks["m1|"+uAlice].Method; got != AckVoice {
		t.Fatalf("second ack should update the method to VOICE, got %q", got)
	}
}

// - UnackedRequiresAck: the kiosk poll read (service, DB-free) -

func TestUnackedRequiresAck_ReturnsStoreRows(t *testing.T) {
	f := newFakeStore()
	f.unacked[uAlice] = []Message{
		{ID: "m2", Kind: MsgEvent, Body: "Credit hold, Acme (NSF).", RequiresAck: true},
	}
	svc := svcWith(f)
	got, err := svc.UnackedRequiresAck(context.Background(), Caller{UserID: uAlice})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(got) != 1 || got[0].ID != "m2" || !got[0].RequiresAck {
		t.Fatalf("poll should surface the un-acked forced-ack message, got %+v", got)
	}
}

// A user with nothing un-acked must get a NON-NIL empty slice so the endpoint serializes
// {"messages":[]} (mirrors GetMessages' empty-channel coercion), never {"messages":null}.
func TestUnackedRequiresAck_EmptyIsNonNil(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	got, err := svc.UnackedRequiresAck(context.Background(), Caller{UserID: uBob})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got == nil {
		t.Fatal("empty poll must be a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("empty poll should be length 0, got %d", len(got))
	}
}

// - Consumer allowlist: only credit-hold / order-blocked force an ack (0071) -

func TestConsumer_CreditHoldForcesAck(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	c := NewConsumer(svcWith(f), nil)
	ev := mkEvent(t, TopicCreditHold, eventPayload{EventID: "e-ack", Customer: "Acme", Reason: "NSF"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(f.events) != 1 || !f.events[0].RequiresAck {
		t.Fatalf("a credit hold must post a forced-ack (requires_ack) message, got %+v", f.events)
	}
}

func TestConsumer_OrderReleasedDoesNotForceAck(t *testing.T) {
	f := newFakeStore()
	f.yard["branch-trt"] = "yard-trenton"
	f.slugs["yard-trenton"] = "ch-yard-trt"
	c := NewConsumer(svcWith(f), nil)
	ev := mkEvent(t, TopicOrderReleased, eventPayload{EventID: "e-plain", BranchID: "branch-trt", OrderNo: "SO-1"})
	if err := c.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(f.events) != 1 || f.events[0].RequiresAck {
		t.Fatalf("an order-released is an ordinary post, must NOT force an ack, got %+v", f.events)
	}
}

// requiresAck is the pure allowlist predicate, pin the exact P1 set so a future edit is deliberate.
func TestRequiresAck_Allowlist(t *testing.T) {
	forced := map[string]bool{TopicCreditHold: true, TopicOrderBlocked: true}
	for _, tp := range append(MessagingTopics, "some.other.topic") {
		if requiresAck(tp) != forced[tp] {
			t.Errorf("requiresAck(%q) = %v, want %v", tp, requiresAck(tp), forced[tp])
		}
	}
}

// - Handler: path-param validation + happy path (mirrors the markRead handler tests) -

func ackReq(t *testing.T, id, msgID, body string) *http.Request {
	t.Helper()
	var r *http.Request
	url := "/api/v1/messaging/channels/" + id + "/messages/" + msgID + "/ack"
	if body == "" {
		r = httptest.NewRequest(http.MethodPost, url, nil)
	} else {
		r = httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	}
	if id != "" {
		r.SetPathValue("id", id)
	}
	if msgID != "" {
		r.SetPathValue("msgID", msgID)
	}
	return r.WithContext(identity.ContextWithStaff(r.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
}

// A malformed (non-UUID) {id} or {msgID} on POST .../ack must be a 4xx client fault, not the catch-all
// 500 (pre-fix the junk id would flow into a Postgres text->uuid cast). Guarded at the boundary by
// pathUUID, exactly like markRead / listThread.
func TestAckMessage_NonUUIDPath_Returns4xx(t *testing.T) {
	h := newCastHandler(t)
	cases := []struct{ id, msg string }{
		{"not-a-uuid", rootUUID}, // bad channel id
		{chUUID, "not-a-uuid"},   // valid channel, bad message id
		{"xx", "yy"},             // both bad
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		h.ackMessage(rr, ackReq(t, c.id, c.msg, `{"method":"TAP"}`), guards.StaffCaller{})
		if rr.Code < 400 || rr.Code >= 500 {
			t.Fatalf("ack id=%q msg=%q: want 4xx, got %d (body %s)", c.id, c.msg, rr.Code, rr.Body.String())
		}
	}
}

// A well-formed ack by a member reaches 200, proving the boundary guards don't over-reject valid ids,
// and that an EMPTY body defaults the method to TAP (no 400 for a body-less "I got it").
func TestAckMessage_ValidReaches200(t *testing.T) {
	h := newCastHandler(t) // newCastHandler makes Alice a member of chUUID
	for _, body := range []string{`{"method":"TAP"}`, ""} {
		rr := httptest.NewRecorder()
		h.ackMessage(rr, ackReq(t, chUUID, rootUUID, body), guards.StaffCaller{})
		if rr.Code != http.StatusOK {
			t.Fatalf("valid ack body=%q: want 200, got %d (body %s)", body, rr.Code, rr.Body.String())
		}
	}
}

// A bad method must map to HTTP 400 through the full handler→service chain (ErrBadAckMethod), not 500.
func TestAckMessage_BadMethod_Returns400(t *testing.T) {
	h := newCastHandler(t)
	rr := httptest.NewRecorder()
	h.ackMessage(rr, ackReq(t, chUUID, rootUUID, `{"method":"SHOUT"}`), guards.StaffCaller{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad ack method: want 400, got %d (body %s)", rr.Code, rr.Body.String())
	}
}
