//go:build integration

package main

// claim_pool_starvation_test.go is the outbox claim loops' connection starvation leg (hardening
// sweep site 23). Both claim loops (the events_outbox store and the sms_outbox store) hold the
// claimed row in an open transaction, with FOR UPDATE SKIP LOCKED, while every registered handler
// runs inside it, and eight of the ten registered handlers ask a pool for a SECOND connection in
// that window. When the claim connection comes from the SHARED pool, the dispatcher is one of the
// pool's holders: with the pool at 4 (pgx sets max(4, CPUs) and the deploy sets no pool_max_conns),
// three other transactions that each hold a connection and each want a second one leave the
// dispatcher and all three waiting for a connection nobody can release. Postgres sees nothing (every
// session is idle in transaction and no lock is waited on), so the process stalls until each
// context dies. Money stays correct, availability does not.
//
// The leg uses the REAL seams: the real stores through the worker's own buildClaimStores, the real
// dispatcher, and real handlers (the order lifecycle observer for events_outbox, the AR notice
// consumer for sms_outbox) that read through the shared pool exactly as production does. The shared
// pool is built at poolSize explicitly, poolSize-1 contender transactions hold the rest of it and
// each asks for a second connection, and the claim is forced to be held before the contenders are
// released, so the overlap does not depend on goroutine timing.
//
// WHAT THE LEGS TOUCH. Each leg back-dates its own row by a year and the store claims the OLDEST due
// row in the table (forced to one row per batch). On a database that already holds a PENDING row
// older than a year, the leg would claim that foreign row instead, mutate it (attempts plus one,
// last_error set) and fail. The leg needs a migrated database with no such row; the seed's rows are
// newer and are never touched. Its own row is deleted at the end.
//
// The file carries the integration tag: it needs a migrated database, and the untagged suite runs on
// an unmigrated DATABASE_URL in the demo job. The integration job runs cmd/core on the migrated,
// seeded gate.
//
// Run (own throwaway container; the leg skips without DATABASE_URL and pins no port or database):
//
//	DATABASE_URL='postgres://USER:PASS@HOST:PORT/DB?sslmode=disable' \
//	  go test -tags integration ./cmd/core/ -run TestClaimPool -count=1 -v

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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

const (
	// poolSize is the shared pool the scenario runs at (pgx's default is max(4, CPUs)); the contender count derives from it.
	claimLegPoolSize = 4
	// claimLegBound is the one shared deadline: a stalled run fails in seconds, a healthy one takes milliseconds.
	claimLegBound = 10 * time.Second
)

func claimLegURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("claim pool leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	return url
}

// claimLegPool builds a pool of exactly size connections over the leg's URL.
func claimLegPool(t *testing.T, url string, size int) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = int32(size)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}
	return pool
}

// gatedStore wraps a claim store so the leg knows the claim is held: the signal fires from inside
// the settle callback, which runs inside the claim transaction, and the real settle follows.
type gatedStore struct {
	inner   notify.OutboxStore
	claimed chan struct{}
	once    sync.Once
}

func (g *gatedStore) ProcessBatch(ctx context.Context, n int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	// One row only: the leg's row is back-dated so it is the oldest PENDING one, and nothing else in
	// the database is claimed or settled by the leg.
	return g.inner.ProcessBatch(ctx, 1, func(c context.Context, e notify.Event) notify.Settlement {
		g.once.Do(func() { close(g.claimed) })
		return settle(c, e)
	})
}

type claimLegCase struct {
	table   string
	topic   string
	payload string
	store   func(cs *claimStores) notify.OutboxStore
	// register wires the REAL handlers onto the dispatcher over the shared pool.
	register func(d *notify.Dispatcher, shared *pgxpool.Pool, log *slog.Logger)
}

