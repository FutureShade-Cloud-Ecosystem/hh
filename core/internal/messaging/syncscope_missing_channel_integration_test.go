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
// THE MEMBERSHIP FENCE (UX-122): a prefs write on a real channel the caller is
// not a member of refuses with the same bare sync.ErrRowNotFound a missing
// channel answers, on both create and update, silencing and not, while a
// seated member's write still lands.
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

// TestSyncPush_PrefsWriteIsNotMembershipFenced: flipped into a refusal leg in
// UX-122.
//
// A staff caller who is not a member of a real channel pushes a prefs write
// there, both silencing (muted: true) and not (mode: MENTIONS), on both create
// and update. Every path must be rejected with sync: row not found, the same
// words a missing channel answers, and nothing may land on the non-member's
// row.
func TestSyncPush_PrefsWriteIsNotMembershipFenced(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync kingston caller: %v", err)
	}
	if ok, _ := f.repo.IsMember(ctx, chYardTrenton, uCaseyDoyle); ok {
		t.Fatalf("the fixture is wrong: kingston caller is seated in trenton's yard")
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uCaseyDoyle, chYardTrenton)
	})

	// Create, silencing (muted: true).
	silencing := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "muted": true})
	t.Logf("ux122 non-member create silencing: %s", ux115Answer(silencing))
	if silencing.Status != "rejected" || silencing.Seq != nil || silencing.Conflict != nil {
		t.Errorf("non-member silencing create answered %s, want rejected", ux115Answer(silencing))
	}
	if got := ux115ErrWords(silencing); got != sync.ErrRowNotFound.Error() {
		t.Errorf("non-member silencing create error = %q, want %q", got, sync.ErrRowNotFound.Error())
	}

	// Create, non-silencing (mode: MENTIONS).
	nonSilencing := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	t.Logf("ux122 non-member create non-silencing: %s", ux115Answer(nonSilencing))
	if nonSilencing.Status != "rejected" || nonSilencing.Seq != nil || nonSilencing.Conflict != nil {
		t.Errorf("non-member non-silencing create answered %s, want rejected", ux115Answer(nonSilencing))
	}
	if got := ux115ErrWords(nonSilencing); got != sync.ErrRowNotFound.Error() {
		t.Errorf("non-member non-silencing create error = %q, want %q", got, sync.ErrRowNotFound.Error())
	}

	var count int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`,
		uCaseyDoyle, chYardTrenton).Scan(&count); err != nil {
		t.Fatalf("count non-member prefs rows: %v", err)
	}
	if count != 0 {
		t.Errorf("non-member create wrote %d rows in msg_notification_prefs, want 0", count)
	}

	// Update (Write path): even if a stale prefs row exists for the caller on a
	// channel they are not a member of, both silencing and non-silencing updates
	// must be refused with sync: row not found.
	_, _ = f.pool.Exec(ctx,
		`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uCaseyDoyle, chYardTrenton)
	seededID, _, err := f.repo.syncPrefsWrite(ctx, DefaultNotificationPrefs(chYardTrenton, uCaseyDoyle), uuid.NewString())
	if err != nil {
		t.Fatalf("seed stale prefs row for update check: %v", err)
	}
	updSilencing := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "update",
		map[string]any{"id": seededID, "muted": true})
	t.Logf("ux122 non-member update silencing: %s", ux115Answer(updSilencing))
	if updSilencing.Status != "rejected" || updSilencing.Seq != nil || updSilencing.Conflict != nil {
		t.Errorf("non-member silencing update answered %s, want rejected", ux115Answer(updSilencing))
	}
	if got := ux115ErrWords(updSilencing); got != sync.ErrRowNotFound.Error() {
		t.Errorf("non-member silencing update error = %q, want %q", got, sync.ErrRowNotFound.Error())
	}

	updNonSilencing := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "update",
		map[string]any{"id": seededID, "mode": ModeMentions})
	t.Logf("ux122 non-member update non-silencing: %s", ux115Answer(updNonSilencing))
	if updNonSilencing.Status != "rejected" || updNonSilencing.Seq != nil || updNonSilencing.Conflict != nil {
		t.Errorf("non-member non-silencing update answered %s, want rejected", ux115Answer(updNonSilencing))
	}
	if got := ux115ErrWords(updNonSilencing); got != sync.ErrRowNotFound.Error() {
		t.Errorf("non-member non-silencing update error = %q, want %q", got, sync.ErrRowNotFound.Error())
	}
}

