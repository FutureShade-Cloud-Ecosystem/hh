//go:build integration

// syncscope_refusal_words_integration_test.go: UX-108 round 3. The messages
// scope's create refusal answers the seam's plain words.
//
// The defect (M3's round 2 read of this PR, P3-2): for the messages scope's
// post-0449 key, a member the room fence refuses gets two answers that
// differ in words only. The bound key gets the gate's plain refusal
// ("sync: row not found", core/internal/sync/engine.go:273) and a key nobody
// ever applied gets the module's own create refusal ("sync: row not found:
// messaging: not found", core/internal/messaging/syncscope.go:281). The
// suffix tells a spent key from a free one: a word oracle on a security
// seam.
//
// The legs run on the real database, through the real engine and the real key
// ledger, and compare the FULL answers field for field: the status word, the
// seq, the error words and the envelope. A caller who cannot read the row
// must not be able to tell a spent key from a free one.
//
// The scope's other refusal paths are measured too. The update, the delete
// and the prefs paths answer the seam's plain words already (the engine
// refuses a row the caller cannot read before the store is asked), and the
// acks path does not: it carries the same module suffix.
package messaging

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// ux108R3Room is a plain room: no member_roles list, so the room fence admits
// every caller and the module's own membership rule is the only refusal under
// test.
const ux108R3Room = chYardTrenton

// ux108R3Push pushes one operation through the real engine over the real key
// ledger and answers the way a device sees it.
func ux108R3Push(t *testing.T, e *sync.Engine, info sync.ScopeInfo, f sync.ScopeFilter, op apigen.SyncPushOperation) apigen.SyncPushResult {
	t.Helper()
	res, err := e.Push(context.Background(), info, f, []apigen.SyncPushOperation{op})
	if err != nil {
		t.Fatalf("the push failed: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("the push answered %d results, want 1", len(res.Results))
	}
	return res.Results[0]
}

// ux108R3Err is one answer's error words, the empty string when the answer
// carries none.
func ux108R3Err(r apigen.SyncPushResult) string {
	if r.Error == nil {
		return ""
	}
	return *r.Error
}

// ux108R3Seq is one answer's seq, the word null when the answer carries none.
func ux108R3Seq(r apigen.SyncPushResult) string {
	if r.Seq == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *r.Seq)
}

// ux108R3Envelope is one answer's conflict envelope, the word null when the
// answer carries none.
func ux108R3Envelope(r apigen.SyncPushResult) string {
	if r.Conflict == nil {
		return "null"
	}
	return fmt.Sprintf("%s:%v", r.Conflict.Policy, r.Conflict.ServerState)
}

// ux108R3Answer renders one answer the way a device sees it: the status word,
// the seq, the error words and the envelope.
func ux108R3Answer(r apigen.SyncPushResult) string {
	return fmt.Sprintf(`{"status":%q,"seq":%s,"error":%q,"conflict":%s}`,
		r.Status, ux108R3Seq(r), ux108R3Err(r), ux108R3Envelope(r))
}

// ux108R3Same compares two answers the way a device sees them: the status
// word, the seq, the error words and the envelope. The client id is left out,
// because the two answers compared are about two different keys.
func ux108R3Same(t *testing.T, what string, got, want apigen.SyncPushResult) {
	t.Helper()
	if got.Status != want.Status {
		t.Errorf("%s: the status words differ: got %q want %q (%s)", what, got.Status, want.Status, ux108R3Answer(got))
	}
	if ux108R3Seq(got) != ux108R3Seq(want) {
		t.Errorf("%s: the seq answers differ: got %s want %s (%s)", what, ux108R3Seq(got), ux108R3Seq(want), ux108R3Answer(got))
	}
	if ux108R3Err(got) != ux108R3Err(want) {
		t.Errorf("%s: the error words differ: got %q want %q (%s)", what, ux108R3Err(got), ux108R3Err(want), ux108R3Answer(got))
	}
	if ux108R3Envelope(got) != ux108R3Envelope(want) {
		t.Errorf("%s: the envelopes differ: got %s want %s (%s)", what, ux108R3Envelope(got), ux108R3Envelope(want), ux108R3Answer(got))
	}
}

