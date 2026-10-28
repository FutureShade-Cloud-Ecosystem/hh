//go:build integration

// syncscope_tombstone_integration_test.go: UX-108 round 2. A withdrawn
// message answers every read the way the pull answers it: a bare tombstone,
// the id and the seq and no projection. The pull has always said so (the
// tombstone arm of the messages scope's Pull), but Load and FindApplied
// answered a live-shaped row with its projection and Deleted false, so a
// device that read a withdrawn post back held a row the server had already
// dropped, and the contract a deleted row carries its seq and no projection
// was broken for this scope alone.
//
// The leg: the real store over the real database. Sam posts in the money
// room, withdraws the post, and the three reads answer alike: the pull
// carries a tombstone, Load answers a tombstone, FindApplied answers a
// tombstone. The room fence is unchanged: a member the room refuses still
// resolves nothing.
package messaging

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// TestMessagesSyncReadsAnswerAWithdrawnMessageAsATombstone: the load and the
// key read answer a withdrawn message the way the pull answers it.
func TestMessagesSyncReadsAnswerAWithdrawnMessageAsATombstone(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, casey := ux108Filters()

	key := uuid.NewString()
	rs, err := f.store.Create(ctx, sam, key, map[string]any{
		"channel_id": ux108Room,
		"body":       "ux108r2: a post that is withdrawn"})
	if err != nil {
		t.Fatalf("the trail holder posts in the room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1::uuid`, rs.ID)
	})

	// The author withdraws his own post: the body is dropped in the row
	// itself and the tombstone takes a fresh seq.
	withdrawn, err := NewRepository(f.pool).DeleteMessage(ctx, ux108Room, rs.ID)
	if err != nil {
		t.Fatalf("the author withdraws his own post: %v", err)
	}
	if withdrawn.DeletedAt == nil {
		t.Fatalf("the post is not withdrawn: %+v", withdrawn)
	}

	// The pull answers a bare tombstone.
	items, tombs, err := f.store.Pull(ctx, sam, 0, 200)
	if err != nil {
		t.Fatalf("the pull: %v", err)
	}
	var tomb *sync.Tombstone
	for i := range tombs {
		if tombs[i].ID == rs.ID {
			tomb = &tombs[i]
		}
	}
	if tomb == nil {
		t.Fatalf("the pull carries no tombstone for the withdrawn post: %+v", tombs)
	}
	for _, it := range items {
		if it.ID == rs.ID {
			t.Errorf("the pull carries the withdrawn post as a live row: %+v", it)
		}
	}

	// Load answers the same tombstone: the id, the seq, no projection.
	row, found, err := f.store.Load(ctx, sam, rs.ID)
	if err != nil || !found {
		t.Fatalf("the load finds the withdrawn post: found %v err %v", found, err)
	}
	if !row.Deleted {
		t.Errorf("the load answers a withdrawn post as a tombstone: %+v", row)
	}
	if len(row.Data) != 0 {
		t.Errorf("the load answers a withdrawn post with no projection: %+v", row.Data)
	}
	if row.ID != rs.ID {
		t.Errorf("the load answers the row's id: got %q want %q", row.ID, rs.ID)
	}
	if row.Seq != tomb.Seq {
		t.Errorf("the load answers the tombstone's seq: got %v want %v", row.Seq, tomb.Seq)
	}

	// FindApplied answers the same tombstone.
	applied, found, err := f.store.FindApplied(ctx, sam, key)
	if err != nil || !found {
		t.Fatalf("the key resolves: found %v err %v", found, err)
	}
	if !applied.Deleted {
		t.Errorf("the key answers a withdrawn post as a tombstone: %+v", applied)
	}
	if len(applied.Data) != 0 {
		t.Errorf("the key answers a withdrawn post with no projection: %+v", applied.Data)
	}
	if applied.ID != rs.ID {
		t.Errorf("the key answers the row's id: got %q want %q", applied.ID, rs.ID)
	}
	if applied.Seq != tomb.Seq {
		t.Errorf("the key answers the tombstone's seq: got %v want %v", applied.Seq, tomb.Seq)
	}

	// The room fence is unchanged: a member the room refuses resolves
	// nothing, a withdrawn post or not.
	_, found, err = f.store.FindApplied(ctx, casey, key)
	if err != nil {
		t.Fatalf("the refused member's key: %v", err)
	}
	if found {
		t.Errorf("the refused member resolves the key to the withdrawn post")
	}
}

// TestMessagesSyncDeleteOfAWithdrawnPostReportsTheTombstonesSeq: the
// engine's own tombstone rule reaches this scope now that the load answers a
// withdrawn post as a tombstone. A delete of a post that is already withdrawn
// reports the tombstone's seq rather than allocating a second one for the
// same fact, the answer every scope that answers a tombstone gives. A delete
// of a LIVE post still refuses: the module's own wall holds.
func TestMessagesSyncDeleteOfAWithdrawnPostReportsTheTombstonesSeq(t *testing.T) {
	f := newUX108Fixture(t)
	ctx := context.Background()
	sam, _ := ux108Filters()

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("messages", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages missing from the registry")
	}

	// A live post: the module's own wall refuses the delete.
	live, err := f.store.Create(ctx, sam, uuid.NewString(), map[string]any{
		"channel_id": ux108Room, "body": "ux108r2: a live post"})
	if err != nil {
		t.Fatalf("the trail holder posts in the room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1::uuid`, live.ID)
	})
	res, err := engine.Push(ctx, info, sam, []apigen.SyncPushOperation{{
		ClientId: uuid.NewString(), Op: "delete", Payload: map[string]any{"id": live.ID}}})
	if err != nil {
		t.Fatalf("the delete of a live post failed the push: %v", err)
	}
	if res.Results[0].Status != "rejected" {
		t.Errorf("a delete of a live post is refused: %+v", res.Results[0])
	}

	// A withdrawn post: the delete's effect already stands.
	rs, err := f.store.Create(ctx, sam, uuid.NewString(), map[string]any{
		"channel_id": ux108Room, "body": "ux108r2: a withdrawn post"})
	if err != nil {
		t.Fatalf("the trail holder posts in the room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1::uuid`, rs.ID)
	})
	if _, err := NewRepository(f.pool).DeleteMessage(ctx, ux108Room, rs.ID); err != nil {
		t.Fatalf("the author withdraws his own post: %v", err)
	}
	var tombSeq int64
	if err := f.pool.QueryRow(ctx, `SELECT seq FROM msg_messages WHERE id = $1::uuid`, rs.ID).Scan(&tombSeq); err != nil {
		t.Fatalf("read the tombstone's seq: %v", err)
	}

	res, err = engine.Push(ctx, info, sam, []apigen.SyncPushOperation{{
		ClientId: uuid.NewString(), Op: "delete", Payload: map[string]any{"id": rs.ID}}})
	if err != nil {
		t.Fatalf("the delete of a withdrawn post failed the push: %v", err)
	}
	if res.Results[0].Status != "applied" || res.Results[0].Seq == nil {
		t.Errorf("a delete of a withdrawn post reports the tombstone's seq: %+v", res.Results[0])
	} else if *res.Results[0].Seq != tombSeq {
		t.Errorf("the answer carries the tombstone's seq: got %d want %d", *res.Results[0].Seq, tombSeq)
	}
}
