//go:build integration && demo

package main

// demoseed_orderrep_integration_test.go, the order rep repair proved against a real Postgres: it
// stamps the Sales persona on the open yard-branch orders, a second run changes nothing, and an
// order that already carries a salesperson is never overwritten.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// orderRepWorld is a seeded database plus the Sales login the demo populate provisions on a booted
// box (the repair waits for it, the same wait the org stamp takes for the org row).
func orderRepWorld(t *testing.T, ctx context.Context, suffix string) (*pgxpool.Pool, string) {
	t.Helper()
	pool := repairWorld(t, ctx, suffix)
	sales := "11111111-1111-4111-8111-111111111111"
	mustExec(t, ctx, pool, `INSERT INTO users (id, email, display_name, status) VALUES ($1, $2, 'Sales (demo)', 'ACTIVE')`,
		sales, personaQuoteSales)
	return pool, sales
}

// runOrderRepRepair runs the whole repair pass the way the apply does, on the branches the seed
// wrote, so the leg proves the wiring and not only the statement.
func runOrderRepRepair(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var branches []branch
	rows, err := pool.Query(ctx, `SELECT id, code FROM locations ORDER BY code`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var b branch
		if err := rows.Scan(&b.ID, &b.Code); err != nil {
			t.Fatal(err)
		}
		branches = append(branches, b)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := repairDemoWorld(ctx, tx, time.Now(), branches)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("repairDemoWorld: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return n
}

// orderRepTargets is the repair's own scope, asked of the database: the open demo orders of the
// second branch (the yard branch the demo logins work), the ones whose notice would otherwise
// reach nobody.
func orderRepTargets(t *testing.T, ctx context.Context, pool *pgxpool.Pool, statusOp string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM orders
		 WHERE metadata->>'demo_seed' = $1
		   AND branch_id = (SELECT id FROM locations ORDER BY code OFFSET 1 LIMIT 1)
		   AND status `+statusOp, demoMarker).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegration_OrderRepRepairStampsTheOpenYardBranchOrders(t *testing.T) {
	ctx := context.Background()
	pool, sales := orderRepWorld(t, ctx, "orderrep_a")

	open := orderRepTargets(t, ctx, pool, `= ANY('{CONFIRMED,PICKING,READY,PARTIALLY_FULFILLED}')`)
	if open == 0 {
		t.Fatal("the seeded world holds no open yard-branch order, the repair has nothing to address")
	}
	var unowned int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE metadata->>'demo_seed' = $1 AND salesperson_id IS NOT NULL`, demoMarker).Scan(&unowned); err != nil || unowned != 0 {
		t.Fatalf("%d demo orders already carry a salesperson (err %v), the first apply writes none", unowned, err)
	}

	// The whole pass runs the way the apply runs it; the other repairs it holds stamp their own rows
	// beside this one, and only the rep count is asserted here.
	if got := runOrderRepRepair(t, ctx, pool); got < open {
		t.Fatalf("the pass repaired %d rows in all, want at least the %d open yard-branch orders", got, open)
	}
	var stamped int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM orders
		 WHERE metadata->>'demo_seed' = $1
		   AND branch_id = (SELECT id FROM locations ORDER BY code OFFSET 1 LIMIT 1)
		   AND status = ANY('{CONFIRMED,PICKING,READY,PARTIALLY_FULFILLED}')
		   AND salesperson_id = $2`, demoMarker, sales).Scan(&stamped); err != nil {
		t.Fatal(err)
	}
	if stamped != open {
		t.Fatalf("%d of %d open yard-branch orders carry the Sales persona, want all of them", stamped, open)
	}
	// A finished yard-branch order and every Julian order stay NULL: the rep story is the open
	// walk's, not the history's.
	var closed, julian int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE metadata->>'demo_seed' = $1 AND salesperson_id IS NULL AND status = 'CLOSED'`, demoMarker).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM orders
		 WHERE metadata->>'demo_seed' = $1
		   AND branch_id = (SELECT id FROM locations ORDER BY code LIMIT 1)
		   AND salesperson_id IS NULL`, demoMarker).Scan(&julian); err != nil {
		t.Fatal(err)
	}
	if closed == 0 || julian == 0 {
		t.Fatalf("out of scope: %d closed and %d Julian orders stay NULL, want both non-zero", closed, julian)
	}

	// A second run changes nothing.
	before := orderRepTargets(t, ctx, pool, `= ANY('{CONFIRMED,PICKING,READY,PARTIALLY_FULFILLED}') AND salesperson_id = '`+sales+`'`)
	var rep int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE metadata->>'demo_seed' = $1 AND salesperson_id IS NULL AND branch_id = (SELECT id FROM locations ORDER BY code OFFSET 1 LIMIT 1) AND status = ANY('{CONFIRMED,PICKING,READY,PARTIALLY_FULFILLED}')`, demoMarker).Scan(&rep); err != nil {
		t.Fatal(err)
	}
	if rep != 0 {
		t.Fatalf("%d open yard-branch orders are still NULL before the second run", rep)
	}
	if got := runOrderRepRepair(t, ctx, pool); got != 0 {
		t.Fatalf("second run repaired %d rows, want 0 (the other repairs are idempotent too)", got)
	}
	if after := orderRepTargets(t, ctx, pool, `= ANY('{CONFIRMED,PICKING,READY,PARTIALLY_FULFILLED}') AND salesperson_id = '`+sales+`'`); after != before {
		t.Fatalf("the second run moved the stamped set from %d to %d", before, after)
	}
}

func TestIntegration_OrderRepRepairNeverOverwritesASalesperson(t *testing.T) {
	ctx := context.Background()
	pool, _ := orderRepWorld(t, ctx, "orderrep_b")
	other := "22222222-2222-4222-8222-222222222222"
	mustExec(t, ctx, pool, `INSERT INTO users (id, email, display_name, status) VALUES ($1, 'senior@hh-demo.invalid', 'Senior Sales (demo)', 'ACTIVE')`, other)
	var mine string
	if err := pool.QueryRow(ctx, `
		SELECT id FROM orders
		 WHERE metadata->>'demo_seed' = $1
		   AND branch_id = (SELECT id FROM locations ORDER BY code OFFSET 1 LIMIT 1)
		   AND status = ANY('{CONFIRMED,PICKING,READY,PARTIALLY_FULFILLED}') LIMIT 1`, demoMarker).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, pool, `UPDATE orders SET salesperson_id = $1 WHERE id = $2`, other, mine)

	runOrderRepRepair(t, ctx, pool)
	var got string
	if err := pool.QueryRow(ctx, `SELECT salesperson_id::text FROM orders WHERE id = $1`, mine).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != other {
		t.Fatalf("the repair overwrote a hand-set salesperson with %s", got)
	}
}
