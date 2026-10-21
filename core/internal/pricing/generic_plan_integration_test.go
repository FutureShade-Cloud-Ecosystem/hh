//go:build integration

// generic_plan_integration_test.go: every pricing statement that takes a branch, product or category id as text and casts
// it to a uuid must work for an empty value when the planner has chosen a generic plan.
//
// The shape that broke: `$1 <> ” AND col = $1::uuid` inside an OR. SQL does not promise the guard runs first, and
// under a generic plan the planner lifts `col = $1::uuid` into an index condition and casts the parameter when the
// scan starts, so an empty value failed with invalid input syntax for type uuid (22P02). Each test here runs on a
// connection of its own with `SET plan_cache_mode = force_generic_plan` (and the non bitmap scans off, so a small table
// still takes the bitmap plan), so it does not depend on how many times the pool reused a statement or on table size.
package pricing

import (
	"context"
	"os"
	"testing"
	"time"

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

// freshIDs returns n fresh uuids as text, none of which is a row anywhere.
func freshIDs(t *testing.T, pool *pgxpool.Pool, n int) []string {
	t.Helper()
	out := make([]string, n)
	for i := range out {
		if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// combos returns "" or the real id for each of n slots, in every combination.
func combos(real []string) [][]string {
	out := [][]string{{}}
	for _, r := range real {
		var next [][]string
		for _, o := range out {
			next = append(next, append(append([]string{}, o...), ""), append(append([]string{}, o...), r))
		}
		out = next
	}
	return out
}

func TestGenericPlan_ActivePromotionsAcceptsAnyEmptyScope(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	real := append(freshIDs(t, pool, 2), aBranchID(t, pool))
	for _, c := range combos(real) {
		if _, err := repo.ActivePromotions(context.Background(), c[0], c[1], c[2], time.Now()); err != nil {
			t.Fatalf("ActivePromotions(%q, %q, %q): %v", c[0], c[1], c[2], err)
		}
	}
}

func TestGenericPlan_ProgramsForLineAcceptsAnyEmptyScope(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	for _, c := range combos(freshIDs(t, pool, 3)) {
		if _, err := repo.ProgramsForLine(context.Background(), c[0], c[1], c[2]); err != nil {
			t.Fatalf("ProgramsForLine(%q, %q, %q): %v", c[0], c[1], c[2], err)
		}
	}
}

func TestGenericPlan_ConversionsForAcceptsAnyEmptyScope(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	for _, c := range combos(freshIDs(t, pool, 2)) {
		if _, err := repo.ConversionsFor(context.Background(), c[0], c[1]); err != nil {
			t.Fatalf("ConversionsFor(%q, %q): %v", c[0], c[1], err)
		}
	}
}
