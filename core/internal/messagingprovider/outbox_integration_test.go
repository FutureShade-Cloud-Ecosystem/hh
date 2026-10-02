//go:build integration

// Integration cover for the out-of-band notification queue (migration 0097) against a real migrated
// Postgres. Run with: DATABASE_URL=... go test -tags integration -count=1 ./internal/messagingprovider/
//
// What only a database can prove:
//   - the claim really is FOR UPDATE SKIP LOCKED (two concurrent drains never double-send a text);
//   - a published notice's payload is REDACTED at rest, so a customer's phone number does not linger;
//   - a permanently failing provider dead-letters instead of retrying a text message forever.
//
// FIXTURES RESTORE WHAT THEY TOUCH: every row is tagged with a per-test marker in its payload and
// deleted on cleanup, so these tests are order-independent and safe alongside other suites.

package messagingprovider

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
)

func smsPool(t *testing.T) *pgxpool.Pool {
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
		fail("no database: %v", err)
		return nil
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("database unreachable: %v", err)
		return nil
	}
	t.Cleanup(pool.Close)
	return pool
}

// markedFixture mints a per-test marker and registers the cleanup that removes every row carrying it.
func markedFixture(t *testing.T) (*pgxpool.Pool, *Outbox, string) {
	t.Helper()
	pool := smsPool(t)
	var marker string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&marker); err != nil {
		t.Fatalf("mint marker: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sms_outbox WHERE last_error LIKE $1 OR payload->>'task_id' = $2`,
			"%"+marker+"%", marker)
	})
	return pool, NewOutbox(pool), marker
}

// countByStatus returns how many of THIS test's rows are in each status. Rows are located by the
// marker the test wrote into the payload; a published row's payload is redacted, so those are counted
// by the event ids the test kept.
func countByStatus(t *testing.T, pool *pgxpool.Pool, ids []string) map[string]int {
	t.Helper()
	out := map[string]int{}
	rows, err := pool.Query(context.Background(), `SELECT status, COUNT(*) FROM sms_outbox WHERE event_id = ANY($1::uuid[]) GROUP BY status`, ids)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[s] = n
	}
	return out
}

// idsFor reads back this test's row ids by marker (before they are published and redacted).
func idsFor(t *testing.T, pool *pgxpool.Pool, marker string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT event_id::text FROM sms_outbox WHERE payload->>'task_id' = $1`, marker)
	if err != nil {
		t.Fatalf("ids: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func TestIntegrationStubModeDeliversAQueuedNoticeEndToEnd(t *testing.T) {
	pool, outbox, marker := markedFixture(t)
	ctx := context.Background()

	// Flag off, Select gives the stub with no credentials anywhere.
	notifier, err := Select(Settings{}, nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	stub := notifier.(*StubNotifier)

	if err := outbox.Enqueue(ctx, TopicDispatchNotice, DispatchNotice{
		TaskID: marker, OrderID: "o-" + marker, OrderNo: "SO-INT-1",
		BranchName: "Trenton", StageLocation: "Bay 3", StageRef: "STG-INT1",
		To: "+16135550142",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	ids := idsFor(t, pool, marker)
	if len(ids) != 1 {
		t.Fatalf("queued %d rows, want 1", len(ids))
	}

	d := notify.NewDispatcher(outbox, slog.Default())
	// UNION-W4 (D E-2): delivery re-resolves via the seam, the payload To is no longer trusted.
	NewConsumer(notifier, NewFixtureRecipients().SeedOrder("o-"+marker, Recipient{Phone: "+16135550142"}), slog.Default()).RegisterHandlers(d)
	// Drain until this test's row is gone from PENDING; other suites may share the table.
	for i := 0; i < 20 && countByStatus(t, pool, ids)["PENDING"] > 0; i++ {
		d.DrainOnce(ctx)
	}

	got := countByStatus(t, pool, ids)
	if got["PUBLISHED"] != 1 {
		t.Fatalf("statuses = %v, want 1 PUBLISHED", got)
	}
	sent := stub.Sent()
	if len(sent) == 0 || sent[len(sent)-1].Body == "" {
		t.Fatalf("stub delivered nothing: %+v", sent)
	}

	// The recipient's number must not linger at rest once it has been used.
	var payload string
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM sms_outbox WHERE event_id = $1`, ids[0]).Scan(&payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if payload != "{}" {
		t.Fatalf("published payload = %s, want it redacted", payload)
	}
}

func TestIntegrationConcurrentDrainsNeverSendTheSameTextTwice(t *testing.T) {
	pool, outbox, marker := markedFixture(t)
	ctx := context.Background()

	const notices = 12
	for i := 0; i < notices; i++ {
		if err := outbox.Enqueue(ctx, TopicDispatchNotice, DispatchNotice{
			TaskID: marker, OrderID: "o-" + marker, OrderNo: "SO-INT-2", To: "+16135550142",
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	ids := idsFor(t, pool, marker)
	if len(ids) != notices {
		t.Fatalf("queued %d rows, want %d", len(ids), notices)
	}

	// Two independent dispatchers over the same table, exactly like two server replicas.
	stub := NewStubNotifier(nil)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		d := notify.NewDispatcher(NewOutbox(pool), nil)
		NewConsumer(stub, NewFixtureRecipients().SeedOrder("o-"+marker, Recipient{Phone: "+16135550142"}), nil).RegisterHandlers(d)
		wg.Add(1)
		go func(d *notify.Dispatcher) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				d.DrainOnce(ctx)
			}
		}(d)
	}
	wg.Wait()

	got := countByStatus(t, pool, ids)
	if got["PUBLISHED"] != notices {
		t.Fatalf("statuses = %v, want %d PUBLISHED", got, notices)
	}
	// The stub is shared, so it counts every delivery both dispatchers made, for THESE rows. Other
	// suites cannot contribute because the stub instance is local to this test.
	if len(stub.Sent()) != notices {
		t.Fatalf("delivered %d texts for %d notices, SKIP LOCKED did not hold", len(stub.Sent()), notices)
	}
}

