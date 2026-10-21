//go:build integration

// generic_plan_integration_test.go: every gl statement that takes a branch, product or category id as text and casts
// it to a uuid must work for an empty value when the planner has chosen a generic plan.
//
// The shape that broke: `$1 <> ” AND col = $1::uuid` inside an OR. SQL does not promise the guard runs first, and
// under a generic plan the planner lifts `col = $1::uuid` into an index condition and casts the parameter when the
// scan starts, so an empty value failed with invalid input syntax for type uuid (22P02). Each test here runs on a
// connection of its own with `SET plan_cache_mode = force_generic_plan` (and the non bitmap scans off, so a small table
// still takes the bitmap plan), so it does not depend on how many times the pool reused a statement or on table size.
package gl

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"

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

func TestGenericPlan_PolicyReadsTheOrgDefaultForAnEmptyBranch(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	branch := aBranchID(t, pool)
	for i := 0; i < 3; i++ {
		p, err := repo.Policy(context.Background(), "")
		if err != nil {
			t.Fatalf("Policy(empty) call %d: %v", i+1, err)
		}
		if p.BranchID != "" {
			t.Fatalf("an empty branch must resolve the org default, got branch %q", p.BranchID)
		}
		if _, err := repo.Policy(context.Background(), branch); err != nil {
			t.Fatalf("Policy(real branch) call %d: %v", i+1, err)
		}
	}
}

func TestGenericPlan_PostingAccountsResolveForAnEmptyBranch(t *testing.T) {
	pool := genericPlanPool(t)
	repo := NewRepository(pool)
	branch := aBranchID(t, pool)
	for i := 0; i < 3; i++ {
		for _, b := range []string{"", branch} {
			rules, err := repo.PostingAccounts(context.Background(), "invoice.finalized", b)
			if err != nil {
				t.Fatalf("PostingAccounts(%q) call %d: %v", b, i+1, err)
			}
			if rules["ar"] == "" {
				t.Fatalf("PostingAccounts(%q) call %d has no ar leg: %v", b, i+1, rules)
			}
		}
	}
}

// reliefSplit is the tax split reader a tax relief posting asks for.
type reliefSplit struct{}

func (reliefSplit) TaxSplit(context.Context, string) (int64, int64, int64, bool, error) {
	return 1300, 800, 500, true, nil
}

// A GL posting whose posting rule is read with no branch (tax relief reads its rules for the dealer, never a
// branch) must post on a connection that plans generically, not leave the outbox row to fail and retry.
func TestGenericPlan_AJournalPostsWhoseRulesAreReadWithNoBranch(t *testing.T) {
	ctx := context.Background()
	plain := intPool(t)
	user := aUser(t, plain)
	ensurePeriods(t, plain, 2097)
	pool := genericPlanPool(t)
	at := time.Date(2097, 6, 15, 12, 0, 0, 0, time.UTC)
	caller := identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}
	svc := svcAt(pool, caller, at).WithTaxSplitReader(reliefSplit{})

	var order string
	if err := plain.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&order); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"order_id":%q}`, order)
	if err := svc.consume(ctx, notify.Event{EventID: order, Topic: "tax.relief.assessed", Payload: []byte(payload)}); err != nil {
		t.Fatalf("a journal whose rules are read with no branch must post: %v", err)
	}
	var n int
	if err := plain.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "tax.relief.assessed:"+order).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected the relief journal to post once, found %d", n)
	}
}
