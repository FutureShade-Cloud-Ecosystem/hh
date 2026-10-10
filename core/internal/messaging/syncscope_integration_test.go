//go:build integration

// syncscope_integration_test.go: the four messaging sync feeds against the
// migrated + seeded database, over the SAME store constructors the
// composition root wires. Red first, the way the brief states it: a caller
// never receives another's direct messages or a channel it is not in. Sam
// (Trenton) and Casey (Kingston) share only the GLOBAL rooms; the seeded
// Alex-Sam DM is Sam's alone. Every pull below proves the fence feed by feed,
// and the push legs prove the module's own refusals hold at the sync door
// too (a foreign channel's message create is rejected, an escalation channel
// cannot be muted over sync).
package messaging

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// syncFeedFixture is one engine with the four messaging stores wired, plus
// the stub linker that maps the filter's Actor onto the seeded staffers.
type syncFeedFixture struct {
	pool   *pgxpool.Pool
	engine *sync.Engine
	repo   *Repository
}

// escalationChannelID resolves the seeded GLOBAL ESCALATION room (0069's
// tier seed), the one channel whose mute the module refuses, by its slug.
func escalationChannelID(t *testing.T, f *syncFeedFixture) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT id::text FROM msg_channels WHERE slug = 'escalations'`).Scan(&id); err != nil {
		t.Fatalf("resolve the escalations room: %v", err)
	}
	return id
}

// the seeded staffers the leak battery uses (integration_test.go's consts):
// Sam is granted Trenton, Casey Kingston, Alex is GLOBAL.
var syncSubOf = map[string]string{
	"sub-sam":   uSamRivera,
	"sub-casey": uCaseyDoyle,
	"sub-alex":  uAlexMorgan,
}

func newSyncFeedFixture(t *testing.T) *syncFeedFixture {
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
	must(engine.RegisterScope("channels", NewChannelsSyncStore(repo, link)))
	must(engine.RegisterScope("messages", NewMessagesSyncStore(repo, link)))
	must(engine.RegisterScope("acks", NewAcksSyncStore(repo, link)))
	must(engine.RegisterScope("notify_prefs", NewNotifyPrefsSyncStore(repo, link)))
	return &syncFeedFixture{pool: pool, engine: engine, repo: repo}
}

// samFilter and caseyFilter are the callers' scope filters (org + actor).
func samFilter() sync.ScopeFilter   { return sync.ScopeFilter{OrgID: "org-north", Actor: "sub-sam"} }
func caseyFilter() sync.ScopeFilter { return sync.ScopeFilter{OrgID: "org-north", Actor: "sub-casey"} }

// pullIDs collects one pull's live entity ids.
func pullIDs(t *testing.T, e *sync.Engine, scope string, f sync.ScopeFilter) map[string]bool {
	t.Helper()
	info, ok := sync.Lookup(scope)
	if !ok {
		t.Fatalf("scope %s missing from the registry", scope)
	}
	resp, err := e.Pull(context.Background(), info, f, 0, 200)
	if err != nil {
		t.Fatalf("pull %s: %v", scope, err)
	}
	ids := map[string]bool{}
	for _, it := range resp.Items {
		ids[it.Id] = true
	}
	return ids
}

// TestSyncFeeds_NeverLeakAnotherWorld: channels, messages, acks and prefs
// each answer only the caller's own world. The DM is the sharpest probe: it
// is a real seeded room whose messages Sam can read and Casey must never
// see, although both pull the same GLOBAL rooms.
func TestSyncFeeds_NeverLeakAnotherWorld(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()

	// Materialize both staffers' derived membership so the world is settled.
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync casey: %v", err)
	}

	// The DM message only Sam's world holds (the seeded Alex-Sam room).
	dmMsg, err := f.repo.InsertMessage(ctx, chDMAlexSam, uAlexMorgan, "fx sync feed probe: dm", "")
	if err != nil {
		t.Fatalf("seed dm message: %v", err)
	}
	t.Cleanup(func() { deleteMessage(t, f, dmMsg.ID) })
	// A Kingston yard message only Casey's world holds.
	ktMsg, err := f.repo.InsertMessage(ctx, chYardKingston, uCaseyDoyle, "fx sync feed probe: kingston", "")
	if err != nil {
		t.Fatalf("seed kingston message: %v", err)
	}
	t.Cleanup(func() { deleteMessage(t, f, ktMsg.ID) })

	// Channels: Casey's feed carries the GLOBAL rooms and Kingston's, never
	// the Alex-Sam DM; Sam's carries the DM.
	caseyChans := pullIDs(t, f.engine, "channels", caseyFilter())
	if caseyChans[chDMAlexSam] {
		t.Fatalf("casey's channels feed carried sam's direct message channel")
	}
	if !caseyChans[chOffice] || !caseyChans[chYardKingston] {
		t.Fatalf("casey's channels feed missed its own rooms: %v", caseyChans)
	}
	samChans := pullIDs(t, f.engine, "channels", samFilter())
	if !samChans[chDMAlexSam] {
		t.Fatalf("sam's channels feed missed the direct message channel")
	}

	// Messages: each caller sees its own probe and the shared room's, never
	// the other's.
	caseyMsgs := pullIDs(t, f.engine, "messages", caseyFilter())
	if caseyMsgs[dmMsg.ID] {
		t.Fatalf("casey's messages feed carried sam's direct message")
	}
	if !caseyMsgs[ktMsg.ID] {
		t.Fatalf("casey's messages feed missed its own yard's message")
	}
	samMsgs := pullIDs(t, f.engine, "messages", samFilter())
	if samMsgs[ktMsg.ID] {
		t.Fatalf("sam's messages feed carried kingston's message (no branch grant)")
	}
	if !samMsgs[dmMsg.ID] {
		t.Fatalf("sam's messages feed missed the direct message")
	}

	// Acks: an ack is the acker's own fact only.
	officeMsg, err := f.repo.InsertMessage(ctx, chOffice, uAlexMorgan, "fx sync feed probe: office", "")
	if err != nil {
		t.Fatalf("seed office message: %v", err)
	}
	t.Cleanup(func() { deleteMessage(t, f, officeMsg.ID) })
	if _, err := f.repo.AckMessage(ctx, chOffice, officeMsg.ID, uSamRivera, AckTap); err != nil {
		t.Fatalf("sam acks: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM msg_acks WHERE message_id = $1`, officeMsg.ID)
	})
	caseyAcks := pullIDs(t, f.engine, "acks", caseyFilter())
	if len(caseyAcks) != 0 {
		t.Fatalf("casey's acks feed carried another's acks: %v", caseyAcks)
	}
	samAcks := pullIDs(t, f.engine, "acks", samFilter())
	if len(samAcks) != 1 {
		t.Fatalf("sam's acks feed did not carry exactly the one ack: %v", samAcks)
	}

	// Prefs: a pref row is the caller's own only.
	if _, err := f.repo.UpsertNotificationPrefs(ctx, NotificationPrefs{
		ChannelID: chOffice, UserID: uSamRivera, DesktopEnabled: true,
		EscalationOverride: true, Mode: ModeMentions,
	}); err != nil {
		t.Fatalf("sam prefs: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uSamRivera, chOffice)
	})
	caseyPrefs := pullIDs(t, f.engine, "notify_prefs", caseyFilter())
	if len(caseyPrefs) != 0 {
		t.Fatalf("casey's prefs feed carried another's prefs: %v", caseyPrefs)
	}
	samPrefs := pullIDs(t, f.engine, "notify_prefs", samFilter())
	if len(samPrefs) != 1 {
		t.Fatalf("sam's prefs feed did not carry exactly the one row: %v", samPrefs)
	}

	// A caller that resolves to no linked ERP user fails closed: nothing
	// pulls, on every feed.
	unknown := sync.ScopeFilter{OrgID: "org-north", Actor: "sub-nobody"}
	for _, scope := range []string{"channels", "messages", "acks", "notify_prefs"} {
		if ids := pullIDs(t, f.engine, scope, unknown); len(ids) != 0 {
			t.Fatalf("an unlinked caller pulled %s rows: %v", scope, ids)
		}
	}
}

