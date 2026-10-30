//go:build integration

// syncscope_missing_channel_integration_test.go: UX-115. A prefs write naming
// a channel that does not exist refuses that one operation, not the whole push.
//
// THE DEFECT: notifyPrefsSyncStore.write handed the repository's raw answers
// straight back. The tier read on a channel that does not exist answers
// errNotFound (repository.go, ChannelTier) and the upsert answers a foreign
// key violation; neither is one of the Store seam's declared answers
// (core/internal/sync/store.go), so the engine read both as a fault of the
// whole push (core/internal/sync/engine.go, the classified helper) and every
// good operation in the same batch failed with them.
//
// THE LEGS: they run on the real database through the real engine and the
// real key ledger. One batch carries one good operation and one operation
// naming a channel that does not exist, once silencing (muted: true) and once
// not (mode: ALL): the good operation must land, and the bad one alone must be
// refused with the seam's plain words.
//
// THE FENCE FINDING: the last leg pins what the prefs write is fenced by
// today, because the existence oracle question turns on it. The sync prefs
// write is not membership fenced: it writes the caller's own pref row for any
// channel that exists, whether or not the caller is seated in it. UX-115
// reports that gap and does not close it.
package messaging

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux115Filters are the two callers' scope filters. The org is a uuid because
// the engine's ledger holds the key under it (the messaging tables carry no
// org column, so the scope itself reads nothing from it).
func ux115Filters() (sync.ScopeFilter, sync.ScopeFilter) {
	sam := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "sub-sam"}
	casey := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "sub-casey"}
	return sam, casey
}

// ux115Answer renders one answer the way a device sees it: the status word,
// the seq and the error words.
func ux115Answer(r apigen.SyncPushResult) string {
	seq := "null"
	if r.Seq != nil {
		seq = fmt.Sprintf("%d", *r.Seq)
	}
	words := ""
	if r.Error != nil {
		words = *r.Error
	}
	return fmt.Sprintf(`{"status":%q,"seq":%s,"error":%q}`, r.Status, seq, words)
}

// ux115ErrWords is one answer's error words, the empty string when the answer
// carries none.
func ux115ErrWords(r apigen.SyncPushResult) string {
	if r.Error == nil {
		return ""
	}
	return *r.Error
}

// ux115MissingChannel is a channel id that names no row, checked against the
// table so the leg's own fixture can never be the wrong one.
func ux115MissingChannel(t *testing.T, f *syncFeedFixture) string {
	t.Helper()
	id := uuid.NewString()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM msg_channels WHERE id = $1::uuid`, id).Scan(&n); err != nil {
		t.Fatalf("read the channel table: %v", err)
	}
	if n != 0 {
		t.Fatalf("the fixture is wrong: the missing channel id names a row")
	}
	return id
}

// ux115PushPrefsBatch pushes one batch of two prefs operations and answers the
// way a device sees it: the two results, or the fault that sank the batch.
//
// The operation naming the missing channel comes FIRST, so a batch that dies
// on it takes the good operation down with it: that is the defect's own
// shape, and it is what the good operation landing proves afterwards.
func ux115PushPrefsBatch(t *testing.T, f *syncFeedFixture, filter sync.ScopeFilter, patch map[string]any) ([]apigen.SyncPushResult, error) {
	t.Helper()
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	bad := map[string]any{"channel_id": ux115MissingChannel(t, f)}
	for k, v := range patch {
		bad[k] = v
	}
	ops := []apigen.SyncPushOperation{
		{ClientId: uuid.NewString(), Op: "create", Payload: bad},
		{ClientId: uuid.NewString(), Op: "create", Payload: map[string]any{"channel_id": chOffice, "mode": "MENTIONS"}},
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uSamRivera, chOffice)
	})
	resp, err := f.engine.Push(context.Background(), prefs, filter, ops)
	if err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// ux115CheckBatch holds the batch's answers: the push must not fail as a
// whole, the operation naming the missing channel alone must be refused with
// the seam's plain words, and the good operation must land.
func ux115CheckBatch(t *testing.T, f *syncFeedFixture, results []apigen.SyncPushResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the push failed as a whole: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("the push answered %d results, want 2", len(results))
	}
	bad := results[0]
	if bad.Status != "rejected" {
		t.Errorf("the missing channel's operation answered %s, want rejected", ux115Answer(bad))
	}
	if got := ux115ErrWords(bad); got != sync.ErrRowNotFound.Error() {
		t.Errorf("the missing channel's refusal answered %q, want the seam's plain words %q",
			got, sync.ErrRowNotFound.Error())
	}
	good := results[1]
	if good.Status != "applied" || good.Seq == nil {
		t.Errorf("the good operation answered %s, want applied", ux115Answer(good))
	}
	// The good operation landed: the caller's own pref row for the real
	// channel carries the mode it asked for.
	var mode string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT mode FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`,
		uSamRivera, chOffice).Scan(&mode); err != nil {
		t.Errorf("the good operation did not land: %v", err)
		return
	}
	if mode != ModeMentions {
		t.Errorf("the good operation landed the mode %q, want %q", mode, ModeMentions)
	}
}

