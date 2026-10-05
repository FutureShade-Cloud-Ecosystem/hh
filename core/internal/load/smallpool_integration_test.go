//go:build integration

// smallpool_integration_test.go is the pool stall's named leg for the
// load frame (HH-LOAD-1 round 4, the card_charge_one_connection
// precedent): a pool with fewer connections than concurrent submitters
// must still serve every batch, because a batch in flight holds exactly
// one connection from its claim to its finish. Five concurrent batches
// over a four connection pool, mixed shapes: three distinct batch ids
// and two submitters sharing a fourth id (the client that times out and
// resubmits). Each submission carries its own deadline, wider than a
// healthy batch and narrower than the leg's watchdog, so a stall fails
// here with the deadline named instead of hanging the suite.
//
// All fixtures are invented.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5636/r4_gate?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -run TestSmallPool
package load

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// smallPoolConns is the pool the stall probe runs against: fewer
// connections than submitters, the shape the reviewer proved deadlocks
// when a batch needs a second connection mid flight.
const smallPoolConns = 4

// smallPoolSubmitters runs against the small pool: one more than the
// pool holds, so a protocol needing two connections per batch cannot
// finish even one round of them.
const smallPoolSubmitters = 5

// smallPoolBatchTimeout bounds one batch's whole run. A healthy batch of
// six items answers in well under a second; the stall never answers at
// all, and this is what ends it with the stall named.
const smallPoolBatchTimeout = 20 * time.Second

// smallPoolTestBudget is the leg's own watchdog, wider than every
// submission deadline it wraps: the leg must report the stall itself.
const smallPoolTestBudget = 90 * time.Second

// testPoolDSN hands the suite's database URL over, or skips politely.
func testPoolDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	return dsn
}

// smallPool opens the stall probe's pool over the suite's database URL.
func smallPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(testPoolDSN(t))
	if err != nil {
		t.Fatalf("small pool config: %v", err)
	}
	cfg.MaxConns = smallPoolConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("small pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("small pool ping: %v", err)
	}
	return pool
}

// TestSmallPoolServesConcurrentBatches: five concurrent batches on a
// four connection pool, mixed distinct and shared batch ids, every one
// recorded or answered from the record, none erroring, nothing partial.
func TestSmallPoolServesConcurrentBatches(t *testing.T) {
	setup(t) // the scratch shape and the org slice, on its own pool
	pool := smallPool(t)
	store := NewStore(pool)
	sink := &countingAuditSink{}
	door := NewDoor(store, "probe", probeWriter{}, sink)

	batches := make([]struct {
		batchID string
		items   []Item
	}, 0, smallPoolSubmitters)
	for i := 0; i < 3; i++ {
		batches = append(batches, struct {
			batchID string
			items   []Item
		}{fmt.Sprintf("b-pool-%d", i), concItems(fmt.Sprintf("P%dL", i), fmt.Sprintf("P%dc", i))})
	}
	shared := "b-pool-shared"
	sharedItems := concItems("PSL", "PSc")
	batches = append(batches,
		struct {
			batchID string
			items   []Item
		}{shared, sharedItems},
		struct {
			batchID string
			items   []Item
		}{shared, sharedItems},
	)

	type answer struct {
		batchID string
		result  BatchResult
		err     error
	}
	answers := make([]answer, smallPoolSubmitters)
	var wg sync.WaitGroup
	watchdog := time.AfterFunc(smallPoolTestBudget, func() {
		t.Fatalf("the small pool leg never finished even past its own watchdog: a batch holds a connection while an item waits for a second (the stall)")
	})
	defer watchdog.Stop()
	for k, b := range batches {
		wg.Add(1)
		go func(k int, batchID string, items []Item) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), smallPoolBatchTimeout)
			defer cancel()
			r, err := door.UpsertBatch(ctx, Batch{
				OrgID: testOrgID, SourceSystem: testSource, BatchID: batchID, Caller: testActorID,
				Items: append([]Item(nil), items...),
			})
			answers[k] = answer{batchID: batchID, result: r, err: err}
		}(k, b.batchID, b.items)
	}
	wg.Wait()

	for k, a := range answers {
		if a.err != nil {
			t.Fatalf("submitter %d (%s): %v", k, a.batchID, a.err)
		}
		if len(a.result.Items) == 0 {
			t.Fatalf("submitter %d (%s) answered no outcomes", k, a.batchID)
		}
	}
	// The shared id's two submitters answered one record: the same
	// outcomes, and exactly one audit row for the whole shared id.
	for i := range answers[3].result.Items {
		if answers[3].result.Items[i] != answers[4].result.Items[i] {
			t.Fatalf("the shared id's submitters disagree at %d: %+v vs %+v",
				i, answers[3].result.Items[i], answers[4].result.Items[i])
		}
	}
	// Every distinct batch id stands recorded exactly once.
	distinct := map[string]bool{"b-pool-0": true, "b-pool-1": true, "b-pool-2": true, shared: true}
	for batchID := range distinct {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND source_system = $2 AND batch_id = $3`,
			testOrgID, testSource, batchID).Scan(&n); err != nil {
			t.Fatalf("batch %s read: %v", batchID, err)
		}
		if n != 1 {
			t.Fatalf("batch %s stands %d times, want exactly one record", batchID, n)
		}
	}
	// One audit row per distinct recorded batch, none for the replay.
	if got := sink.len(); got != len(distinct) {
		t.Fatalf("%d audit rows stand, want one per distinct batch (%d)", got, len(distinct))
	}
	// Nothing partial: every item name the winners carried stands named,
	// and no name exists without its row behind it.
	for _, prefix := range []string{"P0L", "P1L", "P2L", "PSL"} {
		var orphans int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM load_key_registry r
			WHERE org_id = $1::uuid AND source_system = $2 AND legacy_id LIKE $3
			  AND NOT EXISTS (
				SELECT 1 FROM load_probe p
				WHERE p.org_id = r.org_id AND p.id = r.hh_id::uuid)`,
			testOrgID, testSource, prefix+"-%").Scan(&orphans); err != nil {
			t.Fatalf("orphan read %s: %v", prefix, err)
		}
		if orphans != 0 {
			t.Fatalf("%d of the %s names stand without their rows: the writes ran partial", orphans, prefix)
		}
	}
}
