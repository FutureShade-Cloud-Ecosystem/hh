//go:build integration

// holds_pool_cycle_integration_test.go, the money-class pool-cycle regression for the credit hold read
// (row 784 item 5). The defect: HoldAwareCreditGate is consulted INSIDE the caller's write transaction
// (the quote conversion locks the quote row FOR UPDATE and calls the gate between the lock and the
// order INSERT, quote/repository.go Convert; the POS ring consults the same gate through the widened
// pos.CreditGate seam), but Repository.HasActiveHold read credit_holds through the POOL. A conversion
// that already holds the quote row and its own pool connection asked the pool for a SECOND connection,
// and at the pool the deploy runs (pgx sets max(4, CPUs), nothing in the deploy overrides it) that
// forms a cycle Postgres cannot see: the holder waits for a free connection, every other connection
// waits on the holder's row lock, and the process stalls to the timeout. It is the same cycle class
// the inventory lane closed for the reservation policy read and the pos ACCOUNT_CREDIT rail closed
// with the ReadCustomerTx discipline.
//
// The required shape, fail first: the pool size is set ON THE COMMAND, in this leg's throwaway
// database URL (the pool_max_conns query parameter; no export, no in-code override, no shared
// database), one held conversion (a transaction that has locked the quote row the contenders need)
// plus pool-size-minus-one contender transactions, all released together under one shared bound, the
// hold read running on the gate's real composition (NewHoldAwareCreditGate over the real repository).
// On the pre-fix tree the held conversion's gate read cannot get a connection, the shared bound fires,
// and this leg FAILS with the stall observed and the timings recorded. With the hold read joined to
// the caller's context-carried transaction (AGENTS.md: transactions span services through a
// context-carried transaction), every participant completes and the timings are printed.
//
// Connection accounting is the point, so it is exact: every participant opens its transaction BEFORE
// the release, and an open transaction holds its pool connection whatever it is doing, so at the
// release the pool owns zero free connections: the held conversion's tx plus the contender txs.
//
// Run (own throwaway container, removed afterwards, a non-default port):
//
//	docker run --name hh-r10-holds-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hh_r10_holds \
//	  -p 5679:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5679/hh_r10_holds?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5679/hh_r10_holds?sslmode=disable&pool_max_conns=4' \
//	  go test -tags integration ./internal/till/ -run TestHasActiveHold_GateReadJoinsCallerTx_NoPoolCycle -count=1 -v -p 1
package till

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Fixed ids so the leg's rows are deterministic and its cleanup cannot collide with another leg's.
const (
	holdsCycleBranchID   = "a0d10000-0000-4000-8000-000000000001"
	holdsCycleCustomerID = "a0d10000-0000-4000-8000-000000000002"
	holdsCycleQuoteID    = "a0d10000-0000-4000-8000-000000000003"
)