// TestSyncPush_PrefsWriteNonMemberAndMissingChannelParity: a non-member's
// write on a real channel and anyone's write on a missing channel answer
// byte-identical results (status, seq and error words), on both silencing and
// non-silencing patches and on both create and update.
func TestSyncPush_PrefsWriteNonMemberAndMissingChannelParity(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
	}
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync kingston caller: %v", err)
	}
	if ok, _ := f.repo.IsMember(ctx, chYardTrenton, uCaseyDoyle); ok {
		t.Fatalf("the fixture is wrong: kingston caller is seated in trenton's yard")
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uCaseyDoyle, chYardTrenton)
	})

	missing := ux115MissingChannel(t, f)

	// Non-silencing create: non-member on real channel vs missing channel (both callers).
	nonMemberMode := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeAll})
	missingCaseyMode := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": missing, "mode": ModeAll})
	missingSamMode := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": missing, "mode": ModeAll})

	// Silencing create: non-member on real channel vs missing channel (both callers).
	nonMemberMute := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "muted": true})
	missingCaseyMute := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": missing, "muted": true})
	missingSamMute := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": missing, "muted": true})

	// Update (Write): non-member on real channel vs missing pref row.
	_, _ = f.pool.Exec(ctx,
		`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uCaseyDoyle, chYardTrenton)
	seededID, _, err := f.repo.syncPrefsWrite(ctx, DefaultNotificationPrefs(chYardTrenton, uCaseyDoyle), uuid.NewString())
	if err != nil {
		t.Fatalf("seed stale prefs row for parity check: %v", err)
	}
	nonMemberUpdate := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "update",
		map[string]any{"id": seededID, "mode": ModeMentions})
	missingUpdate := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "update",
		map[string]any{"id": uuid.NewString(), "channel_id": missing, "mode": ModeMentions})

	wantBytes := ux115Answer(missingSamMode)
	t.Logf("ux122 parity reference (missing channel): %s", wantBytes)
	if wantBytes != `{"status":"rejected","seq":null,"error":"sync: row not found"}` {
		t.Fatalf("unexpected reference answer: %s", wantBytes)
	}

	cases := []struct {
		label string
		res   apigen.SyncPushResult
	}{
		{"non-member real channel create (mode: ALL)", nonMemberMode},
		{"kingston caller missing channel create (mode: ALL)", missingCaseyMode},
		{"trenton caller missing channel create (mode: ALL)", missingSamMode},
		{"non-member real channel create (muted: true)", nonMemberMute},
		{"kingston caller missing channel create (muted: true)", missingCaseyMute},
		{"trenton caller missing channel create (muted: true)", missingSamMute},
		{"non-member real channel update", nonMemberUpdate},
		{"missing channel update", missingUpdate},
	}
	for _, tc := range cases {
		gotBytes := ux115Answer(tc.res)
		t.Logf("ux122 parity [%s]: %s", tc.label, gotBytes)
		if gotBytes != wantBytes {
			t.Errorf("%s answered %s, want byte-identical %s", tc.label, gotBytes, wantBytes)
		}
		if tc.res.Conflict != nil {
			t.Errorf("%s carried a conflict envelope: %+v", tc.label, tc.res.Conflict)
		}
	}
}

// TestSyncPush_PrefsWriteByMemberApplies: a seated member's prefs write on a
// real channel still applies on both create (silencing and non-silencing) and
// update.
func TestSyncPush_PrefsWriteByMemberApplies(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
	}
	if ok, _ := f.repo.IsMember(ctx, chYardTrenton, uSamRivera); !ok {
		t.Fatalf("the fixture is wrong: trenton caller is not seated in trenton's yard")
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uSamRivera, chYardTrenton)
	})

	// Member create (non-silencing).
	created := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	t.Logf("ux122 member create (mode: MENTIONS): %s", ux115Answer(created))
	if created.Status != "applied" || created.Seq == nil {
		t.Fatalf("member create answered %s, want applied", ux115Answer(created))
	}
	rowID, _ := appliedRow(t, f.engine, prefs, samFilter(), *created.Seq)

	// Member create (silencing).
	muted := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardTrenton, "muted": true})
	t.Logf("ux122 member create (muted: true): %s", ux115Answer(muted))
	if muted.Status != "applied" || muted.Seq == nil {
		t.Fatalf("member silencing create answered %s, want applied", ux115Answer(muted))
	}

	// Member update (Write path).
	updated := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "update",
		map[string]any{"id": rowID, "mode": ModeAll, "desktop_enabled": false})
	t.Logf("ux122 member update (mode: ALL, desktop_enabled: false): %s", ux115Answer(updated))
	if updated.Status != "applied" || updated.Seq == nil {
		t.Fatalf("member update answered %s, want applied", ux115Answer(updated))
	}
	var gotMode string
	var gotDesktop bool
	if err := f.pool.QueryRow(ctx,
		`SELECT mode, desktop_enabled FROM msg_notification_prefs WHERE id = $1::uuid`, rowID).
		Scan(&gotMode, &gotDesktop); err != nil {
		t.Fatalf("read member pref row: %v", err)
	}
	if gotMode != ModeAll || gotDesktop {
		t.Errorf("member update stored mode=%q desktop_enabled=%v, want mode=%q desktop_enabled=false",
			gotMode, gotDesktop, ModeAll)
	}
}

// TestSyncPrefsReads_ServeOnlyCallersOwnRows: Pull, FindApplied and Load serve
// only the caller's own rows; a non-member cannot read another user's prefs
// row through any of the three read seams.
func TestSyncPrefsReads_ServeOnlyCallersOwnRows(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
	}
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync kingston caller: %v", err)
	}
	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}
	link := func(_ context.Context, sub string) (string, bool) {
		uid, ok := syncSubOf[sub]
		return uid, ok
	}
	store := NewNotifyPrefsSyncStore(f.repo, link)

	key := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uSamRivera, chYardTrenton)
	})
	res := push(t, f.engine, prefs, samFilter(), key, "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	if res.Status != "applied" || res.Seq == nil {
		t.Fatalf("member prefs create answered %s, want applied", ux115Answer(res))
	}
	prefID, _ := appliedRow(t, f.engine, prefs, samFilter(), *res.Seq)

	// The owner reads the row through Pull, FindApplied and Load.
	if ids := pullIDs(t, f.engine, "notify_prefs", samFilter()); !ids[prefID] {
		t.Fatalf("owner Pull missed own pref row %s: %v", prefID, ids)
	}
	if rs, found, err := store.FindApplied(ctx, samFilter(), key); err != nil || !found || rs.ID != prefID {
		t.Fatalf("owner FindApplied = (%+v, %v, %v), want found %s", rs, found, err, prefID)
	}
	if rs, found, err := store.Load(ctx, samFilter(), prefID); err != nil || !found || rs.ID != prefID {
		t.Fatalf("owner Load = (%+v, %v, %v), want found %s", rs, found, err, prefID)
	}

	// Another caller (not a member of chYardTrenton) sees nothing through
	// Pull, FindApplied or Load.
	if ids := pullIDs(t, f.engine, "notify_prefs", caseyFilter()); len(ids) != 0 {
		t.Errorf("non-member Pull carried another user's prefs rows: %v", ids)
	}
	if rs, found, err := store.FindApplied(ctx, caseyFilter(), key); err != nil || found || rs.ID != "" || rs.Seq != 0 {
		t.Errorf("non-member FindApplied = (%+v, %v, %v), want not found", rs, found, err)
	}
	if rs, found, err := store.Load(ctx, caseyFilter(), prefID); err != nil || found || rs.ID != "" || rs.Seq != 0 || rs.Data != nil {
		t.Errorf("non-member Load = (%+v, %v, %v), want not found", rs, found, err)
	}
}

// TestSyncPush_PrefsUpdateCannotMoveRowToAnotherChannel: UX-123 edge (a).
//
// A caller seated in two channels (chYardTrenton and chOffice) creates a pref
// row on channel A (chYardTrenton) and then sends an update on that row's id
// whose payload names channel B (chOffice). Before the fix, the write landed on
// the caller's row for channel B while the key ledger recorded the client key
// against row A's id. After the fix, the update is refused as a single
// operation with "sync: operation refused: a preference belongs to its channel;
// it cannot be moved", neither row changes, and the refusal is identical
// whether the other channel is one the caller is seated in, a real channel the
// caller is not seated in, or a missing channel id.
func TestSyncPush_PrefsUpdateCannotMoveRowToAnotherChannel(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
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
	caller, _ := ux115Filters()

	createKey := uuid.NewString()
	moveKey := uuid.NewString()
	unseatedKey := uuid.NewString()
	missingKey := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id IN ($2, $3)`,
			uSamRivera, chYardTrenton, chOffice)
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
			[]string{createKey, moveKey, unseatedKey, missingKey})
	})

	created := push(t, engine, prefs, caller, createKey, "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	if created.Status != "applied" || created.Seq == nil {
		t.Fatalf("create on channel A answered %s, want applied", ux115Answer(created))
	}
	rowAID, _ := appliedRow(t, engine, prefs, caller, *created.Seq)

	// Update row A while naming channel B (chOffice, where the caller is also seated).
	moved := push(t, engine, prefs, caller, moveKey, "update",
		map[string]any{"id": rowAID, "channel_id": chOffice, "mode": ModeMuted})
	t.Logf("ux123a update row A naming channel B: %s", ux115Answer(moved))

	var officeCount int
	var officeRowID, officeKey string
	_ = f.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(id::text), ''), COALESCE(max(idempotency_key::text), '')
		 FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`,
		uSamRivera, chOffice).Scan(&officeCount, &officeRowID, &officeKey)
	var ledgerEntityID string
	_ = f.pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id, '') FROM sync_op_hashes WHERE client_id = $1::uuid`,
		moveKey).Scan(&ledgerEntityID)
	t.Logf("ux123a after update: rowA=%s officeCount=%d officeRowID=%s officeKey=%s ledgerEntityID=%s",
		rowAID, officeCount, officeRowID, officeKey, ledgerEntityID)

	wantRefusal := "sync: operation refused: a preference belongs to its channel; it cannot be moved"
	if moved.Status != "rejected" || moved.Seq != nil || moved.Conflict != nil {
		t.Errorf("update naming another channel answered %s, want rejected", ux115Answer(moved))
	}
	if got := ux115ErrWords(moved); got != wantRefusal {
		t.Errorf("update naming another channel error = %q, want %q", got, wantRefusal)
	}
	if officeCount != 0 {
		t.Errorf("update on row A (%s) wrote %d row(s) on channel B (row=%s idem_key=%s), want 0",
			rowAID, officeCount, officeRowID, officeKey)
	}
	if ledgerEntityID != "" {
		t.Errorf("rejected update bound key %s in sync_op_hashes to entity_id=%q, want unbound",
			moveKey, ledgerEntityID)
	}

	var modeA string
	if err := f.pool.QueryRow(ctx,
		`SELECT mode FROM msg_notification_prefs WHERE id = $1::uuid`, rowAID).Scan(&modeA); err != nil {
		t.Fatalf("read row A after refused update: %v", err)
	}
	if modeA != ModeMentions {
		t.Errorf("row A mode = %q after refused update, want %q", modeA, ModeMentions)
	}

	// Parity check: naming a real channel the caller is not in (chYardKingston)
	// and naming a missing channel id produce the exact same refusal as naming
	// a channel the caller is in, so the refusal leaks nothing about whether
	// the other channel exists.
	unseated := push(t, engine, prefs, caller, unseatedKey, "update",
		map[string]any{"id": rowAID, "channel_id": chYardKingston, "mode": ModeMuted})
	missing := push(t, engine, prefs, caller, missingKey, "update",
		map[string]any{"id": rowAID, "channel_id": ux115MissingChannel(t, f), "mode": ModeMuted})
	t.Logf("ux123a parity: seated=%s unseated=%s missing=%s",
		ux115Answer(moved), ux115Answer(unseated), ux115Answer(missing))
	if ux115Answer(unseated) != ux115Answer(moved) {
		t.Errorf("update naming unseated real channel answered %s, want %s",
			ux115Answer(unseated), ux115Answer(moved))
	}
	if ux115Answer(missing) != ux115Answer(moved) {
		t.Errorf("update naming missing channel answered %s, want %s",
			ux115Answer(missing), ux115Answer(moved))
	}
}