// TestSyncPush_ModuleRulesHold: the push legs run the module's own rules. A
// message create in the caller's channel lands and replays deduped; a create
// naming a channel the caller is not in is the operation's rejected result;
// an escalation channel refuses a mute over sync exactly as the REST surface
// refuses it with 409.
func TestSyncPush_ModuleRulesHold(t *testing.T) {
	f := newSyncFeedFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	if err := f.repo.SyncMembership(ctx, uCaseyDoyle); err != nil {
		t.Fatalf("sync casey: %v", err)
	}
	msgs, _ := sync.Lookup("messages")
	prefs, _ := sync.Lookup("notify_prefs")

	// Sam posts to #office from the device: applied, and the replay of the
	// same key answers the SAME row.
	key := uuid.NewString()
	res := push(t, f.engine, msgs, samFilter(), key, "create",
		map[string]any{"channel_id": chOffice, "body": "fx sync push probe"})
	if res.Status != "applied" || res.Seq == nil {
		t.Fatalf("message create answered %+v", res)
	}
	firstID, firstSeq := appliedRow(t, f.engine, msgs, samFilter(), *res.Seq)
	t.Cleanup(func() { deleteMessage(t, f, firstID) })
	res = push(t, f.engine, msgs, samFilter(), key, "create",
		map[string]any{"channel_id": chOffice, "body": "fx sync push probe"})
	if res.Status != "applied" || res.Seq == nil || *res.Seq != firstSeq {
		t.Fatalf("the replayed create did not dedup to the one row: %+v (want seq %d)", res, firstSeq)
	}

	// Sam names Kingston's yard: not a member, the operation is rejected and
	// nothing lands.
	res = push(t, f.engine, msgs, samFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chYardKingston, "body": "fx sync push probe"})
	if res.Status != "rejected" {
		t.Fatalf("a foreign channel's create answered %+v, want rejected", res)
	}

	// Edits and deletes of sent messages refuse with the module's words.
	res = push(t, f.engine, msgs, samFilter(), uuid.NewString(), "update",
		map[string]any{"id": firstID, "body": "rewritten"})
	if res.Status != "rejected" {
		t.Fatalf("a sent message's edit answered %+v, want rejected", res)
	}
	res = push(t, f.engine, msgs, samFilter(), uuid.NewString(), "delete", map[string]any{"id": firstID})
	if res.Status != "rejected" {
		t.Fatalf("a sent message's delete answered %+v, want rejected", res)
	}

	// Casey tunes #office prefs from the device (a GLOBAL room both hold):
	// applied; muting the ESCALATION #escalations room is refused with the
	// module's own words, never silently dropped.
	res = push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": chOffice, "mode": "MENTIONS"})
	if res.Status != "applied" {
		t.Fatalf("prefs create answered %+v err %v", res, res.Error)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM msg_notification_prefs WHERE user_id = $1 AND channel_id = $2`, uCaseyDoyle, chOffice)
	})
	escID := escalationChannelID(t, f)
	res = push(t, f.engine, prefs, caseyFilter(), uuid.NewString(), "create",
		map[string]any{"channel_id": escID, "mode": "MUTED"})
	if res.Status != "rejected" || res.Error == nil || !strings.Contains(*res.Error, "escalation") {
		t.Fatalf("the escalation mute answered %+v err %v, want the module's refusal", res, res.Error)
	}
}

// push posts one operation and returns its result.
func push(t *testing.T, e *sync.Engine, info sync.ScopeInfo, f sync.ScopeFilter, key, op string, payload map[string]any) apigen.SyncPushResult {
	t.Helper()
	resp, err := e.Push(context.Background(), info, f,
		[]apigen.SyncPushOperation{{ClientId: key, Op: op, Payload: payload}})
	if err != nil {
		t.Fatalf("push %s: %v", info.Name, err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("push %s: %d results", info.Name, len(resp.Results))
	}
	return resp.Results[0]
}

// appliedRow resolves the row id at the given seq on the caller's feed.
func appliedRow(t *testing.T, e *sync.Engine, info sync.ScopeInfo, f sync.ScopeFilter, seq int64) (string, int64) {
	t.Helper()
	resp, err := e.Pull(context.Background(), info, f, seq-1, 10)
	if err != nil {
		t.Fatalf("appliedRow pull: %v", err)
	}
	for _, it := range resp.Items {
		if it.Seq == seq {
			return it.Id, it.Seq
		}
	}
	t.Fatalf("no row at seq %d on the %s feed", seq, info.Name)
	return "", 0
}

// deleteMessage removes a test-written message row (the seeded database is
// shared; every probe cleans its own rows).
func deleteMessage(t *testing.T, f *syncFeedFixture, id string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM msg_messages WHERE id = $1`, id); err != nil {
		t.Fatalf("delete probe message: %v", err)
	}
}