func TestHasActiveHold_GateReadJoinsCallerTx_NoPoolCycle(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("hold gate pool-cycle leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	// THE POOL SIZE IS THE POINT and it is set ON THE COMMAND: the URL this leg runs under carries
	// pool_max_conns, and the leg builds the cycle at exactly that size (the held conversion plus
	// size-minus-one contenders). At the deploy default of 4 that is the documented stall.
	if cfg.MaxConns < 2 {
		t.Skipf("hold gate pool-cycle leg: pool_max_conns on the command's DATABASE_URL must be at least 2 to form the cycle, got %d", cfg.MaxConns)
	}
	poolSize := int(cfg.MaxConns)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}

	// The world the gate reads: one branch, one contractor customer with ample limit, an ACCEPTED
	// quote, and an ACTIVE credit hold on the customer (0035). Cleanup is LIFO, children first, and
	// runs before the pool closes (its Close was registered first).
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, loc_type, code, name)
		VALUES ($1, $1, 'BRANCH', 'HOLDSCYC', 'hold gate pool cycle leg branch')`,
		holdsCycleBranchID); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM locations WHERE id = $1`, holdsCycleBranchID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ($1, 'HOLDSCYC-1', 'hold gate pool cycle leg customer', 'CONTRACTOR', $2, 500000)`,
		holdsCycleCustomerID, holdsCycleBranchID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, holdsCycleCustomerID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, 'HOLDSCYC-Q1', $2, $3, 'ACCEPTED', 10000, 1300, 11300)`,
		holdsCycleQuoteID, holdsCycleCustomerID, holdsCycleBranchID); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, holdsCycleQuoteID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO credit_holds (customer_id, status, reason)
		VALUES ($1, 'ACTIVE', 'hold gate pool cycle leg')`, holdsCycleCustomerID); err != nil {
		t.Fatalf("seed credit hold: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM credit_holds WHERE customer_id = $1`, holdsCycleCustomerID)
	})

	gate := NewHoldAwareCreditGate(NewRepository(pool))

	const (
		// The bound the red run fails at: seconds, not minutes. The green path completes in
		// milliseconds, so the bound has room to spare on a loaded machine and still caps the stall.
		stallBound = 10 * time.Second
	)
	contenders := poolSize - 1

	type outcome struct {
		tag     string
		verdict *identity.ActionBlocked // the gate's refusal; nil means the gate never answered
		// checkDur is the gate Check's own duration; lockWait, when set, is how long the
		// participant queued on the quote row before its gate Check ran.
		checkDur time.Duration
		lockWait time.Duration
		lockErr  error
		begunErr error // non nil when the transaction never opened
		begun    bool // the participant's transaction opened (its pool connection is held)
	}

	// One deadline for the whole scenario, started when the participants are released together: on the
	// pre-fix tree every participant still waiting at the bound is the stall, and no single participant
	// can slip past another's expiry and stumble through a half-released pool.
	scenarioCtx, scenarioCancel := context.WithTimeout(ctx, stallBound)
	defer scenarioCancel()

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
				outCh <- outcome{tag: tag, begunErr: err}
				return
			}
			begun.Done()
			<-hold // every contender transaction is open (its connection held) before anyone is released
			start := time.Now()
			_, lockErr := tx.Exec(scenarioCtx, `
				SELECT status FROM quotes WHERE id = $1 FOR UPDATE`, holdsCycleQuoteID)
			lockWait := time.Since(start)
			if lockErr != nil {
				outCh <- outcome{tag: tag, lockWait: lockWait, lockErr: lockErr, begun: true}
			} else {
				// THE SEAM UNDER TEST: the caller's transaction goes into the context the gate reads.
				// On the pre-fix tree no context-carried transaction exists, the plain context rides
				// (nothing is carried), and the gate's hold read asks the pool for a second
				// connection. The fix commit points this line at WithCallerTx(ctx, tx).
				ctxT := ctx
				gateStart := time.Now()
				blk := gate.Check(ctxT, holdsCycleCustomerID, 11300, 0, 500000, true, holdsCycleBranchID, false)
				outCh <- outcome{tag: tag, verdict: blk, checkDur: time.Since(gateStart), lockWait: lockWait, begun: true}
			}
			rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = tx.Rollback(rbCtx)
			rbCancel()
		}(i)
	}

	// The held conversion: its transaction locks the quote row FIRST, mirroring a real conversion that
	// has already locked the quote by the time it reaches the gate.
	held, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("held conversion begin: %v", err)
	}
	if _, err := held.Exec(ctx, `
		SELECT status FROM quotes WHERE id = $1 FOR UPDATE`, holdsCycleQuoteID); err != nil {
		rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = held.Rollback(rbCtx)
		rbCancel()
		t.Fatalf("held conversion lock: %v", err)
	}

	begun.Wait()                       // every pool connection is now held: the held tx plus the contender txs
	time.Sleep(250 * time.Millisecond) // settle the scheduler before the release

	close(hold)
	// THE SEAM UNDER TEST, held side: on the pre-fix tree the plain context rides, the gate's hold read
	// runs on the POOL, and with every connection held it stops at the bound and the gate answers fail
	// closed. The fix commit points this line at WithCallerTx(scenarioCtx, held).
	heldCtxT := scenarioCtx
	heldStart := time.Now()
	heldBlk := gate.Check(heldCtxT, holdsCycleCustomerID, 11300, 0, 500000, true, holdsCycleBranchID, false)
	heldDur := time.Since(heldStart)
	rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = held.Rollback(rbCtx)
	rbCancel()

	done.Wait()
	close(outCh)
	outcomes := make([]outcome, 0, contenders)
	begunCount := 0
	for o := range outCh {
		if o.begun {
			begunCount++
		}
		switch {
		case !o.begun:
			t.Logf("%s: never opened its transaction (begin error: %v): the rig did not hold the full pool", o.tag, o.begunErr)
		case o.lockErr != nil:
			t.Logf("%s: queued on the quote row for %s, then the bound fired: %v", o.tag, o.lockWait.Round(time.Millisecond), o.lockErr)
		case o.verdict != nil:
			t.Logf("%s: queued on the quote row for %s, the gate refused in %s (%s)", o.tag, o.lockWait.Round(time.Millisecond), o.checkDur.Round(time.Millisecond), o.verdict.Detail)
		default:
			t.Logf("%s: queued on the quote row for %s, the gate never answered within the bound", o.tag, o.lockWait.Round(time.Millisecond))
		}
		outcomes = append(outcomes, o)
	}
	if begunCount < contenders {
		t.Fatalf("the rig did not hold the full pool (%d of %d contenders opened a transaction): the cycle is unproven, fix the rig before trusting this leg", begunCount, contenders)
	}

	// The held conversion's gate read must COMPLETE, inside the bound, with the real hold verdict the
	// seeded ACTIVE hold earns. On the pre-fix tree the read runs on the pool, the pool is fully held,
	// the read stops at the bound, and the gate answers fail closed ("status unavailable"): that
	// verdict at the bound IS the stall, the cycle Postgres cannot see.
	if heldBlk == nil {
		t.Fatalf("THE GATE DID NOT REFUSE: the held conversion's Check completed with no block after %s; an ACCOUNT tender under an ACTIVE hold must be refused", heldDur.Round(time.Millisecond))
	}
	if strings.Contains(heldBlk.Detail, "unavailable") {
		t.Fatalf("THE STALL REPRODUCED: the held conversion's hold read did not complete within the %s bound "+
			"(stopped at %s, the gate answered fail closed: %q). The read ran on the POOL and asked for a second "+
			"connection while the held quote row kept every other connection waiting on its lock; timings for the "+
			"%d contenders are logged above.", stallBound, heldDur.Round(time.Millisecond), heldBlk.Detail, contenders)
	}
	if !strings.Contains(heldBlk.Detail, "on a credit hold") || heldBlk.Policy != identity.PolicyCOD {
		t.Fatalf("the held conversion's gate verdict is not the hold refusal: policy %q detail %q", heldBlk.Policy, heldBlk.Detail)
	}
	t.Logf("held-conversion: the hold read completed on the caller's transaction in %s", heldDur.Round(time.Millisecond))

	for _, o := range outcomes {
		if o.lockErr != nil {
			t.Errorf("%s: did not take the quote row with the fix in place after %s: %v", o.tag, o.lockWait.Round(time.Millisecond), o.lockErr)
			continue
		}
		if o.verdict == nil {
			t.Errorf("%s: the gate never answered within the bound with the fix in place", o.tag)
			continue
		}
		if !strings.Contains(o.verdict.Detail, "on a credit hold") || o.verdict.Policy != identity.PolicyCOD {
			t.Errorf("%s: verdict is not the hold refusal: policy %q detail %q", o.tag, o.verdict.Policy, o.verdict.Detail)
		}
	}
}