// permanentlyFailingNotifier stands in for a provider that rejects everything (bad sender, suspended
// account). It must dead-letter, not retry forever.
type permanentlyFailingNotifier struct{ calls int }

func (p *permanentlyFailingNotifier) Name() string { return "always-fails" }
func (p *permanentlyFailingNotifier) Live() bool   { return false }
func (p *permanentlyFailingNotifier) Send(context.Context, notify.Message) error {
	p.calls++
	return errors.New("provider rejected the message")
}

func TestIntegrationAFailingProviderDeadLettersRatherThanLoopingForever(t *testing.T) {
	pool, outbox, marker := markedFixture(t)
	ctx := context.Background()

	if err := outbox.Enqueue(ctx, TopicDispatchNotice, DispatchNotice{
		TaskID: marker, OrderID: "o-" + marker, To: "+16135550142",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	ids := idsFor(t, pool, marker)

	failing := &permanentlyFailingNotifier{}
	d := notify.NewDispatcher(outbox, slog.Default())
	NewConsumer(failing, NewFixtureRecipients().SeedOrder("o-"+marker, Recipient{Phone: "+16135550142"}), slog.Default()).RegisterHandlers(d)
	for i := 0; i < 12 && countByStatus(t, pool, ids)["DEAD"] == 0; i++ {
		d.DrainOnce(ctx)
	}

	got := countByStatus(t, pool, ids)
	if got["DEAD"] != 1 {
		t.Fatalf("statuses = %v, want the row dead-lettered", got)
	}
	var lastErr string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM sms_outbox WHERE event_id = $1`, ids[0]).Scan(&lastErr); err != nil {
		t.Fatalf("read last_error: %v", err)
	}
	if lastErr == "" {
		t.Fatal("a dead-lettered notice must record why")
	}
}

// TestIntegrationADeadRowDoesNotKeepItsPayload (HH-188): a DEAD row is as terminal as a PUBLISHED one,
// and its payload carries the same phone number (and, on the quote topics, the live share link, a
// bearer credential for the public quote view and accept routes). The dead-letter UPDATE must redact
// it, the way the published one always has.
//
// The driver is the worker's exact main shape for this topic: the Wave 2 consumer registered, the
// quote topics unowned, so the share notice settles "no handler registered for topic". That
// unregistered path is the one that must never park a token at rest, a registered secret topic that
// fails five times gets the same terminal redaction through the same UPDATE, and no secret marking
// exists to consult for a topic nobody registered.
func TestIntegrationADeadRowDoesNotKeepItsPayload(t *testing.T) {
	pool, outbox, _ := markedFixture(t)
	ctx := context.Background()

	// EnqueueAt returns the row's id (Enqueue does not), and a zero notBefore means immediately due.
	id, err := outbox.EnqueueAt(ctx, TopicQuoteShare, QuoteShareNotice{
		QuoteID: "fx-dead-share", QuoteNo: "Q-FX-1", CustomerID: "fx-cust-1",
		ShareURL: "https://erp.fx.invalid/quote.html?t=fx-live-share-token",
	}, time.Time{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	stub, serr := Select(Settings{}, nil)
	if serr != nil {
		t.Fatalf("select: %v", serr)
	}
	d := notify.NewDispatcher(outbox, slog.Default())
	NewConsumer(stub, NewConsentResolver(NewPrefsStore(pool), nil), slog.Default()).RegisterHandlers(d)

	// One tick is enough: the due clause has no backoff, so the drain re-claims the still-PENDING
	// row until the fifth attempt dead-letters it (the audit's "all in one drain").
	d.DrainOnce(ctx)

	var status string
	var attempts int
	var lastErr, payload string
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, last_error, payload::text FROM sms_outbox WHERE event_id = $1`, id).
		Scan(&status, &attempts, &lastErr, &payload); err != nil {
		t.Fatalf("read the dead row: %v", err)
	}
	if status != "DEAD" {
		t.Fatalf("status = %s after one drain, want DEAD (the unregistered topic must dead-letter)", status)
	}
	if attempts != 5 {
		t.Fatalf("attempts = %d, want 5 burned in the one drain", attempts)
	}
	if lastErr != "no handler registered for topic" {
		t.Fatalf("last_error = %q, want the unregistered-topic diagnosis", lastErr)
	}
	// Round 2: the redaction is SELECTIVE. The sensitive keys are gone (the share link nobody
	// will ever deliver, the recipient's number and address), and the business reference stays:
	// a dead row is the operator's only trace of what failed, and an anonymous one (this topic's
	// enqueue returns no event id) leaves nobody to call.
	var kept map[string]any
	if err := json.Unmarshal([]byte(payload), &kept); err != nil {
		t.Fatalf("the DEAD row's payload is not JSON (%s): %v", payload, err)
	}
	for _, key := range []string{"share_url", "to", "email"} {
		if _, still := kept[key]; still {
			t.Errorf("the DEAD row still carries %s (%v): a credential or a contact nobody will ever deliver must not sit at rest", key, kept[key])
		}
	}
	if kept["quote_id"] != "fx-dead-share" {
		t.Errorf("the DEAD row lost its quote reference (payload %s): the operator's only trace of what failed must survive the redaction", payload)
	}
}
