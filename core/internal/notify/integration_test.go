//go:build integration

// Package notify's integration evidence, DIB-426 (VERIFY:notify, W6-D finding W6D-6).
//
// The ledger row read "PgOutboxStore on a real pool, 3 test files, 0 integration, 36% coverage",
// and it was right to single this package out. Everything the unit tests cover is the DISPATCHER's
// routing and retry policy over a fake store. The store itself is the part that cannot be tested
// with a fake, because the things it promises are properties of Postgres:
//
//   - the claim is `FOR UPDATE SKIP LOCKED`, so two dispatchers divide the work and never deliver
//     one row twice, a fake cannot be concurrent with itself in a way that means anything;
//   - the settle UPDATE runs in the SAME transaction as the claim, so a row is PUBLISHED only after
//     its handler succeeded, and a rollback leaves it claimable again;
//   - `Redact` really drops the payload at rest, which is a one-time token's whole safety story;
//   - `RequeueDead` moves DEAD rows back to PENDING for the named topics AND NO OTHERS.
//
// Every row these tests touch is created by them, under topics namespaced to this file, and dropped
// in t.Cleanup, so the file is order-independent, re-runnable, and leaves a shared demo database
// exactly as it found it (the DIB-396 rule, and the DIB-453 one).
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/notify/
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// itSchema is the schema this file's outbox lives in.
//
// THE WHOLE POINT, and the thing an earlier draft of this file got wrong. PgOutboxStore's claim is
// `SELECT … FROM events_outbox WHERE status='PENDING' ORDER BY occurred_at LIMIT 1 FOR UPDATE SKIP
// LOCKED`, it is unqualified and unscoped, so on a shared database it competes with every genuine
// outbox row AND with any other package's drain running at the same moment (`go test -shuffle=on`
// runs packages in parallel). A fixture that tried to win that race by back-dating its rows to 1990
// only made things worse: its rows became the FIRST thing any concurrent drain anywhere would grab.
//
// A test that needs deterministic control of a global queue needs the queue to itself. Because the
// store's SQL is unqualified, a `search_path` is enough: the same code, the same table name, a
// private table. Nothing about the store changes to accommodate the test.
const itSchema = "notify_it"

// intPool opens a pool whose search_path resolves `events_outbox` to this file's OWN copy of the
// table, created with `LIKE … INCLUDING ALL` so the columns, defaults, indexes and the status CHECK
// are the real ones rather than a hand-written approximation that could drift.
func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...) // a configured DB must FAIL, never skip
		}
		t.Skipf(format, a...)
	}
	ctx := context.Background()

	// An admin pool on the default search_path, to build the schema.
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		fail("integration DB not reachable: %v", err)
	}
	defer admin.Close()
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS ` + itSchema + ` CASCADE`,
		`CREATE SCHEMA ` + itSchema,
		`CREATE TABLE ` + itSchema + `.events_outbox (LIKE public.events_outbox INCLUDING ALL)`,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			fail("prepare %s: %v", itSchema, err)
		}
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		fail("parse DATABASE_URL: %v", err)
	}
	// public stays on the path so gen_random_uuid() and friends still resolve; itSchema is FIRST, so
	// the store's unqualified events_outbox is this file's.
	cfg.ConnConfig.RuntimeParams["search_path"] = itSchema + ", public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fail("open scoped pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		clean, err := pgxpool.New(context.Background(), url)
		if err == nil {
			_, _ = clean.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+itSchema+` CASCADE`)
			clean.Close()
		}
	})
	return pool
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// topicSeq numbers this file's topics so each test can tell its own rows apart in an assertion.
var topicSeq int

// topicFor mints a topic name for one test.
func topicFor(t *testing.T, suffix string) string {
	t.Helper()
	topicSeq++
	return fmt.Sprintf("notifyint.%s.%d", suffix, topicSeq)
}

// emit writes one PENDING outbox row into this file's private table.
//
// No back-dating and no cleanup: the schema is dropped whole in intPool's cleanup, and within it the
// rows a test creates are the ONLY rows there are, which is what makes "claim exactly one row and
// assert which one it was" a meaningful thing to write.
func emit(t *testing.T, pool *pgxpool.Pool, topic, payload string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO events_outbox (topic, payload) VALUES ($1, $2::jsonb) RETURNING event_id::text`,
		topic, payload).Scan(&id); err != nil {
		t.Fatalf("emit %s: %v", topic, err)
	}
	return id
}