// TestSyncPush_PrefsUpdateLeavesCallerPayloadMapUntouched: UX-123 edge (b).
//
// When a client sends an update without channel_id (or with an empty
// channel_id), notifyPrefsSyncStore.Write fills the row's channel_id before
// delegating to write. Before the fix, it mutated the caller's payload map in
// place. After the fix, Write copies the map first so the caller's payload map
// is unchanged after the push.
func TestSyncPush_PrefsUpdateLeavesCallerPayloadMapUntouched(t *testing.T) {
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

	payload := map[string]any{"id": rowID, "mode": ModeAll}
	updated := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "update", payload)
	if updated.Status != "applied" || updated.Seq == nil {
		t.Fatalf("update without channel_id answered %s, want applied", ux115Answer(updated))
	}
	t.Logf("ux123b payload after update (omitted channel_id): %v", payload)
	if got, has := payload["channel_id"]; has {
		t.Errorf("update mutated caller payload map in place: payload[\"channel_id\"] = %v, want key absent", got)
	}

	payloadEmpty := map[string]any{"id": rowID, "channel_id": "", "mode": ModeMentions}
	updatedEmpty := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "update", payloadEmpty)
	if updatedEmpty.Status != "applied" || updatedEmpty.Seq == nil {
		t.Fatalf("update with empty channel_id answered %s, want applied", ux115Answer(updatedEmpty))
	}
	t.Logf("ux123b payload after update (empty channel_id): %v", payloadEmpty)
	if got, _ := payloadEmpty["channel_id"].(string); got != "" {
		t.Errorf("update mutated caller payload map in place: payloadEmpty[\"channel_id\"] = %q, want \"\"", got)
	}
}

