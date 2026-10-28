//go:build integration

// syncscope_gone_row_gate_integration_test.go: UX-114, the gone-row residual,
// measured through the real engine and the real key ledger on the migrated and
// seeded database (principal item 295). Sam binds a key on a post in the
// Trenton yard room and the post is then withdrawn; Casey, a member the room
// fence refuses, replays the key. Before the fix the bound answer (applied)
// leaks the one bit: the key was spent on a row that is gone. After the fix
// the non binder is refused with the seam's row not found, the absent key's
// answer; the binder keeps the bound answer and nothing writes twice; a key
// with no binder (bound before migration 0449) keeps today's answer.
package messaging

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// gateOrgID is the probe org anchor. The ledger keys its rows by org uuid
// (the filter's org is cast at the door), and the messaging tables keep no
// org column, so any well-formed uuid serves.
const gateOrgID = "3ef10000-0000-4000-8000-0000000000e1"

func gateSamFilter() sync.ScopeFilter { return sync.ScopeFilter{OrgID: gateOrgID, Actor: "sub-sam"} }
func gateCaseyFilter() sync.ScopeFilter {
	return sync.ScopeFilter{OrgID: gateOrgID, Actor: "sub-casey"}
}

// gateRoomFence is the stub money room fence: the Trenton yard room admits Sam
// alone. Casey is seated in the room and refused it, the shape the leak probe
// needs.
func gateRoomFence(_ context.Context, userID, channelID string) bool {
	if channelID == chYardTrenton {
		return userID == uSamRivera
	}
	return true
}

// newGateFixture wires the real messages and acks stores over the seeded
// database, the real key ledger, and the stub money room fence on the
// messages feed.
func newGateFixture(t *testing.T) *syncFeedFixture {
	t.Helper()
	pool := intPool(t)
	repo := NewRepository(pool)
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	engine := sync.NewEngine()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	must(engine.RegisterScope("messages", NewMessagesSyncStore(repo, link).WithRoomFence(gateRoomFence)))
	must(engine.RegisterScope("acks", NewAcksSyncStore(repo, link)))
	engine.WithKeyLedger(sync.NewPgKeyLedger(pool))
	return &syncFeedFixture{pool: pool, engine: engine, repo: repo}
}

// seatCaseyInTheMoneyRoom plants Casey's membership in the Trenton yard room:
// he is a member the room fence refuses, not a stranger.
func seatCaseyInTheMoneyRoom(t *testing.T, f *syncFeedFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO msg_members (channel_id, user_id, role, source)
		 VALUES ($1, $2, 'MEMBER', 'EXPLICIT')
		 ON CONFLICT DO NOTHING`, chYardTrenton, uCaseyDoyle); err != nil {
		t.Fatalf("seat casey in the money room: %v", err)
	}
}

// cleanProbeRows removes the probe's message, its ledger rows and Casey's
// planted seat (the seeded database is shared; every probe cleans its own).
func cleanProbeRows(t *testing.T, f *syncFeedFixture, keys ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx,
		`DELETE FROM msg_messages WHERE idempotency_key = ANY($1::uuid[])`, keys); err != nil {
		t.Fatalf("clean probe messages: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`, keys); err != nil {
		t.Fatalf("clean probe ledger rows: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`DELETE FROM msg_members WHERE channel_id = $1 AND user_id = $2 AND source = 'EXPLICIT'`,
		chYardTrenton, uCaseyDoyle); err != nil {
		t.Fatalf("clean casey's seat: %v", err)
	}
}

