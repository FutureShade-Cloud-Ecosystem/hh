//go:build integration

// Integration tests run the real pgx Repository against the migrated + seeded Postgres 16 (the
// compose PG on :5435, or DATABASE_URL). They prove the properties the in-memory unit tests cannot:
// derived membership from the identity role/branch model (FR-1), channel-scoping enforced in SQL,
// the unread-summary math over the view (FR-7), event→message idempotency (FR-3), and thread
// replies. Run: cd backend && DATABASE_URL=... go run ./cmd/migrate && go test -tags integration ./internal/messaging/...
package messaging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Seeded demo fixtures (cmd/seed/dibbits_seed.sql + migration 0052).
const (
	branchTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	branchKingston = "5d1b3f6e-0000-4000-9000-000000000002"

	uSamRivera  = "d7000000-0000-4000-9000-000000000003" // Sales Manager, granted Trenton
	uCaseyDoyle = "d7000000-0000-4000-9000-000000000005" // Yard Manager, granted Kingston
	uAlexMorgan = "d7000000-0000-4000-9000-000000000002" // General Manager, GLOBAL

	chYardTrenton  = "a5000000-0000-4000-9000-000000000001"
	chYardKingston = "a5000000-0000-4000-9000-000000000002"
	chOffice       = "a5000000-0000-4000-9000-000000000003"
	chFinance      = "a5000000-0000-4000-9000-000000000004"
	chDMAlexSam    = "a5000000-0000-4000-9000-0000000000d1"

	// Migration-only actor + the 0060 demo-gap fixtures. colton is the ONLY staff login that exists on
	// a migrations-only DB (staging), so the TOPIC and DM kinds must be demonstrable through HIM.
	uColton    = "d7000000-0000-4000-9000-000000000010" // SUPER_ADMIN, GLOBAL (seeded 0043)
	chTopicFB  = "a6000000-0000-4000-9000-000000000010" // #feedback TOPIC (seeded 0060)
	chDMColton = "a6000000-0000-4000-9000-0000000000d1" // Colton↔Dana DM (seeded 0060)
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// FR-1: derived membership. A Trenton-granted user auto-joins #yard-trenton and #office (GLOBAL) but
// NOT #yard-kingston. A GLOBAL user joins both yards. Sync is idempotent and self-healing.
func TestIntegration_DerivedMembership_FromRoleBranch(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))

	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	chans, err := repo.ListChannelsForUser(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("list sam: %v", err)
	}
	got := channelSet(chans)
	if !got[chYardTrenton] {
		t.Errorf("Trenton user should be in #yard-trenton")
	}
	if !got[chOffice] {
		t.Errorf("every user should be in the GLOBAL #office")
	}
	if got[chYardKingston] {
		t.Errorf("Trenton user must NOT be in #yard-kingston (branch scoping)")
	}

	// A GLOBAL user (GM) reaches BOTH yards.
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync alex: %v", err)
	}
	alex, _ := repo.ListChannelsForUser(ctx, uAlexMorgan)
	ga := channelSet(alex)
	if !ga[chYardTrenton] || !ga[chYardKingston] {
		t.Errorf("GLOBAL user should reach both yards, got %v", ga)
	}

	// Idempotent: a second sync doesn't duplicate.
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	chans2, _ := repo.ListChannelsForUser(ctx, uSamRivera)
	if len(chans2) != len(chans) {
		t.Errorf("re-sync changed channel count %d -> %d", len(chans), len(chans2))
	}
}

// Channel-scoping: a non-member is not a member of a channel they weren't derived/added into.
func TestIntegration_ChannelScoping_IsMember(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.IsMember(ctx, chYardTrenton, uSamRivera); !ok {
		t.Errorf("Sam should be a member of #yard-trenton")
	}
	if ok, _ := repo.IsMember(ctx, chYardKingston, uSamRivera); ok {
		t.Errorf("Sam must NOT be a member of #yard-kingston")
	}
}