// drain processes exactly n rows through the dispatcher's own settle policy. DrainOnce is one line
// over this call; the batch size is not what any of these tests are about.
func drain(t *testing.T, d *Dispatcher, store *PgOutboxStore, n int) {
	t.Helper()
	if _, err := store.ProcessBatch(context.Background(), n, d.settle); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

// rowState reads back what the store actually wrote.
func rowState(t *testing.T, pool *pgxpool.Pool, id string) (status string, attempts int, lastErr string, payload string, publishedAt *time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, attempts, last_error, payload::text, published_at FROM events_outbox WHERE event_id = $1::uuid`,
		id).Scan(&status, &attempts, &lastErr, &payload, &publishedAt); err != nil {
		t.Fatalf("read outbox row %s: %v", id, err)
	}
	return
}

// settleAs builds a settle func that returns one fixed disposition and records what it saw.
func settleAs(seen *[]Event, st Settlement) func(context.Context, Event) Settlement {
	var mu sync.Mutex
	return func(_ context.Context, e Event) Settlement {
		mu.Lock()
		*seen = append(*seen, e)
		mu.Unlock()
		return st
	}
}

// -- the settle paths, against the real column values ---------------------------------------------

// PUBLISHED stamps published_at, bumps attempts and clears last_error, and, for a secret-bearing
// topic, DROPS THE PAYLOAD. That last one is the entire safety story of a one-time invite token:
// the row is kept for audit, the token is not kept at all.
func TestIntegration_Notify_PublishStampsTheRowAndRedactionDropsThePayload(t *testing.T) {
	pool := intPool(t)
	store := NewPgOutboxStore(pool)
	ctx := context.Background()

	plain := emit(t, pool, topicFor(t, "plain"), `{"invite":"keep-me"}`)
	secret := emit(t, pool, topicFor(t, "secret"), `{"token":"one-time-secret"}`)

	var seen []Event
	if _, err := store.ProcessBatch(ctx, 2, func(_ context.Context, e Event) Settlement {
		seen = append(seen, e)
		return Settlement{Status: statusPublished, Redact: e.EventID == secret}
	}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("the drain delivered %d of this test's 2 rows", len(seen))
	}

	st, attempts, lastErr, payload, publishedAt := rowState(t, pool, plain)
	if st != "PUBLISHED" || attempts != 1 || lastErr != "" {
		t.Fatalf("plain row: status=%s attempts=%d lastErr=%q, want PUBLISHED/1/empty", st, attempts, lastErr)
	}
	if publishedAt == nil {
		t.Fatal("a PUBLISHED row has no published_at; 'when did this go out' is unanswerable")
	}
	if payload != `{"invite": "keep-me"}` && payload != `{"invite":"keep-me"}` {
		t.Fatalf("a NON-secret topic's payload was altered: %s, it is kept for audit", payload)
	}

	st, _, _, payload, _ = rowState(t, pool, secret)
	if st != "PUBLISHED" {
		t.Fatalf("secret row status=%s, want PUBLISHED", st)
	}
	if payload != "{}" {
		t.Fatalf("a REDACTED row still carries %s at rest, the one-time token outlived its delivery", payload)
	}
}

// A transient failure leaves the row PENDING with the error recorded and attempts bumped, so the
// NEXT tick can pick it up and the dispatcher can count toward dead-lettering.
func TestIntegration_Notify_ATransientFailureLeavesTheRowClaimableAgain(t *testing.T) {
	pool := intPool(t)
	store := NewPgOutboxStore(pool)
	ctx := context.Background()
	topic := topicFor(t, "transient")
	id := emit(t, pool, topic, `{"n":1}`)

	var seen []Event
	if _, err := store.ProcessBatch(ctx, 1, settleAs(&seen, Settlement{Status: statusPending, LastError: "smtp unavailable"})); err != nil {
		t.Fatalf("failing batch: %v", err)
	}
	st, attempts, lastErr, _, publishedAt := rowState(t, pool, id)
	if st != "PENDING" || attempts != 1 || lastErr != "smtp unavailable" {
		t.Fatalf("after a transient failure: status=%s attempts=%d lastErr=%q, want PENDING/1/the error", st, attempts, lastErr)
	}
	if publishedAt != nil {
		t.Fatal("a row that FAILED carries a published_at")
	}

	// The load-bearing half: it is still claimable, and the second attempt sees attempts=1 so the
	// dispatcher's dead-letter arithmetic works off the DATABASE's count, not an in-memory one.
	var second []Event
	if _, err := store.ProcessBatch(ctx, 1, settleAs(&second, Settlement{Status: statusPublished})); err != nil {
		t.Fatalf("retry batch: %v", err)
	}
	var sawIt bool
	for _, e := range second {
		if e.EventID == id {
			sawIt = true
			if e.Attempts != 1 {
				t.Fatalf("the retry saw Attempts=%d, want 1, the count must come back from the row", e.Attempts)
			}
		}
	}
	if !sawIt {
		t.Fatal("a row left PENDING was not re-claimed on the next drain; a transient failure is permanent")
	}
	if st, _, lastErr, _, _ := rowState(t, pool, id); st != "PUBLISHED" || lastErr != "" {
		t.Fatalf("after the successful retry: status=%s lastErr=%q, want PUBLISHED and a cleared error", st, lastErr)
	}
}

// DEAD is terminal: the row is not claimed again, and its diagnosis is kept.
func TestIntegration_Notify_ADeadRowIsNotClaimedAgain(t *testing.T) {
	pool := intPool(t)
	store := NewPgOutboxStore(pool)
	ctx := context.Background()
	topic := topicFor(t, "dead")
	id := emit(t, pool, topic, `{"n":1}`)
	// A sentinel emitted AFTER the dead row, so it is strictly newer. If the DEAD row were still
	// claimable it would come back before this one.
	sentinel := emit(t, pool, topic, `{"n":2}`)

	var seen []Event
	if _, err := store.ProcessBatch(ctx, 1, settleAs(&seen, Settlement{Status: statusDead, LastError: "no handler registered for topic"})); err != nil {
		t.Fatalf("dead batch: %v", err)
	}
	st, attempts, lastErr, _, _ := rowState(t, pool, id)
	if st != "DEAD" || attempts != 1 || lastErr == "" {
		t.Fatalf("dead row: status=%s attempts=%d lastErr=%q", st, attempts, lastErr)
	}

	var again []Event
	if _, err := store.ProcessBatch(ctx, 1, settleAs(&again, Settlement{Status: statusPublished})); err != nil {
		t.Fatalf("post-dead batch: %v", err)
	}
	if len(again) != 1 || again[0].EventID != sentinel {
		t.Fatalf("the next claim returned %+v, want the newer sentinel, a DEAD row that is claimed again is not terminal, and a broken handler loops forever", again)
	}
}

// A secret topic that dead-letters drops its payload (HH-188): the row is kept for audit with its
// diagnosis, but the one-time token it carried is as spent in a DEAD row as it is in a published
// one, and it must not sit at rest behind a failure nobody will retry. Driven through the REAL
// dispatcher so the redact flag is the one fail() computed, not a hand-built Settlement.
func TestIntegration_Notify_ADeadSecretRowDropsItsPayload(t *testing.T) {
	pool := intPool(t)
	store := NewPgOutboxStore(pool)
	topic := topicFor(t, "dead.secret")
	id := emit(t, pool, topic, `{"token":"one-time-invite"}`)

	d := NewDispatcher(store, quietLog())
	d.RegisterSecret(topic, func(context.Context, Event) error { return errors.New("smtp down") })
	for i := 0; i < defaultMaxAttempts; i++ {
		drain(t, d, store, 1)
	}

	st, attempts, lastErr, payload, publishedAt := rowState(t, pool, id)
	if st != "DEAD" || attempts != defaultMaxAttempts {
		t.Fatalf("dead secret row: status=%s attempts=%d, want DEAD/%d", st, attempts, defaultMaxAttempts)
	}
	if lastErr != "smtp down" {
		t.Fatalf("last_error=%q, want the handler's diagnosis kept", lastErr)
	}
	if publishedAt != nil {
		t.Fatal("a DEAD row carries a published_at")
	}
	if payload != "{}" {
		t.Fatalf("the DEAD row still carries %s at rest: a one-time token must not outlive its delivery in a dead letter", payload)
	}
}

// -- the claim, under real concurrency ------------------------------------------------------------

// FOR UPDATE SKIP LOCKED is the promise that two dispatchers (or two replicas) divide the work and
// never deliver one row twice. This is the test a fake store cannot write: it needs two real
// transactions holding real row locks at the same moment.
//
// The settle callback BLOCKS until every worker has claimed something, which is what forces the
// overlap, without it the first goroutine can finish the whole batch before the second starts, and
// the test would pass on a store with no locking at all.
func TestIntegration_Notify_ConcurrentDispatchersNeverDeliverOneRowTwice(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	topic := topicFor(t, "concurrent")

	const rows, workers = 12, 4
	ids := map[string]bool{}
	for i := 0; i < rows; i++ {
		ids[emit(t, pool, topic, fmt.Sprintf(`{"n":%d}`, i))] = true
	}

	var mu sync.Mutex
	delivered := map[string]int{}
	gate := make(chan struct{})
	var claimed sync.WaitGroup
	claimed.Add(workers)
	go func() { claimed.Wait(); close(gate) }()

	// One settle closure PER WORKER, each of which blocks on its FIRST claim until every worker is
	// holding a row of its own. That is what forces the claims to overlap in time, without it the
	// first goroutine can drain the whole batch before the second starts, and the test would pass
	// against a store with no row locking at all.
	makeSettle := func() func(context.Context, Event) Settlement {
		gated := false
		return func(_ context.Context, e Event) Settlement {
			mu.Lock()
			delivered[e.EventID]++
			mu.Unlock()
			if !gated {
				gated = true
				claimed.Done()
				select {
				case <-gate:
				case <-time.After(10 * time.Second):
					// A timeout rather than a deadlock: if a worker never claims anything the test
					// should FAIL on its assertions, not hang the suite.
				}
			}
			return Settlement{Status: statusPublished}
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			store := NewPgOutboxStore(pool) // a separate store value per "dispatcher"
			// rows/workers each, so the workers between them claim EXACTLY this test's rows and
			// never reach past them into the database's own PENDING queue.
			// A generous per-worker budget: a foreign row costs a claim, and the owner leaves it
			// PENDING, so a worker must be able to step past one and still reach its share.
			_, errs[w] = store.ProcessBatch(ctx, rows, makeSettle())
		}(w)
	}
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", w, err)
		}
	}

	for id, n := range delivered {
		if !ids[id] {
			continue // another suite's row; this test does not own it
		}
		if n != 1 {
			t.Fatalf("event %s was delivered %d times, SKIP LOCKED is not holding, and a customer gets two emails", id, n)
		}
	}
	for id := range ids {
		if delivered[id] == 0 {
			t.Fatalf("event %s was never delivered; the workers skipped past a row instead of dividing them", id)
		}
		if st, _, _, _, _ := rowState(t, pool, id); st != "PUBLISHED" {
			t.Fatalf("event %s finished as %s", id, st)
		}
	}
}

// -- RequeueDead ----------------------------------------------------------------------------------

// The DEAD replay seam moves the NAMED topics back to PENDING and leaves everything else alone.
// The second half is the one that matters: a requeue that swept every DEAD row would re-deliver
// other modules' permanently-failed events as a side effect of one module's boot.
func TestIntegration_Notify_RequeueDeadMovesOnlyTheNamedTopics(t *testing.T) {
	pool := intPool(t)
	store := NewPgOutboxStore(pool)
	ctx := context.Background()

	wanted := topicFor(t, "requeue.wanted")
	other := topicFor(t, "requeue.other")
	mine := emit(t, pool, wanted, `{"n":1}`)
	theirs := emit(t, pool, other, `{"n":2}`)
	alreadyFine := emit(t, pool, wanted, `{"n":3}`)

	// Two DEAD rows on different topics, and one PUBLISHED row on the named topic.
	deaden := func(id string) {
		if _, err := pool.Exec(ctx,
			`UPDATE events_outbox SET status='DEAD', attempts=5, last_error='no handler registered for topic' WHERE event_id=$1::uuid`, id); err != nil {
			t.Fatalf("deaden %s: %v", id, err)
		}
	}
	deaden(mine)
	deaden(theirs)
	// A FIXTURE instant, not now(): nothing asserts this column, and a SQL clock beside a fixed date
	// is the combination the DIB-427 tripwire catches.
	publishedWhen := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE events_outbox SET status='PUBLISHED', published_at=$2 WHERE event_id=$1::uuid`, alreadyFine, publishedWhen); err != nil {
		t.Fatalf("publish %s: %v", alreadyFine, err)
	}

	n, err := store.RequeueDead(ctx, []string{wanted})
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 1 {
		t.Fatalf("RequeueDead reported %d rows, want exactly the 1 DEAD row on the named topic", n)
	}

	st, attempts, lastErr, _, _ := rowState(t, pool, mine)
	if st != "PENDING" || attempts != 0 || lastErr != "" {
		t.Fatalf("requeued row: status=%s attempts=%d lastErr=%q, want PENDING/0/cleared, a replay that kept the old count would dead-letter again immediately",
			st, attempts, lastErr)
	}
	if st, _, _, _, _ := rowState(t, pool, theirs); st != "DEAD" {
		t.Fatalf("another topic's DEAD row became %s; one module's boot must not replay a peer's failures", st)
	}
	if st, _, _, _, _ := rowState(t, pool, alreadyFine); st != "PUBLISHED" {
		t.Fatalf("an already-PUBLISHED row on the named topic became %s", st)
	}

	// Idempotent: a second call finds nothing left to move.
	if n, err := store.RequeueDead(ctx, []string{wanted}); err != nil || n != 0 {
		t.Fatalf("second requeue moved %d rows (err=%v), want 0, boot runs this every time", n, err)
	}
	// And an empty topic list is a no-op rather than "every topic".
	if n, err := store.RequeueDead(ctx, nil); err != nil || n != 0 {
		t.Fatalf("RequeueDead(nil) moved %d rows (err=%v); an empty list must never mean ALL", n, err)
	}
}

