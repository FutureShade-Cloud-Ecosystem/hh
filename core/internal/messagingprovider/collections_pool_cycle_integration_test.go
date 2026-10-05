//go:build integration

// collections_pool_cycle_integration_test.go, the connection-starvation regression for the AR notice
// sweep. The defect: ARSweep.considerOne holds an open transaction (claimBucket has already inserted
// the ledger row) and then asked the SHARED POOL for a second connection, through the consent read
// (PrefsStore.Get) and the contact name read (ContactSource), before it released the first. With
// pool-size candidates in flight together, every one holds a connection and waits for one more that
// never comes. The sessions sit idle in transaction, no lock is waited on, so the database's deadlock
// detector sees nothing and the sweep stalls until its action timeout.
//
// The overlap is forced, not left to goroutine timing: a blocker transaction on a SEPARATE
// single-connection pool takes a table lock that conflicts with the ledger insert, so every candidate
// opens its transaction and parks at claimBucket. The leg waits until poolSize sessions are parked
// there, then commits the blocker. From that instant every candidate holds one of the poolSize
// connections and needs the next read, which is exactly the starvation shape.
//
// The seam is the production one: a real ARSweep, a real ConsentResolver over a real PrefsStore, and a
// ContactSource whose name read goes through the pool (the production adapter reads through its own
// pool the same way). The leg goes red if the fix is undone at any layer of that chain.
//
// The leg takes its database from DATABASE_URL on the command and skips without it. It never falls
// back to a default database:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5732/hh_r11_messagingprovider_ar?sslmode=disable' \
//	  go test -tags=integration ./internal/messagingprovider/ -run TestIntegration_ARSweep_ConsentReadsJoinTx_NoPoolCycle -count=1 -v -p 1
package messagingprovider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolNameSource is a ContactSource whose name read goes through the pool, the shape of the
// composition root's adapter over the customer module.
type poolNameSource struct{ pool *pgxpool.Pool }