// FR-7: unread math. Post a message from Alex to #office; Sam (another member) has 1 unread until he
// marks read, then 0. The author never counts their own message as unread.
func TestIntegration_UnreadMath(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	for _, u := range []string{uSamRivera, uAlexMorgan} {
		if err := repo.SyncMembership(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	// Baseline: mark BOTH Sam and Alex read so pre-existing (seeded) #office messages don't skew the
	// count, we want to measure ONLY the message Alex is about to post.
	for _, u := range []string{uSamRivera, uAlexMorgan} {
		if err := repo.MarkRead(ctx, chOffice, u); err != nil {
			t.Fatal(err)
		}
	}
	// A beat so the new message's created_at is strictly after the read cursor just set.
	time.Sleep(5 * time.Millisecond)
	// Alex posts.
	if _, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, fmt.Sprintf("unread-probe-%d", time.Now().UnixNano()), ""); err != nil {
		t.Fatal(err)
	}
	// Sam now has ≥1 unread in #office; the author (Alex) counts it as 0.
	samUnread := unreadFor(ctx, t, repo, uSamRivera, chOffice)
	if samUnread < 1 {
		t.Errorf("Sam should have ≥1 unread after Alex posts, got %d", samUnread)
	}
	if alexUnread := unreadFor(ctx, t, repo, uAlexMorgan, chOffice); alexUnread != 0 {
		t.Errorf("author's own message must not be unread, got %d", alexUnread)
	}
	// Sam marks read → unread clears.
	if err := repo.MarkRead(ctx, chOffice, uSamRivera); err != nil {
		t.Fatal(err)
	}
	if got := unreadFor(ctx, t, repo, uSamRivera, chOffice); got != 0 {
		t.Errorf("after mark-read unread should be 0, got %d", got)
	}
}

// Thread replies: a reply carries the root and is fetched under it; it is NOT a top-level message.
func TestIntegration_ThreadReplies(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatal(err)
	}
	root, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, "thread-root", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, "reply-one", root.ID); err != nil {
		t.Fatal(err)
	}
	replies, err := repo.ListThread(ctx, chOffice, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].Body != "reply-one" {
		t.Fatalf("thread should have 1 reply, got %+v", replies)
	}
	// Cross-channel thread-read IDOR guard: fetching the SAME root through a DIFFERENT channel
	// (chYardTrenton) returns nothing, the read is bound to (thread_root_id, channel_id), so a
	// member of one channel can never read another channel's thread by passing a foreign root id.
	foreign, err := repo.ListThread(ctx, chYardTrenton, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 0 {
		t.Errorf("thread read must be channel-bound; foreign-channel read leaked %d replies", len(foreign))
	}
	// A reply in a foreign channel is rejected (cross-channel root guard).
	if _, err := repo.InsertMessage(ctx, chYardTrenton, uAlexMorgan, "bad", root.ID); err == nil {
		t.Errorf("a reply whose root is in another channel must be rejected")
	}
}

// evtIntKeyPrefix is the event_key prefix this file's #finance fixture owns outright. Every key it
// writes starts with it, so the fixture can delete EXACTLY its own rows, never the channel, which
// carries seeded demo messages and, on an evidence DB, whatever the walkthrough put there.
const evtIntKeyPrefix = "evt-int-"

// financeChannelID resolves #finance, the one channel DIB-449 promises stays clean.
func financeChannelID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM msg_channels WHERE slug = 'finance'`).Scan(&id); err != nil {
		t.Fatalf("resolve #finance: %v", err)
	}
	return id
}

// purgeFinanceEvents deletes the #finance event rows matching one event_key pattern. It is scoped to a
// pattern this file OWNS (evtIntKeyPrefix) rather than truncating the channel, which would restore
// nothing and destroy the seeded demo conversation, the rollup fixture's rule, applied here.
func purgeFinanceEvents(t *testing.T, pool *pgxpool.Pool, channelID, pattern string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM msg_messages WHERE channel_id = $1 AND event_key LIKE $2`, channelID, pattern); err != nil {
		t.Errorf("clean #finance (%s): %v", pattern, err)
	}
}