// TestSyncPush_PrefsWriteWithMalformedChannelIDRefusesThatOneOperation:
// UX-123 edge (c).
//
// A channel_id that is not a uuid previously reached Repository.IsMember and
// failed the whole push with a raw Postgres 22P02 error, sinking valid
// operations in the same batch and answering differently under a spent key vs a
// free key at the bound-key gate. After the fix, prefsPatchShape refuses a
// non-uuid channel_id as a single operation with "sync: operation refused:
// channel_id is not a valid id", the good operation in the batch lands, and
// ValidateCreate answers the exact same words under a spent key and a free key.
func TestSyncPush_PrefsWriteWithMalformedChannelIDRefusesThatOneOperation(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
	}
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync kingston caller: %v", err)
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
	callerA, callerB := ux115Filters()

	badKey := uuid.NewString()
	goodKey := uuid.NewString()
	boundKey := uuid.NewString()
	freeKey := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id IN ($2, $3)`,
			uSamRivera, chOffice, chYardTrenton)
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
			[]string{badKey, goodKey, boundKey, freeKey})
	})

	ops := []apigen.SyncPushOperation{
		{ClientId: badKey, Op: "create", Payload: map[string]any{"channel_id": "not-a-uuid", "mode": ModeAll}},
		{ClientId: goodKey, Op: "create", Payload: map[string]any{"channel_id": chOffice, "mode": ModeMentions}},
	}
	resp, err := engine.Push(ctx, prefs, callerA, ops)
	t.Logf("ux123c batch push: err=%v results=%v", err, resp.Results)
	if err != nil {
		t.Fatalf("malformed channel_id failed the whole push: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("batch push answered %d results, want 2", len(resp.Results))
	}
	wantRefusal := "sync: operation refused: channel_id is not a valid id"
	if resp.Results[0].Status != "rejected" || ux115ErrWords(resp.Results[0]) != wantRefusal {
		t.Errorf("malformed channel_id op answered %s, want rejected with %q",
			ux115Answer(resp.Results[0]), wantRefusal)
	}
	if resp.Results[1].Status != "applied" || resp.Results[1].Seq == nil {
		t.Errorf("good op behind malformed channel_id answered %s, want applied",
			ux115Answer(resp.Results[1]))
	}

	// Bound-key gate check (ValidateCreate): caller A binds boundKey on a row
	// caller B cannot see; caller B then pushes a create with a non-uuid
	// channel_id under boundKey (spent) and freeKey (free). Both must answer
	// the same validation refusal.
	seeded := push(t, engine, prefs, callerA, boundKey, "create",
		map[string]any{"channel_id": chYardTrenton, "mode": ModeMentions})
	if seeded.Status != "applied" || seeded.Seq == nil {
		t.Fatalf("setup boundKey create answered %s, want applied", ux115Answer(seeded))
	}
	badBody := map[string]any{"channel_id": "not-a-uuid", "mode": ModeAll}
	spentRes, spentErr := engine.Push(ctx, prefs, callerB, []apigen.SyncPushOperation{
		{ClientId: boundKey, Op: "create", Payload: badBody},
	})
	freeRes, freeErr := engine.Push(ctx, prefs, callerB, []apigen.SyncPushOperation{
		{ClientId: freeKey, Op: "create", Payload: badBody},
	})
	if spentErr != nil || freeErr != nil {
		t.Fatalf("bound-key gate pushes failed: spentErr=%v freeErr=%v", spentErr, freeErr)
	}
	spent, free := spentRes.Results[0], freeRes.Results[0]
	t.Logf("ux123c bound-key gate: spent=%s free=%s", ux115Answer(spent), ux115Answer(free))
	if ux115Answer(spent) != ux115Answer(free) {
		t.Errorf("spent key and free key differ on malformed channel_id: spent=%s free=%s",
			ux115Answer(spent), ux115Answer(free))
	}
	if ux115ErrWords(spent) != wantRefusal {
		t.Errorf("malformed channel_id under spent key answered %q, want %q",
			ux115ErrWords(spent), wantRefusal)
	}
}

// TestSyncPush_PrefsWriteNonMemberSilencingEscalationChannelAnswersRowNotFound:
// pins the UX-122 check order on an ESCALATION channel. A seated member who
// attempts to silence the ESCALATION room meets "sync: operation refused:
// escalation channels cannot be muted", while a non-member sending the same
// silencing patch (mode: MUTED or muted: true) meets bare "sync: row not found",
// identical to a missing channel, leaking neither the channel's existence nor
// its tier.
func TestSyncPush_PrefsWriteNonMemberSilencingEscalationChannelAnswersRowNotFound(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync trenton caller: %v", err)
	}
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync kingston caller: %v", err)
	}
	escID := escalationChannelID(t, f)
	if _, err := f.pool.Exec(ctx,
		`DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`,
		escID, uCaseyDoyle); err != nil {
		t.Fatalf("remove kingston caller from escalation room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(),
			`DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`,
			uCaseyDoyle, escID)
		_ = f.repo.SyncMembership(context.Background(), uCaseyDoyle)
	})
	if ok, _ := f.repo.IsMember(ctx, escID, uCaseyDoyle); ok {
		t.Fatalf("the fixture is wrong: kingston caller is still seated in escalation room")
	}

	prefs, ok := sync.Lookup("notify_prefs")
	if !ok {
		t.Fatal("notify_prefs missing from the registry")
	}

	// Control: seated member attempting to mute the escalation room meets the
	// escalation refusal.
	memberMute := push(t, f.engine, prefs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": escID, "mode": ModeMuted})
	t.Logf("ux123 escalation control (seated member): %s", ux115Answer(memberMute))
	wantEscRefusal := "sync: operation refused: escalation channels cannot be muted"
	if memberMute.Status != "rejected" || ux115ErrWords(memberMute) != wantEscRefusal {
		t.Fatalf("seated member escalation mute answered %s, want rejected with %q",
			ux115Answer(memberMute), wantEscRefusal)
	}

	// Non-member silencing patches on the escalation room must answer bare
	// sync: row not found, matching a missing channel.
	missing := ux115MissingChannel(t, f)
	wantNotFound := ux115Answer(push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": missing, "mode": ModeMuted}))

	nonMemberMode := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": escID, "mode": ModeMuted})
	nonMemberMuted := push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": escID, "muted": true})
	t.Logf("ux123 escalation non-member mode:MUTED=%s muted:true=%s missing=%s",
		ux115Answer(nonMemberMode), ux115Answer(nonMemberMuted), wantNotFound)

	if ux115Answer(nonMemberMode) != wantNotFound {
		t.Errorf("non-member mode:MUTED on escalation channel answered %s, want %s",
			ux115Answer(nonMemberMode), wantNotFound)
	}
	if ux115Answer(nonMemberMuted) != wantNotFound {
		t.Errorf("non-member muted:true on escalation channel answered %s, want %s",
			ux115Answer(nonMemberMuted), wantNotFound)
	}
}
