//go:build integration

// postbill_pool_integration_test.go, the concurrency leg for PostBill's reads inside its own transaction.
//
// THE DEFECT CLASS. PostBill holds a pool connection (its transaction) and, on the unfixed tree, asks the SAME
// pool for a second one to read the PO and the match tolerance. With the pool at four connections and four
// bills posting together, each caller holds one connection and waits for a fifth that never comes. Postgres
// sees nothing wrong (the sessions sit idle in transaction, no lock is waited on), so the process stalls until
// each action's timeout. Money stays correct (everything rolls back); availability does not.
//
// THE SHAPE. A blocker transaction on its own single-connection pool holds the shared PO lines FOR UPDATE, the
// row every PostBill needs after its transaction opens. Four callers (poolSize, the scenario pool's size) run
// against a scenario pool built at that size, so the overlap is forced, not left to goroutine timing. The
// helper connections (blocker, monitor, setup) come from other pools and never eat a scenario connection.
//
// The GL seams are fakes here on purpose: the real gl.Service reads through its own pool handle (ResolveRule
// and PostExternal), which is a separate site outside purchasing. The leg isolates the purchasing reads.
package purchasing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// poolSize is the scenario pool's connection cap and the number of concurrent posters derives from it.
	poolSize = 4
	// stallDeadline bounds the whole leg so a stall fails in seconds rather than hanging the suite.
	stallDeadline = 10 * time.Second
	// overlapWindow is how long the leg waits for every caller to be parked on the blocker's lock before it
	// releases the blocker anyway (a caller starved earlier than the lock never gets there).
	overlapWindow = 3 * time.Second

	postBillScenarioApp = "postbill_pool_scenario"
)

// fakeBillGL posts nothing and touches no database, a journal id of "" is stored as NULL.
type fakeBillGL struct{}

func (fakeBillGL) PostJournal(context.Context, GLJournal) (string, error) { return "", nil }

func (fakeBillGL) PostJournalTx(context.Context, pgx.Tx, GLJournal) (string, error) { return "", nil }

// fakeBillRules resolves every role to a fixed code without a query.
type fakeBillRules struct{}

func (fakeBillRules) ResolveRule(context.Context, string, string, string) (string, error) {
	return "2100", nil
}

func (fakeBillRules) ResolveRuleTx(context.Context, pgx.Tx, string, string, string) (string, error) {
	return "2100", nil
}

type postResult struct {
	err     error
	elapsed time.Duration
}

func TestIntegration_PostBill_ConcurrentPostsDoNotStarveThePool(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}

	// Setup rides the ordinary harness pool (never the scenario pool).
	h := newHarness(t)
	product := h.newProduct(t, "pbpool")
	po := h.orderedPO(t, product, float64(poolSize*10), 2500)
	lineID := po.Lines[0].ID
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: float64(poolSize * 10)}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	billIDs := make([]string, poolSize)
	for i := range billIDs {
		b, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
			BillNo: billNo(fmt.Sprintf("pool%d", i)), POID: po.ID,
			Lines: []BillLineParams{{POLineID: lineID, QtyBilledBase: 10, UnitCostCents: 2500}},
		})
		if err != nil {
			t.Fatalf("create bill %d: %v", i, err)
		}
		billIDs[i] = b.ID
	}

	// The scenario pool, built at the size the contender count derives from.
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.MaxConns = int32(poolSize)
	cfg.ConnConfig.RuntimeParams["application_name"] = postBillScenarioApp
	scn, err := pgxpool.NewWithConfig(h.ctx, cfg)
	if err != nil {
		t.Fatalf("scenario pool: %v", err)
	}
	t.Cleanup(scn.Close)
	svc := NewService(NewRepository(scn)).
		WithGLPoster(fakeBillGL{}).
		WithRuleResolver(fakeBillRules{}).
		withCaller(ctxCaller(h.user))

	// The blocker, on its own single-connection pool, holds the PO lines every PostBill locks after it opens.
	bcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	bcfg.MaxConns = 1
	bpool, err := pgxpool.NewWithConfig(h.ctx, bcfg)
	if err != nil {
		t.Fatalf("blocker pool: %v", err)
	}
	t.Cleanup(bpool.Close)
	btx, err := bpool.Begin(h.ctx)
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = btx.Rollback(context.Background())
		}
	}
	t.Cleanup(release)
	if _, err := btx.Exec(h.ctx, `SELECT 1 FROM purchasing_po_lines WHERE po_id = $1 FOR UPDATE`, po.ID); err != nil {
		t.Fatalf("blocker lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(h.ctx, stallDeadline)
	defer cancel()
	start := time.Now()

	results := make([]postResult, poolSize)
	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t0 := time.Now()
			_, err := svc.PostBill(ctx, billIDs[i], PostBillRequest{})
			results[i] = postResult{err: err, elapsed: time.Since(t0)}
		}(i)
	}

	// Hold the blocker until every caller is parked on its lock, or the overlap window ends.
	parked := 0
	for until := time.Now().Add(overlapWindow); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		if err := h.pool.QueryRow(h.ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE application_name = $1 AND wait_event_type = 'Lock'`, postBillScenarioApp).Scan(&parked); err != nil {
			t.Fatalf("monitor: %v", err)
		}
		if parked >= poolSize {
			break
		}
	}
	release()
	wg.Wait()

	var stalled, failed []string
	for i, r := range results {
		line := fmt.Sprintf("caller %d: %v after %s", i, r.err, r.elapsed.Round(time.Millisecond))
		switch {
		case r.err == nil:
		case errors.Is(r.err, context.DeadlineExceeded) && ctx.Err() != nil:
			stalled = append(stalled, line)
		default:
			failed = append(failed, line)
		}
	}
	if len(failed) > 0 {
		t.Fatalf("PostBill failed for a reason other than a stall:\n%s", strings.Join(failed, "\n"))
	}
	if len(stalled) > 0 {
		t.Fatalf("STALL: %d of %d concurrent PostBill calls hit the %s deadline at pool size %d "+
			"(connection starvation: each holds a connection and waits for a second from the same pool; %d callers parked on the lock when it was released)\n%s",
			len(stalled), poolSize, stallDeadline, poolSize, parked, strings.Join(stalled, "\n"))
	}
	t.Logf("all %d posts finished in %s with %d parked on the lock", poolSize, time.Since(start).Round(time.Millisecond), parked)

	for i, id := range billIDs {
		b, err := h.svc.GetBill(h.ctx, id)
		if err != nil {
			t.Fatalf("read bill %d: %v", i, err)
		}
		if b.Status != BillPosted {
			t.Fatalf("bill %d status = %s, want POSTED", i, b.Status)
		}
	}
}