// financeEventFixture registers the cleanup for the event row a test is about to post into #finance
// (DIB-453). Called with the SUBTEST's t so the deletion lands before the parent checks the channel.
func financeEventFixture(t *testing.T, pool *pgxpool.Pool, channelID, key string) {
	t.Helper()
	t.Cleanup(func() { purgeFinanceEvents(t, pool, channelID, key) })
}

// financeMessageCount counts the live (not soft-deleted) messages in a channel, the number a person
// looking at #finance would see.
func financeMessageCount(t *testing.T, pool *pgxpool.Pool, channelID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM msg_messages WHERE channel_id = $1 AND deleted_at IS NULL`, channelID).Scan(&n); err != nil {
		t.Fatalf("count #finance messages: %v", err)
	}
	return n
}

// FR-3: event → message, idempotent. Post payment.received to #finance twice with the same key; only
// one message exists and it is a system EVENT (author NULL).
//
// DIB-453: the posting happens inside a subtest so that the fixture's t.Cleanup has ALREADY RUN by the
// time the parent resumes, which is what lets the parent assert the channel was restored. A cleanup
// registered on the parent could only be trusted, not checked, and this fixture writing into the one
// channel DIB-449 promises stays clean is precisely the case where trust is not enough.
func TestIntegration_EventMessageIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	key := fmt.Sprintf("%s%d", evtIntKeyPrefix, time.Now().UnixNano())
	ref := EventRef{Topic: TopicPaymentReceived, Summary: "Payment received."}

	channelID := financeChannelID(t, pool)
	// Clear any evt-int-% row LEFT BEHIND BY AN EARLIER RUN before taking the baseline. Until DIB-453
	// this test cleaned up nothing at all, so every demo and evidence database it has ever run against
	// is already carrying a stack of "Payment received." rows, the exact residue DIB-449's #finance
	// promise is about. A fixture that only tidied up after itself would leave that history in place
	// forever.
	purgeFinanceEvents(t, pool, channelID, evtIntKeyPrefix+"%")
	before := financeMessageCount(t, pool, channelID)

	t.Run("posting the same event twice inserts once", func(t *testing.T) {
		financeEventFixture(t, pool, channelID, key)

		_, inserted1, err := svc.PostEventToChannelSlug(ctx, "finance", key, "Payment received.", ref, false)
		if err != nil || !inserted1 {
			t.Fatalf("first insert: inserted=%v err=%v", inserted1, err)
		}
		_, inserted2, err := svc.PostEventToChannelSlug(ctx, "finance", key, "Payment received.", ref, false)
		if err != nil || inserted2 {
			t.Fatalf("redelivery should be a no-op: inserted=%v err=%v", inserted2, err)
		}
		// The premise this test exists for: one row, not two. Asserted against the DB rather than
		// inferred from the `inserted` flags, which are the thing under test.
		var rows int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM msg_messages WHERE channel_id = $1 AND event_key = $2`, channelID, key).Scan(&rows); err != nil {
			t.Fatalf("count the event's rows: %v", err)
		}
		if rows != 1 {
			t.Fatalf("event %s wrote %d rows to #finance, want exactly 1", key, rows)
		}
		// A system EVENT, author NULL, the other half of the FR-3 premise.
		var kind string
		var authorID *string
		if err := pool.QueryRow(ctx,
			`SELECT kind, author_id::text FROM msg_messages WHERE channel_id = $1 AND event_key = $2`,
			channelID, key).Scan(&kind, &authorID); err != nil {
			t.Fatalf("read the event row: %v", err)
		}
		if kind != "EVENT" || authorID != nil {
			t.Fatalf("event row kind=%q author=%v, want EVENT / NULL", kind, authorID)
		}
	})

	// DIB-453: the fixture's cleanup has run. #finance holds exactly what it held before this test -
	// no "Payment received." row is left sitting in the channel DIB-449 promises stays clean.
	if after := financeMessageCount(t, pool, channelID); after != before {
		t.Fatalf("#finance held %d live messages before this test and %d after, the fixture left %d row(s) behind",
			before, after, after-before)
	}
	var leftover int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM msg_messages WHERE channel_id = $1 AND event_key LIKE $2`,
		channelID, evtIntKeyPrefix+"%").Scan(&leftover); err != nil {
		t.Fatalf("count leftover fixture rows: %v", err)
	}
	if leftover != 0 {
		t.Fatalf("%d %s* row(s) still in #finance after cleanup", leftover, evtIntKeyPrefix)
	}
}

// FR-3 routing helper: a branch id resolves to its yard slug.
func TestIntegration_BranchYardSlug(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	if got, err := repo.BranchYardSlug(ctx, branchTrenton); err != nil || got != "yard-trenton" {
		t.Errorf("Trenton -> yard-trenton, got %q (%v)", got, err)
	}
	if got, err := repo.BranchYardSlug(ctx, branchKingston); err != nil || got != "yard-kingston" {
		t.Errorf("Kingston -> yard-kingston, got %q (%v)", got, err)
	}
}

// DM channel: seeded explicit members can see it; a non-participant cannot.
func TestIntegration_DMExplicitMembership(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	if ok, _ := repo.IsMember(ctx, chDMAlexSam, uAlexMorgan); !ok {
		t.Errorf("Alex should be in the Alex↔Sam DM")
	}
	if ok, _ := repo.IsMember(ctx, chDMAlexSam, uCaseyDoyle); ok {
		t.Errorf("Casey must NOT be in a DM she isn't part of")
	}
	// Derived sync must never remove an EXPLICIT DM membership.
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.IsMember(ctx, chDMAlexSam, uAlexMorgan); !ok {
		t.Errorf("SyncMembership must not prune EXPLICIT DM rows")
	}
}

// FR-7 pagination keyset: when several messages share a created_at, paging with a page size of 1
// must visit EVERY message exactly once, no same-timestamp row is skipped at a page boundary. This
// exercises the SQL tuple predicate `(created_at, id) < (cursor_ts, cursor_id)` against real
// Postgres (microsecond timestamps collide readily when rows are inserted at one now()).
func TestIntegration_PaginationTimestampCollision(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatal(err)
	}
	// Insert several top-level messages that all share ONE created_at, so the created_at-only cursor
	// would have collapsed them and dropped rows across page boundaries.
	tag := fmt.Sprintf("collide-%d", time.Now().UnixNano())
	const n = 5
	pool := repo.pool
	sharedTS := time.Now()
	ids := map[string]bool{}
	for i := 0; i < n; i++ {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO msg_messages (channel_id, author_id, kind, body, created_at)
			VALUES ($1, $2, 'TEXT', $3, $4) RETURNING id::text`,
			chOffice, uAlexMorgan, fmt.Sprintf("%s-%d", tag, i), sharedTS).Scan(&id); err != nil {
			t.Fatalf("insert collide %d: %v", i, err)
		}
		ids[id] = true
	}

	// Page through the WHOLE channel one row at a time; collect only our tagged rows.
	seen := map[string]int{}
	cursor := ""
	for pages := 0; pages < 500; pages++ {
		page, err := repo.ListMessages(ctx, chOffice, cursor, 1)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		for _, m := range page.Messages {
			if ids[m.ID] {
				seen[m.ID]++
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != n {
		t.Fatalf("paging with same-timestamp rows must reach all %d messages, reached %d", n, len(seen))
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("message %s visited %d times (want exactly 1)", id, c)
		}
	}
}

// A malformed pagination cursor must surface as ErrBadCursor (client fault → HTTP 400 at the
// handler), never a raw error that maps to 500. A valid cursor minted by the same channel must still
// paginate. This exercises the real ListMessages → decodeCursor path against live Postgres.
func TestIntegration_ListMessages_BadCursor(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"garbage", "not-a-timestamp|x", "2026-13-99T00:00:00Z|x"} {
		_, err := repo.ListMessages(ctx, chOffice, bad, 10)
		if err == nil {
			t.Fatalf("bad cursor %q must error", bad)
		}
		if !errors.Is(err, ErrBadCursor) {
			t.Fatalf("bad cursor %q: want errors.Is ErrBadCursor, got %v", bad, err)
		}
	}
	// A valid cursor from a real first page must still paginate (no false ErrBadCursor).
	first, err := repo.ListMessages(ctx, chOffice, "", 1)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextCursor != "" {
		if _, err := repo.ListMessages(ctx, chOffice, first.NextCursor, 1); err != nil {
			t.Fatalf("valid next-page cursor must paginate, got %v", err)
		}
	}
}