// TestSyncPush_PrefsWriteOnAMissingChannelRefusesThatOneOperation: the
// silencing half. A prefs write that would mute a channel that does not
// exist (muted: true) reads the channel's tier first, and the tier read
// answered errNotFound, which the engine read as a fault of the whole push.
//
// Before the fix the batch died on the first operation and the good operation
// never landed. After it the good operation lands and the missing channel's
// operation alone is refused, with the seam's plain words.
func TestSyncPush_PrefsWriteOnAMissingChannelRefusesThatOneOperation(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	results, err := ux115PushPrefsBatch(t, f, samFilter(), map[string]any{"muted": true})
	t.Logf("ux115 silencing patch: %v", results)
	ux115CheckBatch(t, f, results, err)
}

// TestSyncPush_PrefsWriteOnAMissingChannelRefusesThatOneOperationWithoutSilencing:
// the other half. A prefs write that does NOT silence (mode: ALL) never reads
// the tier; it read the current row, which answers the flat defaults for a
// channel that does not exist, and then the upsert tripped the channel's
// foreign key. The foreign key violation is not one of the seam's declared
// answers either, so this path sank the whole push in its own way.
//
// After the fix both paths answer the same thing: this operation alone is
// refused, with the seam's plain words.
func TestSyncPush_PrefsWriteOnAMissingChannelRefusesThatOneOperationWithoutSilencing(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	results, err := ux115PushPrefsBatch(t, f, samFilter(), map[string]any{"mode": ModeAll})
	t.Logf("ux115 non-silencing patch: %v", results)
	ux115CheckBatch(t, f, results, err)
}

