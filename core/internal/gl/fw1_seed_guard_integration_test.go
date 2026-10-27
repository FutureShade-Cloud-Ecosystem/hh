//go:build integration

// fw1_seed_guard_integration_test.go, FW1-09b round 6's guard: after the gl
// package's integration tests, every gl_account that existed when the package
// started still exists unchanged (the 0007 seed among it, 9999 Suspense
// included: 619's paid in and paid out post to 9999). The package's legs plant
// and clean up accounts of their own; none may delete or alter one that was
// already here. The guard is read only: it snapshots before m.Run, compares
// after, and fails the package naming the account and what changed.
package gl

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fw1GuardAccount struct {
	name    string
	acctTyp string
	normal  string
	active  bool
}

func fw1GuardReadAccounts(ctx context.Context, pool *pgxpool.Pool) (map[string]fw1GuardAccount, error) {
	rows, err := pool.Query(ctx,
		`SELECT code, name, account_type, normal_balance, is_active FROM gl_accounts ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]fw1GuardAccount{}
	for rows.Next() {
		var code string
		var a fw1GuardAccount
		if err := rows.Scan(&code, &a.name, &a.acctTyp, &a.normal, &a.active); err != nil {
			return nil, err
		}
		out[code] = a
	}
	return out, rows.Err()
}

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL")
	var before map[string]fw1GuardAccount
	var beforeErr error
	if url != "" {
		pool, err := pgxpool.New(context.Background(), url)
		if err == nil {
			before, err = fw1GuardReadAccounts(context.Background(), pool)
			pool.Close()
		}
		if err != nil {
			beforeErr = err
			before = nil
		}
	}
	code := m.Run()
	if url != "" {
		switch {
		case beforeErr != nil:
			// The guard could not watch this run: it does not know what was there before, so it
			// cannot say what the package's tests changed, and a run it cannot watch must not pass
			// (round 7, S3's round 6 P3: the old guard warned and let the package through).
			fmt.Fprintf(os.Stderr, "gl seed guard: the before snapshot could not be read (%v); the guard cannot watch this run, so the package fails\n", beforeErr)
			code = 1
		case before != nil:
			pool, err := pgxpool.New(context.Background(), url)
			var after map[string]fw1GuardAccount
			if err == nil {
				after, err = fw1GuardReadAccounts(context.Background(), pool)
				pool.Close()
			}
			switch {
			case err != nil:
				fmt.Fprintf(os.Stderr, "gl seed guard: the after snapshot could not be read (%v)\n", err)
				code = 1
			default:
				for acct, want := range before {
					got, ok := after[acct]
					switch {
					case !ok:
						fmt.Fprintf(os.Stderr, "gl seed guard: gl_account %s (%s) is missing after the package's tests\n", acct, want.name)
						code = 1
					case got != want:
						// The account's kind leads the line (its OLD type, the field a reader needs
						// first), and every field of both sides is named, so nothing in the line can
						// be read as another field (round 7, S3's round 6 P3: the old line printed
						// the old name where it meant the old type).
						fmt.Fprintf(os.Stderr, "gl seed guard: gl_account %s (%s) changed: was (type %s, normal %s, name %s, active=%t), now (type %s, normal %s, name %s, active=%t)\n",
							acct, want.acctTyp, want.acctTyp, want.normal, want.name, want.active,
							got.acctTyp, got.normal, got.name, got.active)
						code = 1
					}
				}
			}
		}
	}
	os.Exit(code)
}
