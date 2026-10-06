//go:build integration

// vendorload_pool_integration_test.go is the pool 4 leg for the
// supplier door (HH-LOAD-1 LD-4, decision row 786): the writer runs on
// the frame's transaction and nothing else, so three contending batches
// on a pool of four, with the suite itself holding the fourth
// connection, must all finish. One stray pool use inside the writer
// (a second connection asked for while the batch holds its own) leaves
// no connection free and the leg fails on its deadline with the stall
// named. The second leg proves the deadlock recognition through the
// real writer: a Postgres deadlock inside the writer's statement
// surfaces as load.ErrBatchContended with nothing of the batch
// standing, and the resubmission converges.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5625/ld4_gate?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestVendorLoadPool
package purchasing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolFourConns is the pool the leg runs against.
const poolFourConns = 4

// poolFourBatchTimeout ends a stalled batch with the stall named.
const poolFourBatchTimeout = 20 * time.Second

// poolFour opens a pool of exactly four connections over the suite's
// database URL.
func poolFour(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pool four config: %v", err)
	}
	cfg.MaxConns = poolFourConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool four: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestVendorLoadPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	e := vendorLoadSetup(t) // clean fixtures; its own pool is not used below
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
	door := load.NewDoor(store, "supplier", VendorLoadWriter{}, &loadAuditSpy{})

	var wg sync.WaitGroup
	results := make([]load.BatchResult, 3)
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, poolFourBatchTimeout)
			defer cancel()
			// The same tree under three batch ids: the three contend on
			// the same names, rows and the scope draw.
			results[k], errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxVendorOrgID,
				SourceSystem: fxVendorSource, BatchID: fmt.Sprintf("b-vend-pool-%d", k), Caller: fxVendorActorID,
				Items: []load.Item{
					vendItem("FXV-0001", "Fixture Supplier One", map[string]any{"contact": map[string]any{"contact_name": "Fixture Desk", "email": "desk@example.invalid"}}),
					vendItem("FXV-0002", "Fixture Supplier Two", nil),
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
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM vendors WHERE vendor_no IN ('FXV-0001','FXV-0002')`).Scan(&rows); err != nil {
		t.Fatalf("row count: %v", err)
	}
	if rows != 2 {
		t.Fatalf("%d vendor rows stand, want two", rows)
	}
}

// strayPoolWriter is the test's deliberate violation: a writer that
// reaches for the pool while the batch holds its transaction. The leg
// above is only worth its name if this one fails it.
type strayPoolWriter struct {
	pool  *pgxpool.Pool
	inner load.Writer
}

func (w strayPoolWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	if _, err := w.pool.Exec(ctx, `SELECT 1`); err != nil {
		return "", fmt.Errorf("stray pool use: %w", err)
	}
	return w.inner.Apply(ctx, tx, in)
}

func TestVendorLoadPoolFourBitesAWriterThatUsesThePool(t *testing.T) {
	vendorLoadSetup(t)
	pool := poolFour(t)
	ctx := context.Background()
	// Three conns held by contending batches and the fourth by the suite:
	// the stray statement of any batch has no connection to take.
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewDoor(load.NewStore(pool), "supplier", strayPoolWriter{pool: pool, inner: VendorLoadWriter{}}, &loadAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxVendorOrgID,
				SourceSystem: fxVendorSource, BatchID: fmt.Sprintf("b-vend-stray-%d", k), Caller: fxVendorActorID,
				Items: []load.Item{vendItem("FXV-0001", "Fixture Supplier One", nil)}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a writer that uses the pool", k)
		}
	}
}

func TestVendorLoadDeadlockInTheWriterAnswersTheContentionSentinel(t *testing.T) {
	e := vendorLoadSetup(t)
	ctx := context.Background()
	// A pre existing row the outside transaction will hold.
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO vendors (vendor_no, name) VALUES ('FXV-0002', 'Fixture Held')`); err != nil {
		t.Fatalf("plant held row: %v", err)
	}
	// The scope's sequence row exists before the batch draws, so the
	// outside transaction's later update waits on the batch's hold
	// instead of missing a row the batch has not committed.
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ('vendors') ON CONFLICT (scope) DO NOTHING`); err != nil {
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
	if _, err := outside.Exec(ctx, `UPDATE vendors SET name = name WHERE vendor_no = 'FXV-0002'`); err != nil {
		t.Fatalf("outside lock: %v", err)
	}

	// The batch: item one lands fresh (it draws the scope's sequence and
	// holds it to the batch's end), item two upserts the held row and
	// waits on the outside transaction.
	batch := load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource, BatchID: "b-vend-dl", Caller: fxVendorActorID,
		Items: []load.Item{
			vendItem("FXV-0001", "Fixture Fresh", nil),
			vendItem("FXV-0002", "Fixture Held Renamed", nil),
		}}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := e.door.UpsertBatch(cctx, batch)
		done <- err
	}()
	waitForLockWait(t, e.pool)
	// The outside transaction now wants the scope's sequence row the
	// batch holds: the cycle closes and Postgres kills the batch.
	_, outsideErr := outside.Exec(ctx, `UPDATE scope_sequences SET next_value = next_value WHERE scope = 'vendors'`)
	_ = outsideErr
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)

	var batches, names, fresh int
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-vend-dl'),
		       (SELECT count(*) FROM load_key_registry WHERE org_id = $1::uuid),
		       (SELECT count(*) FROM vendors WHERE vendor_no = 'FXV-0001')`, fxVendorOrgID).
		Scan(&batches, &names, &fresh); err != nil {
		t.Fatalf("remainder read: %v", err)
	}
	if batches != 0 || names != 0 || fresh != 0 {
		t.Fatalf("the victim left %d batch rows, %d names, %d fresh rows: a contended batch rolls back whole", batches, names, fresh)
	}
	// The resubmission converges.
	r, err := e.door.UpsertBatch(ctx, batch)
	if err != nil {
		t.Fatalf("resubmission: %v", err)
	}
	for _, o := range r.Items {
		if o.Status != load.OutcomeCreated {
			t.Fatalf("the resubmission answered %+v, want created", o)
		}
	}
}

// waitForLockWait polls until some session in this database waits on a
// lock, the batch standing behind the outside transaction.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatalf("lock wait poll: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no session ever waited on a lock: the batch never reached the held row")
}
