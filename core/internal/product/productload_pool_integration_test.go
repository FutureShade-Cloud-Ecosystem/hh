//go:build integration

// productload_pool_integration_test.go is the pool 4 leg for the product
// master door (HH-LOAD-1 LD-5, decision row 786): the writer runs on the
// frame's transaction and nothing else, so three contending batches on a
// pool of four, with the suite itself holding the fourth connection,
// must all finish. One stray pool use inside the writer (a second
// connection asked for while the batch holds its own) leaves no
// connection free and the leg fails on its deadline with the stall
// named. The second leg proves the deadlock recognition through the
// real writer: a Postgres deadlock inside the writer's statement
// surfaces as load.ErrBatchContended with nothing of the batch
// standing, and the resubmission converges. The leg is its own %w bite:
// the sentinel assertion reads the wrapped Postgres error through the
// writer's %w, so flattening the wrap to %v turns this leg red.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5680/integration_gate?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run 'TestProductLoadPool|TestProductLoadDeadlock'
package product

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolFour, poolFourConns and poolFourBatchTimeout are the category
// pool leg's helpers beside this file; the legs share them.
// waitForLockWait is the deadlock leg's poll, shared the same way.

func TestProductLoadPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	e := productLoadSetup(t) // clean fixtures; its own pool is not used below
	e.prSeedUnit(t, "EA", "Each", "count")
	pool := poolFour(t)
	ctx := context.Background()
	// The suite holds one of the four connections for the whole leg: the
	// three contenders take the other three, so any pool use inside a
	// batch (a second connection while one is held) has none to get.
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	store := load.NewStore(pool)
	door := load.NewDoor(store, load.KindProduct, ProductLoadWriter{}, &loadAuditSpy{})

	var wg sync.WaitGroup
	results := make([]load.BatchResult, 3)
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, poolFourBatchTimeout)
			defer cancel()
			// The same two products under three batch ids: the three
			// contend on the same names, rows and the scope draw.
			results[k], errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxProdOrgID,
				SourceSystem: fxProdSource, BatchID: fmt.Sprintf("b-pr-pool-%d", k), Caller: fxProdActorID,
				Items: []load.Item{
					prItem("PR-P1", "FXPR-9001", "Fixture Pool One", nil),
					prItem("PR-P2", "FXPR-9002", "Fixture Pool Two", nil),
				}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v (a batch needed a connection beyond the transaction it holds)", k, err)
		}
	}
	created := 0
	for k := range results {
		for _, o := range results[k].Items {
			if o.Status == load.OutcomeCreated {
				created++
			}
			if o.Status == load.OutcomeRefused {
				t.Fatalf("contender %d refused %+v", k, o)
			}
		}
	}
	if created != 2 {
		t.Fatalf("%d creates across the three contenders, want exactly two (one per name)", created)
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE sku IN ('FXPR-9001','FXPR-9002')`).Scan(&rows); err != nil {
		t.Fatalf("row count: %v", err)
	}
	if rows != 2 {
		t.Fatalf("%d product rows stand, want two", rows)
	}
}

// strayProductPoolWriter is the test's deliberate violation: a writer
// that reaches for the pool while the batch holds its transaction. The
// leg above is only worth its name if this one fails it.
type strayProductPoolWriter struct {
	pool  *pgxpool.Pool
	inner load.Writer
}

func (w strayProductPoolWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	if _, err := w.pool.Exec(ctx, `SELECT 1`); err != nil {
		return "", fmt.Errorf("stray pool use: %w", err)
	}
	return w.inner.Apply(ctx, tx, in)
}

func TestProductLoadPoolFourBitesAWriterThatUsesThePool(t *testing.T) {
	productLoadSetup(t)
	pool := poolFour(t)
	ctx := context.Background()
	// Three conns held by contending batches and the fourth by the suite:
	// the stray statement of any batch has no connection to take.
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewDoor(load.NewStore(pool), load.KindProduct,
		strayProductPoolWriter{pool: pool, inner: ProductLoadWriter{}}, &loadAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxProdOrgID,
				SourceSystem: fxProdSource, BatchID: fmt.Sprintf("b-pr-stray-%d", k), Caller: fxProdActorID,
				Items: []load.Item{prItem("PR-P1", "FXPR-9001", "Fixture Pool One", nil)}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a writer that uses the pool", k)
		}
	}
}

// TestProductLoadDeadlockAnswersTheContentionSentinel is the real
// deadlock leg through the real writer: an outside transaction holds the
// second product row while the batch holds the products scope's
// sequence row and wants the second product, the outside then wants the
// scope row, and Postgres kills the batch as the victim. The batch
// answers load.ErrBatchContended, nothing of it stands, and the
// resubmission converges. The sentinel assertion is the %w bite: it
// reads the Postgres error through the writer's own %w wrap, so
// flattening that wrap to %v turns this leg red with the raw error
// named.
func TestProductLoadDeadlockAnswersTheContentionSentinel(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	// Two product rows stand with names recorded, so the batch runs the
	// update arm and locks the products rows in its item order.
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-dl-seed", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-D1", "FXPR-9101", "Fixture Deadlock One", nil),
			prItem("PR-D2", "FXPR-9102", "Fixture Deadlock Two", nil)}}); err != nil {
		t.Fatalf("deadlock seed batch: %v", err)
	}
	// The names must take the update arm, so the recorded hash is
	// replaced by one no submission carries.
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('a', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3`,
		fxProdOrgID, fxProdSource, load.KindProduct); err != nil {
		t.Fatalf("deadlock hash plant: %v", err)
	}
	// The scope's sequence row exists before the batch draws, so the
	// outside transaction's later update waits on the batch's hold
	// instead of missing a row the batch has not committed.
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ('products') ON CONFLICT (scope) DO NOTHING`); err != nil {
		t.Fatalf("plant scope row: %v", err)
	}
	outside, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("outside begin: %v", err)
	}
	defer func() { _ = outside.Rollback(ctx) }()
	// A long deadlock timeout on the outside session, so the batch's own
	// detection (the default, a second) fires first and the batch is the
	// victim.
	if _, err := outside.Exec(ctx, `SET LOCAL deadlock_timeout = '30s'`); err != nil {
		t.Fatalf("outside timeout: %v", err)
	}
	if _, err := outside.Exec(ctx, `UPDATE products SET name = name WHERE sku = 'FXPR-9102'`); err != nil {
		t.Fatalf("outside lock: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := e.door.UpsertBatch(cctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
			BatchID: "b-pr-dl", Caller: fxProdActorID,
			Items: []load.Item{prItem("PR-D1", "FXPR-9101", "Fixture Deadlock One A", nil),
				prItem("PR-D2", "FXPR-9102", "Fixture Deadlock Two A", nil)}})
		done <- err
	}()
	waitForLockWait(t, e.pool)
	// The outside now wants the products scope's sequence row the batch
	// holds: the cycle closes and Postgres kills the batch.
	_, _ = outside.Exec(ctx, `UPDATE scope_sequences SET next_value = next_value WHERE scope = 'products'`)
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)
	// Nothing of the victim stands: no batch row, the first item's
	// update rolled back with it.
	var batches int
	var name string
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-pr-dl'),
		       (SELECT name FROM products WHERE sku = 'FXPR-9101')`, fxProdOrgID).Scan(&batches, &name); err != nil {
		t.Fatalf("victim remainder read: %v", err)
	}
	if batches != 0 || name != "Fixture Deadlock One" {
		t.Fatalf("the victim left %d batch rows and name %q: a contended batch rolls back whole", batches, name)
	}
	// The resubmission converges.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-dl", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-D1", "FXPR-9101", "Fixture Deadlock One A", nil),
			prItem("PR-D2", "FXPR-9102", "Fixture Deadlock Two A", nil)}})
	if err != nil {
		t.Fatalf("deadlock resubmission: %v", err)
	}
	for _, o := range r.Items {
		if o.Status == load.OutcomeRefused {
			t.Fatalf("the resubmission refused %+v", o)
		}
	}
}