// -- the dispatcher over the REAL store -----------------------------------------------------------

// The routing policy and the store, together: an unknown topic retries until maxAttempts and then
// dead-letters, and a registered handler publishes. The unit tests prove the policy over a fake;
// this proves the ATTEMPTS COUNT survives the round trip to the database, which is what the policy
// actually branches on.
func TestIntegration_Notify_DispatcherDeadLettersAnUnknownTopicAfterMaxAttempts(t *testing.T) {
	pool := intPool(t)
	unknown := topicFor(t, "dispatcher.unknown")
	known := topicFor(t, "dispatcher.known")
	// The ROUTED row is emitted first so it is strictly older and gets claimed first. The orphan
	// stays PENDING after every failed delivery, so it is the oldest row from then on and each
	// subsequent single-row drain re-claims exactly it.
	routed := emit(t, pool, known, `{"n":2}`)
	orphan := emit(t, pool, unknown, `{"n":1}`)

	var handled int
	store := NewPgOutboxStore(pool)
	d := NewDispatcher(store, quietLog())
	d.Register(known, func(context.Context, Event) error { handled++; return nil })

	drain(t, d, store, 1) // the routed row publishes
	for i := 0; i < defaultMaxAttempts; i++ {
		drain(t, d, store, 1) // the orphan, once per attempt
	}

	if handled != 1 {
		t.Fatalf("the registered handler ran %d times, want exactly 1, a PUBLISHED row must not be re-delivered", handled)
	}
	if st, _, _, _, _ := rowState(t, pool, routed); st != "PUBLISHED" {
		t.Fatalf("the routed row finished as %s", st)
	}
	st, attempts, lastErr, _, _ := rowState(t, pool, orphan)
	if st != "DEAD" {
		t.Fatalf("an unknown topic finished as %s after %d drains, want DEAD", st, defaultMaxAttempts)
	}
	if attempts != defaultMaxAttempts {
		t.Fatalf("the dead-lettered row shows %d attempts, want %d, the policy counts from the ROW, and a miscount either gives up early or never gives up",
			attempts, defaultMaxAttempts)
	}
	if lastErr != "no handler registered for topic" {
		t.Fatalf("dead-letter last_error=%q; the diagnosis is the only thing left to act on", lastErr)
	}
}