func runClaimLeg(t *testing.T, c claimLegCase) {
	url := claimLegURL(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	shared := claimLegPool(t, url, claimLegPoolSize)
	helper := claimLegPool(t, url, 2) // the leg's own queries never eat one of the scenario's connections

	var eventID string
	if err := helper.QueryRow(ctx, `INSERT INTO `+c.table+` (topic, payload, occurred_at) VALUES ($1, $2::jsonb, now() - interval '1 year') RETURNING event_id::text`,
		c.topic, c.payload).Scan(&eventID); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = helper.Exec(context.Background(), `DELETE FROM `+c.table+` WHERE event_id = $1::uuid`, eventID)
	})

	cs, err := buildClaimStores(ctx, shared, nil, log)
	if err != nil {
		t.Fatalf("buildClaimStores: %v", err)
	}
	t.Cleanup(cs.Close)
	gate := &gatedStore{inner: c.store(cs), claimed: make(chan struct{})}
	d := notify.NewDispatcher(gate, log)
	c.register(d, shared, log)

	scenarioCtx, cancel := context.WithTimeout(ctx, claimLegBound)
	defer cancel()

	contenders := claimLegPoolSize - 1
	type outcome struct {
		tag string
		dur time.Duration
		err error
	}
	outs := make(chan outcome, contenders)
	var begun, done sync.WaitGroup
	begun.Add(contenders)
	done.Add(contenders)
	hold := make(chan struct{})
	for i := 1; i <= contenders; i++ {
		go func(i int) {
			defer done.Done()
			tag := fmt.Sprintf("contender-%d", i)
			tx, err := shared.Begin(ctx)
			begun.Done()
			if err != nil {
				outs <- outcome{tag: tag, err: err}
				return
			}
			<-hold
			start := time.Now()
			// The second acquisition: a read on the shared pool while this transaction holds its connection.
			var one int
			err = shared.QueryRow(scenarioCtx, `SELECT 1`).Scan(&one)
			outs <- outcome{tag: tag, dur: time.Since(start), err: err}
			rb, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = tx.Rollback(rb)
			rbCancel()
		}(i)
	}
	begun.Wait() // every contender holds its connection before the dispatcher claims

	type drained struct {
		n   int
		dur time.Duration
	}
	drainCh := make(chan drained, 1)
	go func() {
		start := time.Now()
		n := d.DrainOnce(scenarioCtx)
		drainCh <- drained{n: n, dur: time.Since(start)}
	}()
	select {
	case <-gate.claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatcher never claimed the row: the rig did not reach the scenario (not the stall)")
	}
	close(hold) // the claim is held and every contender holds a connection: release them together

	done.Wait()
	close(outs)
	dr := <-drainCh

	stalled := errors.Is(scenarioCtx.Err(), context.DeadlineExceeded)
	for o := range outs {
		t.Logf("%s: second acquisition took %s, err=%v", o.tag, o.dur.Round(time.Millisecond), o.err)
	}
	t.Logf("dispatcher: drained %d row(s) in %s", dr.n, dr.dur.Round(time.Millisecond))
	if stalled {
		t.Fatalf("STALL: the %s bound fired with the pool at %d: the dispatcher's claim holds a shared connection while its "+
			"handler (and %d contender transactions) wait for another one that nobody can release. Per-caller timings are logged above.",
			claimLegBound, claimLegPoolSize, contenders)
	}
	if dr.n != 1 {
		t.Errorf("the dispatcher drained %d rows, want 1", dr.n)
	}
	var status string
	if err := helper.QueryRow(ctx, `SELECT status FROM `+c.table+` WHERE event_id = $1::uuid`, eventID).Scan(&status); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "PUBLISHED" {
		t.Errorf("row status %q, want PUBLISHED", status)
	}
}

func TestClaimPool_EventsOutboxClaimDoesNotTakeASharedConnection(t *testing.T) {
	runClaimLeg(t, claimLegCase{
		table:   "events_outbox",
		topic:   "ar.invoice.finalized",
		payload: `{"invoice_id":"00000000-0000-4000-8000-0000000000aa"}`,
		store:   func(cs *claimStores) notify.OutboxStore { return cs.events },
		register: func(d *notify.Dispatcher, shared *pgxpool.Pool, log *slog.Logger) {
			noop := func(context.Context, notify.Event) error { return nil }
			// The primary owners of the two topics the order observers watch (production: the GL consumer).
			d.Register("ar.invoice.finalized", noop)
			d.Register("order.fulfilled", noop)
			// The REAL order lifecycle observer: its INVOICED driver resolves the order through the
			// billing repository, a read on the shared pool taken while the claim is held.
			order.NewLifecycleConsumer(nil, billing.NewRepository(shared), log).RegisterHandlers(d)
		},
	})
}

func TestClaimPool_SmsOutboxClaimDoesNotTakeASharedConnection(t *testing.T) {
	runClaimLeg(t, claimLegCase{
		table:   "sms_outbox",
		topic:   messagingprovider.TopicARNotice,
		payload: `{"invoice_id":"00000000-0000-4000-8000-0000000000aa","customer_id":"00000000-0000-4000-8000-0000000000bb"}`,
		store:   func(cs *claimStores) notify.OutboxStore { return cs.sms },
		register: func(d *notify.Dispatcher, shared *pgxpool.Pool, log *slog.Logger) {
			// The REAL AR notice consumer: its recipient resolution reads consent through the shared pool.
			resolver := messagingprovider.NewConsentResolver(messagingprovider.NewPrefsStore(shared), log)
			messagingprovider.NewConsumer(notify.NewLogNotifier(log), resolver, log).RegisterHandlers(d)
		},
	})
}
