//go:build integration

// syncscope_ux130_acks_seat_first_integration_test.go: UX-130. Two defects in the acks sync write.
//
// (a) Timing, introduced by UX-129: the write ran the money room fence before it knew the caller was seated, the
// reverse of REST's order (seat first, then the room). A non-member holding a message's uuid could tell a money room
// message from a missing one by the fence's cost. The order is proved at source: a fence spy counts calls, and it must
// stay at zero for a caller with no seat and for a missing message.
//
// (b) A message_id that is not a uuid failed the whole acks push with 503, good acks included. It is now refused as
// one operation, with plain words, under a spent key and a free key alike.
package messaging

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

type ux130Rig struct {
	f      *ux108Fixture
	engine *sync.Engine
	acks   sync.ScopeInfo
	calls  *int
	sam    sync.ScopeFilter
	casey  sync.ScopeFilter
	postID string
}

func newUX130Rig(t *testing.T) *ux130Rig {
	t.Helper()
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()
	calls := 0
	spy := func(c context.Context, userID, channelID string) bool {
		calls++
		return f.store.fence(c, userID, channelID)
	}
	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	if err := engine.RegisterScope("acks", NewAcksSyncStore(NewRepository(f.pool), link).WithRoomFence(spy)); err != nil {
		t.Fatalf("register the acks scope: %v", err)
	}
	acks, ok := sync.Lookup("acks")
	if !ok {
		t.Fatal("acks missing from the registry")
	}
	post, err := f.store.Create(ctx, sam, uuid.NewString(), map[string]any{
		"channel_id": ux108Room, "body": "ux130: a money room post"})
	if err != nil {
		t.Fatalf("the trail holder posts in the money room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_acks WHERE message_id = $1::uuid`, post.ID)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1::uuid`, post.ID)
	})
	return &ux130Rig{f: f, engine: engine, acks: acks, calls: &calls, sam: sam, casey: casey, postID: post.ID}
}

func (r *ux130Rig) push(t *testing.T, f sync.ScopeFilter, ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	t.Helper()
	res, err := r.engine.Push(context.Background(), r.acks, f, ops)
	if err != nil {
		t.Fatalf("the push failed as a whole: %v", err)
	}
	if len(res.Results) != len(ops) {
		t.Fatalf("the push answered %d results, want %d", len(res.Results), len(ops))
	}
	for _, o := range ops {
		id := o.ClientId
		t.Cleanup(func() {
			_, _ = r.f.pool.Exec(context.Background(), `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, id)
		})
	}
	return res.Results
}

func ux130Ack(messageID string) apigen.SyncPushOperation {
	return apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"message_id": messageID, "method": "TAP"}}
}

// TestUX130AckNeverCallsTheFenceForAnUnseatedCallerOrAMissingMessage is the order proof for (a).
func TestUX130AckNeverCallsTheFenceForAnUnseatedCallerOrAMissingMessage(t *testing.T) {
	r := newUX130Rig(t)
	ctx := context.Background()

	// Casey is unseated: he holds the message's uuid and no seat in the money room.
	if _, err := r.f.pool.Exec(ctx,
		`DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ux108Room, uCaseyDoyle); err != nil {
		t.Fatalf("unseat Casey: %v", err)
	}

	*r.calls = 0
	nonMember := r.push(t, r.casey, ux130Ack(r.postID))[0]
	missing := r.push(t, r.casey, ux130Ack(uuid.NewString()))[0]
	if *r.calls != 0 {
		t.Errorf("the fence was called %d time(s) for an unseated caller or a missing message, want 0", *r.calls)
	}
	ux108R3Plain(t, "the non-member", nonMember)
	ux108R3Plain(t, "the missing message", missing)
	ux108R3Same(t, "a non-member and a missing message", nonMember, missing)
	if nonMember.Seq != nil || missing.Seq != nil {
		t.Errorf("a refusal took a seq: %s, %s", ux108R3Answer(nonMember), ux108R3Answer(missing))
	}

	// A seated, fence-refused member (stale seat, no trail) meets the same bytes, and the fence ran for him.
	if _, err := r.f.pool.Exec(ctx,
		`INSERT INTO msg_members (channel_id, user_id, role, source) VALUES ($1::uuid, $2::uuid, 'MEMBER', 'EXPLICIT')
		 ON CONFLICT (channel_id, user_id) DO NOTHING`, ux108Room, uCaseyDoyle); err != nil {
		t.Fatalf("seat Casey: %v", err)
	}
	*r.calls = 0
	refused := r.push(t, r.casey, ux130Ack(r.postID))[0]
	if *r.calls == 0 {
		t.Errorf("the fence never ran for a seated caller")
	}
	ux108R3Same(t, "a fence-refused member and a missing message", refused, missing)

	// An admitted member still applies.
	*r.calls = 0
	ok := r.push(t, r.sam, ux130Ack(r.postID))[0]
	if ok.Status != "applied" || ok.Seq == nil {
		t.Errorf("the admitted member's ack did not apply: %s", ux108R3Answer(ok))
	}
	if *r.calls == 0 {
		t.Errorf("the fence never ran for the admitted member")
	}
}

// TestUX130AckMalformedMessageIDIsRefusedAlone is the leg for (b).
func TestUX130AckMalformedMessageIDIsRefusedAlone(t *testing.T) {
	r := newUX130Rig(t)

	bad := ux130Ack("not-a-uuid")
	good := ux130Ack(r.postID)
	res := r.push(t, r.sam, bad, good)
	if res[0].Status == "applied" || ux108R3Err(res[0]) == "" {
		t.Errorf("the malformed ack was not refused: %s", ux108R3Answer(res[0]))
	}
	if ux108R3Err(res[0]) != "sync: operation refused: message_id is not a valid id" {
		t.Errorf("the refusal words: got %q", ux108R3Err(res[0]))
	}
	if res[1].Status != "applied" || res[1].Seq == nil {
		t.Errorf("the good ack beside the malformed one did not apply: %s", ux108R3Answer(res[1]))
	}

	// A spent key and a free key answer alike: Sam binds a key, Casey replays it with a malformed id.
	spent := r.push(t, r.sam, ux130Ack(r.postID))[0]
	if spent.Status != "applied" {
		t.Fatalf("bind a key: %s", ux108R3Answer(spent))
	}
	boundOp := apigen.SyncPushOperation{ClientId: spent.ClientId, Op: "create",
		Payload: map[string]any{"message_id": "not-a-uuid", "method": "TAP"}}
	boundAns := r.push(t, r.casey, boundOp)[0]
	freeAns := r.push(t, r.casey, ux130Ack("not-a-uuid"))[0]
	ux108R3Same(t, "a spent key and a free key with a malformed id", boundAns, freeAns)
}