// A handler that FAILS then succeeds: the row is not lost and is not double-delivered.
func TestIntegration_Notify_AFailingHandlerRetriesAndThenPublishes(t *testing.T) {
	pool := intPool(t)
	topic := topicFor(t, "dispatcher.flaky")
	id := emit(t, pool, topic, `{"n":1}`)

	var calls int
	store := NewPgOutboxStore(pool)
	d := NewDispatcher(store, quietLog())
	d.Register(topic, func(context.Context, Event) error {
		calls++
		if calls < 3 {
			return errors.New("smtp unavailable")
		}
		return nil
	})

	for i := 0; i < 3; i++ {
		drain(t, d, store, 1)
	}
	if calls != 3 {
		t.Fatalf("handler called %d times, want 3", calls)
	}
	st, attempts, lastErr, _, publishedAt := rowState(t, pool, id)
	if st != "PUBLISHED" || lastErr != "" || publishedAt == nil {
		t.Fatalf("after a recovered failure: status=%s lastErr=%q published_at=%v", st, lastErr, publishedAt)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d after two failures and a success, want 3, every attempt is counted, including the one that worked", attempts)
	}
}

// A nil pool is a NO-OP, not a crash: the server boots in scaffold/offline mode and the dispatcher
// simply drains nothing.
func TestIntegration_Notify_ANilPoolDrainsNothingAndDoesNotError(t *testing.T) {
	store := NewPgOutboxStore(nil)
	n, err := store.ProcessBatch(context.Background(), 10, func(context.Context, Event) Settlement {
		panic("a nil-pool store must never reach a handler")
	})
	if n != 0 || err != nil {
		t.Fatalf("nil-pool ProcessBatch returned %d, %v; want a silent no-op so the server still boots", n, err)
	}
	if n, err := store.RequeueDead(context.Background(), []string{"anything"}); n != 0 || err != nil {
		t.Fatalf("nil-pool RequeueDead returned %d, %v", n, err)
	}
}
