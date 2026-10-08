//go:build integration

// supplierskuload_pool_integration_test.go is the pool 4 leg for the
// supplier SKU door (HH-LOAD-1 LD-5, decision row 786): the writer runs
// on the frame's transaction and nothing else, so three contending
// batches on a pool of four, with the suite itself holding the fourth
// connection, must all finish. One stray pool use inside the writer
// fails the leg on its deadline with the stall named. The deadlock leg
// is the contention sentinel's proof through the real writer: an
// outside transaction holds the vendor row the write's foreign key
// check needs, the batch holds its name row and wants the vendor row,
// the outside then wants the name row, and Postgres kills the batch as
// the victim: the sentinel answers, nothing survives, the resubmission
// converges. The sentinel assertion is the %w bite: it reads the
// Postgres error through the writer's own %w wrap, so flattening that
// wrap to %v turns this leg red.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5680/integration_gate?sslmode=disable' \
//	  go test -tags integration ./internal/purchasing/... -count=1 -run 'TestSupplierSKULoadPool|TestSupplierSKULoadDeadlock'
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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// skuPoolFour opens a pool of exactly four connections over the suite's
// database URL. The URL rides the command line (never a default): with
// none set the legs skip politely.
func skuPoolFour(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pool four config: %v", err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool four: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestSupplierSKULoadPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	e := supplierSkuSetup(t) // clean fixtures; its own pool is not used below
	vendorID, productID := e.plantSupplierSkuRefs(t)
	pool := skuPoolFour(t)
	ctx := context.Background()
	// The suite holds one of the four connections for the whole leg: the
	// three contenders take the other three, so any pool use inside a
	// batch (a second connection while one is held) has none to get.
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewDoor(load.NewStore(pool), load.KindSupplierSku, SupplierSKULoadWriter{Units: product.UnitCatalogueReader{}}, &loadAuditSpy{})
	items := []load.Item{skuItem("SK-1", "FXV-SKU-POOL", nil,
		load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}
	var wg sync.WaitGroup
	results := make([]load.BatchResult, 3)
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			results[k], errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxSkuOrgID,
				SourceSystem: fxSkuSource, BatchID: fmt.Sprintf("b-sk-pool-%d", k), Caller: fxSkuActorID,
				Items: append([]load.Item(nil), items...)})
		}(k)
	}
	wg.Wait()
	created := 0
	for k, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v (a batch needed a connection beyond the transaction it holds)", k, err)
		}
		for _, o := range results[k].Items {
			if o.Status == load.OutcomeCreated {
				created++
			}
			if o.Status == load.OutcomeRefused {
				t.Fatalf("contender %d refused %+v", k, o)
			}
		}
	}
	if created != 1 {
		t.Fatalf("%d creates across the three contenders, want exactly one", created)
	}
	var rows int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM purchasing_vendor_skus WHERE vendor_id = $1::uuid AND product_id = $2::uuid`,
		vendorID, productID).Scan(&rows); err != nil {
		t.Fatalf("row count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("%d rows stand, want the one row three contenders raced on", rows)
	}
}

// straySkuPoolWriter is the test's deliberate violation: a writer that
// reaches for the pool while the batch holds its transaction. The leg
// above is only worth its name if this one fails it.
type straySkuPoolWriter struct {
	pool  *pgxpool.Pool
	inner load.Writer
}

func (w straySkuPoolWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	if _, err := w.pool.Exec(ctx, `SELECT 1`); err != nil {
		return "", fmt.Errorf("stray pool use: %w", err)
	}
	return w.inner.Apply(ctx, tx, in)
}

func TestSupplierSKULoadPoolFourBitesAWriterThatUsesThePool(t *testing.T) {
	e := supplierSkuSetup(t)
	vendorID, productID := e.plantSupplierSkuRefs(t)
	ctx := context.Background()
	// The name stands and its recorded hash is corrupted, so every
	// contender takes the update arm: each locks the name row (FOR
	// UPDATE) before its writer runs, so the three contenders hold all
	// three free connections while they wait on one another's name row,
	// and the stray pool ask of any batch has none to take.
	door := load.NewDoor(load.NewStore(e.pool), load.KindSupplierSku, SupplierSKULoadWriter{Units: product.UnitCatalogueReader{}}, &loadAuditSpy{})
	if _, err := door.UpsertBatch(ctx, load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-stray-seed", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-1", "FXV-SKU-POOL", nil,
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}}); err != nil {
		t.Fatalf("stray seed batch: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('a', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'SK-1'`,
		fxSkuOrgID, fxSkuSource, load.KindSupplierSku); err != nil {
		t.Fatalf("stray hash plant: %v", err)
	}
	_ = vendorID
	_ = productID
	pool := skuPoolFour(t)
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	strayDoor := load.NewDoor(load.NewStore(pool), load.KindSupplierSku,
		straySkuPoolWriter{pool: pool, inner: SupplierSKULoadWriter{Units: product.UnitCatalogueReader{}}}, &loadAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, errs[k] = strayDoor.UpsertBatch(cctx, load.Batch{OrgID: fxSkuOrgID,
				SourceSystem: fxSkuSource, BatchID: fmt.Sprintf("b-sk-stray-%d", k), Caller: fxSkuActorID,
				Items: []load.Item{skuItem("SK-1", "FXV-SKU-POOL", nil,
					load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a writer that uses the pool", k)
		}
	}
}

func TestSupplierSKULoadDeadlockAnswersTheContentionSentinel(t *testing.T) {
	e := supplierSkuSetup(t)
	ctx := context.Background()
	vendorID, productID := e.plantSupplierSkuRefs(t)
	// The name stands with its row, and its recorded hash is replaced by
	// one no submission carries, so the batch runs the update arm and
	// holds the name row while its write waits.
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-dl-seed", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-DL", "FXV-SKU-DL", nil,
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}}); err != nil {
		t.Fatalf("deadlock seed batch: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('a', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'SK-DL'`,
		fxSkuOrgID, fxSkuSource, load.KindSupplierSku); err != nil {
		t.Fatalf("deadlock hash plant: %v", err)
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
	// The outside transaction holds the vendor SKU row itself: the
	// batch's upsert (its ON CONFLICT DO UPDATE) waits behind it while
	// holding its name row.
	if _, err := outside.Exec(ctx,
		`UPDATE purchasing_vendor_skus SET vendor_sku = vendor_sku
		 WHERE vendor_id = $1::uuid AND product_id = $2::uuid`, vendorID, productID); err != nil {
		t.Fatalf("outside row lock: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := e.door.UpsertBatch(cctx, load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
			BatchID: "b-sk-dl", Caller: fxSkuActorID,
			Items: []load.Item{skuItem("SK-DL", "FXV-SKU-DL-A", nil,
				load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
		done <- err
	}()
	waitForLockWaitPool(t, e.pool)
	// The outside now wants the name row the batch holds: the cycle
	// closes and Postgres kills the batch.
	_, _ = outside.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = content_hash
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'SK-DL'`,
		fxSkuOrgID, fxSkuSource, load.KindSupplierSku)
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)
	// Nothing of the victim stands: no batch row, the recorded code is
	// the seed's.
	var batches int
	var vendorSKU string
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-sk-dl'),
		       (SELECT vendor_sku FROM purchasing_vendor_skus WHERE vendor_id = $2::uuid AND product_id = $3::uuid)`,
		fxSkuOrgID, vendorID, productID).Scan(&batches, &vendorSKU); err != nil {
		t.Fatalf("victim remainder read: %v", err)
	}
	if batches != 0 || vendorSKU != "FXV-SKU-DL" {
		t.Fatalf("the victim left %d batch rows and code %q: a contended batch rolls back whole", batches, vendorSKU)
	}
	// The resubmission converges.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-dl", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-DL", "FXV-SKU-DL-A", nil,
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("deadlock resubmission: %v", err)
	}
	if o := skuOutcome(t, r, "SK-DL"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the resubmission answered %+v, want updated off the corrupted hash", o)
	}
}

// waitForLockWaitPool polls until some session in this database waits on
// a lock, the batch standing behind the outside transaction.
func waitForLockWaitPool(t *testing.T, pool *pgxpool.Pool) {
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