// ux108R3Plain holds one refusal to the seam's plain words, with no module
// suffix: the word every scope that refuses a row the caller cannot read
// gives, so the engine's gate and the store's own refusal are the same word.
func ux108R3Plain(t *testing.T, what string, r apigen.SyncPushResult) {
	t.Helper()
	if ux108R3Err(r) != sync.ErrRowNotFound.Error() {
		t.Errorf("%s: the refusal answers the seam's plain words: got %q want %q (%s)",
			what, ux108R3Err(r), sync.ErrRowNotFound.Error(), ux108R3Answer(r))
	}
}

// TestMessagesSyncCreateRefusalAnswersTheSeamPlainWords: the money room leg.
// The trail holder posts under a key, so the key is bound to him by that
// write, and the key is bound after migration 0449 (the ledger row names its
// binder), so the engine's gate holds it. A member the room fence refuses
// replays that key and pushes a key nobody ever applied, with the same body
// and with another body: the four answers must be the same word for word.
//
// Before the fix the gate answers the seam's plain "sync: row not found" and
// the module's own create refusal answers "sync: row not found: messaging:
// not found", so the suffix tells the member a spent key from a free one.
func TestMessagesSyncCreateRefusalAnswersTheSeamPlainWords(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("messages", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages missing from the registry")
	}

	body := "ux108r3: a money room post"
	key := uuid.NewString()
	op := apigen.SyncPushOperation{ClientId: key, Op: "create",
		Payload: map[string]any{"channel_id": ux108Room, "body": body}}

	// The trail holder posts: the key is bound to him by that write.
	first := ux108R3Push(t, engine, info, sam, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the trail holder posts in the money room: %+v", first)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE idempotency_key = $1::uuid`, key)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})

	// The key is bound after migration 0449: the ledger row names its binder,
	// so the engine's gate holds it. A key bound before 0449 (bound_by NULL)
	// skips the gate and keeps the bound answer for every caller.
	var boundBy *string
	if err := f.pool.QueryRow(ctx, `SELECT bound_by FROM sync_op_hashes WHERE client_id = $1::uuid`, key).Scan(&boundBy); err != nil {
		t.Fatalf("read the ledger row: %v", err)
	}
	if boundBy == nil || *boundBy != sam.Actor {
		t.Fatalf("the key is bound to the caller who applied it: %v", boundBy)
	}

	// The refused member replays the bound key: the gate's own refusal.
	bound := ux108R3Push(t, engine, info, casey, op)
	// The refused member pushes a key nobody ever applied, the same body:
	// the module's own create refusal.
	free := ux108R3Push(t, engine, info, casey, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"channel_id": ux108Room, "body": body}})
	t.Logf("ux108r3 money room, the same body: the bound key %s, the never-bound key %s",
		ux108R3Answer(bound), ux108R3Answer(free))
	ux108R3Same(t, "the bound key and a never-bound key, the same body", bound, free)
	ux108R3Plain(t, "the create refusal", free)

	// Another body under the same two keys: the same pair, the same words.
	boundOther := ux108R3Push(t, engine, info, casey, apigen.SyncPushOperation{
		ClientId: key, Op: "create",
		Payload: map[string]any{"channel_id": ux108Room, "body": "ux108r3: another body"}})
	freeOther := ux108R3Push(t, engine, info, casey, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"channel_id": ux108Room, "body": "ux108r3: another body"}})
	t.Logf("ux108r3 money room, another body: the bound key %s, the never-bound key %s",
		ux108R3Answer(boundOther), ux108R3Answer(freeOther))
	ux108R3Same(t, "the bound key and a never-bound key, another body", boundOther, freeOther)

	// The binder's own replay keeps its bound answer.
	replay := ux108R3Push(t, engine, info, sam, op)
	if replay.Status != "applied" || replay.Seq == nil || *replay.Seq != *first.Seq {
		t.Errorf("the binder's own replay keeps its bound answer: %+v", replay)
	}

	// Nothing was written by the refused member.
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM msg_messages WHERE author_id = $1::uuid`, uCaseyDoyle).Scan(&n); err != nil || n != 0 {
		t.Errorf("the refused member wrote nothing: %d messages, %v", n, err)
	}
}

// TestMessagesSyncCreateRefusalOnAMembershipMissAnswersTheSeamPlainWords: the
// module's own refusal, the one the create reaches when the room admits the
// caller but the channel does not seat him. The same pair, the same words: a
// caller who cannot read the row must not be able to tell a spent key from a
// free one.
func TestMessagesSyncCreateRefusalOnAMembershipMissAnswersTheSeamPlainWords(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()

	// The plain room: the trail holder is seated, the other member is not.
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO msg_members (channel_id, user_id, role, source) VALUES ($1::uuid, $2::uuid, 'MEMBER', 'EXPLICIT')
		 ON CONFLICT (channel_id, user_id) DO NOTHING`, ux108R3Room, uSamRivera); err != nil {
		t.Fatalf("seat the trail holder in the plain room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ux108R3Room, uSamRivera)
	})
	var seated bool
	if err := f.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid)`,
		ux108R3Room, uCaseyDoyle).Scan(&seated); err != nil {
		t.Fatalf("read the seat table: %v", err)
	}
	if seated {
		t.Fatalf("the fixture is wrong: the member who is not seated is seated in the plain room")
	}
	if !admitsRoom(ctx, f.store.fence, uCaseyDoyle, ux108R3Room) {
		t.Fatalf("the fixture is wrong: the plain room refuses a member it does not seat")
	}

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("messages", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages missing from the registry")
	}

	body := "ux108r3: a post in a room the other member is not seated in"
	key := uuid.NewString()
	op := apigen.SyncPushOperation{ClientId: key, Op: "create",
		Payload: map[string]any{"channel_id": ux108R3Room, "body": body}}

	first := ux108R3Push(t, engine, info, sam, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the trail holder posts in the plain room: %+v", first)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE idempotency_key = $1::uuid`, key)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})

	// The bound key: the gate's own refusal.
	bound := ux108R3Push(t, engine, info, casey, op)
	// The never-bound key: the module's own membership refusal.
	free := ux108R3Push(t, engine, info, casey, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"channel_id": ux108R3Room, "body": body}})
	t.Logf("ux108r3 membership miss: the bound key %s, the never-bound key %s",
		ux108R3Answer(bound), ux108R3Answer(free))
	ux108R3Same(t, "the bound key and a never-bound key, the same body", bound, free)
	ux108R3Plain(t, "the create refusal", free)
}

