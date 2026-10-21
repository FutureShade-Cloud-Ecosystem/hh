//go:build integration

// generic_plan_integration_test.go: every billing statement that takes a branch or order id as text and casts it
// to a uuid must work for an empty value when the planner has chosen a generic plan.
//
// The shape that broke: `$1 <> ” AND col = $1::uuid` inside an OR. SQL does not promise the guard runs first, and
// under a generic plan the planner lifts `col = $1::uuid` into an index condition and casts the parameter when the
// scan starts, so an empty value failed with invalid input syntax for type uuid (22P02). Each test here runs on a
// connection of its own with `SET plan_cache_mode = force_generic_plan` (and the non bitmap scans off, so a small table
// still takes the bitmap plan), so it does not depend on how many times the pool reused a statement or on table size.
package billing

import (
	"context"
	"os"
	"strings"
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

func TestGenericPlan_CreditPolicyReadsTheOrgDefaultForAnEmptyBranch(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	branch := aBranchID(t, pool)
	for i := 0; i < 3; i++ {
		p, err := repo.CreditPolicy(context.Background(), "")
		if err != nil {
			t.Fatalf("CreditPolicy(empty) call %d: %v", i+1, err)
		}
		if p.BranchID != "" {
			t.Fatalf("an empty branch must resolve the org default, got branch %q", p.BranchID)
		}
		if _, err := repo.CreditPolicy(context.Background(), branch); err != nil {
			t.Fatalf("CreditPolicy(real branch) call %d: %v", i+1, err)
		}
	}
}

func TestGenericPlan_SetAutoHoldEnabledTargetsTheOrgDefaultForAnEmptyBranch(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	var current bool
	if err := pool.QueryRow(ctx, `SELECT auto_hold_enabled FROM ar_credit_policy WHERE branch_id IS NULL`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	// Writing the value the row already holds changes nothing, whatever else shares this database.
	if err := repo.SetAutoHoldEnabled(ctx, "", current); err != nil {
		t.Fatalf("SetAutoHoldEnabled(empty): %v", err)
	}
	// A real branch with no row of its own is refused in words, never with a uuid parse error.
	err := repo.SetAutoHoldEnabled(ctx, "00000000-0000-4000-8000-0000000000aa", current)
	if err == nil || strings.Contains(err.Error(), "invalid input syntax") {
		t.Fatalf("SetAutoHoldEnabled(unknown branch) must refuse in words, got %v", err)
	}
}

func TestGenericPlan_ARLandingPolicyReadsTheDealerRowForAnEmptyBranch(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewARLandingRepository(pool)
	branch := aBranchID(t, pool)
	for i := 0; i < 3; i++ {
		if _, err := repo.ReadARLandingPolicy(context.Background(), ""); err != nil {
			t.Fatalf("ReadARLandingPolicy(empty) call %d: %v", i+1, err)
		}
		if _, err := repo.ReadARLandingPolicy(context.Background(), branch); err != nil {
			t.Fatalf("ReadARLandingPolicy(real branch) call %d: %v", i+1, err)
		}
	}
}

func TestGenericPlan_BranchHeaderResolvesTheFirstBranchForAnEmptyId(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	branch := aBranchID(t, pool)
	var want string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM locations WHERE id = $1::uuid`, branch).Scan(&want); err != nil {
		t.Fatal(err)
	}
	h, err := repo.BranchHeader(context.Background(), branch)
	if err != nil {
		t.Fatalf("BranchHeader(real branch): %v", err)
	}
	if h.BranchName != want {
		t.Fatalf("BranchHeader(real branch) = %q, want %q", h.BranchName, want)
	}
	if _, err := repo.BranchHeader(context.Background(), ""); err != nil {
		t.Fatalf("BranchHeader(empty): %v", err)
	}
}

func TestGenericPlan_ActivityForReadsWithoutAnOrder(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	var invoice, order string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text`).Scan(&invoice, &order); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ActivityFor(ctx, invoice, "", true); err != nil {
		t.Fatalf("ActivityFor(no order): %v", err)
	}
	if _, err := repo.ActivityFor(ctx, invoice, order, true); err != nil {
		t.Fatalf("ActivityFor(order): %v", err)
	}
}

func TestGenericPlan_RegisterPolicyResolvesTheDealerDefaultForAnEmptyBranch(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	branch := aBranchID(t, pool)
	for i := 0; i < 3; i++ {
		if _, err := repo.RegisterPolicyFor(context.Background(), ""); err != nil {
			t.Fatalf("RegisterPolicyFor(empty) call %d: %v", i+1, err)
		}
		if _, err := repo.RegisterPolicyFor(context.Background(), branch); err != nil {
			t.Fatalf("RegisterPolicyFor(real branch) call %d: %v", i+1, err)
		}
	}
}
