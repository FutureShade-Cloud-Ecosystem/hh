//go:build integration

// syncscope_pool_integration_test.go, the row 786 leg for the anchor's
// own unit (HH-LOAD-1 LD-3 part B): pool of four, the suite holding
// the fourth connection, three contending anchor writes, every read
// and write of the one list price write path on the caller's own
// transaction. The unit set check a caller named unit adds reads
// beside the write on that same transaction, so all three finish; a
// write whose set check reaches for the pool instead is bitten by the
// stall (the last test), which is what proves the leg can fail.
//
// The lock order leg: the unit check takes the products row FOR NO KEY
// UPDATE, and it takes it BEHIND the price_matrix allocator's lock,
// allocator first, lock after. Every list price writer draws the
// allocator before any products row, so two writers applying lines in
// opposite product order serialise on the allocator and converge (the
// convergence leg); the bite swaps the order and watches the same two
// writers deadlock, which is what proves the order is load bearing.
//
// Run with (the URL is the command's own throwaway database):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5715/ld3bpool?sslmode=disable' \
//	  go test -tags=integration ./internal/pricing/ -run TestMatrixWrite -count=1 -v -p 1
package pricing

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// matrixPool builds the pool the connection accounting hangs on: four
// EXPLICITLY (the deploy default the stall reproduces at, pinned so
// the leg measures the same shape on any machine), from the command's
// own database URL.
func matrixPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("matrix pool leg: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
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

// newMatrixPoolFixture is the unit fixture over the pool of four.
func newMatrixPoolFixture(t *testing.T) *unitFixture {
	t.Helper()
	return newUnitFixtureWithPool(t, matrixPool(t))
}

// matrixWriteOutcome is one contender's result.
type matrixWriteOutcome struct {
	err error
	dur time.Duration
}

// runMatrixContenders begins every contender's transaction first (the
// pool of four fully held: the suite's connection plus three
// transactions), releases them together and runs write against the
// executor each contender carries. Every contender rolls back its own
// transaction on a failed write under a fresh context, so no
// goroutine dies holding a connection. Returns the contenders' own
// fixture products, so every count a caller asserts is scoped to what
// this leg seeded and never to the database at large.
func runMatrixContenders(t *testing.T, f *unitFixture, bound time.Duration,
	write func(scenarioCtx context.Context, tx pgx.Tx, id string, n int) error) ([]string, []matrixWriteOutcome) {
	t.Helper()
	ctx := context.Background()
	products := []string{
		f.product("PC", "BOX", 100),
		f.product("PC", "BOX", 100),
		f.product("PC", "BOX", 100),
	}

	held, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()

	const contenders = 3
	scenarioCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	var begun, done sync.WaitGroup
	begun.Add(contenders)
	done.Add(contenders)
	out := make(chan matrixWriteOutcome, contenders)
	hold := make(chan struct{})

	for n, id := range products {
		go func(id string, n int) {
			defer done.Done()
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				begun.Done()
				out <- matrixWriteOutcome{err: fmt.Errorf("begin: %w", err)}
				return
			}
			begun.Done()
			<-hold // every contender transaction is open before anyone is released
			start := time.Now()
			err = write(scenarioCtx, tx, id, n)
			dur := time.Since(start)
			if err == nil {
				err = tx.Commit(scenarioCtx)
			}
			if err != nil {
				rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = tx.Rollback(rbCtx)
				rbCancel()
			}
			out <- matrixWriteOutcome{err: err, dur: dur}
		}(id, n)
	}

	begun.Wait()                       // four of four pool connections now held
	time.Sleep(250 * time.Millisecond) // settle the scheduler before the release
	close(hold)
	done.Wait()
	close(out)
	outcomes := make([]matrixWriteOutcome, 0, contenders)
	for o := range out {
		outcomes = append(outcomes, o)
	}
	return products, outcomes
}