// Corrected-comment behavior (repository.go InsertMessage): there is NO re-rooting of a
// reply-to-a-reply. A REPLY's id passed as thread_root_id fails the `thread_root_id IS NULL`
// EXISTS check and is rejected as not-found (errNotFound / ErrNotFound → HTTP 404). This pins the
// behavior the comment now describes.
func TestIntegration_ReplyToReply_RejectedNotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatal(err)
	}
	root, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, fmt.Sprintf("rr-root-%d", time.Now().UnixNano()), "")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := repo.InsertMessage(ctx, chOffice, uAlexMorgan, "rr-reply", root.ID)
	if err != nil {
		t.Fatalf("first-level reply must succeed: %v", err)
	}
	// Replying to the REPLY (reply.ID as root) must be rejected as not-found, not re-rooted onto
	// root.ID, not silently accepted.
	_, err = repo.InsertMessage(ctx, chOffice, uAlexMorgan, "rr-reply-to-reply", reply.ID)
	if err == nil {
		t.Fatal("replying to a reply must be rejected, not accepted/re-rooted")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("reply-to-reply must be ErrNotFound (404), got %v", err)
	}
}

// DEMO-GAP (migration 0060): with the single migrations-only credential (colton), the TOPIC and DM
// channel KINDS must be demonstrable end-to-end. Pre-0060, colton was a participant in NO DM and NO
// TOPIC channel existed, so ListMyChannels returned neither kind. After 0060 colton's channel list
// includes a TOPIC (#feedback) and a DM, and each reads its messages 200 (member read succeeds).
// This test fails on a DB migrated only through 0059.
func TestIntegration_DemoGap_ColtonSeesTopicAndDM(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	svc := NewService(repo)

	// ListMyChannels resyncs derived membership then returns colton's channels (derived + explicit).
	chans, err := svc.ListMyChannels(ctx, Caller{UserID: uColton, Scope: "GLOBAL"})
	if err != nil {
		t.Fatalf("list colton channels: %v", err)
	}
	byID := make(map[string]Channel, len(chans))
	var haveTopic, haveDM bool
	for _, c := range chans {
		byID[c.ID] = c
		switch c.Kind {
		case KindTopic:
			haveTopic = true
		case KindDM:
			haveDM = true
		}
	}
	if !haveTopic {
		t.Errorf("colton's channel list must include a TOPIC channel (demo gap), got kinds %v", kinds(chans))
	}
	if !haveDM {
		t.Errorf("colton's channel list must include a DM channel (demo gap), got kinds %v", kinds(chans))
	}
	// The specific 0060 fixtures must be present and colton a member of each.
	for _, id := range []string{chTopicFB, chDMColton} {
		if _, ok := byID[id]; !ok {
			t.Errorf("colton must be a member of seeded channel %s", id)
		}
		if ok, _ := repo.IsMember(ctx, id, uColton); !ok {
			t.Errorf("IsMember(%s, colton) must be true", id)
		}
	}
	// A real member read must succeed (200-equivalent): messages come back, not an authz/parse error.
	topicMsgs, err := svc.GetMessages(ctx, Caller{UserID: uColton, Scope: "GLOBAL"}, chTopicFB, "", 0)
	if err != nil {
		t.Fatalf("colton reading the TOPIC channel must succeed, got %v", err)
	}
	if len(topicMsgs.Messages) == 0 {
		t.Errorf("the seeded TOPIC channel must have demo messages")
	}
	dmMsgs, err := svc.GetMessages(ctx, Caller{UserID: uColton, Scope: "GLOBAL"}, chDMColton, "", 0)
	if err != nil {
		t.Fatalf("colton reading the DM channel must succeed, got %v", err)
	}
	if len(dmMsgs.Messages) == 0 {
		t.Errorf("the seeded DM channel must have a demo message")
	}
}

