// finance_check.go carries the finance box's read only check (W2-1b): the
// one-shot the exit test runs against the live box, and the one the operator
// runs before they trust the numbers on the screen.
//
// The check opens one READ ONLY transaction and runs the shared ties
// (fw1_ties.go) for every closed month and for the month the house is
// trading in. It prints one line per tie per month and exits non zero on any
// FAIL. It writes nothing: the ties' own SQL rides the read only
// transaction (Postgres refuses a write inside one), the module services the
// ties read through are read only query surfaces, and the leg counts the
// rows before and after to prove the whole.
//
// The guard is the finance box's own (financeBoxReady): the environment must
// name the finance box and the staging kind, the sign in must be the console
// exchange, and the database itself must carry the box kind mark. A check
// that ran against somebody else's books would answer for a world it knows
// nothing about, so it refuses.
//
// The ties are the month walk's own rules, so the walk, the world generator
// and this check run one code and one set of numbers.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
)

// financeCheckRole is the one-shot: connect, guard, open the read only
// transaction, run the ties, print, exit.
func financeCheckRole(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("finance-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("finance-check: unexpected arguments %q", strings.Join(fs.Args(), " "))
	}
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("finance-check: %w", err)
	}
	printOneshotDatabaseTarget(stderr, "finance-check", dsn)
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("finance-check: cannot connect: %w", err)
	}
	defer pool.Close()
	if err := financeBoxReady(ctx, "finance-check", getenv, pool); err != nil {
		return err
	}
	// The read only transaction: every read the check's own SQL makes rides
	// it, and Postgres refuses a write inside one.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("finance-check: cannot open the read only transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	failed, err := financeCheckRun(ctx, pool, tx, stdout)
	if err != nil {
		return fmt.Errorf("finance-check: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("finance-check: %d tie(s) failed", failed)
	}
	return nil
}

// financeCheckRun runs the ties for every closed month and for the open
// month and prints one line per tie per month. It answers the number of
// failing ties.
func financeCheckRun(ctx context.Context, pool *pgxpool.Pool, querier tieQuerier, stdout io.Writer) (int, error) {
	return financeCheckRunWired(ctx, pool, querier, stdout, nil)
}

// financeCheckRunWired is the check's core, with the production wiring's own
// instances when the caller has them.
func financeCheckRunWired(ctx context.Context, pool *pgxpool.Pool, querier tieQuerier, stdout io.Writer, wired *financeCheckWiring) (int, error) {
	seams, err := financeCheckSeams(ctx, pool, querier, wired)
	if err != nil {
		return 0, err
	}
	bankAccountID, err := financeCheckBankAccount(ctx, pool)
	if err != nil {
		return 0, err
	}
	months, err := financeCheckMonths(ctx, seams, bankAccountID)
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, m := range months {
		results, err := TiesForMonth(ctx, seams, m)
		if err != nil {
			return failed, err
		}
		for _, res := range results {
			printFinanceCheckTie(stdout, m, res)
			if res.Applicable && !res.Pass {
				failed++
			}
		}
	}
	// The closed year's income statement is a YEAR tie, not a month tie: it
	// answers whether the close left the year's statement alone, and only a
	// caller that ran the close has the reading it took before it. The month
	// walk and the world generator run it; the box's check runs the month
	// ties, which is what the box's own months are.
	return failed, nil
}

// printFinanceCheckTie writes one line per tie per month: the month, the
// tie's name, and PASS or FAIL with the variance in cents. A tie that does
// not apply to the month says SKIP and why, and is not a failure.
func printFinanceCheckTie(stdout io.Writer, m TieMonth, res TieResult) {
	month := fmt.Sprintf("%04d-%02d", m.Year, m.Month)
	switch {
	case !res.Applicable:
		fmt.Fprintf(stdout, "%s %s SKIP %s\n", month, res.Name, res.Detail)
	case res.Pass:
		fmt.Fprintf(stdout, "%s %s PASS\n", month, res.Name)
	default:
		fmt.Fprintf(stdout, "%s %s FAIL %d %s\n", month, res.Name, res.VarianceCents, res.Detail)
	}
}

// financeCheckWiring are the production wiring's own instances. A caller that
// has the wiring (the composition leg) hands them over, so the check reads
// the very services the box serves; a one-shot run leaves them nil and
// builds the same composition itself.
type financeCheckWiring struct {
	GL      *gl.Service
	Billing *billing.Repository
	Tax     *tax.Service
}

