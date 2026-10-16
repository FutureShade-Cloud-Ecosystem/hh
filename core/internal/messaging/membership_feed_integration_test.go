//go:build integration

// membership_feed_integration_test.go: the channels feed's side of a
// membership change, against the migrated + seeded database. Red first
// per the review: AddChannelMember wrote the seat without bumping the
// channel's feed seq, so a device holding a cursor past the room's seq
// never learned it had a new room, through the channels scope or any
// other.

package messaging

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// Adding a member bumps the channel's feed sequence: a device whose
// cursor already sits past the room's stamped seq sees the room arrive
// through the channels sync scope, without any other change to the room.
func TestIntegration_AddMember_Bumps_The_Channels_Feed_Seq(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	// The room is setup for the owner pen and the feed legs, so it is
	// founded through the repository seam (the module's provisioning path,
	// ungated by the route's admin pen), keeping sam the room's OWNER.
	sam := samCaller()
	room, _, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("Feed seq probe %d", time.Now().UnixNano()),
	}, uSamRivera)
	if err != nil {
		t.Fatalf("found room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})

	// The device's cursor: everything stamped so far, including the fresh
	// room's own insert seq.
	var before int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&before); err != nil {
		t.Fatalf("read room seq: %v", err)
	}

	// The owner seats Casey; the seat is the ONLY change to the room.
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{ChannelID: room.ID, UserID: uCaseyDoyle}); err != nil {
		t.Fatalf("seat casey: %v", err)
	}

	var after int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&after); err != nil {
		t.Fatalf("reread room seq: %v", err)
	}
	if after <= before {
		t.Fatalf("the seat must bump the channel's feed seq: before=%d after=%d", before, after)
	}

	// The leg through the channels scope: Casey's device, holding the
	// cursor it synced to before the seat, pulls the scope and the room
	// arrives.
	store := NewChannelsSyncStore(repo, func(_ context.Context, _ string) (string, bool) {
		return uCaseyDoyle, true
	})
	items, _, err := store.Pull(ctx, sync.ScopeFilter{Actor: "casey"}, before, 100)
	if err != nil {
		t.Fatalf("channels pull: %v", err)
	}
	found := false
	for _, it := range items {
		if it.ID == room.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the newly joined room must arrive through the channels scope past the old cursor (items=%d)", len(items))
	}
}

// Removing a member refreshes the room's row for every device still in it
// (the existing bump), and the removal contract is documented at the feed:
// a left room is not announced to the device that left it, the feed is
// membership-fenced and carries no leave tombstones; the cached room dies
// on the device's next full refresh. This leg pins the refresh half.
func TestIntegration_RemoveMember_Refreshes_The_Room_Row(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	sam := samCaller()
	room, _, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("Remove feed probe %d", time.Now().UnixNano()),
	}, uSamRivera)
	if err != nil {
		t.Fatalf("found room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{ChannelID: room.ID, UserID: uCaseyDoyle}); err != nil {
		t.Fatalf("seat casey: %v", err)
	}
	var before int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&before); err != nil {
		t.Fatalf("read room seq: %v", err)
	}

	if err := svc.RemoveMember(ctx, sam, room.ID, uCaseyDoyle); err != nil {
		t.Fatalf("remove casey: %v", err)
	}

	var after int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&after); err != nil {
		t.Fatalf("reread room seq: %v", err)
	}
	if after <= before {
		t.Fatalf("the removal must refresh the channel's feed seq: before=%d after=%d", before, after)
	}
	// Sam (still a member) sees the refreshed row; Casey (no longer a
	// member) sees nothing through the scope, the documented contract.
	store := NewChannelsSyncStore(repo, func(_ context.Context, _ string) (string, bool) {
		return uSamRivera, true
	})
	items, _, err := store.Pull(ctx, sync.ScopeFilter{Actor: "sam"}, before, 100)
	if err != nil {
		t.Fatalf("channels pull: %v", err)
	}
	found := false
	for _, it := range items {
		if it.ID == room.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the remaining members must see the refreshed room row")
	}
}

