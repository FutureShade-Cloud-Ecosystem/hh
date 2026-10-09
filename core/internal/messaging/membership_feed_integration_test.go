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

	sam := samCaller()
	room, _, err := svc.CreateChannel(ctx, sam, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("Feed seq probe %d", time.Now().UnixNano()),
	})
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
	if _, err := svc.AddMember(ctx, sam, room.ID, uCaseyDoyle); err != nil {
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
	room, _, err := svc.CreateChannel(ctx, sam, CreateChannelInput{
		Kind: KindTeam, Name: fmt.Sprintf("Remove feed probe %d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("found room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, room.ID)
	})
	if _, err := svc.AddMember(ctx, sam, room.ID, uCaseyDoyle); err != nil {
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