// TestMessagesSyncAckRefusalAnswersTheSeamPlainWords: the acks path. The ack
// is the caller's own row, so a caller who is not seated in the message's
// channel cannot ack it, and the module's own refusal answers the same words
// the gate answers.
func TestMessagesSyncAckRefusalAnswersTheSeamPlainWords(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()

	if _, err := f.pool.Exec(ctx,
		`INSERT INTO msg_members (channel_id, user_id, role, source) VALUES ($1::uuid, $2::uuid, 'MEMBER', 'EXPLICIT')
		 ON CONFLICT (channel_id, user_id) DO NOTHING`, ux108R3Room, uSamRivera); err != nil {
		t.Fatalf("seat the trail holder in the plain room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ux108R3Room, uSamRivera)
	})

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("messages", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	if err := engine.RegisterScope("acks", NewAcksSyncStore(NewRepository(f.pool), link)); err != nil {
		t.Fatalf("register the acks scope: %v", err)
	}
	acks, ok := sync.Lookup("acks")
	if !ok {
		t.Fatal("acks missing from the registry")
	}

	// A post in the plain room, and the trail holder's acknowledgement of it.
	post, err := f.store.Create(ctx, sam, uuid.NewString(), map[string]any{
		"channel_id": ux108R3Room, "body": "ux108r3: a post to acknowledge"})
	if err != nil {
		t.Fatalf("the trail holder posts in the plain room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_acks WHERE message_id = $1::uuid`, post.ID)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1::uuid`, post.ID)
	})

	key := uuid.NewString()
	ackOp := apigen.SyncPushOperation{ClientId: key, Op: "create",
		Payload: map[string]any{"message_id": post.ID, "method": "TAP"}}
	first := ux108R3Push(t, engine, acks, sam, ackOp)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the trail holder acknowledges the post: %+v", first)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})

	// The bound key: the gate's own refusal.
	bound := ux108R3Push(t, engine, acks, casey, ackOp)
	// The never-bound key: the module's own refusal.
	free := ux108R3Push(t, engine, acks, casey, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"message_id": post.ID, "method": "TAP"}})
	t.Logf("ux108r3 acks: the bound key %s, the never-bound key %s",
		ux108R3Answer(bound), ux108R3Answer(free))
	ux108R3Same(t, "the bound key and a never-bound key, the same body", bound, free)
	ux108R3Plain(t, "the ack refusal", free)

	// The binder's own replay keeps its bound answer.
	replay := ux108R3Push(t, engine, acks, sam, ackOp)
	if replay.Status != "applied" || replay.Seq == nil || *replay.Seq != *first.Seq {
		t.Errorf("the binder's own replay keeps its bound answer: %+v", replay)
	}
}