// The bump commits WITH the seat: a seat whose commit is aborted must
// leave the feed seq unmoved, or a device that pulled the feed in the
// window would advance past a seat that never landed and never learn of
// the room (the original miss the bump exists to fix). The leg aborts the
// seat's COMMIT itself, after the insert and the bump, with a deferred
// constraint trigger; only a bump inside the seat's transaction rolls
// back with it. The trigger and its function are the leg's own, dropped
// in cleanup.
func TestIntegration_AddMember_RolledBack_Seat_Leaves_Seq_Unmoved(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	sam := samCaller()
	room, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("Rollback feed probe %d", time.Now().UnixNano()),
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("found room: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})
	var before int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&before); err != nil {
		t.Fatalf("read room seq: %v", err)
	}

	// The deferred trigger fires at COMMIT time, after the seat's insert
	// and after the bump. The blocked user id is embedded as a literal (a
	// function body cannot take bind args).
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE OR REPLACE FUNCTION feed_probe_block_seat() RETURNS trigger AS $fn$
		 BEGIN
		 	IF NEW.user_id = '%s'::uuid THEN RAISE EXCEPTION 'feed probe: seat aborted at commit'; END IF;
		 	RETURN NEW;
		 END $fn$ LANGUAGE plpgsql`, uCaseyDoyle)); err != nil {
		t.Fatalf("create probe function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS feed_probe_block_seat ON msg_members`)
		_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS feed_probe_block_seat()`)
	})
	if _, err := pool.Exec(ctx, `CREATE CONSTRAINT TRIGGER feed_probe_block_seat
		AFTER INSERT ON msg_members
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION feed_probe_block_seat()`); err != nil {
		t.Fatalf("create probe trigger: %v", err)
	}

	if _, err := svc.AddMember(ctx, sam, AddMemberInput{ChannelID: room.ID, UserID: uCaseyDoyle}); err == nil {
		t.Fatalf("the seat's commit must fail under the probe trigger")
	}
	var after int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&after); err != nil {
		t.Fatalf("reread room seq: %v", err)
	}
	if after != before {
		t.Fatalf("a seat rolled back must leave the feed seq unmoved: before=%d after=%d", before, after)
	}
	if ok, _ := repo.IsMember(ctx, room.ID, uCaseyDoyle); ok {
		t.Fatalf("the rolled back seat must not stand")
	}
}

// A removal whose feed bump fails must not leave the member gone: the
// delete and the bump commit together or not at all, so the caller's
// error and the world always agree. Before the fix the delete committed
// on the pool first, so a failing bump answered the caller with an error
// while the member was already gone and the room's feed seq never moved:
// the remaining devices got no refresh until the room changed by
// something else.
func TestIntegration_RemoveMember_Failed_Bump_Leaves_The_Member_Seated(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	sam := samCaller()
	room, created, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("Remove rollback probe %d", time.Now().UnixNano()),
	}, uSamRivera)
	if err != nil || !created {
		t.Fatalf("found room: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})
	if _, err := svc.AddMember(ctx, sam, AddMemberInput{ChannelID: room.ID, UserID: uCaseyDoyle}); err != nil {
		t.Fatalf("seat casey: %v", err)
	}
	var before int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&before); err != nil {
		t.Fatalf("read room seq: %v", err)
	}

	// Block the bump itself: any update of this room's row fails, so the
	// remove's announce step is the failing half. The room id is embedded
	// as a literal (a function body cannot take bind args).
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE OR REPLACE FUNCTION feed_probe_block_bump() RETURNS trigger AS $fn$
		 BEGIN
		 	IF NEW.id = '%s'::uuid THEN RAISE EXCEPTION 'feed probe: bump aborted'; END IF;
		 	RETURN NEW;
		 END $fn$ LANGUAGE plpgsql`, room.ID)); err != nil {
		t.Fatalf("create probe function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS feed_probe_block_bump ON msg_channels`)
		_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS feed_probe_block_bump()`)
	})
	if _, err := pool.Exec(ctx, `CREATE TRIGGER feed_probe_block_bump
		BEFORE UPDATE ON msg_channels
		FOR EACH ROW EXECUTE FUNCTION feed_probe_block_bump()`); err != nil {
		t.Fatalf("create probe trigger: %v", err)
	}

	if err := svc.RemoveMember(ctx, sam, room.ID, uCaseyDoyle); err == nil {
		t.Fatalf("the removal's bump must fail under the probe trigger")
	}
	var after int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, room.ID).Scan(&after); err != nil {
		t.Fatalf("reread room seq: %v", err)
	}
	if after != before {
		t.Fatalf("a removal rolled back must leave the feed seq unmoved: before=%d after=%d", before, after)
	}
	if ok, _ := repo.IsMember(ctx, room.ID, uCaseyDoyle); !ok {
		t.Fatalf("a removal whose bump failed must leave the member seated")
	}
}

// The derived membership sync owes the channels feed the same announce
// the explicit seat and removal owe: a derived seat or prune moves the
// room's feed seq, in the sync's own transaction, so a device holding a
// cursor past the room's stamped seq sees the change arrive instead of
// missing the room class until some other write stirs the room.
func TestIntegration_SyncMembership_Bumps_The_Feed_Seq_Of_Derived_Changes(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	// Settle first: the sync is idempotent, so after it Sam's derived
	// rows exist and a further sync changes nothing.
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("settle sam's sync: %v", err)
	}

	// The seat arm: clear Sam's derived rows (the sync's self healing
	// re-derives exactly them), and every room that comes back must
	// carry a higher seq than before.
	roomRows, err := pool.Query(ctx,
		`SELECT channel_id::text FROM msg_members WHERE user_id = $1 AND source = 'DERIVED'`, uSamRivera)
	if err != nil {
		t.Fatalf("read sam's derived rooms: %v", err)
	}
	var derived []string
	for roomRows.Next() {
		var ch string
		if err := roomRows.Scan(&ch); err != nil {
			t.Fatalf("scan derived room: %v", err)
		}
		derived = append(derived, ch)
	}
	roomRows.Close()
	if len(derived) == 0 {
		t.Fatalf("setup: sam must hold derived rows after the settle sync")
	}
	before := map[string]int64{}
	for _, ch := range derived {
		var seq int64
		if err := pool.QueryRow(ctx,
			`SELECT seq FROM msg_channels WHERE id = $1`, ch).Scan(&seq); err != nil {
			t.Fatalf("read room seq: %v", err)
		}
		before[ch] = seq
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM msg_members WHERE user_id = $1 AND source = 'DERIVED'`, uSamRivera); err != nil {
		t.Fatalf("clear sam's derived rows: %v", err)
	}
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("resync sam: %v", err)
	}
	for _, ch := range derived {
		var after int64
		if err := pool.QueryRow(ctx,
			`SELECT seq FROM msg_channels WHERE id = $1`, ch).Scan(&after); err != nil {
			t.Fatalf("reread room seq: %v", err)
		}
		if after <= before[ch] {
			t.Fatalf("a re-derived seat must bump the room's feed seq: room=%s before=%d after=%d", ch, before[ch], after)
		}
	}

	// The prune arm: a DERIVED row Sam does not qualify for (the Kingston
	// yard) is pruned by the sync, and the room's seq moves with it.
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'MEMBER', 'DERIVED')`, chYardKingston, uSamRivera); err != nil {
		t.Fatalf("plant sam's unqualified derived row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM msg_members WHERE channel_id = $1 AND user_id = $2 AND source = 'DERIVED'`,
			chYardKingston, uSamRivera)
	})
	var yardBefore int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, chYardKingston).Scan(&yardBefore); err != nil {
		t.Fatalf("read yard seq: %v", err)
	}
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam over the planted row: %v", err)
	}
	if ok, _ := repo.IsMember(ctx, chYardKingston, uSamRivera); ok {
		t.Fatalf("the unqualified derived row must be pruned")
	}
	var yardAfter int64
	if err := pool.QueryRow(ctx,
		`SELECT seq FROM msg_channels WHERE id = $1`, chYardKingston).Scan(&yardAfter); err != nil {
		t.Fatalf("reread yard seq: %v", err)
	}
	if yardAfter <= yardBefore {
		t.Fatalf("a pruned derived row must bump the room's feed seq: before=%d after=%d", yardBefore, yardAfter)
	}
}