// financeCheckSeams builds the production seams the ties read through: the
// GL service over the dealer's own zone, billing's own repository, and the
// tax engine over the owning modules' repositories, exactly as the money and
// tops wirings build them.
func financeCheckSeams(ctx context.Context, pool *pgxpool.Pool, querier tieQuerier, wired *financeCheckWiring) (TieSeams, error) {
	glSvc := (*gl.Service)(nil)
	billingRepo := (*billing.Repository)(nil)
	taxSvc := (*tax.Service)(nil)
	if wired != nil {
		glSvc, billingRepo, taxSvc = wired.GL, wired.Billing, wired.Tax
	}
	if glSvc == nil {
		glSvc = gl.NewService(gl.NewRepository(pool).WithZone(newDealerZone(pool)))
	}
	if billingRepo == nil {
		billingRepo = billing.NewRepository(pool).WithZone(newDealerZone(pool))
	}
	if taxSvc == nil {
		taxSvc = newTaxEngine(pool).
			WithGLBalances(glBalanceAdapter{svc: glSvc}).
			WithSalesHSTs(billingRepo).
			WithReturnHSTs(pos.NewRepository(pool)).
			WithBillITCs(purchasing.NewRepository(pool))
	}
	glRepo := gl.NewRepository(pool).WithZone(newDealerZone(pool))
	branches, err := financeCheckBranches(ctx, pool)
	if err != nil {
		return TieSeams{}, err
	}
	zone, err := financeCheckZone(ctx, pool)
	if err != nil {
		return TieSeams{}, err
	}
	return TieSeams{
		Querier:  querier,
		GL:       glSvc,
		GLStore:  glRepo,
		Billing:  billingRepo,
		Tax:      taxSvc,
		Branches: branches,
		Zone:     zone,
	}, nil
}

// financeCheckBranches reads the branches the trial balances are read for.
func financeCheckBranches(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("the branches read: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("the branches read: %w", err)
	}
	return out, nil
}

// financeCheckBankAccount reads the operating bank account, the one the
// monthly reconciliations belong to.
func financeCheckBankAccount(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000' ORDER BY id LIMIT 1`).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("the operating bank account (GL 1000) could not be found")
		}
		return "", fmt.Errorf("the operating bank account read: %w", err)
	}
	return id, nil
}

// financeCheckMonths are the months the check runs the ties for: every fiscal
// period that is CLOSED, and the month the house is trading in. The house
// month is read in the dealer's own business day zone, the same zone the
// close reads.
func financeCheckMonths(ctx context.Context, seams TieSeams, bankAccountID string) ([]TieMonth, error) {
	rows, err := seams.Querier.Query(ctx, `
		SELECT fp.year, fp.month
		  FROM fiscal_periods fp
		 WHERE fp.status = 'CLOSED'
		    OR EXISTS (SELECT 1 FROM gl_journals j WHERE j.period_id = fp.id AND j.status = 'POSTED')
		 ORDER BY fp.year, fp.month`)
	if err != nil {
		return nil, fmt.Errorf("the closed periods read: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []TieMonth
	for rows.Next() {
		var year, month int
		if err := rows.Scan(&year, &month); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%04d-%02d", year, month)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, TieMonth{Year: year, Month: month, MonthEnd: seams.monthEnd(year, month), BankAccountID: bankAccountID})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("the closed periods read: %w", err)
	}
	now := seams.now()
	open := TieMonth{Year: now.Year(), Month: int(now.Month()), MonthEnd: seams.monthEnd(now.Year(), int(now.Month())), BankAccountID: bankAccountID}
	if !seen[fmt.Sprintf("%04d-%02d", open.Year, open.Month)] {
		out = append(out, open)
	}
	return out, nil
}

// financeCheckZone is the dealer's own business day zone, the zone the house
// month and the month's end are read in. A box that names no zone reads the
// Ontario dealer's, the tree's default.
func financeCheckZone(ctx context.Context, pool *pgxpool.Pool) (*time.Location, error) {
	name, err := settings.NewDealerConfigReader(pool).BusinessZone(ctx)
	if err != nil || strings.TrimSpace(name) == "" {
		name = "America/Toronto"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("the business day zone %q could not be read: %w", name, err)
	}
	return loc, nil
}