// TestMessagesSyncOtherPathsRefuseWithTheSeamPlainWords: the control. The
// update, the delete and the prefs paths refuse a row the caller cannot read
// with the seam's plain words already, because the engine refuses it before
// the store is asked. The words carry no module suffix, and this leg holds
// them there.
func TestMessagesSyncOtherPathsRefuseWithTheSeamPlainWords(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("messages", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	if err := engine.RegisterScope("notify_prefs", NewNotifyPrefsSyncStore(NewRepository(f.pool), link)); err != nil {
		t.Fatalf("register the notify_prefs scope: %v", err)
	}
	messages, _ := sync.Lookup("messages")
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}

	// A post in the money room the refused member cannot read.
	post, err := f.store.Create(ctx, sam, uuid.NewString(), map[string]any{
		"channel_id": ux108Room, "body": "ux108r3: a post in the money room"})
	if err != nil {
		t.Fatalf("the trail holder posts in the money room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1::uuid`, post.ID)
	})

	// The update arm: the engine refuses the row the caller cannot read.
	update := ux108R3Push(t, engine, messages, casey, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": post.ID, "body": "ux108r3: an edit the room refuses"}})
	t.Logf("ux108r3 messages update: %s", ux108R3Answer(update))
	ux108R3Plain(t, "the update refusal", update)

	// The delete arm: the same plain refusal.
	del := ux108R3Push(t, engine, messages, casey, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "delete", Payload: map[string]any{"id": post.ID}})
	t.Logf("ux108r3 messages delete: %s", ux108R3Answer(del))
	ux108R3Plain(t, "the delete refusal", del)

	// The prefs arm: a row the caller does not hold answers the plain words.
	pref := ux108R3Push(t, engine, prefs, casey, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": uuid.NewString(), "muted": true}})
	t.Logf("ux108r3 notify_prefs update: %s", ux108R3Answer(pref))
	ux108R3Plain(t, "the prefs refusal", pref)

	// The binder's own delete still refuses with the module's own wall, the
	// one refusal the module keeps: a sent message is not deletable over
	// sync. That word is not a row's existence, so it carries no suffix.
	own := ux108R3Push(t, engine, messages, sam, apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "delete", Payload: map[string]any{"id": post.ID}})
	t.Logf("ux108r3 messages delete by the trail holder: %s", ux108R3Answer(own))
	if own.Status != "rejected" || own.Error == nil {
		t.Errorf("a delete of a live post is refused by the module's own wall: %+v", own)
	}
	if ux108R3Err(own) == ux108R3Err(update) {
		t.Errorf("the module's own wall is not the seam's plain refusal: %+v", own)
	}
}