// MAJOR (live root cause): a non-UUID channel id bound into the repository's `uuid` columns fails the
// Postgres text->uuid cast and returns a generic error that is NOT ErrBadCursor / ErrNotFound, so it
// would map to the catch-all HTTP 500. This pins WHY the handler must reject a malformed {id} at the
// boundary (TestListMessages_NonUUIDPath_Returns4xx et al.) before it ever reaches the DB.
func TestIntegration_NonUUIDChannelID_IsRawCastError(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	// IsMember is the first thing every read/write hits; a non-UUID id fails the uuid comparison.
	_, err := repo.IsMember(ctx, "not-a-uuid", uColton)
	if err == nil {
		t.Fatal("a non-uuid channel id must error at the DB (uuid cast), got nil")
	}
	// It is neither a client-cursor fault nor a clean not-found, it is the raw cast error that the
	// handler's catch-all maps to 500. The boundary guard is what turns this into a 4xx.
	if errors.Is(err, ErrBadCursor) || errors.Is(err, ErrNotFound) {
		t.Fatalf("non-uuid cast error must be a raw (500-mapping) error, got %v", err)
	}
	// ListMessages on a non-uuid channel id likewise raises the cast error (not ErrBadCursor).
	if _, err := repo.ListMessages(ctx, "not-a-uuid", "", 10); err == nil {
		t.Fatal("ListMessages with a non-uuid channel id must error")
	}
}