// ledgerRowsFor counts the ledger rows the probe's keys hold.
func ledgerRowsFor(t *testing.T, f *syncFeedFixture, keys ...string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`, keys).Scan(&n); err != nil {
		t.Fatalf("count probe ledger rows: %v", err)
	}
	return n
}

// TestUX114MessagesGoneRowRefusesANonBinder: the withdrawn post's bound key.
func TestUX114MessagesGoneRowRefusesANonBinder(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	seatCaseyInTheMoneyRoom(t, f)

	boundKey := uuid.NewString()
	freeKey := uuid.NewString()
	body := map[string]any{"channel_id": chYardTrenton, "body": "ux114 gone row probe"}
	t.Cleanup(func() { cleanProbeRows(t, f, boundKey, freeKey) })

	msgs, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages scope missing from the registry")
	}

	// The binder applies the key on a live post.
	first := push(t, f.engine, msgs, gateSamFilter(), boundKey, "create", body)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the binder's create applies: %+v", first)
	}
	postID, _ := appliedRow(t, f.engine, msgs, gateSamFilter(), *first.Seq)

	// The post is withdrawn: the row is a tombstone for the room's readers.
	if _, err := f.repo.DeleteMessage(ctx, chYardTrenton, postID); err != nil {
		t.Fatalf("withdraw the post: %v", err)
	}
	var tombSeq int64
	feed, err := f.engine.Pull(ctx, msgs, gateSamFilter(), *first.Seq, 10)
	if err != nil {
		t.Fatalf("pull the tombstone: %v", err)
	}
	for _, tb := range feed.Tombstones {
		if tb.Id == postID {
			tombSeq = tb.Seq
		}
	}
	if tombSeq == 0 {
		t.Fatalf("the withdrawn post pulls as a tombstone for the binder: %+v", feed.Tombstones)
	}

	// Casey replays the bound key. The fix: the seam's refusal, the absent
	// key's answer, no seq, no envelope, nothing written.
	replay := push(t, f.engine, msgs, gateCaseyFilter(), boundKey, "create", body)
	if replay.Status != "rejected" {
		t.Errorf("the bound key on the gone row answers %+v, want rejected", replay)
	}
	if replay.Error == nil || *replay.Error != sync.ErrRowNotFound.Error() {
		t.Errorf("the bound key on the gone row answers error %v, want %q", replay.Error, sync.ErrRowNotFound)
	}
	if replay.Seq != nil || replay.Conflict != nil {
		t.Errorf("the refusal carries a seq or an envelope: %+v", replay)
	}

	// The never bound key with the same body: the same status, no seq and no
	// envelope either. The words carry the module's own suffix on this path
	// until the read fence agreement lands the seam's plain words; the status,
	// the seq and the envelope are the comparison this leg pins, and both
	// answers are the seam's miss.
	absent := push(t, f.engine, msgs, gateCaseyFilter(), freeKey, "create", body)
	if absent.Status != replay.Status || absent.Seq != nil || absent.Conflict != nil {
		t.Errorf("the never bound key answers %+v, want the same status with no seq and no envelope", absent)
	}
	if absent.Error == nil || !strings.HasPrefix(*absent.Error, sync.ErrRowNotFound.Error()) {
		t.Errorf("the never bound key answers error %v, want the seam's miss", absent.Error)
	}

	// The binder keeps the bound answer: applied, the tombstone's seq, and no
	// second write (the tombstone's seq stands, one ledger row only).
	again := push(t, f.engine, msgs, gateSamFilter(), boundKey, "create", body)
	if again.Status != "applied" || again.Seq == nil || *again.Seq != tombSeq {
		t.Errorf("the binder's replay answers %+v, want applied with the tombstone's seq %d", again, tombSeq)
	}
	if n := ledgerRowsFor(t, f, boundKey, freeKey); n != 1 {
		t.Errorf("the replay binds nothing new: %d ledger rows, want 1", n)
	}
}

// TestUX114MessagesGoneRowKeepsTheBoundAnswerWithoutABinder: a key whose
// ledger row names no binder (the pre 0449 shape) keeps today's answer for
// every caller and ages out.
func TestUX114MessagesGoneRowKeepsTheBoundAnswerWithoutABinder(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	seatCaseyInTheMoneyRoom(t, f)

	boundKey := uuid.NewString()
	body := map[string]any{"channel_id": chYardTrenton, "body": "ux114 pre 0449 probe"}
	t.Cleanup(func() { cleanProbeRows(t, f, boundKey) })

	msgs, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages scope missing from the registry")
	}
	first := push(t, f.engine, msgs, gateSamFilter(), boundKey, "create", body)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the binder's create applies: %+v", first)
	}
	postID, _ := appliedRow(t, f.engine, msgs, gateSamFilter(), *first.Seq)
	if _, err := f.repo.DeleteMessage(ctx, chYardTrenton, postID); err != nil {
		t.Fatalf("withdraw the post: %v", err)
	}

	// The pre 0449 shape: the binding stands, the binder is unknown.
	if _, err := f.pool.Exec(ctx,
		`UPDATE sync_op_hashes SET bound_by = NULL WHERE client_id = $1::uuid`, boundKey); err != nil {
		t.Fatalf("drop the binder: %v", err)
	}

	kept := push(t, f.engine, msgs, gateCaseyFilter(), boundKey, "create", body)
	if kept.Status != "applied" {
		t.Errorf("a key with no binder keeps the bound answer on a gone row: %+v", kept)
	}
	if n := ledgerRowsFor(t, f, boundKey); n != 1 {
		t.Errorf("the replay binds nothing new: %d ledger rows, want 1", n)
	}
}