// TestSyncPush_PrefsWriteOnAMissingChannelAnswersTheSeamPlainWords: the
// refusal's words.
//
// The engine refuses a key bound to a row the caller cannot read with the
// seam's plain not-found (core/internal/sync/engine.go, the bound key gate),
// and the store's own refusal for a row the caller cannot read carries the
// same word (UX-108 round 3: a module suffix on a row's refusal tells the
// caller a spent key from a free one). A missing channel must answer that
// same word, or the words tell the caller its key is bound to a row it
// cannot see.
//
// The leg proves it over the real key ledger: Sam applies a key (the ledger
// binds it to him, migration 0449), then Casey pushes that bound key with a
// body naming a channel that does not exist, and then pushes a key nobody
// ever applied with the same body. The two answers must be the same word for
// word.
func TestSyncPush_PrefsWriteOnAMissingChannelAnswersTheSeamPlainWords(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("notify_prefs", NewNotifyPrefsSyncStore(NewRepository(f.pool), link)); err != nil {
		t.Fatalf("register the notify_prefs scope: %v", err)
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	sam, casey := ux115Filters()

	// Sam applies a key: the ledger binds it to him.
	key := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uSamRivera, chOffice)
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})
	applied, err := engine.Push(ctx, prefs, sam, []apigen.SyncPushOperation{{
		ClientId: key, Op: "create", Payload: map[string]any{"channel_id": chOffice, "mode": ModeMentions}}})
	if err != nil {
		t.Fatalf("sam's prefs write: %v", err)
	}
	if len(applied.Results) != 1 || applied.Results[0].Status != "applied" {
		t.Fatalf("sam's prefs write answered %v", applied.Results)
	}
	var boundBy *string
	if err := f.pool.QueryRow(ctx,
		`SELECT bound_by FROM sync_op_hashes WHERE client_id = $1::uuid`, key).Scan(&boundBy); err != nil {
		t.Fatalf("read the ledger row: %v", err)
	}
	if boundBy == nil || *boundBy != sam.Actor {
		t.Fatalf("the key is bound to the caller who applied it: %v", boundBy)
	}

	// Casey pushes that bound key with a body naming a channel that does not
	// exist: the gate refuses it, because the row the key is bound to is not
	// his to read.
	missing := ux115MissingChannel(t, f)
	body := map[string]any{"channel_id": missing, "mode": ModeAll}
	bound, err := engine.Push(ctx, prefs, casey, []apigen.SyncPushOperation{{
		ClientId: key, Op: "create", Payload: body}})
	if err != nil {
		t.Fatalf("the bound key's push: %v", err)
	}
	// And a key nobody ever applied, with the same body.
	free, err := engine.Push(ctx, prefs, casey, []apigen.SyncPushOperation{{
		ClientId: uuid.NewString(), Op: "create", Payload: body}})
	if err != nil {
		t.Fatalf("the free key's push: %v", err)
	}
	if len(bound.Results) != 1 || len(free.Results) != 1 {
		t.Fatalf("the two pushes answered %d and %d results, want 1 each", len(bound.Results), len(free.Results))
	}
	got, want := bound.Results[0], free.Results[0]
	t.Logf("ux115 the bound key %s, the never-bound key %s", ux115Answer(got), ux115Answer(want))
	if got.Status != want.Status {
		t.Errorf("the bound key and a never-bound key differ in status: %s and %s", ux115Answer(got), ux115Answer(want))
	}
	if ux115ErrWords(got) != ux115ErrWords(want) {
		t.Errorf("the bound key and a never-bound key differ in words: %s and %s", ux115Answer(got), ux115Answer(want))
	}
	if ux115ErrWords(want) != sync.ErrRowNotFound.Error() {
		t.Errorf("the missing channel's refusal answered %q, want the seam's plain words %q",
			ux115ErrWords(want), sync.ErrRowNotFound.Error())
	}
}

// TestSyncPush_PrefsWriteIsNotMembershipFenced: the fence finding the
// existence oracle question turns on.
//
// The REST door requires membership for a prefs write (the service's
// SetNotifyPrefs asserts the membership first). This door does not: the sync
// prefs write reads the channel by id and writes the caller's own pref row
// for it, whether or not the caller is seated in that channel. So the module
// has no "you may not see that channel" answer here at all, and the answer
// for a real channel the caller cannot see is the applied one, not the
// not-found one.
//
// The leg pins today's fence state so the gap is visible in the suite and a
// future fence lands as a deliberate change. UX-115 reports the gap and does
// not close it: the brief forbids widening the fence in this item.
func TestSyncPush_PrefsWriteIsNotMembershipFenced(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	// Casey is seated in Kingston's yard, never in Trenton's.
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync casey: %v", err)
	}
	if ok, _ := f.repo.IsMember(ctx, chYardTrenton, uCaseyDoyle); ok {
		t.Fatalf("the fixture is wrong: casey is seated in trenton's yard")
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uCaseyDoyle, chYardTrenton)
	})
	res := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	t.Logf("ux115 a write for a channel the caller is not seated in: %s", ux115Answer(res))
	if res.Status != "applied" {
		t.Errorf("the write for a channel the caller is not seated in answered %s, want applied: the prefs write is not membership fenced",
			ux115Answer(res))
	}
}
