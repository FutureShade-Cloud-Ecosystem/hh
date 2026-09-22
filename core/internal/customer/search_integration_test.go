//go:build integration

// search_integration_test.go, POS account-tender UX fix. Proves the real pgx SearchAccounts query
// matches by name and account_no, excludes the WALK-IN house account, and reads open_balance_cents from
// the latest customer_transactions.balance_after (the same running-balance basis the POS credit gate uses).
// Run: make reset && go test -tags integration ./internal/customer/...
package customer

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegration_SearchAccounts(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	suffix := t.Name()
	var custID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		 VALUES ('SRCH-'||$1, 'Searchable Hardscapes '||$1, 'CONTRACTOR', $2::uuid, 30, 500000)
		 RETURNING id::text`,
		suffix, branchID).Scan(&custID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// DIB-384: the account_no is derived from t.Name(), so it is STABLE across runs, without a teardown
	// the second run on the same DB dies on customers_account_no_key. Deleting the child subledger row
	// first keeps the FK happy.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id=$1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id=$1::uuid`, custID)
	})
	// A subledger row so open_balance_cents reads the latest balance_after.
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id)
		 VALUES ($1::uuid, 'INVOICE', 25000, 25000, 'invoice', $1::uuid, $2::uuid)`,
		custID, branchID); err != nil {
		t.Fatalf("seed txn: %v", err)
	}

	// Match by name.
	byName, err := repo.SearchAccounts(ctx, "Searchable Hardscapes "+suffix, 20)
	if err != nil {
		t.Fatalf("search by name: %v", err)
	}
	var found *SearchResult
	for i := range byName {
		if byName[i].ID == custID {
			found = &byName[i]
		}
	}
	if found == nil {
		t.Fatalf("name search did not return the seeded customer")
	}
	if found.OpenBalanceCents != 25000 {
		t.Fatalf("open_balance_cents = %d, want 25000 (latest balance_after)", found.OpenBalanceCents)
	}
	if found.CreditLimitCents != 500000 || found.PaymentTermsDays != 30 {
		t.Fatalf("account facts wrong: %+v", found)
	}

	// Match by account_no.
	byAcct, err := repo.SearchAccounts(ctx, "SRCH-"+suffix, 20)
	if err != nil {
		t.Fatalf("search by account_no: %v", err)
	}
	if len(byAcct) == 0 || byAcct[0].ID != custID {
		t.Fatalf("account_no search did not return the seeded customer: %+v", byAcct)
	}

	// The WALK-IN house account must never surface.
	walk, err := repo.SearchAccounts(ctx, "walk", 50)
	if err != nil {
		t.Fatalf("search walk: %v", err)
	}
	for _, r := range walk {
		if r.AccountNo == "WALK-IN" {
			t.Fatalf("WALK-IN house account leaked into results: %+v", r)
		}
	}
}

// -- U-park E4: the branch fence, at the SQL level ------------------------------------------------
//
// The service-level tests prove the DECISION; this proves the QUERY actually filters. A fence that
// is computed and then ignored by the SQL is worse than none, because it reads as protection.
//
// MUTATION TARGET: the `AND ($3::uuid IS NULL OR c.home_branch_id = $3::uuid OR c.home_branch_id IS
// NULL)` predicate in Repository.SearchAccountsScoped. Delete it and the "other branch's account is
// excluded" case below fails.
func TestIntegration_SearchAccountsScoped_BranchFence(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	var branchA, branchB string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' ORDER BY code LIMIT 1`).Scan(&branchA); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM locations WHERE loc_type='BRANCH' AND id <> $1 ORDER BY code LIMIT 1`, branchA).Scan(&branchB); err != nil {
		t.Skipf("need two seeded branches to fence between: %v", err)
	}

	suffix := t.Name()
	mk := func(t *testing.T, acct, name string, branch any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
			VALUES ($1, $2, 'CONTRACTOR', $3, 30, 100000)
			ON CONFLICT (account_no) DO UPDATE SET home_branch_id = EXCLUDED.home_branch_id, name = EXCLUDED.name
			RETURNING id::text`, acct, name, branch).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", acct, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, id) })
		return id
	}
	// A distinctive shared term so one query returns all three fixtures when unfenced.
	term := "Fencemark" + suffix
	homedA := mk(t, "FNCA-"+suffix, term+" Trenton Yards", branchA)
	homedB := mk(t, "FNCB-"+suffix, term+" Kingston Yards", branchB)
	unhomed := mk(t, "FNCN-"+suffix, term+" No Home Branch", nil)

	t.Run("unfenced sees all three", func(t *testing.T) {
		got, err := repo.SearchAccountsScoped(ctx, term, 50, SearchScope{Global: true})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("an unfenced search must see every fixture, got %d: %+v", len(got), got)
		}
	})

	t.Run("fenced to branch A excludes branch B, keeps the unhomed account", func(t *testing.T) {
		got, err := repo.SearchAccountsScoped(ctx, term, 50, SearchScope{BranchID: branchA})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		ids := map[string]bool{}
		for _, r := range got {
			ids[r.ID] = true
		}
		if ids[homedB] {
			t.Fatal("a fenced search leaked another branch's account, this is the whole point of U-park E4")
		}
		if !ids[homedA] {
			t.Fatal("the caller's own branch account must be visible")
		}
		if !ids[unhomed] {
			t.Fatal("an account with no home branch is company-wide and must stay visible to every branch")
		}
	})

	t.Run("fenced to branch B is the mirror image", func(t *testing.T) {
		got, err := repo.SearchAccountsScoped(ctx, term, 50, SearchScope{BranchID: branchB})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, r := range got {
			if r.ID == homedA {
				t.Fatal("branch B must not see branch A's account")
			}
		}
	})

	t.Run("the unfenced entry point still behaves as it always did", func(t *testing.T) {
		// SearchAccounts (the accountStore signature the portal composes) delegates to the fenced query
		// with an empty scope, so it must remain byte-equivalent to the pre-fence behavior.
		got, err := repo.SearchAccounts(ctx, term, 50)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("the unfenced entry point must be unchanged, got %d", len(got))
		}
	})
}
