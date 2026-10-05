//go:build integration

// collections_consent_window_integration_test.go, the consent window regression for the AR notice
// sweep. The window is the interval between the sweep's consent read and the queue: the sweep reads
// consent and the greeting name BEFORE it opens the transaction that claims the ledger slot and
// enqueues the notice, so a STOP, an opt-out, or a phone number change that commits inside that
// interval is invisible to the send decision. The decision must come from a re-read taken at queue
// time, inside the claim transaction, through that transaction.
//
// The window is forced, not left to goroutine timing: the ContactSource's name read is the hook.
// The resolver asks for the greeting name only for a customer whose consent row said sendable, so
// the hook fires after the consent read and before Begin, exactly inside the interval. Whatever the
// hook commits through a SEPARATE pool has landed, durably, by the time the sweep decides.
//
// The seam is the production one: a real ARSweep over a real ConsentResolver and a real PrefsStore,
// the real aging projection narrowed to the fixture customer, and the tree's real outbox table.
//
// The legs take their database from DATABASE_URL on the command and skip without it. They never fall
// back to a default database:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:<port>/<database>?sslmode=disable' \
//	  go test -tags=integration ./internal/messagingprovider/ -run TestIntegration_ARSweep_ConsentWindow -count=1 -v -p 1
package messagingprovider

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// windowPools builds the leg's scenario pool (the sweep's) and the SEPARATE helper pool the fixture
// and the window hook write through, so a helper connection never eats one of the scenario's.
func windowPools(t *testing.T) (scenario, helper *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("AR consent window leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	// poolSize is the ONE constant the scenario's connection accounting hangs on: 4 is pgx's default
	// on a host with four or fewer CPUs (max(4, CPUs)), pinned explicitly so the leg behaves
	// identically on any machine.
	const poolSize = 4
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = 0
	scenario, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("build scenario pool: %v", err)
	}
	t.Cleanup(scenario.Close)

	helperCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL for the helper pool: %v", err)
	}
	helperCfg.MaxConns = 2
	helper, err = pgxpool.NewWithConfig(ctx, helperCfg)
	if err != nil {
		t.Fatalf("build helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := helper.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}
	return scenario, helper
}

// windowContactSource is a ContactSource whose name read fires a caller-supplied hook at exactly the
// moment the resolver asks for the greeting. A nil hook is the converse control: the same plumbing
// with nothing landing in the window.
type windowContactSource struct {
	hook func(ctx context.Context)
}

func (s windowContactSource) ContactForCustomer(ctx context.Context, customerID string) (Contact, error) {
	if s.hook != nil {
		s.hook(ctx)
	}
	return Contact{CustomerID: customerID, Name: "Window fixture"}, nil
}

func (windowContactSource) CustomerForInvoice(context.Context, string) (string, error) {
	return "", ErrNoRecipient
}

func (windowContactSource) CustomerForOrder(context.Context, string) (string, error) {
	return "", ErrNoRecipient
}

// windowFixture is one leg's isolated world: a verified, opted-in customer and one invoice 35 days
// past due (the 30-day bucket), built through the helper pool and removed on cleanup.
type windowFixture struct {
	helper     *pgxpool.Pool
	CustomerID string
	Phone      string
	Marker     string
}

func newWindowFixture(t *testing.T, helper *pgxpool.Pool) *windowFixture {
	t.Helper()
	ctx := context.Background()

	var branch string
	if err := helper.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY created_at LIMIT 1`).Scan(&branch); err != nil {
		t.Fatalf("no branch in this database (run the seed first): %v", err)
	}
	var marker string
	if err := helper.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&marker); err != nil {
		t.Fatalf("mint marker: %v", err)
	}
	f := &windowFixture{helper: helper, Phone: fmt.Sprintf("+1613555%04d", 300+int(marker[0])%1000), Marker: marker[:8]}

	if err := helper.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, payment_terms_days)
		VALUES ($1, $2, 30) RETURNING id::text`,
		"ARWIN-"+f.Marker, "ARWIN fixture "+f.Marker).Scan(&f.CustomerID); err != nil {
		t.Fatalf("create fixture customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = helper.Exec(c, `DELETE FROM ar_notice_log WHERE customer_id = $1`, f.CustomerID)
		_, _ = helper.Exec(c, `DELETE FROM sms_outbox WHERE payload->>'customer_id' = $1`, f.CustomerID)
		_, _ = helper.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, f.CustomerID)
		_, _ = helper.Exec(c, `DELETE FROM customer_sms_prefs WHERE customer_id = $1`, f.CustomerID)
		_, _ = helper.Exec(c, `DELETE FROM customers WHERE id = $1`, f.CustomerID)
	})

	if _, err := helper.Exec(ctx, `
		INSERT INTO customer_sms_prefs (customer_id, phone_e164, sms_verified, verified_at)
		VALUES ($1, $2, TRUE, now())`, f.CustomerID, f.Phone); err != nil {
		t.Fatalf("seed consent: %v", err)
	}

	now := businessHours(t)
	asOf := now.Format("2006-01-02")
	if _, err := helper.Exec(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, total_cents, balance_cents)
		VALUES ($1, $2, $3, 'FINALIZED', $5::date - 35 - 30, $5::date - 35, $4, $4, $4)`,
		"ARWIN-"+f.Marker, f.CustomerID, branch, int64(150000), asOf); err != nil {
		t.Fatalf("create fixture invoice: %v", err)
	}
	return f
}

// arWindowSweep wires the production sweep over the scenario pool, the fixture's consent resolver
// through the hooking contact source, and the real aging projection narrowed to the fixture customer.
func arWindowSweep(t *testing.T, scenario *pgxpool.Pool, f *windowFixture, hook func(context.Context), now time.Time) *ARSweep {
	t.Helper()
	resolver := NewConsentResolver(NewPrefsStore(scenario), slog.Default()).
		WithContactSource(windowContactSource{hook: hook})
	return NewARSweep(scenario, NewOutbox(scenario), resolver, slog.Default()).
		WithSource(scopedAging{pool: scenario, customerID: f.CustomerID}).
		WithClock(func() time.Time { return now })
}

// topicOutboxCount counts every queued row for the AR topic, before and after a run, so the
// nothing-enqueued claim is checked against the tree's real outbox table rather than a return value.
func topicOutboxCount(t *testing.T, helper *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := helper.QueryRow(context.Background(),
		`SELECT count(*) FROM sms_outbox WHERE topic = $1`, TopicARNotice).Scan(&n); err != nil {
		t.Fatalf("count AR rows in the outbox: %v", err)
	}
	return n
}

// TestIntegration_ARSweep_ConsentWindow_OptOutInTheWindowIsHonoured: a STOP commits between the
// consent read and the queue, the notice must NOT go out, and the ledger must record why.
func TestIntegration_ARSweep_ConsentWindow_OptOutInTheWindowIsHonoured(t *testing.T) {
	scenario, helper := windowPools(t)
	f := newWindowFixture(t, helper)
	ctx := context.Background()
	now := businessHours(t)

	hook := func(ctx context.Context) {
		if _, err := helper.Exec(ctx,
			`UPDATE customer_sms_prefs SET opted_out = TRUE, opted_out_at = now(), opt_out_reason = 'STOP'
			 WHERE customer_id = $1`, f.CustomerID); err != nil {
			t.Fatalf("window hook could not commit the opt-out: %v", err)
		}
	}

	before := topicOutboxCount(t, helper)
	res, err := arWindowSweep(t, scenario, f, hook, now).Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	after := topicOutboxCount(t, helper)

	if after != before {
		t.Fatalf("the sweep enqueued %d notices for a customer whose opt-out committed inside the "+
			"consent window, want 0: the queue-time decision read a consent answer already known to be stale",
			after-before)
	}
	if res.Enqueued != 0 {
		t.Fatalf("run reported %d enqueued for an opt-out that landed in the window, want 0 (%+v)", res.Enqueued, res)
	}

	var suppressed bool
	var why string
	if err := helper.QueryRow(ctx,
		`SELECT suppressed, suppress_why FROM ar_notice_log WHERE customer_id = $1 AND bucket_days = 30`,
		f.CustomerID).Scan(&suppressed, &why); err != nil {
		t.Fatalf("no ledger row for the claimed bucket, the decision was not recorded: %v", err)
	}
	if !suppressed {
		t.Fatalf("the ledger row is not marked suppressed, an opt-out that landed in the window was ignored")
	}
	if why != "no SMS consent" {
		t.Fatalf("suppression reason = %q, want the no SMS consent reason so an operator can read what happened", why)
	}
}

// TestIntegration_ARSweep_ConsentWindow_PhoneChangeInTheWindowQueuesTheFreshNumber: the number is
// changed inside the window, the notice that does go out must carry the FRESH number.
func TestIntegration_ARSweep_ConsentWindow_PhoneChangeInTheWindowQueuesTheFreshNumber(t *testing.T) {
	scenario, helper := windowPools(t)
	f := newWindowFixture(t, helper)
	ctx := context.Background()
	now := businessHours(t)
	fresh := "+16135558642"

	hook := func(ctx context.Context) {
		if _, err := helper.Exec(ctx,
			`UPDATE customer_sms_prefs SET phone_e164 = $2, updated_at = now() WHERE customer_id = $1`,
			f.CustomerID, fresh); err != nil {
			t.Fatalf("window hook could not commit the number change: %v", err)
		}
	}

	before := topicOutboxCount(t, helper)
	res, err := arWindowSweep(t, scenario, f, hook, now).Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	after := topicOutboxCount(t, helper)

	if after-before != 1 || res.Enqueued != 1 {
		t.Fatalf("the notice was not queued (enqueued=%d, outbox delta=%d), the control premise is broken: %+v",
			res.Enqueued, after-before, res)
	}
	var to string
	if err := helper.QueryRow(ctx,
		`SELECT payload->>'to' FROM sms_outbox WHERE topic = $1 AND payload->>'customer_id' = $2`,
		TopicARNotice, f.CustomerID).Scan(&to); err != nil {
		t.Fatalf("read the queued notice: %v", err)
	}
	if to != fresh {
		t.Fatalf("the queued notice carries %q, want the FRESH number the customer changed to inside the window", to)
	}
}

// TestIntegration_ARSweep_ConsentWindow_Control_NothingLandsSoTheNoticeQueues: the converse. The same
// plumbing with an empty hook must queue exactly one notice at the number on file, which is what
// keeps the window legs honest about the direction they bite in.
func TestIntegration_ARSweep_ConsentWindow_Control_NothingLandsSoTheNoticeQueues(t *testing.T) {
	scenario, helper := windowPools(t)
	f := newWindowFixture(t, helper)
	ctx := context.Background()
	now := businessHours(t)

	before := topicOutboxCount(t, helper)
	res, err := arWindowSweep(t, scenario, f, nil, now).Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	after := topicOutboxCount(t, helper)

	if after-before != 1 || res.Enqueued != 1 {
		t.Fatalf("an undisturbed consenting customer must get exactly one notice (enqueued=%d, outbox delta=%d)",
			res.Enqueued, after-before)
	}
	var to string
	if err := helper.QueryRow(ctx,
		`SELECT payload->>'to' FROM sms_outbox WHERE topic = $1 AND payload->>'customer_id' = $2`,
		TopicARNotice, f.CustomerID).Scan(&to); err != nil {
		t.Fatalf("read the queued notice: %v", err)
	}
	if to != f.Phone {
		t.Fatalf("the queued notice carries %q, want the number on file %q", to, f.Phone)
	}
}
