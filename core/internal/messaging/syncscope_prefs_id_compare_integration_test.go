//go:build integration

// syncscope_prefs_id_compare_integration_test.go: UX-124 (messaging part).
//
// A prefs update compares its payload channel_id with the row's own as ids,
// not as strings, and checks the id's shape before it compares. Two edges the
// reads of the UX-123 fix found:
//
//   - an update that restates its own row's channel in capitals was refused as
//     a move; it names the same channel, so it applies, written with the row's
//     own canonical id;
//   - a malformed channel_id on update read "cannot be moved" rather than
//     "channel_id is not a valid id", the words create answers.
package messaging

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func TestSyncPush_PrefsUpdateRestatingOwnChannelInCapitalsApplies(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`,
			uSamRivera, chYardTrenton)
	})

	created := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	if created.Status != "applied" || created.Seq == nil {
		t.Fatalf("setup create answered %s, want applied", ux115Answer(created))
	}
	rowID, _ := appliedRow(t, f.engine, prefs, samFilter(), *created.Seq)

	payload := map[string]any{"id": rowID, "channel_id": strings.ToUpper(chYardTrenton), "mode": ModeMuted}
	updated := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "update", payload)
	t.Logf("ux124 update restating own channel in capitals: %s", ux115Answer(updated))
	if updated.Status != "applied" || updated.Seq == nil {
		t.Fatalf("update restating own channel in capitals answered %s, want applied", ux115Answer(updated))
	}

	var mode, channel string
	var rows int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(mode), ''), COALESCE(max(channel_id::text), '')
		 FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`,
		uSamRivera, chYardTrenton).Scan(&rows, &mode, &channel); err != nil {
		t.Fatalf("read the row after the update: %v", err)
	}
	if rows != 1 || mode != ModeMuted || channel != chYardTrenton {
		t.Errorf("after the update rows=%d mode=%q channel=%q, want 1, %q, %q",
			rows, mode, channel, ModeMuted, chYardTrenton)
	}
}

func TestSyncPush_PrefsUpdateWithMalformedChannelIDReadsAsMalformed(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`,
			uSamRivera, chYardTrenton)
	})

	created := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	if created.Status != "applied" || created.Seq == nil {
		t.Fatalf("setup create answered %s, want applied", ux115Answer(created))
	}
	rowID, _ := appliedRow(t, f.engine, prefs, samFilter(), *created.Seq)

	want := "sync: operation refused: channel_id is not a valid id"
	for _, bad := range []string{"not-a-uuid", chYardTrenton[:35], "{" + chYardTrenton + "}"} {
		got := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "update",
			map[string]any{"id": rowID, "channel_id": bad, "mode": ModeMuted})
		t.Logf("ux124 update with channel_id %q: %s", bad, ux115Answer(got))
		if got.Status != "rejected" || ux115ErrWords(got) != want {
			t.Errorf("update with channel_id %q answered %s, want rejected with %q", bad, ux115Answer(got), want)
		}
	}
}