func (s poolNameSource) ContactForCustomer(ctx context.Context, customerID string) (Contact, error) {
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM customers WHERE id = $1`, customerID).Scan(&name); err != nil {
		return Contact{}, ErrNoRecipient
	}
	return Contact{CustomerID: customerID, Name: name}, nil
}

func (poolNameSource) CustomerForInvoice(context.Context, string) (string, error) {
	return "", ErrNoRecipient
}

func (poolNameSource) CustomerForOrder(context.Context, string) (string, error) {
	return "", ErrNoRecipient
}

func TestIntegration_ARSweep_ConsentReadsJoinTx_NoPoolCycle(t *testing.T) {
	// poolSize is the ONE constant the scenario's connection accounting hangs on: the scenario pool
	// is built at it and the candidate count is derived from it.
	const poolSize = 4
	const stallBound = 10 * time.Second

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("AR sweep pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	// 4 is pgx's default on a host with four or fewer CPUs (max(4, CPUs)), pinned explicitly so the starvation reproduces
	// identically on any machine.
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Helper connections come from a SEPARATE pool so they never eat one of the scenario's four.
	helperCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL for the helper pool: %v", err)
	}
	helperCfg.MaxConns = 2
	helper, err := pgxpool.NewWithConfig(ctx, helperCfg)
	if err != nil {
		t.Fatalf("build helper pool: %v", err)
	}
	t.Cleanup(helper.Close)
	if err := helper.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	now := businessHours(t)
	candidates := poolSize

	var branch string
	if err := helper.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY created_at LIMIT 1`).Scan(&branch); err != nil {
		t.Fatalf("no branch in this database (run the seed first): %v", err)
	}

	// Distinct customers, each with consent and one overdue invoice, so no candidate suppresses
	// another through the cooldown and every one reaches the consent and recipient reads.
	var marker string
	if err := helper.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&marker); err != nil {
		t.Fatalf("mint marker: %v", err)
	}
	invs := make([]AgingInvoice, 0, candidates)
	custIDs := make([]string, 0, candidates)
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range custIDs {
			_, _ = helper.Exec(c, `DELETE FROM ar_notice_log WHERE customer_id = $1`, id)
			_, _ = helper.Exec(c, `DELETE FROM sms_outbox WHERE payload->>'customer_id' = $1`, id)
			_, _ = helper.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, id)
			_, _ = helper.Exec(c, `DELETE FROM customer_sms_prefs WHERE customer_id = $1`, id)
			_, _ = helper.Exec(c, `DELETE FROM customers WHERE id = $1`, id)
		}
	})
	asOf := now.Format("2006-01-02")
	for i := 1; i <= candidates; i++ {
		var cust string
		if err := helper.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, payment_terms_days)
			VALUES ($1, $2, 30) RETURNING id::text`,
			fmt.Sprintf("ARCYC-%s-%d", marker[:8], i), fmt.Sprintf("ARCYC fixture %s %d", marker[:8], i)).Scan(&cust); err != nil {
			t.Fatalf("create customer %d: %v", i, err)
		}
		custIDs = append(custIDs, cust)
		if _, err := helper.Exec(ctx, `
			INSERT INTO customer_sms_prefs (customer_id, phone_e164, sms_verified, verified_at)
			VALUES ($1, $2, TRUE, now())`, cust, fmt.Sprintf("+1613555%04d", 200+i)); err != nil {
			t.Fatalf("seed consent %d: %v", i, err)
		}
		var invID string
		invNo := fmt.Sprintf("ARCYC-%s-%d", marker[:8], i)
		if err := helper.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, total_cents, balance_cents)
			VALUES ($1, $2, $3, 'FINALIZED', $5::date - 35 - 30, $5::date - 35, $4, $4, $4)
			RETURNING id::text`, invNo, cust, branch, int64(100000+i), asOf).Scan(&invID); err != nil {
			t.Fatalf("create invoice %d: %v", i, err)
		}
		invs = append(invs, AgingInvoice{
			InvoiceID: invID, InvoiceNo: invNo, CustomerID: cust, BalanceCents: int64(100000 + i),
			DueDate: now.AddDate(0, 0, -35), DaysPastDue: 35,
		})
	}

	resolver := NewConsentResolver(NewPrefsStore(pool), slog.Default()).WithContactSource(poolNameSource{pool: pool})
	sweep := NewARSweep(pool, NewOutbox(pool), resolver, slog.Default()).
		WithClock(func() time.Time { return now })

	// The blocker: a transaction on the helper pool holds a table lock that conflicts with the ledger
	// insert, so every candidate parks at claimBucket with its transaction already open.
	blocker, err := helper.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	blockerOpen := true
	releaseBlocker := func() {
		if !blockerOpen {
			return
		}
		blockerOpen = false
		rbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = blocker.Rollback(rbCtx)
	}
	t.Cleanup(releaseBlocker)
	if _, err := blocker.Exec(ctx, `LOCK TABLE ar_notice_log IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		t.Fatalf("blocker lock: %v", err)
	}

	type outcome struct {
		tag      string
		duration time.Duration
		enqueued bool
		err      error
	}

	// One bound for the whole scenario, armed when the blocker is released: every candidate still
	// waiting at the bound is the stall.
	scenarioCtx, scenarioCancel := context.WithCancel(ctx)
	defer scenarioCancel()
	var stalled bool
	var stalledMu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(candidates)
	outCh := make(chan outcome, candidates)
	var releasedAt time.Time
	var releasedMu sync.Mutex
	for i := 0; i < candidates; i++ {
		go func(i int) {
			defer wg.Done()
			tag := fmt.Sprintf("candidate-%d", i+1)
			enq, _, err := sweep.considerOne(scenarioCtx, invs[i], 30, now, map[string]bool{})
			releasedMu.Lock()
			d := time.Since(releasedAt)
			releasedMu.Unlock()
			outCh <- outcome{tag, d, enq, err}
		}(i)
	}

	// Wait until every candidate is parked on the blocker's lock with its transaction open.
	parkDeadline := time.Now().Add(stallBound)
	for {
		var parked int
		if err := helper.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'
			   AND state = 'active' AND query ILIKE '%INSERT INTO ar_notice_log%'`).Scan(&parked); err != nil {
			t.Fatalf("count parked candidates: %v", err)
		}
		if parked == candidates {
			break
		}
		if time.Now().After(parkDeadline) {
			scenarioCancel()
			wg.Wait()
			t.Fatalf("setup: %d of %d candidates parked at claimBucket within %s", parked, candidates, stallBound)
		}
		time.Sleep(20 * time.Millisecond)
	}

	releasedMu.Lock()
	releasedAt = time.Now()
	releasedMu.Unlock()
	timer := time.AfterFunc(stallBound, func() {
		stalledMu.Lock()
		stalled = true
		stalledMu.Unlock()
		scenarioCancel()
	})
	defer timer.Stop()
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("blocker release: %v", err)
	}
	blockerOpen = false

	wg.Wait()
	close(outCh)
	stalledMu.Lock()
	wasStall := stalled
	stalledMu.Unlock()

	var outcomes []outcome
	for o := range outCh {
		if o.err != nil {
			t.Logf("%s: did not complete, stopped at %s: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
		} else {
			t.Logf("%s: completed in %s (enqueued=%v)", o.tag, o.duration.Round(time.Millisecond), o.enqueued)
		}
		outcomes = append(outcomes, o)
	}

	var failed []outcome
	for _, o := range outcomes {
		if o.err != nil {
			failed = append(failed, o)
		}
	}
	if len(failed) > 0 {
		var other error
		for _, o := range failed {
			if !errors.Is(o.err, context.Canceled) {
				other = o.err
			}
		}
		if wasStall && other == nil {
			t.Fatalf("THE STALL REPRODUCED: %d of %d candidates did not finish within the %s bound. Each held "+
				"one pool connection inside its transaction and asked the pool for another (consent and "+
				"contact reads), so the %d in flight pinned every connection with no lock waited on; "+
				"per-candidate timings are logged above.", len(failed), candidates, stallBound, poolSize)
		}
		t.Fatalf("a candidate failed for a reason other than the stall: %v", failed[0].err)
	}

	for i, o := range outcomes {
		if !o.enqueued {
			t.Errorf("%s: not enqueued with the fix in place", o.tag)
		}
		_ = i
	}
	// The completion claim, checked against the database: one queued notice and one ledger row per
	// customer for the notified bucket.
	var queued int
	if err := helper.QueryRow(ctx, `
		SELECT count(*) FROM sms_outbox
		 WHERE topic = $1 AND payload->>'customer_id' = ANY($2)`, TopicARNotice, custIDs).Scan(&queued); err != nil {
		t.Fatalf("count queued: %v", err)
	}
	if queued != candidates {
		t.Errorf("queued notices = %d, want %d", queued, candidates)
	}
}