// - helpers -

func kinds(chans []Channel) []string {
	out := make([]string, 0, len(chans))
	for _, c := range chans {
		out = append(out, c.Kind)
	}
	return out
}

// DIB-272: per-branch role TEAM channels (0076). auto_role narrows derived membership to a single role
// code WITHIN a branch. This proves it for TWO roles: an OUTSIDE_SALES user granted Trenton derives into
// #sales-trenton ONLY (not #sales-kingston, branch scoping; not #drivers-trenton, role scoping), and a
// DRIVERS user granted Kingston derives into #drivers-kingston ONLY. The seeded demo staff are managers
// (no OUTSIDE_SALES/DRIVERS grant), so this test provisions two synthetic front-line users and tears them
// down (users delete CASCADEs their role/branch grants + derived msg_members rows).
func TestIntegration_BranchTeamChannels_AutoRole(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	const (
		uSales   = "d7000000-0000-4000-9000-000000000272" // synthetic: OUTSIDE_SALES @ Trenton
		uDriver  = "d7000000-0000-4000-9000-000000000275" // synthetic: DRIVERS @ Kingston
		rOutside = "e7000000-0000-4000-9000-000000000011" // OUTSIDE_SALES (0015)
		rDrivers = "e7000000-0000-4000-9000-000000000013" // DRIVERS (0015)

		chSalesTrenton    = "a5000000-0000-4000-9000-000000000272"
		chSalesKingston   = "a5000000-0000-4000-9000-000000000273"
		chDriversTrenton  = "a5000000-0000-4000-9000-000000000274"
		chDriversKingston = "a5000000-0000-4000-9000-000000000275"
	)

	// Provision the two synthetic ACTIVE staff + their role/branch grants (idempotent).
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,display_name,status,password_hash) VALUES
	    ($1,'dib272.sales@dibbits.test','DIB-272 Sales (test)','ACTIVE','x'),
	    ($2,'dib272.driver@dibbits.test','DIB-272 Driver (test)','ACTIVE','x')
	  ON CONFLICT (id) DO NOTHING`, uSales, uDriver); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id,role_id) VALUES ($1,$3),($2,$4)
	  ON CONFLICT DO NOTHING`, uSales, uDriver, rOutside, rDrivers); err != nil {
		t.Fatalf("seed user_roles: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_branch_grants (user_id,branch_id) VALUES ($1,$3),($2,$4)
	  ON CONFLICT DO NOTHING`, uSales, uDriver, branchTrenton, branchKingston); err != nil {
		t.Fatalf("seed branch grants: %v", err)
	}
	t.Cleanup(func() {
		// CASCADE removes user_roles, user_branch_grants and derived msg_members.
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`,
			[]string{uSales, uDriver})
	})

	// OUTSIDE_SALES @ Trenton → #sales-trenton only.
	if err := repo.SyncMembership(ctx, uSales); err != nil {
		t.Fatalf("sync sales: %v", err)
	}
	sales := channelSet(mustList(ctx, t, repo, uSales))
	if !sales[chSalesTrenton] {
		t.Errorf("OUTSIDE_SALES/Trenton should be in #sales-trenton")
	}
	if sales[chSalesKingston] {
		t.Errorf("OUTSIDE_SALES/Trenton must NOT be in #sales-kingston (branch scoping)")
	}
	if sales[chDriversTrenton] {
		t.Errorf("OUTSIDE_SALES must NOT be in #drivers-trenton (role scoping)")
	}

	// DRIVERS @ Kingston → #drivers-kingston only.
	if err := repo.SyncMembership(ctx, uDriver); err != nil {
		t.Fatalf("sync driver: %v", err)
	}
	drv := channelSet(mustList(ctx, t, repo, uDriver))
	if !drv[chDriversKingston] {
		t.Errorf("DRIVERS/Kingston should be in #drivers-kingston")
	}
	if drv[chDriversTrenton] {
		t.Errorf("DRIVERS/Kingston must NOT be in #drivers-trenton (branch scoping)")
	}
	if drv[chSalesKingston] {
		t.Errorf("DRIVERS must NOT be in #sales-kingston (role scoping)")
	}

	// Idempotent: a second sync doesn't change the derived channel set.
	before := len(mustList(ctx, t, repo, uSales))
	if err := repo.SyncMembership(ctx, uSales); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if after := len(mustList(ctx, t, repo, uSales)); after != before {
		t.Errorf("re-sync changed channel count %d -> %d", before, after)
	}
}

