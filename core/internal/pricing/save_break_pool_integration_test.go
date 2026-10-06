//go:build integration

package pricing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSaveQuantityBreakHeader_ConflictArmNoPoolStarve is the pool second-acquisition regression for
// SaveQuantityBreakHeader (decision row 790). The defect: the conflict arm (the guarded UPDATE matched
// no row) read through r.pool while the save's own transaction still held its pool connection, so at the
// default pool of 4 four concurrent version-mismatch saves each held a connection and each waited for a
// fifth: a cycle Postgres cannot see, stalled to the action timeout.
//
// The pool size is set ON THE COMMAND (pool_max_conns=4 on DATABASE_URL). Side connections outside the
// pool holds a real table lock on quantity_break_headers, so each of the four saves opens its transaction and parks on
// its UPDATE; a pg_stat_activity barrier proves all four are parked (the pool owns zero free
// connections) before the lock is released. Fixed, every save answers errConflict; unfixed, the conflict
// read cannot get a connection and the shared bound fires.
//
// Run (own throwaway database, migrated, pool size on the command):
//
//	DATABASE_URL='postgres://...?sslmode=disable&pool_max_conns=4' \
//	  go test -tags integration ./internal/pricing/ -run TestSaveQuantityBreakHeader_ConflictArmNoPoolStarve -count=1 -v
func TestSaveQuantityBreakHeader_ConflictArmNoPoolStarve(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("pool-cycle leg: DATABASE_URL not set (needs its own throwaway database)")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	if cfg.MaxConns < 2 {
		t.Skipf("pool-cycle leg: pool_max_conns on DATABASE_URL must be at least 2, got %d", cfg.MaxConns)
	}
	size := int(cfg.MaxConns)
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}
	repo := NewRepository(pool)

	code := fmt.Sprintf("POOLSAVE-%d", time.Now().UnixNano())
	var categoryID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM product_categories ORDER BY id LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("pick a seeded category (run the seed on the leg's database): %v", err)
	}
	h, err := repo.CreateQuantityBreakHeader(ctx, QuantityBreakHeader{
		Code: code, Name: "pool save leg", CategoryID: categoryID, ThresholdUOM: "EA", IsActive: true, Version: 1,
		Items: []QuantityBreakItem{{FromQty: 1, ValueType: BreakPctOff, DiscountBps: p64(500), PriceUOM: "EA"}},
	})
	if err != nil {
		t.Fatalf("seed header: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quantity_break_headers WHERE id = $1`, h.ID)
	})

	// The side lock lives on its own connection, outside the repository's pool.
	side, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatalf("side connect: %v", err)
	}
	t.Cleanup(func() { _ = side.Close(context.Background()) })
	// The barrier reads pg_stat_activity on a third connection: the stats snapshot is cached per
	// transaction, so the lock holder's own connection would see a stale picture.
	probe, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatalf("probe connect: %v", err)
	}
	t.Cleanup(func() { _ = probe.Close(context.Background()) })
	stx, err := side.Begin(ctx)
	if err != nil {
		t.Fatalf("side begin: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = stx.Rollback(context.Background())
		}
	}
	defer release()
	// A table lock, not a row lock: the stale UPDATE's WHERE (version) fails on the snapshot before any
	// row lock is looked at, so a row lock would not park it. SHARE mode conflicts with the ROW EXCLUSIVE
	// lock every UPDATE needs, so each save parks at its UPDATE with its transaction open.
	if _, err := stx.Exec(ctx, `LOCK TABLE quantity_break_headers IN SHARE MODE`); err != nil {
		t.Fatalf("side lock: %v", err)
	}

	const bound = 10 * time.Second
	runCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	type result struct {
		err error
		dur time.Duration
	}
	out := make(chan result, size)
	var wg sync.WaitGroup
	for i := 0; i < size; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			stale := h
			stale.Name = "stale writer"
			stale.Version = h.Version + 1
			// expected version 99 never matches: the UPDATE matches no row once the lock clears.
			_, err := repo.SaveQuantityBreakHeader(runCtx, stale, 99)
			out <- result{err, time.Since(start)}
		}()
	}

	// Barrier: all `size` saves hold their transaction (and so their pool connection) and are parked
	// on the side lock, seen from Postgres.
	deadline := time.Now().Add(5 * time.Second)
	parked := 0
	for time.Now().Before(deadline) {
		if err := probe.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND pid <> pg_backend_pid()
			   AND wait_event_type = 'Lock' AND query LIKE 'UPDATE quantity_break_headers SET%'`).Scan(&parked); err != nil {
			t.Fatalf("barrier query: %v", err)
		}
		if parked == size {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if parked != size {
		release()
		wg.Wait()
		t.Fatalf("rig did not park all %d saves on the lock (parked %d)", size, parked)
	}
	t.Logf("barrier: %d of %d pool connections hold a save transaction parked on the table lock", parked, size)

	release()
	wg.Wait()
	close(out)
	stalls := 0
	for r := range out {
		switch {
		case errors.Is(r.err, errConflict):
			t.Logf("save answered the conflict in %s", r.dur.Round(time.Millisecond))
		default:
			stalls++
			t.Logf("save did not answer the conflict after %s: %v", r.dur.Round(time.Millisecond), r.err)
		}
	}
	if stalls > 0 {
		t.Fatalf("THE STALL REPRODUCED: %d of %d version-mismatch saves never reached the conflict answer within %s "+
			"(the conflict read asked the pool for a second connection while the save held its first)", stalls, size, bound)
	}
}
