//go:build integration

// generic_plan_integration_test.go: every purchasing statement that takes a branch, product or category id as text and casts
// it to a uuid must work for an empty value when the planner has chosen a generic plan.
//
// The shape that broke: `$1 <> ” AND col = $1::uuid` inside an OR. SQL does not promise the guard runs first, and
// under a generic plan the planner lifts `col = $1::uuid` into an index condition and casts the parameter when the
// scan starts, so an empty value failed with invalid input syntax for type uuid (22P02). Each test here runs on a
// connection of its own with `SET plan_cache_mode = force_generic_plan` (and the non bitmap scans off, so a small table
// still takes the bitmap plan), so it does not depend on how many times the pool reused a statement or on table size.
package purchasing

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// genericPlanPool is a pool of exactly one connection that plans every statement generically.
func genericPlanPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse database url: %v", err)
	}
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		if _, err := c.Exec(ctx, "SET plan_cache_mode = force_generic_plan"); err != nil {
			return err
		}
		// A table this small is read by a sequential scan or an ordered index scan, which keep the guard and the cast
		// in the order written. With a real dealer's rows the planner may instead OR two bitmap index scans, whose
		// index condition casts the parameter first; turning the other scans off makes this connection take that
		// plan whatever the table's size, which is the plan the bug needs.
		_, err := c.Exec(ctx, "SET enable_seqscan = off; SET enable_indexscan = off; SET enable_indexonlyscan = off")
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		if set {
			t.Fatalf("integration DB unavailable: %v", err)
		}
		t.Skipf("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		if set {
			t.Fatalf("integration DB not reachable: %v", err)
		}
		t.Skipf("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	var mode string
	if err := pool.QueryRow(context.Background(), `SHOW plan_cache_mode`).Scan(&mode); err != nil || mode != "force_generic_plan" {
		t.Fatalf("the test connection must plan generically, got %q (%v)", mode, err)
	}
	return pool
}

func aBranchID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var branch string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY code LIMIT 1`).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	return branch
}

func TestGenericPlan_ToleranceResolvesTheDealerDefaultForAnEmptyBranch(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	branch := aBranchID(t, pool)
	for i := 0; i < 3; i++ {
		tol, err := repo.Tolerance(context.Background(), "")
		if err != nil {
			t.Fatalf("Tolerance(empty) call %d: %v", i+1, err)
		}
		if tol.IsBranchOverride {
			t.Fatalf("an empty branch must resolve the dealer default, got branch %q", tol.BranchID)
		}
		if _, err := repo.Tolerance(context.Background(), branch); err != nil {
			t.Fatalf("Tolerance(real branch) call %d: %v", i+1, err)
		}
	}
}
