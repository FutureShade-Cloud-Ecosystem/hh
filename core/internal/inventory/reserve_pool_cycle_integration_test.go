//go:build integration

// reserve_pool_cycle_integration_test.go, the money-class connection-starvation regression for the
// reservation flow. The defect: ReserveTx runs in the CALLER's transaction, but its allocation-policy
// read went through the POOL, so every in-flight reservation held one connection and asked for a
// SECOND. Pool-size reservations in flight together (a burst of confirms or quote holds on ANY
// products, no shared row needed) pin every connection of the pool and each waits for one more: the
// sessions sit idle in transaction (no lock is waited on; the red run's pg_locks snapshot shows zero
// ungranted waits) and the database's deadlock detector is blind to the client-side half. The
// process stalls to the action timeout. The held confirm's row lock below is not needed for the
// stall; it makes the scenario realistic and orders the green path's projections.
//
// The required shape, fail first: a pool built at 4 EXPLICITLY (so the leg measures the deploy
// default on any machine), one held confirm (a transaction that has locked the product's
// stock_balances row, the row every contender's projection update needs) plus pool-size-minus-one
// contender transactions, all released together against one product. On the pre-fix tree the
// confirm's policy read cannot get a connection, the shared bound fires, and this leg FAILS with the
// stall and the timings recorded. With the policy read joined to the caller's transaction, every
// participant completes and the timings are printed.
//
// Connection accounting is the point, so it is exact: the scenario pool is 4 connections and the
// scenario uses exactly 4 (the confirm's tx plus three contender txs). The DIB-396 drift lease this
// leg owes for planting a balance with no matching move is taken on its OWN single-connection pool,
// so the fence stays held without eating one of the four scenario connections.
//
// The pool size is set HERE, in the leg (cfg.MaxConns = 4 below), from the leg's own throwaway
// database URL carried on the command; no export, no shared database:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5571/hh_r10_reserve?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/ -run TestReserveTx_PolicyReadJoinsCallerTx_NoPoolCycle -count=1 -v -p 1
package inventory

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReserveTx_PolicyReadJoinsCallerTx_NoPoolCycle(t *testing.T) {
	// poolSize is the ONE constant the scenario's connection accounting hangs on: the pool is
	// built at it below and the contender count is derived from it (pool-size-minus-one).
	const poolSize = 4

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("reserve pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	// THE POOL SIZE IS THE POINT: 4 is the deploy default the defect stalls at, and it is pinned
	// explicitly rather than left to max(4, CPUs) so the starvation reproduces identically
	// everywhere. Both the pool and the contender count derive from the one poolSize constant, so
	// they can never disagree (a disagreement would hang the leg instead of failing it).
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	// The DIB-396 drift lease on its own connection: the leg plants a stock_balances row with no
	// matching move, and the lease fences the whole-database drift readers without consuming one of
	// the four scenario connections the cycle below is built from.
	leaseCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL for the lease pool: %v", err)
	}
	leaseCfg.MaxConns = 1
	leasePool, err := pgxpool.NewWithConfig(context.Background(), leaseCfg)
	if err != nil {
		t.Fatalf("build lease pool: %v", err)
	}
	t.Cleanup(leasePool.Close)
	dib396LeaseShared(t, leasePool, dib396StockDriftLease)

	svc := NewService(NewRepository(pool))
	ctx := context.Background()

	p := mkProduct(t, pool, "RESERVECyc")
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, 1000)`, p, branch1); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	// Runs BEFORE mkProduct's teardown (cleanup is LIFO): the holds this leg wrote are gone before
	// the balances row they project onto is deleted.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM stock_reservations WHERE product_id = $1`, p)
	})

	const (
		// The bound the red run fails at: seconds, not minutes. The green path completes in
		// milliseconds, so the bound has room to spare on a loaded machine and still caps the stall.
		stallBound = 10 * time.Second
	)
	contenders := poolSize - 1

	// source_ref_id is a UUID column and created_by carries an FK to users (0115), so the tags are
	// UUIDs and the actor is the seeded staff user the composition legs use.
	input := func(n int) ReserveInput {
		return ReserveInput{
			ProductID:   p,
			BranchID:    branch1,
			ResType:     ResTypeOrder,
			QtyBase:     1,
			SourceType:  "reserve-pool-cycle-leg",
			SourceRefID: fmt.Sprintf("a0c10000-0000-4000-8000-%012d", n),
			CreatedBy:   "40000061-0000-4000-9000-000000000001",
		}
	}

	type outcome struct {
		tag      string
		duration time.Duration
		err      error
	}

	// One deadline for the whole scenario, started when the participants are released together: on
	// the pre-fix tree every participant still waiting at the bound is the stall, and no single
	// participant can slip past another's expiry and stumble through a half-released pool.
	scenarioCtx, scenarioCancel := context.WithTimeout(ctx, stallBound)
	defer scenarioCancel()

	// The held confirm: its transaction locks the product's stock_balances row FIRST, mirroring a
	// real confirm that has already written its order rows by the time it reaches the reserve.
	held, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("held confirm begin: %v", err)
	}
	if _, err := held.Exec(ctx, `
		SELECT qty_reserved FROM stock_balances
		 WHERE product_id = $1 AND location_id = $2
		 FOR UPDATE`, p, branch1); err != nil {
		rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = held.Rollback(rbCtx)
		rbCancel()
		t.Fatalf("held confirm lock: %v", err)
	}

	var begun sync.WaitGroup
	begun.Add(contenders)
	var done sync.WaitGroup
	done.Add(contenders)
	outCh := make(chan outcome, contenders)
	hold := make(chan struct{})

	for i := 1; i <= contenders; i++ {
		go func(i int) {
			defer done.Done()
			tag := fmt.Sprintf("contender-%d", i)
			tx, err := pool.Begin(ctx)
			if err != nil {
				begun.Done()
				outCh <- outcome{tag, 0, fmt.Errorf("begin: %w", err)}
				return
			}
			begun.Done()
			<-hold // every contender transaction is open before anyone is released
			start := time.Now()
			_, err = svc.ReserveTx(scenarioCtx, tx, input(i))
			d := time.Since(start)
			if err == nil {
				err = tx.Commit(scenarioCtx)
			}
			if err != nil {
				// The bound fired mid-stall and the transaction still owns a pool connection; a
				// rollback under the spent context cannot free it. A fresh bounded context releases
				// the connection, so pool.Close in cleanup never waits on a dead goroutine's tx.
				rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = tx.Rollback(rbCtx)
				rbCancel()
			}
			outCh <- outcome{tag, d, err}
		}(i)
	}

	begun.Wait()                       // four of four pool connections now held: the confirm's tx plus three contender txs
	time.Sleep(250 * time.Millisecond) // settle the scheduler before the release

	start := time.Now()
	close(hold)
	_, confirmErr := svc.ReserveTx(scenarioCtx, held, input(0))
	confirmDur := time.Since(start)
	if confirmErr == nil {
		confirmErr = held.Commit(scenarioCtx)
	}

	done.Wait()
	close(outCh)
	outcomes := make([]outcome, 0, contenders)
	for o := range outCh {
		if o.err != nil {
			t.Logf("%s: did not complete within the bound, stopped at %s: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
		} else {
			t.Logf("%s: completed in %s", o.tag, o.duration.Round(time.Millisecond))
		}
		outcomes = append(outcomes, o)
	}

	if confirmErr != nil {
		rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = held.Rollback(rbCtx)
		rbCancel()
		t.Fatalf("THE STALL REPRODUCED: the held confirm's ReserveTx did not complete within the %s bound "+
			"(stopped at %s, err: %v). The policy read on the pool asked for a second connection while the "+
			"confirm's row locks held the rest of the pool; timings for the %d contenders are logged above.",
			stallBound, confirmDur.Round(time.Millisecond), confirmErr, contenders)
	}
	t.Logf("held-confirm: completed in %s", confirmDur.Round(time.Millisecond))
	for _, o := range outcomes {
		if o.err != nil {
			t.Errorf("%s: ReserveTx failed with the fix in place after %s: %v", o.tag, o.duration.Round(time.Millisecond), o.err)
		}
	}
	if t.Failed() {
		_ = held.Rollback(context.Background())
		return
	}

	// The completion claim, checked against the database rather than only the return values: the
	// confirm and every contender left one ACTIVE HARD hold each, the projection carries all of it.
	var active, projected int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_reservations
		 WHERE product_id = $1 AND status = 'ACTIVE' AND hold_mode = 'HARD'`, p).Scan(&active); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT qty_reserved::float8 FROM stock_balances
		 WHERE product_id = $1 AND location_id = $2`, p, branch1).Scan(&projected); err != nil {
		t.Fatalf("read projection: %v", err)
	}
	if active != poolSize || projected != poolSize {
		t.Errorf("after completion: %d ACTIVE HARD holds, projection carries %d, want %d and %d",
			active, projected, poolSize, poolSize)
	}
}