// TestMatrixWritePoolFourEveryReadAndWriteOnTheCallerTransaction: the
// three contending writes, unit named, all complete on their own
// transactions against the fully held pool of four.
func TestMatrixWritePoolFourEveryReadAndWriteOnTheCallerTransaction(t *testing.T) {
	f := newMatrixPoolFixture(t)
	products, outcomes := runMatrixContenders(t, f, 20*time.Second,
		func(scenarioCtx context.Context, tx pgx.Tx, id string, n int) error {
			return applyMatrixPrice(scenarioCtx, tx, matrixPriceWrite{
				ProductID: id, Cents: 900 + int64(n), PriceUOM: "BOX",
			})
		})
	for k, o := range outcomes {
		if o.err != nil {
			t.Fatalf("contender %d: %v (a write needed a connection beyond its transaction)", k, o.err)
		}
		t.Logf("contender %d: completed in %s", k, o.dur.Round(time.Millisecond))
	}
	// The count is scoped to the leg's own fixture products: a BOX
	// anchor any other test or seed ever lands must not move it.
	var anchored int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_list_prices WHERE price_uom = 'BOX' AND product_id IN ($1::uuid, $2::uuid, $3::uuid)`,
		products[0], products[1], products[2]).Scan(&anchored); err != nil {
		t.Fatalf("anchor count: %v", err)
	}
	if anchored != 3 {
		t.Fatalf("%d BOX anchors landed, want one per contender", anchored)
	}
}

// strayExec is the violation the leg exists to catch: the writes run
// on the caller's transaction and the reads reach for the pool, the
// shape a stray set check would take.
type strayExec struct {
	tx   pgx.Tx
	pool *pgxpool.Pool
}

func (e strayExec) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return e.tx.Exec(ctx, sql, args...)
}

func (e strayExec) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return e.pool.QueryRow(ctx, sql, args...)
}

// TestMatrixWritePoolFourBitesAWriteThatReadsOffThePool: with the pool
// of four fully held, a write whose set check reads off the pool
// cannot get a connection, the bound fires and every contender fails
// fast, which is what proves the green leg can fail.
func TestMatrixWritePoolFourBitesAWriteThatReadsOffThePool(t *testing.T) {
	f := newMatrixPoolFixture(t)
	products, outcomes := runMatrixContenders(t, f, 3*time.Second,
		func(scenarioCtx context.Context, tx pgx.Tx, id string, n int) error {
			return applyMatrixPrice(scenarioCtx, strayExec{tx: tx, pool: f.pool}, matrixPriceWrite{
				ProductID: id, Cents: 900 + int64(n), PriceUOM: "BOX",
			})
		})
	for k, o := range outcomes {
		if o.err == nil {
			t.Fatalf("contender %d wrote through a stray pool read inside the bound, the leg cannot bite", k)
		}
		t.Logf("contender %d: failed fast at the bound: %v", k, o.err)
	}
	var anchored int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_list_prices WHERE price_uom = 'BOX' AND product_id IN ($1::uuid, $2::uuid, $3::uuid)`,
		products[0], products[1], products[2]).Scan(&anchored); err != nil {
		t.Fatalf("anchor count: %v", err)
	}
	if anchored != 0 {
		t.Fatalf("%d BOX anchors landed behind the bitten writes, want none", anchored)
	}
}

// TestMatrixWriteTwoWritersOppositeOrderConverge is the lock order
// proof: the unit check takes the products row FOR NO KEY UPDATE, and
// it takes it behind the allocator's lock, so two writers applying two
// product lines each in opposite order serialise on the allocator and
// both converge, whatever the interleaving. The bite swaps the order
// (the products lock before the allocator draw) and the same two
// writers deadlock across the allocator, one aborted with 40P01,
// which is what proves the order is load bearing and no deadlock
// victim leg is owed on the shipped order.
func TestMatrixWriteTwoWritersOppositeOrderConverge(t *testing.T) {
	f := newMatrixPoolFixture(t)
	first := f.product("PC", "BOX", 100)
	second := f.product("PC", "BOX", 100)

	type writerResult struct {
		n   int
		err error
	}
	results := make(chan writerResult, 2)
	// The gate keeps both writers inside their first write: each
	// signals started, the suite closes the gate once both are in, and
	// only then does either reach its second line. Under the shipped
	// order the loser is parked on the allocator holding no products
	// row, so the release is safe; under the bite (the products lock
	// before the allocator hold) the two writers hold their first
	// products rows across the gate and deadlock the moment it opens,
	// which is the red that proves the order is load bearing.
	started := make(chan struct{}, 2)
	gate := make(chan struct{})
	writer := func(n int, a, b string) {
		ctx := context.Background()
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			results <- writerResult{n: n, err: err}
			return
		}
		started <- struct{}{}
		if err := applyMatrixPrice(ctx, tx, matrixPriceWrite{
			ProductID: a, Cents: 900, PriceUOM: "BOX",
		}); err != nil {
			rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = tx.Rollback(rbCtx)
			rbCancel()
			results <- writerResult{n: n, err: err}
			return
		}
		<-gate
		if err := applyMatrixPrice(ctx, tx, matrixPriceWrite{
			ProductID: b, Cents: 901, PriceUOM: "BOX",
		}); err != nil {
			rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = tx.Rollback(rbCtx)
			rbCancel()
			results <- writerResult{n: n, err: err}
			return
		}
		results <- writerResult{n: n, err: tx.Commit(ctx)}
	}
	go writer(0, first, second)
	go writer(1, second, first)
	<-started
	<-started
	time.Sleep(500 * time.Millisecond) // both first writes are parked by now
	close(gate)
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("writer %d: %v (allocator first, the opposite orders must converge, not deadlock)", r.n, r.err)
		}
	}
	var anchors int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_list_prices WHERE price_uom = 'BOX' AND product_id IN ($1::uuid, $2::uuid)`,
		first, second).Scan(&anchors); err != nil {
		t.Fatalf("anchor count: %v", err)
	}
	if anchors != 2 {
		t.Fatalf("%d BOX anchors landed across the two writers, want one per product", anchors)
	}
}