// 0076's auto_role binds a SINGLE role code (repository.go `qualifies`: ro.code = ch.auto_role), so
// #sales-<branch> derives OUTSIDE_SALES only, a SALES_MANAGER, despite running the sales function,
// is NOT auto-added (see 0076 comment). Sam Rivera is the seeded SALES_MANAGER @ Trenton fixture.
func TestIntegration_BranchTeamChannels_SalesManagerExcluded(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(intPool(t))
	const chSalesTrenton = "a5000000-0000-4000-9000-000000000272"

	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	if got, _ := repo.IsMember(ctx, chSalesTrenton, uSamRivera); got {
		t.Errorf("SALES_MANAGER must NOT be auto-added to #sales-trenton (single-role auto_role scoping)")
	}
}

func mustList(ctx context.Context, t *testing.T, repo *Repository, userID string) []Channel {
	t.Helper()
	chans, err := repo.ListChannelsForUser(ctx, userID)
	if err != nil {
		t.Fatalf("list %s: %v", userID, err)
	}
	return chans
}

func channelSet(chans []Channel) map[string]bool {
	m := make(map[string]bool, len(chans))
	for _, c := range chans {
		m[c.ID] = true
	}
	return m
}

func unreadFor(ctx context.Context, t *testing.T, repo *Repository, userID, channelID string) int {
	t.Helper()
	sum, err := repo.UnreadSummary(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range sum.Channels {
		if c.ChannelID == channelID {
			return c.Unread
		}
	}
	return 0
}
